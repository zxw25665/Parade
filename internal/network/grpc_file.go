package network

import (
	"errors"
	"sync"
	"time"

	"parade/internal/core/eventbus"
)

// FileChunkRequest 记录一次文件块请求参数。
type FileChunkRequest struct {
	PeerID string
	TaskID string
	Offset int64
}

// FilePlane 是文件传输面的骨架实现。
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

// RequestFileChunk 记录请求并发布进度事件（用于前后端联调）。
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
