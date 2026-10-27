//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

const signalExitBase = 128

func stopSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}

func terminateSignal() os.Signal {
	return syscall.SIGTERM
}

func killed(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	// baton returns 128+signal after forwarding a child's signal exit status.
	return ok && status.Signal() == syscall.SIGKILL || exit.ExitCode() == signalExitBase+int(syscall.SIGKILL)
}
