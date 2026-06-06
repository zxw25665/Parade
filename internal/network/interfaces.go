package network

import (
	"context"
	"os"
)

// FileTransferEngine defines the file operations the network layer needs.
// This decouples the network package from the concrete file.Engine implementation.
type FileTransferEngine interface {
	GetFileMeta(path string) (os.FileInfo, error)
	GetChunk(path string, offset int64) ([]byte, error)
	PrepareDownload(ctx context.Context, taskID, filePath, peerID string, totalSize int64) (int64, error)
	SaveChunk(ctx context.Context, taskID, targetPath, peerID string, data []byte, offset int64, totalSize int64) error
	GetMissingChunks(ctx context.Context, taskID, targetPath string) ([]int64, error)
	GetSharedRoots() []string
	GetDirectoryChildren(absPath string) (interface{}, error)
}

// CryptoOps is the subset of crypto.Engine that ConnMgr needs.
type CryptoOps interface {
	GetPublicKeyBase64() string
	DecryptTeam(ciphertext []byte) ([]byte, error)
	DecryptTeamForTeam(teamID string, ciphertext []byte) ([]byte, error)
	DecryptPrivate(ciphertext []byte, remotePubKeyBase64 string) ([]byte, error)
	EncryptTeam(plaintext []byte) ([]byte, error)
	TeamKeyHash() string
	GetActiveTeam() string
}

// EventPublisher is the subset of eventbus.EventBus that ConnMgr needs.
type EventPublisher interface {
	Publish(topic string, payload interface{})
}

// LogSink is the subset of logger.Logger that ConnMgr needs.
type LogSink interface {
	Trace(source, msg string)
	Debug(source, msg string)
	Info(source, msg string)
	Warn(source, msg string)
	Error(source, msg string)
}