//go:build darwin || linux

package runner

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"unsafe"
)

const signalExitBase = 128

func prepareGroup(command *exec.Cmd, input io.Reader) func() error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	file, ok := input.(*os.File)
	if !ok {
		return func() error { return nil }
	}
	var previous int32
	if err := terminalGroup(file.Fd(), syscall.TIOCGPGRP, &previous); err != nil || int(previous) != syscall.Getpgrp() {
		return func() error { return nil }
	}
	// The fork driver moves the child to the foreground before exec, avoiding
	// a read/SIGTTIN race. Piped stdin needs no terminal handoff.
	command.SysProcAttr.Foreground = true
	command.SysProcAttr.Ctty = int(file.Fd())
	return func() error {
		// baton is now in the background. Ignore SIGTTOU while restoring its
		// caller's foreground group, then restore the original disposition.
		ignored := signal.Ignored(syscall.SIGTTOU)
		signal.Ignore(syscall.SIGTTOU)
		err := terminalGroup(file.Fd(), syscall.TIOCSPGRP, &previous)
		if !ignored {
			signal.Reset(syscall.SIGTTOU)
		}
		return err
	}
}

func terminalGroup(fd, request uintptr, group *int32) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(group)))
	if errno != 0 {
		return errno
	}
	return nil
}

func forwardSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}
}

func terminateSignal() os.Signal { return syscall.SIGTERM }

func signalGroup(command *exec.Cmd, signal os.Signal) error {
	return syscall.Kill(-command.Process.Pid, signal.(syscall.Signal))
}

func signalCode(signal os.Signal) int {
	return signalExitBase + int(signal.(syscall.Signal))
}

func childCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return signalExitBase + int(status.Signal())
	}
	return state.ExitCode()
}
