package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"parade/internal/core/db"
	"parade/internal/core/eventbus"
)

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

	if log.Transferred < 0 {
		return 0, nil
	}
	if totalSize > 0 && log.Transferred > totalSize {
		return totalSize, nil
	}
	return log.Transferred, nil
}

// SaveChunk 将 chunk 写入 .parade_tmp，并持久化最新 offset。
// 若写入后完成下载，会将临时文件原子重命名为目标文件。
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

	database := e.getDB()
	if database == nil {
		return errors.New("database is not configured")
	}

	tmpPath := targetPath + ".parade_tmp"
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create target directory failed: %w", err)
	}

	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY, 0o644)
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

	nextOffset := offset + int64(len(data))
	status := statusTransferring
	if totalSize > 0 && nextOffset >= totalSize {
		nextOffset = totalSize
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

	e.publishProgress(taskID, targetPath, nextOffset, totalSize)

	if status == statusCompleted {
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
	database    db.Database
	bus         eventbus.EventBus
	chunkPool   sync.Pool
	readLimiter chan struct{}
}

func newRuntimeState() *runtimeState {
	return &runtimeState{
		chunkPool: sync.Pool{
			New: func() interface{} {
				return make([]byte, DefaultChunkSize)
			},
		},
		readLimiter: make(chan struct{}, 4),
	}
}

func (e *Engine) getDB() db.Database {
	runtime := e.getRuntime()
	if runtime == nil {
		return nil
	}
	return runtime.database
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
