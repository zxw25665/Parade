package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"parade/internal/core/eventbus"
	"parade/internal/file"
	pb "parade/internal/network/pb"
)

// FileChunkRequest 记录一次文件块请求参数。
type FileChunkRequest struct {
	PeerID string
	TaskID string
	Offset int64
}

// FilePlane 是文件传输面的轻量状态容器。
type FilePlane struct {
	mu       sync.RWMutex
	bus      eventbus.EventBus
	requests map[string]FileChunkRequest
}

func NewFilePlane(bus eventbus.EventBus) *FilePlane {
	return &FilePlane{
		bus:      bus,
		requests: make(map[string]FileChunkRequest),
	}
}

// RequestFileChunk 记录请求并发布进度事件（用于联调）。
func (f *FilePlane) RequestFileChunk(peerID string, taskID string, offset int64) error {
	if peerID == "" || taskID == "" {
		return errors.New("peerID and taskID are required")
	}
	if offset < 0 {
		return errors.New("offset must be >= 0")
	}

	req := FileChunkRequest{
		PeerID: peerID,
		TaskID: taskID,
		Offset: offset,
	}

	f.mu.Lock()
	f.requests[taskID] = req
	f.mu.Unlock()

	f.bus.Publish(eventbus.TopicFileProgress, eventbus.FileProgressPayload{
		TaskID:      taskID,
		FilePath:    "",
		Transferred: offset,
		TotalSize:   0,
		IsUpload:    false,
	})
	return nil
}

// MarkCompleted 发布文件完成事件。
func (f *FilePlane) MarkCompleted(taskID string) {
	if taskID == "" {
		return
	}
	f.bus.Publish(eventbus.TopicFileCompleted, taskID)
}

// LastRequest 返回某任务最近一次请求。
func (f *FilePlane) LastRequest(taskID string) (FileChunkRequest, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	req, ok := f.requests[taskID]
	return req, ok
}

// SweepOlderThan 为后续超时任务清理预留接口。
func (f *FilePlane) SweepOlderThan(_ time.Duration) {
	// 当前版本不做清理，实现占位。
}

type FileService struct {
	pb.UnimplementedFileTransferServiceServer
	fileEngine *file.Engine
	localPeer  string
}

func NewFileService(fileEngine *file.Engine, localPeer string) *FileService {
	return &FileService{
		fileEngine: fileEngine,
		localPeer:  localPeer,
	}
}

// 服务端：被请求后不断读本地chunk并下发
func (s *FileService) DownloadFile(req *pb.FileRequest, stream pb.FileTransferService_DownloadFileServer) error {
	if req.GetTaskId() == "" || req.GetFilePath() == "" {
		return errors.New("task_id and file_path are required")
	}
	if req.GetOffset() < 0 {
		return errors.New("offset must be >= 0")
	}

	info, err := os.Stat(req.GetFilePath())
	if err != nil {
		return fmt.Errorf("stat file failed: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory: %s", req.GetFilePath())
	}
	totalSize := info.Size()

	offset := req.GetOffset()
	for {
		chunk, err := s.fileEngine.GetChunk(req.GetFilePath(), offset)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// 最后一包EOF通知（可选）
				return stream.Send(&pb.FileChunk{
					TaskId:    req.GetTaskId(),
					PeerId:    s.localPeer,
					FilePath:  req.GetFilePath(),
					Offset:    offset,
					TotalSize: totalSize,
					Eof:       true,
				})
			}
			return err
		}

		resp := &pb.FileChunk{
			TaskId:    req.GetTaskId(),
			PeerId:    s.localPeer,
			FilePath:  req.GetFilePath(),
			Offset:    offset,
			Data:      chunk,
			TotalSize: totalSize,
		}
		if err := stream.Send(resp); err != nil {
			return err
		}

		offset += int64(len(chunk))
	}
}

type DownloadDeps struct {
	FileEngine *file.Engine
	Client     pb.FileTransferServiceClient
	LocalPeer  string
}

type DownloadOptions struct {
	MaxRetries int
	RetryDelay time.Duration
	ChunkSize  int32
}

func DefaultDownloadOptions() DownloadOptions {
	return DownloadOptions{
		MaxRetries: 3,
		RetryDelay: 600 * time.Millisecond,
		ChunkSize:  file.DefaultChunkSize,
	}
}

// StartDownloadWithRetry 将断点续传编排在网络层：
// PrepareDownload -> DownloadFile(stream) -> SaveChunk，失败后重试并从最新 offset 继续。
func StartDownloadWithRetry(
	ctx context.Context,
	deps DownloadDeps,
	taskID string,
	remotePeer string,
	remoteFilePath string,
	localSavePath string,
	totalSize int64,
	opts DownloadOptions,
) error {
	if deps.FileEngine == nil {
		return errors.New("file engine is required")
	}
	if deps.Client == nil {
		return errors.New("file transfer client is required")
	}
	if taskID == "" || remotePeer == "" || remoteFilePath == "" || localSavePath == "" {
		return errors.New("taskID, remotePeer, remoteFilePath and localSavePath are required")
	}
	if totalSize < 0 {
		return errors.New("totalSize must be >= 0")
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 1
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = 500 * time.Millisecond
	}
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = file.DefaultChunkSize
	}

	var lastErr error
	for attempt := 0; attempt < opts.MaxRetries; attempt++ {
		if err := runDownloadOnce(ctx, deps, taskID, remotePeer, remoteFilePath, localSavePath, totalSize, opts.ChunkSize); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return lastErr
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opts.RetryDelay):
		}
	}

	return fmt.Errorf("download failed after %d attempts: %w", opts.MaxRetries, lastErr)
}

func runDownloadOnce(
	ctx context.Context,
	deps DownloadDeps,
	taskID string,
	remotePeer string,
	remoteFilePath string,
	localSavePath string,
	totalSize int64,
	chunkSize int32,
) error {
	startOffset, err := deps.FileEngine.PrepareDownload(ctx, taskID, localSavePath, remotePeer, totalSize)
	if err != nil {
		if errors.Is(err, file.ErrDownloadCompleted) {
			return nil
		}
		return fmt.Errorf("prepare download failed: %w", err)
	}

	stream, err := deps.Client.DownloadFile(ctx, &pb.FileRequest{
		TaskId:    taskID,
		PeerId:    deps.LocalPeer,
		FilePath:  remoteFilePath,
		Offset:    startOffset,
		ChunkSize: chunkSize,
	})
	if err != nil {
		return fmt.Errorf("open download stream failed: %w", err)
	}

	for {
		msg, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("receive file chunk failed: %w", recvErr)
		}
		if msg.GetTaskId() != taskID {
			return fmt.Errorf("task id mismatch: got=%s want=%s", msg.GetTaskId(), taskID)
		}
		if msg.GetEof() {
			return nil
		}
		if len(msg.GetData()) == 0 {
			continue
		}

		saveTotalSize := msg.GetTotalSize()
		if saveTotalSize == 0 {
			saveTotalSize = totalSize
		}
		if err := deps.FileEngine.SaveChunk(
			ctx,
			msg.GetTaskId(),
			localSavePath,
			msg.GetPeerId(),
			msg.GetData(),
			msg.GetOffset(),
			saveTotalSize,
		); err != nil {
			return fmt.Errorf("save chunk failed: %w", err)
		}
	}
}