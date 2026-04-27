package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
)

// ErrDownloadCompleted 在 PrepareDownload 检测到 file_log 状态为已完成时返回。
// 调用方可通过 errors.Is 判断此错误来跳过下载流程。
var ErrDownloadCompleted = errors.New("download already completed")

const (
	statusTransferring = 0
	statusCompleted    = 1
	directionUpload    = 0
	directionDownload  = 1
)

// WithDatabase 注入 file_logs 所需的数据库实现。
func (e *Engine) WithDatabase(database db.Database) *Engine {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		e.runtime = newRuntimeState()
	}
	e.runtime.database = database
	return e
}

// WithEventBus 注入事件总线，用于上报进度与完成事件。
func (e *Engine) WithEventBus(bus eventbus.EventBus) *Engine {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		e.runtime = newRuntimeState()
	}
	e.runtime.bus = bus
	return e
}

// PrepareDownload 根据 file_logs 判断是否断点续传，并返回起始偏移量。
func (e *Engine) PrepareDownload(ctx context.Context, taskID, filePath, peerID string, totalSize int64) (int64, error) {
	if totalSize < 0 {
		return 0, fmt.Errorf("total size must be >= 0")
	}
	if taskID == "" {
		return 0, errors.New("task id is empty")
	}

	database := e.getDB()
	if database == nil {
		return 0, errors.New("database is not configured")
	}

	log, err := database.GetFileLog(ctx, taskID)
	if err != nil {
		return 0, fmt.Errorf("get file log failed: %w", err)
	}

	if log == nil {
		newLog := &db.FileLog{
			TaskID:      taskID,
			FilePath:    filePath,
			PeerID:      peerID,
			Direction:   directionDownload,
			TotalSize:   totalSize,
			Transferred: 0,
			Status:      statusTransferring,
			UpdatedAt:   time.Now(),
		}
		if err := database.UpsertFileLog(ctx, newLog); err != nil {
			return 0, fmt.Errorf("init file log failed: %w", err)
		}
		return 0, nil
	}

	if log.Status == statusCompleted {
		return log.TotalSize, ErrDownloadCompleted
	}

	if log.Transferred < 0 {
		return 0, nil
	}
	if totalSize > 0 && log.Transferred > totalSize {
		return totalSize, nil
	}
	return log.Transferred, nil
}

// SaveChunk 将 chunk 写入 .parade_tmp，并持久化最新 offset。
// 同一 taskID 的调用通过 per-task 锁串行化，防止并发 DB 进度更新竞态。
// 使用 ChunkTracker 追踪每个 slot 的到达状态，支持乱序到达。
func (e *Engine) SaveChunk(ctx context.Context, taskID, targetPath, peerID string, data []byte, offset int64, totalSize int64) error {
	if taskID == "" {
		return errors.New("task id is empty")
	}
	if targetPath == "" {
		return errors.New("target path is empty")
	}
	if offset < 0 {
		return errors.New("offset must be >= 0")
	}
	if totalSize < 0 {
		return errors.New("total size must be >= 0")
	}

	lock := e.getTaskLock(taskID)
	lock.Lock()
	defer lock.Unlock()

	database := e.getDB()
	if database == nil {
		return errors.New("database is not configured")
	}

	tmpPath := targetPath + ".parade_tmp"
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create target directory failed: %w", err)
	}

	// ---- 写入数据 ----
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open temp file failed: %w", err)
	}
	_, writeErr := file.WriteAt(data, offset)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write chunk failed: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close temp file failed: %w", closeErr)
	}

	// ---- ChunkTracker 标记与持久化 ----
	bitmapPath := tmpPath + ".bitmap"
	tracker, trackerErr := e.getOrCreateTracker(taskID, bitmapPath, totalSize)
	if trackerErr != nil {
		return fmt.Errorf("init chunk tracker failed: %w", trackerErr)
	}

	complete, markErr := tracker.MarkReceived(offset, int64(len(data)))
	if markErr != nil {
		return fmt.Errorf("mark chunk received failed: %w", markErr)
	}

	if saveErr := tracker.Save(bitmapPath); saveErr != nil {
		return fmt.Errorf("save chunk tracker failed: %w", saveErr)
	}

	// ---- DB 进度持久化 ----
	nextOffset := offset + int64(len(data))
	status := statusTransferring
	if complete {
		status = statusCompleted
	}

	log := &db.FileLog{
		TaskID:      taskID,
		FilePath:    targetPath,
		PeerID:      peerID,
		Direction:   directionDownload,
		TotalSize:   totalSize,
		Transferred: nextOffset,
		Status:      status,
		UpdatedAt:   time.Now(),
	}
	if err := database.UpsertFileLog(ctx, log); err != nil {
		return fmt.Errorf("update file log failed: %w", err)
	}

	e.publishProgress(taskID, targetPath, tracker.BytesReceived(), totalSize)

	if complete {
		_ = os.Remove(bitmapPath)
		e.cleanupTracker(taskID)
		_ = os.Remove(targetPath)
		if err := os.Rename(tmpPath, targetPath); err != nil {
			return fmt.Errorf("finalize file failed: %w", err)
		}
		e.publishCompleted(taskID)
	}
	return nil
}

// StartDownload 兼容 app.FileEngine。
func (e *Engine) StartDownload(_, _, _ string) error {
	return errors.New("start download is not connected yet")
}

