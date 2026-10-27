package browser

import (
	"os"
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008

func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
}

func browserGroup(*exec.Cmd)               {}
func stopSignal() os.Signal                { return os.Interrupt }
func signals() []os.Signal                 { return []os.Signal{os.Interrupt} }
func endGroup(command *exec.Cmd, _ ending) { _ = command.Process.Kill() }
