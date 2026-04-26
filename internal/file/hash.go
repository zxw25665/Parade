package file

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zeebo/blake3"
)

// HashFile 计算文件内容哈希（BLAKE3）。
func (e *Engine) HashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file failed: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file failed: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("cannot hash directory: %s", path)
	}
	absPath, err := filepathAbs(path)
	if err != nil {
		return "", err
	}

	runtime := e.getRuntime()
	if runtime != nil {
		runtime.cacheMu.RLock()
		cache, ok := runtime.hashCache[absPath]
		runtime.cacheMu.RUnlock()
		if ok && cache.size == info.Size() && cache.modTime.Equal(info.ModTime()) {
			return cache.hash, nil
		}
	}

	hash := blake3.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("read file for hash failed: %w", err)
	}
	sum := hash.Sum(nil)
	hashText := hex.EncodeToString(sum)

	if runtime != nil {
		runtime.cacheMu.Lock()
		runtime.hashCache[absPath] = hashCacheEntry{
			hash:    hashText,
			size:    info.Size(),
			modTime: info.ModTime(),
		}
		runtime.cacheMu.Unlock()
	}
	return hashText, nil
}

func filepathAbs(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path failed: %w", err)
	}
	return filepath.Clean(absPath), nil
}
