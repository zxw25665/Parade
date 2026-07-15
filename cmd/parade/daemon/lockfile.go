package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

const lockFileName = ".parade.lock"

func lockInstance(dataDir string) error {
	lockPath := filepath.Join(dataDir, lockFileName)

	pidStr := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(lockPath, []byte(pidStr), 0o644); err != nil {
		return fmt.Errorf("cannot write lock file %s: %w", lockPath, err)
	}

	lock := flock.New(lockPath)

	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("cannot acquire lock on %s: %w", lockPath, err)
	}
	if !locked {
		return fmt.Errorf("lock held at %s", lockPath)
	}

	locks = append(locks, lockEntry{path: lockPath, lock: lock})
	return nil
}

type lockEntry struct {
	path string
	lock *flock.Flock
}

var locks []lockEntry

func unlockInstance(dataDir string) {
	lockPath := filepath.Join(dataDir, lockFileName)
	for i, l := range locks {
		if l.path == lockPath {
			l.lock.Unlock()
			os.Remove(lockPath)
			locks = append(locks[:i], locks[i+1:]...)
			return
		}
	}
}
