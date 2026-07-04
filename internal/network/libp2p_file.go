package network

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
	"parade/internal/file"
)

const (
	protocolFileMeta     = "/parade/file-meta/1.0.0"
	protocolFileDownload = "/parade/file-download/1.0.0"
	protocolFileBrowse   = "/parade/browse/1.0.0"
)

type libp2pFile struct {
	host       host.Host
	fileEngine FileTransferEngine
	localPeer  string
	bus        eventbus.EventBus
	logr       logger.Logger
}

func NewLibp2pFile(h host.Host, fe FileTransferEngine, localPeer string, bus eventbus.EventBus, logr logger.Logger) *libp2pFile {
	lf := &libp2pFile{host: h, fileEngine: fe, localPeer: localPeer, bus: bus, logr: logr}
	h.SetStreamHandler(protocolFileMeta, lf.handleFileMeta)
	h.SetStreamHandler(protocolFileDownload, lf.handleFileDownload)
	h.SetStreamHandler(protocolFileBrowse, lf.handleFileBrowse)
	return lf
}

// ---- wire helpers ----

func writeMsg(stream network.Stream, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := stream.Write(lenBuf); err != nil {
		return err
	}
	_, err = stream.Write(data)
	return err
}

// readMsg unmarshals a 4-byte-length-prefixed JSON message.
// Returns the raw bytes on success; caller should check for "error" key
// when the response may be either a success or error JSON.
func readMsgRaw(stream network.Stream) ([]byte, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(stream, lenBuf); err != nil {
		return nil, err
	}
	dataLen := binary.BigEndian.Uint32(lenBuf)
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(stream, data); err != nil {
		return nil, err
	}
	return data, nil
}

// checkErrorMsg returns the error string if data is a JSON object with an "error" key.
func checkErrorMsg(data []byte) error {
	var errResp struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &errResp) == nil && errResp.Error != "" {
		return fmt.Errorf("%s", errResp.Error)
	}
	return nil
}

// ---- server handlers ----

// handleFileMeta responds with file size for a given path.
func (f *libp2pFile) handleFileMeta(stream network.Stream) {
	defer stream.Close()

	reqBytes, err := io.ReadAll(stream)
	if err != nil {
		return
	}
	path := strings.TrimSpace(string(reqBytes))
	if path == "" {
		json.NewEncoder(stream).Encode(map[string]string{"error": "file_path is required"})
		return
	}

	info, err := f.fileEngine.GetFileMeta(path)
	if err != nil {
		json.NewEncoder(stream).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(stream).Encode(map[string]any{
		"file_path":  path,
		"total_size": info.Size(),
	})
}

// handleFileDownload streams file chunks to the requester.
func (f *libp2pFile) handleFileDownload(stream network.Stream) {
	defer stream.Close()

	reqBytes, err := io.ReadAll(stream)
	if err != nil {
		return
	}

	var req struct {
		TaskID   string `json:"task_id"`
		FilePath string `json:"file_path"`
		Offset   int64  `json:"offset"`
	}
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		writeMsg(stream, map[string]string{"error": fmt.Sprintf("invalid request: %v", err)})
		return
	}

	if req.TaskID == "" || req.FilePath == "" {
		writeMsg(stream, map[string]string{"error": "task_id and file_path are required"})
		return
	}
	if req.Offset < 0 {
		writeMsg(stream, map[string]string{"error": "offset must be >= 0"})
		return
	}

	cleanPath := filepath.ToSlash(filepath.Clean(req.FilePath))
	sharedRoots := f.fileEngine.GetSharedRoots()
	allowed := false
	for _, root := range sharedRoots {
		prefix := strings.TrimRight(filepath.ToSlash(root), "/") + "/"
		if strings.HasPrefix(cleanPath, prefix) || cleanPath == prefix[:len(prefix)-1] {
			allowed = true
			break
		}
	}
	if !allowed {
		writeMsg(stream, map[string]string{"error": "path not in shared directories"})
		return
	}

	info, err := f.fileEngine.GetFileMeta(cleanPath)
	if err != nil {
		writeMsg(stream, map[string]string{"error": err.Error()})
		return
	}
	totalSize := info.Size()

	offset := req.Offset
	for {
		chunk, err := f.fileEngine.GetChunk(cleanPath, offset)
		if err != nil {
			if errors.Is(err, io.EOF) {
				writeMsg(stream, &FileChunk{
					TaskId:    req.TaskID,
					PeerId:    f.localPeer,
					FilePath:  cleanPath,
					Offset:    offset,
					TotalSize: totalSize,
					Eof:       true,
				})
				return
			}
			writeMsg(stream, map[string]string{"error": err.Error()})
			return
		}

		resp := &FileChunk{
			TaskId:    req.TaskID,
			PeerId:    f.localPeer,
			FilePath:  cleanPath,
			Offset:    offset,
			Data:      chunk,
			TotalSize: totalSize,
		}
		if err := writeMsg(stream, resp); err != nil {
			return
		}
		offset += int64(len(chunk))
	}
}

