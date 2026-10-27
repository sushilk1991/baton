package lockfile

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockExclusive = 0x00000002
	lockNoWait    = 0x00000001
	lockBytes     = 1
	lockViolation = syscall.Errno(33)
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx   = kernel32.NewProc("LockFileEx")
	unlockFileEx = kernel32.NewProc("UnlockFileEx")
)

func tryLock(file *os.File) error {
	// Lock one byte at offset zero; byte-range locks may extend past EOF.
	var overlapped syscall.Overlapped
	result, _, err := lockFileEx.Call(file.Fd(), lockExclusive|lockNoWait, 0, lockBytes, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result != 0 {
		return nil
	}
	if errors.Is(err, lockViolation) {
		return ErrBusy
	}
	return err
}

func unlock(file *os.File) error {
	// Match the exact range acquired above before closing its owning handle.
	var overlapped syscall.Overlapped
	result, _, err := unlockFileEx.Call(file.Fd(), 0, lockBytes, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result != 0 {
		return nil
	}
	return err
}
