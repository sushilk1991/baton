package gc

import (
	"syscall"
	"time"
)

// O_PATH obtains metadata without opening devices or waiting on FIFOs.
const openPath = 0x200000

func openAt(fd int, name string, flags int) (int, error) {
	for {
		result, err := syscall.Openat(fd, name, flags, 0)
		if err == syscall.EINTR {
			continue
		}
		return result, err
	}
}

func inspectAt(fd int, name string) (fileInfo, error) {
	file, err := openAt(fd, name, openPath|syscall.O_CLOEXEC|syscall.O_NOFOLLOW)
	if err != nil {
		return fileInfo{}, err
	}
	defer syscall.Close(file)
	var stat syscall.Stat_t
	if err := syscall.Fstat(file, &stat); err != nil {
		return fileInfo{}, err
	}
	return fileInfo{uint64(stat.Dev), stat.Ino, stat.Size, fileMode(stat.Mode), time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec), time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)}, nil
}

func unlinkAt(fd int, name string) error {
	for {
		err := syscall.Unlinkat(fd, name)
		if err == syscall.EINTR {
			continue
		}
		return err
	}
}