// handleFileBrowse lists directory entries for a path.
func (f *libp2pFile) handleFileBrowse(stream network.Stream) {
	defer stream.Close()

	reqBytes, err := io.ReadAll(stream)
	if err != nil {
		return
	}
	path := strings.TrimSpace(string(reqBytes))

	if path == "" {
		roots := f.fileEngine.GetSharedRoots()
		entries := make([]*BrowseEntry, 0, len(roots))
		for _, root := range roots {
			info, err := os.Stat(root)
			if err != nil {
				continue
			}
			entries = append(entries, &BrowseEntry{
				Name:        filepath.Base(root),
				Path:        root,
				IsDirectory: true,
				Size:        info.Size(),
			})
		}
		json.NewEncoder(stream).Encode(entries)
		return
	}

	cleanPath := filepath.ToSlash(filepath.Clean(path))
	sharedRoots := f.fileEngine.GetSharedRoots()
	allowed := false
	for _, root := range sharedRoots {
		prefix := strings.TrimRight(filepath.ToSlash(root), "/") + "/"
		if strings.HasPrefix(cleanPath, prefix) || cleanPath == prefix[:len(prefix)-1] {
			allowed = true
			break
		}
	}
	if !allowed {
		json.NewEncoder(stream).Encode(map[string]string{"error": "path not in shared directories"})
		return
	}

	children, err := f.fileEngine.GetDirectoryChildren(cleanPath)
	if err != nil {
		json.NewEncoder(stream).Encode(map[string]string{"error": fmt.Sprintf("failed to list directory: %v", err)})
		return
	}

	nodes, ok := children.([]*file.FileNode)
	if !ok {
		json.NewEncoder(stream).Encode(map[string]string{"error": "unexpected type from file engine"})
		return
	}

	entries := make([]*BrowseEntry, 0, len(nodes))
	for _, n := range nodes {
		entries = append(entries, &BrowseEntry{
			Name:        n.Name,
			Path:        n.Path,
			IsDirectory: n.IsFolder,
			Size:        n.Size,
			Hash:        n.Hash,
		})
	}

	json.NewEncoder(stream).Encode(entries)
}

// ---- client methods ----

// StartDownload initiates a full file download from a remote peer via libp2p streams.
// It first fetches file metadata, prepares local download state, then streams chunks
// with retry support matching the legacy gRPC StartDownloadWithRetry semantics.
func (f *libp2pFile) StartDownload(peerID peer.ID, remotePath, localPath string) error {
	ctx := context.Background()

	// 1. Get file meta
	totalSize, err := f.getFileMeta(peerID, remotePath)
	if err != nil {
		return fmt.Errorf("get file meta: %w", err)
	}

	// 2. Generate task ID
	taskID := uuid.NewString()
	peerIDStr := peerID.String()

	// 3. Initial prepare
	_, err = f.fileEngine.PrepareDownload(ctx, taskID, localPath, peerIDStr, totalSize)
	if errors.Is(err, file.ErrDownloadCompleted) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("prepare download: %w", err)
	}

	// 4. Download with retries
	const maxRetries = 3
	const retryDelay = 600 * time.Millisecond
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		startOffset, err := f.fileEngine.PrepareDownload(ctx, taskID, localPath, peerIDStr, totalSize)
		if errors.Is(err, file.ErrDownloadCompleted) {
			return nil
		}
		if err != nil {
			lastErr = err
			goto retrySleep
		}

		if err := f.downloadChunks(ctx, peerID, remotePath, localPath, taskID, startOffset, totalSize); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return lastErr
		}

	retrySleep:
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}

	return fmt.Errorf("download failed after %d attempts: %w", maxRetries, lastErr)
}

