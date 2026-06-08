package network

import (
	"context"
	"os"
)

type FileTransferEngine interface {
	GetFileMeta(path string) (os.FileInfo, error)
	GetChunk(path string, offset int64) ([]byte, error)
	PrepareDownload(ctx context.Context, taskID, filePath, peerID string, totalSize int64) (int64, error)
	SaveChunk(ctx context.Context, taskID, targetPath, peerID string, data []byte, offset int64, totalSize int64) error
	GetMissingChunks(ctx context.Context, taskID, targetPath string) ([]int64, error)
	GetSharedRoots() []string
	GetDirectoryChildren(absPath string) (interface{}, error)
}