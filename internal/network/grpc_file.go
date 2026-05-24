package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	fileEngine FileTransferEngine
	localPeer  string
}

func NewFileService(fileEngine FileTransferEngine, localPeer string) *FileService {
	return &FileService{
		fileEngine: fileEngine,
		localPeer:  localPeer,
	}
}

func (s *FileService) GetFileMeta(ctx context.Context, req *pb.FileMetaRequest) (*pb.FileMetaResponse, error) {
	if req.GetFilePath() == "" {
		return nil, errors.New("file_path is required")
	}
	info, err := s.fileEngine.GetFileMeta(req.GetFilePath())
	if err != nil {
		return nil, err
	}
	return &pb.FileMetaResponse{
		FilePath:  req.GetFilePath(),
		TotalSize: info.Size(),
	}, nil
}

// 服务端：被请求后不断读本地chunk并下发
func (s *FileService) DownloadFile(req *pb.FileRequest, stream pb.FileTransferService_DownloadFileServer) error {
	if req.GetTaskId() == "" || req.GetFilePath() == "" {
		return errors.New("task_id and file_path are required")
	}
	if req.GetOffset() < 0 {
		return errors.New("offset must be >= 0")
	}

	cleanPath := filepath.Clean(req.GetFilePath())
	sharedRoots := s.fileEngine.GetSharedRoots()
	allowed := false
	for _, root := range sharedRoots {
		if strings.HasPrefix(cleanPath, root+string(os.PathSeparator)) || cleanPath == root {
			allowed = true
			break
		}
	}
	if !allowed {
		return status.Error(codes.PermissionDenied, "path not in shared directories")
	}

	info, err := s.fileEngine.GetFileMeta(cleanPath)
	if err != nil {
		return err
	}
	totalSize := info.Size()

	offset := req.GetOffset()
	for {
		chunk, err := s.fileEngine.GetChunk(cleanPath, offset)
		if err != nil {
			if errors.Is(err, io.EOF) {
			// 最后一包EOF通知（可选）
			return stream.Send(&pb.FileChunk{
				TaskId:    req.GetTaskId(),
				PeerId:    s.localPeer,
				FilePath:  cleanPath,
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
		FilePath:  cleanPath,
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

func (s *FileService) BrowseDirectory(ctx context.Context, req *pb.BrowseRequest) (*pb.BrowseResponse, error) {
	path := req.GetPath()
	if path == "" {
		roots := s.fileEngine.GetSharedRoots()
		entries := make([]*pb.BrowseEntry, 0, len(roots))
		for _, root := range roots {
			info, err := os.Stat(root)
			if err != nil {
				continue
			}
			entries = append(entries, &pb.BrowseEntry{
				Name:        filepath.Base(root),
				Path:        root,
				IsDirectory: true,
				Size:        info.Size(),
			})
		}
		return &pb.BrowseResponse{Path: "", Entries: entries}, nil
	}

	absPath := filepath.Clean(path)
	sharedRoots := s.fileEngine.GetSharedRoots()
	allowed := false
	for _, root := range sharedRoots {
		if strings.HasPrefix(absPath, root+string(os.PathSeparator)) || absPath == root {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, status.Error(codes.PermissionDenied, "path not in shared directories")
	}

	children, err := s.fileEngine.GetDirectoryChildren(absPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list directory: %v", err)
	}

	nodes, ok := children.([]*file.FileNode)
	if !ok {
		return nil, status.Error(codes.Internal, "unexpected type from file engine")
	}

	entries := make([]*pb.BrowseEntry, 0, len(nodes))
	for _, n := range nodes {
		entries = append(entries, &pb.BrowseEntry{
			Name:        n.Name,
			Path:        n.Path,
			IsDirectory: n.IsFolder,
			Size:        n.Size,
			Hash:        n.Hash,
		})
	}

	return &pb.BrowseResponse{Path: absPath, Entries: entries}, nil
}

type DownloadDeps struct {
	FileEngine FileTransferEngine
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