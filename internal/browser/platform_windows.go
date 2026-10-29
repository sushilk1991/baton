package browser

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

const (
	detachedProcess  = 0x00000008
	sharingViolation = syscall.Errno(32)
)

func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
}

func browserGroup(*exec.Cmd)               {}
func stopSignal() os.Signal                { return os.Interrupt }
func signals() []os.Signal                 { return []os.Signal{os.Interrupt} }
func endGroup(command *exec.Cmd, _ ending) { _ = command.Process.Kill() }

// sessionBusy reports a session.json that Windows will not open for now:
// a sharing violation mid-rename, or ERROR_ACCESS_DENIED once removed while
// another handle (a virus scan) keeps it delete-pending.
func sessionBusy(err error) bool {
	return errors.Is(err, sharingViolation) || errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
