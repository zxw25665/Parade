package file

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

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

	hash := blake3.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("read file for hash failed: %w", err)
	}
	sum := hash.Sum(nil)
	return hex.EncodeToString(sum), nil
}
