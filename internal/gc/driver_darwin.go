package gc

import (
	"syscall"
	"time"
	"unsafe"
)

// XNU's *at syscall numbers and flag are stable on both supported CPUs.
const (
	sysOpenAt   = 463
	sysStatAt   = 470
	sysUnlinkAt = 472
	atNoFollow  = 0x20
)

func openAt(fd int, name string, flags int) (int, error) {
	ptr, err := syscall.BytePtrFromString(name)
	if err != nil {
		return 0, err
	}
	for {
		result, _, errno := syscall.Syscall6(sysOpenAt, uintptr(fd), uintptr(unsafe.Pointer(ptr)), uintptr(flags), 0, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return 0, errno
		}
		return int(result), nil
	}
}

func inspectAt(fd int, name string) (fileInfo, error) {
	ptr, err := syscall.BytePtrFromString(name)
	if err != nil {
		return fileInfo{}, err
	}
	var stat syscall.Stat_t
	for {
		_, _, errno := syscall.Syscall6(sysStatAt, uintptr(fd), uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&stat)), atNoFollow, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return fileInfo{}, errno
		}
		return fileInfo{uint64(stat.Dev), stat.Ino, stat.Size, fileMode(uint32(stat.Mode)), time.Unix(stat.Mtimespec.Sec, stat.Mtimespec.Nsec), time.Unix(stat.Ctimespec.Sec, stat.Ctimespec.Nsec)}, nil
	}
}

func unlinkAt(fd int, name string) error {
	ptr, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	for {
		_, _, errno := syscall.Syscall(sysUnlinkAt, uintptr(fd), uintptr(unsafe.Pointer(ptr)), 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		return nil
	}
}
