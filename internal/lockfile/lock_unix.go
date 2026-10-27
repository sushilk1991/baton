//go:build darwin || linux

package lockfile

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(file *os.File) error {
	// Nonblocking flock lets the domain layer cancel or retry without a daemon.
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrBusy
		}
		return err
	}
}

func unlock(file *os.File) error {
	// Retry interrupted unlocks so normal release need not await process exit.
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}
