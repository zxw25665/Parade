package file

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// DefaultChunkSize 是固定的分块读取大小（2MB）。
	DefaultChunkSize = 2 * 1024 * 1024
)

// GetChunk 按 offset 读取固定大小的文件分块。
// 当 offset 到达文件尾部时返回 io.EOF。
func (e *Engine) GetChunk(path string, offset int64) ([]byte, error) {
	if offset < 0 {
		return nil, fmt.Errorf("offset must be >= 0")
	}
	if path == "" {
		return nil, errors.New("path is empty")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute path failed: %w", err)
	}

	file, err := os.Open(absPath)
	if err != nil {
		return nil, fmt.Errorf("open file failed: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file failed: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory: %s", absPath)
	}
	if offset >= info.Size() {
		return nil, io.EOF
	}

	runtime := e.getRuntime()
	if runtime != nil && runtime.readLimiter != nil {
		runtime.readLimiter <- struct{}{}
		defer func() { <-runtime.readLimiter }()
	}

	buffer := e.borrowChunkBuffer()
	defer e.releaseChunkBuffer(buffer)

	buf := buffer[:DefaultChunkSize]
	n, readErr := file.ReadAt(buf, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, fmt.Errorf("read chunk failed: %w", readErr)
	}
	if n == 0 {
		return nil, io.EOF
	}

	out := make([]byte, n)
	copy(out, buf[:n])
	return out, nil
}

func (e *Engine) GetFileMeta(path string) (os.FileInfo, error) {
	if path == "" {
		return nil, errors.New("path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute path failed: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat file failed: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory: %s", absPath)
	}
	return info, nil
}
