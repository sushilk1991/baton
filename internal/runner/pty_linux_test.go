package runner

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

func openTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	var unlock int32
	if err := terminalGroup(master.Fd(), syscall.TIOCSPTLCK, &unlock); err != nil {
		t.Fatal(err)
	}
	var number int32
	if err := terminalGroup(master.Fd(), syscall.TIOCGPTN, &number); err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return master, slave
}
