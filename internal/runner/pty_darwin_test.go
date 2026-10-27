package runner

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

const ptyNameBytes = 128

func openTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	for _, request := range []uintptr{syscall.TIOCPTYGRANT, syscall.TIOCPTYUNLK} {
		if err := terminalGroup(master.Fd(), request, nil); err != nil {
			t.Fatal(err)
		}
	}
	var name [ptyNameBytes]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0])))
	if errno != 0 {
		t.Fatal(errno)
	}
	slave, err := os.OpenFile(strings.TrimRight(string(name[:]), "\x00"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	return master, slave
}