type runtimeState struct {
	database      db.Database
	bus           eventbus.EventBus
	chunkPool     sync.Pool
	readLimiter   chan struct{}
	cacheMu       sync.RWMutex
	treeCache     map[string]treeCacheEntry
	hashCache     map[string]hashCacheEntry
	taskLocks     sync.Map // map[string]*sync.Mutex
	chunkTrackers sync.Map // map[string]*ChunkTracker
	watchMu       sync.Mutex
	watchers      map[string]*rootWatcher
}

func newRuntimeState() *runtimeState {
	return &runtimeState{
		chunkPool: sync.Pool{
			New: func() interface{} {
				return make([]byte, DefaultChunkSize)
			},
		},
		readLimiter: make(chan struct{}, 4),
		treeCache:   make(map[string]treeCacheEntry),
		hashCache:   make(map[string]hashCacheEntry),
		watchers:    make(map[string]*rootWatcher),
	}
}

type treeCacheEntry struct {
	node *FileNode
}

type hashCacheEntry struct {
	hash    string
	size    int64
	modTime time.Time
}

type rootWatcher struct {
	watcher *fsnotify.Watcher
	done    chan struct{}
	once    sync.Once
}

func (e *Engine) getDB() db.Database {
	runtime := e.getRuntime()
	if runtime == nil {
		return nil
	}
	return runtime.database
}

// getTaskLock 返回指定 taskID 的互斥锁，确保同一 task 的 SaveChunk 调用串行化。
// 使用 sync.Map 实现懒加载，无需初始化且线程安全。
func (e *Engine) getTaskLock(taskID string) *sync.Mutex {
	runtime := e.getRuntime()
	if runtime == nil {
		return &sync.Mutex{}
	}
	actual, _ := runtime.taskLocks.LoadOrStore(taskID, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// getOrCreateTracker 返回 taskID 对应的 ChunkTracker。
// 优先从 bitmapPath 加载已有状态（断点续传场景），否则新建。
func (e *Engine) getOrCreateTracker(taskID, bitmapPath string, totalSize int64) (*ChunkTracker, error) {
	runtime := e.getRuntime()
	if runtime == nil {
		return nil, errors.New("engine not initialized")
	}

	if cached, ok := runtime.chunkTrackers.Load(taskID); ok {
		return cached.(*ChunkTracker), nil
	}

	tracker, err := LoadChunkTracker(bitmapPath, totalSize)
	if err == nil {
		runtime.chunkTrackers.Store(taskID, tracker)
		return tracker, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	tracker = NewChunkTracker(totalSize)
	runtime.chunkTrackers.Store(taskID, tracker)
	return tracker, nil
}

// cleanupTracker 移除完成任务的 ChunkTracker 缓存，防止内存泄漏。
func (e *Engine) cleanupTracker(taskID string) {
	runtime := e.getRuntime()
	if runtime == nil {
		return
	}
	runtime.chunkTrackers.Delete(taskID)
}

// GetMissingChunks 返回尚未接收的偏移量列表，供网络层 resume 时使用。
func (e *Engine) GetMissingChunks(ctx context.Context, taskID, targetPath string) ([]int64, error) {
	database := e.getDB()
	if database == nil {
		return nil, errors.New("database not configured")
	}
	log, err := database.GetFileLog(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if log == nil {
		return nil, errors.New("file log not found")
	}
	bitmapPath := targetPath + ".parade_tmp.bitmap"
	tracker, err := e.getOrCreateTracker(taskID, bitmapPath, log.TotalSize)
	if err != nil {
		return nil, err
	}
	return tracker.MissingOffsets(), nil
}

func (e *Engine) getRuntime() *runtimeState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.runtime
}

func (e *Engine) borrowChunkBuffer() []byte {
	runtime := e.getRuntime()
	if runtime == nil {
		return make([]byte, DefaultChunkSize)
	}
	return runtime.chunkPool.Get().([]byte)
}

func (e *Engine) releaseChunkBuffer(buf []byte) {
	if cap(buf) < DefaultChunkSize {
		return
	}
	runtime := e.getRuntime()
	if runtime == nil {
		return
	}
	runtime.chunkPool.Put(buf[:DefaultChunkSize])
}

func (e *Engine) publishProgress(taskID, filePath string, transferred, totalSize int64) {
	runtime := e.getRuntime()
	if runtime == nil || runtime.bus == nil {
		return
	}
	runtime.bus.Publish(eventbus.TopicFileProgress, eventbus.FileProgressPayload{
		TaskID:      taskID,
		FilePath:    filePath,
		Transferred: transferred,
		TotalSize:   totalSize,
		IsUpload:    false,
	})
}

func (e *Engine) publishCompleted(taskID string) {
	runtime := e.getRuntime()
	if runtime == nil || runtime.bus == nil {
		return
	}
	runtime.bus.Publish(eventbus.TopicFileCompleted, taskID)
}

func (e *Engine) publishDirChanged(root string) {
	runtime := e.getRuntime()
	if runtime == nil || runtime.bus == nil {
		return
	}
	runtime.bus.Publish(eventbus.TopicDirChanged, root)
}

const hashTaskPrefix = "hash:"

func hashTaskID(hash string) string {
	return hashTaskPrefix + hash
}

func (e *Engine) persistHashToFileLog(ctx context.Context, absPath, hash string, size int64) error {
	database := e.getDB()
	if database == nil {
		return nil
	}
	log := &db.FileLog{
		TaskID:      hashTaskID(hash),
		FilePath:    absPath,
		PeerID:      "local",
		Direction:   directionUpload,
		TotalSize:   size,
		Transferred: size,
		Status:      statusCompleted,
		UpdatedAt:   time.Now(),
	}
	return database.UpsertFileLog(ctx, log)
}