// getFileMeta opens a meta stream to the peer and retrieves the file's total size.
func (f *libp2pFile) getFileMeta(peerID peer.ID, remotePath string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := f.host.NewStream(ctx, peerID, protocolFileMeta)
	if err != nil {
		return 0, fmt.Errorf("open meta stream: %w", err)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte(remotePath)); err != nil {
		return 0, fmt.Errorf("send meta request: %w", err)
	}
	if err := stream.CloseWrite(); err != nil {
		return 0, fmt.Errorf("close write side: %w", err)
	}

	var resp struct {
		FilePath  string `json:"file_path"`
		TotalSize int64  `json:"total_size"`
		Error     string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return 0, fmt.Errorf("read meta response: %w", err)
	}
	if resp.Error != "" {
		return 0, fmt.Errorf("remote meta error: %s", resp.Error)
	}
	return resp.TotalSize, nil
}

// downloadChunks opens a download stream and receives chunk data.
func (f *libp2pFile) downloadChunks(ctx context.Context, peerID peer.ID, remotePath, localPath, taskID string, startOffset, totalSize int64) error {
	stream, err := f.host.NewStream(ctx, peerID, protocolFileDownload)
	if err != nil {
		return fmt.Errorf("open download stream: %w", err)
	}
	defer stream.Close()

	reqJSON, err := json.Marshal(map[string]any{
		"task_id":   taskID,
		"file_path": remotePath,
		"offset":    startOffset,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	if _, err := stream.Write(reqJSON); err != nil {
		return fmt.Errorf("send download request: %w", err)
	}
	if err := stream.CloseWrite(); err != nil {
		return fmt.Errorf("close write side: %w", err)
	}

	for {
		raw, err := readMsgRaw(stream)
		if err != nil {
			if err == io.EOF || errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("receive chunk: %w", err)
		}

		if err := checkErrorMsg(raw); err != nil {
			return fmt.Errorf("remote download error: %w", err)
		}

		var chunk FileChunk
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return fmt.Errorf("unmarshal chunk: %w", err)
		}

		if chunk.TaskId != taskID {
			return fmt.Errorf("task id mismatch: got=%s want=%s", chunk.TaskId, taskID)
		}
		if chunk.Eof {
			return nil
		}
		if len(chunk.Data) == 0 {
			continue
		}

		saveTotalSize := chunk.TotalSize
		if saveTotalSize == 0 {
			saveTotalSize = totalSize
		}
		if err := f.fileEngine.SaveChunk(
			ctx,
			chunk.TaskId,
			localPath,
			chunk.PeerId,
			chunk.Data,
			chunk.Offset,
			saveTotalSize,
		); err != nil {
			return fmt.Errorf("save chunk: %w", err)
		}
	}
}

// BrowseRemote fetches directory listing from a remote peer.
func (f *libp2pFile) BrowseRemote(peerID peer.ID, path string) ([]*BrowseEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := f.host.NewStream(ctx, peerID, protocolFileBrowse)
	if err != nil {
		return nil, fmt.Errorf("open browse stream: %w", err)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte(path)); err != nil {
		return nil, fmt.Errorf("send browse request: %w", err)
	}
	if err := stream.CloseWrite(); err != nil {
		return nil, fmt.Errorf("close write side: %w", err)
	}

	reqBytes, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("read browse response: %w", err)
	}

	var errResp struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(reqBytes, &errResp) == nil && errResp.Error != "" {
		return nil, fmt.Errorf("remote browse error: %s", errResp.Error)
	}

	var entries []*BrowseEntry
	if err := json.Unmarshal(reqBytes, &entries); err != nil {
		return nil, fmt.Errorf("unmarshal browse entries: %w", err)
	}
	return entries, nil
}
