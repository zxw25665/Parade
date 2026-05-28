package file

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"parade/internal/core/logger"

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
			fingerprint, err := e.fileFingerprint(file, info.Size())
			if err != nil {
				return "", fmt.Errorf("compute file fingerprint failed: %w", err)
			}
			if fingerprint == cache.fingerprint {
				if err := e.persistHashToFileLog(context.Background(), absPath, cache.hash, info.Size()); err != nil {
					return "", fmt.Errorf("persist hash cache failed: %w", err)
				}
				return cache.hash, nil
			}
		}
	}

	fingerprint, err := e.fileFingerprint(file, info.Size())
	if err != nil {
		return "", fmt.Errorf("compute file fingerprint failed: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("reset file offset failed: %w", err)
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
			hash:        hashText,
			size:        info.Size(),
			modTime:     info.ModTime(),
			fingerprint: fingerprint,
		}
		runtime.cacheMu.Unlock()
	}
	if err := e.persistHashToFileLog(context.Background(), absPath, hashText, info.Size()); err != nil {
		return "", fmt.Errorf("persist hash result failed: %w", err)
	}
	return hashText, nil
}

func (e *Engine) fileFingerprint(file *os.File, size int64) (string, error) {
	const sampleSize = 32 * 1024

	hash := blake3.New()
	offsets := []int64{0}

	if size > sampleSize {
		offsets = append(offsets, size-sampleSize)
	}
	if size > 2*sampleSize {
		mid := size/2 - sampleSize/2
		if mid < 0 {
			mid = 0
		}
		offsets = append(offsets, mid)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })

	dedupOffsets := offsets[:0]
	var last int64 = -1
	for _, offset := range offsets {
		if offset != last {
			dedupOffsets = append(dedupOffsets, offset)
			last = offset
		}
	}

	for _, offset := range dedupOffsets {
		length := sampleSize
		if remain := size - offset; remain < int64(length) {
			length = int(remain)
		}
		if length <= 0 {
			continue
		}

		buf := make([]byte, length)
		n, err := file.ReadAt(buf, offset)
		if err != nil && err != io.EOF {
			return "", err
		}
		buf = buf[:n]

		var meta [16]byte
		binary.LittleEndian.PutUint64(meta[:8], uint64(offset))
		binary.LittleEndian.PutUint64(meta[8:], uint64(n))
		if _, err := hash.Write(meta[:]); err != nil {
			e.log(logger.Error, "file", fmt.Sprintf("hash write meta at offset %d failed: %v", offset, err))
		}
		if _, err := hash.Write(buf); err != nil {
			e.log(logger.Error, "file", fmt.Sprintf("hash write buf at offset %d failed: %v", offset, err))
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
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
