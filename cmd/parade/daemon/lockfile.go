package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const lockFileName = ".parade.lock"

func lockInstance(dataDir string) error {
	lockPath := filepath.Join(dataDir, lockFileName)

	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open lock file %s: %w", lockPath, err)
	}

	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		syscall.Close(fd)
		return fmt.Errorf("lock file %s is held by another process: %w", lockPath, err)
	}

	pidStr := fmt.Sprintf("%d\n", os.Getpid())
	syscall.Ftruncate(fd, 0)
	syscall.Write(fd, []byte(pidStr))

	locks = append(locks, lockEntry{path: lockPath, fd: fd})
	return nil
}

type lockEntry struct {
	path string
	fd   int
}

var locks []lockEntry

func unlockInstance(dataDir string) {
	lockPath := filepath.Join(dataDir, lockFileName)
	for i, l := range locks {
		if l.path == lockPath {
			syscall.Flock(l.fd, syscall.LOCK_UN)
			syscall.Close(l.fd)
			os.Remove(lockPath)
			locks = append(locks[:i], locks[i+1:]...)
			return
		}
	}
}
