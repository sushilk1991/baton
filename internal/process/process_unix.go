//go:build darwin || linux

package process

import (
	"context"
	"errors"
	"os"
	"syscall"
)

const initPID = 1

func signalValue(signal Signal) (syscall.Signal, error) {
	switch signal {
	case Terminate:
		return syscall.SIGTERM, nil
	case Kill:
		return syscall.SIGKILL, nil
	default:
		return 0, errors.New("invalid process signal")
	}
}

func changed(ctx context.Context, expected Info) error {
	current, err := Read(ctx, expected.PID)
	if err != nil {
		return err
	}
	if expected.PID <= initPID || expected.PID == os.Getpid() || expected.UID != os.Geteuid() || !Same(expected, current) {
		return ErrGone
	}
	return nil
}

func goneError(err error) error {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return ErrGone
	}
	return err
}
