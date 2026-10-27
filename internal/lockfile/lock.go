// Package lockfile provides nonblocking, process-owned kernel file locks.
package lockfile

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

const fileMode = 0600

// ErrBusy means another open handle already owns the lock.
var ErrBusy = errors.New("file lock busy")

// Lock owns an open descriptor until Close; process exit also releases it.
type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

// Try opens path and attempts an exclusive lock without waiting.
func Try(path string) (*Lock, error) {
	// Never truncate or unlink lock files: all callers must lock one inode.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, err
	}
	if err := tryLock(file); err != nil {
		return nil, errors.Join(fmt.Errorf("lock %s: %w", path, err), file.Close())
	}
	return &Lock{file: file}, nil
}

// Close releases the lock and descriptor; repeated or concurrent calls are safe.
func (lock *Lock) Close() error {
	lock.once.Do(func() {
		// Explicit unlock avoids Windows delaying release until handle cleanup.
		lock.err = errors.Join(unlock(lock.file), lock.file.Close())
	})
	return lock.err
}
