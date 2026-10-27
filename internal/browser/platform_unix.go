//go:build darwin || linux

package browser

import (
	"os"
	"os/exec"
	"syscall"
)

func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func browserGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopSignal() os.Signal { return syscall.SIGTERM }

func signals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}

func endGroup(command *exec.Cmd, mode ending) {
	signal := syscall.SIGTERM
	if mode == forced {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-command.Process.Pid, signal)
}
