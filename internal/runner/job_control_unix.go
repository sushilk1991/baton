//go:build darwin || linux

package runner

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
	"unsafe"
)

const (
	terminalPollInterval = 20 * time.Millisecond
	waitPID              = 1
	waitStoppedCode      = 5
	waitContinuedCode    = 6
	waitInfoBytes        = 128
	waitHeaderBytes      = 12
	waitChildBytes       = 12
)

type resumeMode uint8

const (
	resumeSignal resumeMode = iota
	resumeForeground
)

// The header agrees on Darwin/Linux; Linux aligns the child union to eight
// bytes on amd64/arm64. Keep the full buffer for the kernel's siginfo_t.
type childWaitInfo struct {
	signo  int32
	errno  int32
	code   int32
	_      [waitAlignBytes]byte
	pid    int32
	uid    uint32
	status int32
	_      [waitInfoBytes - waitHeaderBytes - waitAlignBytes - waitChildBytes]byte
}

func waitGroup(command *exec.Cmd, changed func(string)) error {
	if command.SysProcAttr.Ctty == noTerminal {
		return command.Wait()
	}
	// Subscribe before peeking so a child transition between waitid and the
	// receive cannot be lost. CONT also wakes a running supervisor after bg.
	events := make(chan os.Signal, 16)
	signal.Notify(events, syscall.SIGCHLD, syscall.SIGCONT)
	defer signal.Stop(events)
	ticker := time.NewTicker(terminalPollInterval)
	defer ticker.Stop()
	for {
		info, err := waitChild(command.Process.Pid, syscall.WEXITED|syscall.WSTOPPED|syscall.WCONTINUED|syscall.WNOWAIT|syscall.WNOHANG)
		if err != nil {
			return errors.Join(err, command.Wait())
		}
		if info.code == 0 {
			// A shell need not send CONT when fg selects an already-running
			// job. Poll tty ownership too, without resuming externally stopped jobs.
			mode := resumeForeground
			select {
			case event := <-events:
				if event != syscall.SIGCONT {
					continue
				}
				mode = resumeSignal
			case <-ticker.C:
			}
			if err := resumeJob(command, mode); err != nil {
				_ = signalGroup(command, syscall.SIGCONT)
				return errors.Join(err, command.Wait())
			}
			continue
		}
		if info.code != waitStoppedCode && info.code != waitContinuedCode {
			return command.Wait()
		}
		// Consume the current transition: CONT can replace a peeked stop.
		// Cmd.Wait retains exit status and rusage.
		info, err = waitChild(command.Process.Pid, syscall.WSTOPPED|syscall.WCONTINUED|syscall.WNOHANG)
		if err != nil {
			return errors.Join(err, command.Wait())
		}
		if info.code == 0 {
			continue
		}
		if info.code == waitContinuedCode {
			changed(phaseRunning)
			continue
		}
		// Publish before stopping Go's sampler. An outside STOP is local to
		// the child; only terminal stop signals suspend the caller's shell job.
		changed(phaseStopped)
		if !terminalStop(syscall.Signal(info.status)) {
			continue
		}
		err = suspendJob(command)
		changed(phaseRunning)
		if err != nil {
			// A lost tty must not strand a stopped child or pending HUP/TERM.
			_ = signalGroup(command, syscall.SIGCONT)
			return errors.Join(err, command.Wait())
		}
	}
}

func terminalStop(stop syscall.Signal) bool {
	return stop == syscall.SIGTSTP || stop == syscall.SIGTTIN || stop == syscall.SIGTTOU
}

func waitChild(pid, options int) (childWaitInfo, error) {
	for {
		var info childWaitInfo
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, waitPID, uintptr(pid), uintptr(unsafe.Pointer(&info)), uintptr(options), 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return info, errno
		}
		return info, nil
	}
}

func suspendJob(command *exec.Cmd) error {
	// Stop the child group and the caller's entire job, including a waiting
	// non-interactive sh/make/npm parent. Locks remain held until completion.
	// child -> baton + parent group stop -> interactive shell -> fg/bg
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
	// Linux can return before every runtime thread stops. Await the actual
	// CONT before allowing any child to run, including in background mode.
	continued := make(chan os.Signal, 1)
	signal.Notify(continued, syscall.SIGCONT)
	defer signal.Stop(continued)
	if err := syscall.Kill(-syscall.Getpgrp(), syscall.SIGSTOP); err != nil {
		return err
	}
	<-continued
	return resumeJob(command, resumeSignal)
}

func resumeJob(command *exec.Cmd, mode resumeMode) error {
	// fg can resume an already-running background job. Always transfer the
	// tty before continuing its child; bg leaves the tty with the shell.
	fd := uintptr(command.SysProcAttr.Ctty)
	var foreground int32
	if err := terminalGroup(fd, syscall.TIOCGPGRP, &foreground); err != nil {
		return err
	}

	// A late supervisor CONT must not undo the foreground child's next stop.
	// Once the child owns the tty, its owner controls any further resumption.
	if int(foreground) == command.Process.Pid {
		return nil
	}

	if int(foreground) != syscall.Getpgrp() && mode == resumeForeground {
		return nil
	}
	if int(foreground) == syscall.Getpgrp() {
		if err := foregroundGroup(fd, int32(command.Process.Pid)); err != nil {
			return err
		}
	}
	return signalGroup(command, syscall.SIGCONT)
}
