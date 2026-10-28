//go:build darwin || linux

package runner

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"unsafe"
)

const (
	waitPID         = 1
	waitStoppedCode = 5
	waitInfoBytes   = 128
	waitHeaderBytes = 12
)

// The first three siginfo_t fields agree on Darwin/Linux and both supported
// architectures. The remaining storage fits either OS's complete structure.
type childWaitInfo struct {
	signo int32
	errno int32
	code  int32
	_     [waitInfoBytes - waitHeaderBytes]byte
}

func waitGroup(command *exec.Cmd) error {
	if !command.SysProcAttr.Foreground {
		return command.Wait()
	}
	// Peek without reaping: Cmd.Wait must retain exit status, rusage and pipe
	// cleanup. Stop notifications are consumed separately to avoid repeats.
	// child stops -> baton stops -> shell -> fg -> baton -> child continues
	for {
		code, err := waitChild(command.Process.Pid, syscall.WEXITED|syscall.WSTOPPED|syscall.WNOWAIT)
		if err != nil {
			return errors.Join(err, command.Wait())
		}
		if code != waitStoppedCode {
			return command.Wait()
		}
		if _, err := waitChild(command.Process.Pid, syscall.WSTOPPED|syscall.WNOHANG); err != nil {
			return errors.Join(err, command.Wait())
		}
		if err := suspendJob(command); err != nil {
			// A vanished terminal must not strand the stopped group. Continuing
			// also lets it handle a pending HUP/TERM before Wait reaps it.
			_ = signalGroup(command, syscall.SIGCONT)
			return errors.Join(err, command.Wait())
		}
	}
}

func waitChild(pid, options int) (int32, error) {
	for {
		var info childWaitInfo
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, waitPID, uintptr(pid), uintptr(unsafe.Pointer(&info)), uintptr(options), 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return 0, errno
		}
		return info.code, nil
	}
}

func suspendJob(command *exec.Cmd) error {
	// Stop the whole owned group, then stop baton so its waiting shell sees
	// a suspended job. The lane locks stay held until the command exits.
	if err := signalGroup(command, syscall.SIGSTOP); err != nil {
		return err
	}
	fd := uintptr(command.SysProcAttr.Ctty)
	var foreground int32
	if err := terminalGroup(fd, syscall.TIOCGPGRP, &foreground); err != nil {
		return err
	}
	if int(foreground) == command.Process.Pid {
		if err := foregroundGroup(fd, int32(syscall.Getpgrp())); err != nil {
			return err
		}
	}
	// Linux can return from kill before all Go runtime threads have stopped.
	// Wait for the shell's CONT, or the child could resume before baton stops.
	continued := make(chan os.Signal, 1)
	signal.Notify(continued, syscall.SIGCONT)
	defer signal.Stop(continued)
	if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
		return err
	}
	<-continued

	// fg first resumes baton in its own foreground group. Transfer the tty
	// before continuing the child to avoid a read/SIGTTIN race. bg leaves the
	// terminal with the shell; a later child read stops the job again.
	if err := terminalGroup(fd, syscall.TIOCGPGRP, &foreground); err != nil {
		return err
	}
	if int(foreground) == syscall.Getpgrp() {
		if err := foregroundGroup(fd, int32(command.Process.Pid)); err != nil {
			return err
		}
	}
	return signalGroup(command, syscall.SIGCONT)
}
