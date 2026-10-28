//go:build darwin || linux

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	parentTrapHelper  = "BATON_PARENT_TRAP_HELPER"
	parentStateSettle = 100 * time.Millisecond
)

// TestParentJobControl compares queued and bare shell job control.
func TestParentJobControl(t *testing.T) {
	isolateHome(t)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "modules"))
	// Exercise the public binary under actual job-controlling shells.
	binary := filepath.Join(t.TempDir(), "baton")
	if output, err := exec.Command("go", "build", "-modcacherw", "-o", binary, "../../cmd/baton").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); os.IsNotExist(err) {
			continue
		}
		for _, scenario := range []string{"parent", "outside-stop", "supervisor-continue", "bg-fg"} {
			for _, mode := range []string{"bare", "baton"} {
				t.Run(filepath.Base(shell)+"/"+scenario+"/"+mode, func(t *testing.T) {
					testParentControl(t, binary, shell, scenario, mode)
				})
			}
		}
	}
}

func testParentControl(t *testing.T, binary, shell, scenario, mode string) {
	t.Helper()
	isolateHome(t)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TERM", "dumb")
	t.Setenv(parentTrapHelper, "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton")
	if err := os.MkdirAll(root, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("[lanes.jc]\nslots=1\nmemory_gate=false\n"), fileMode); err != nil {
		t.Fatal(err)
	}
	terminal := startJobShell(t, shell)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := quoteJobArg(executable) + " -test.run=^TestParentTrapProcess$"
	if mode == "baton" {
		command = quoteJobArg(binary) + " run --lane jc -- " + command
	}
	// Keep a non-interactive parent alive, as make/npm do, rather than exec.
	if scenario != "bg-fg" {
		command = "/bin/sh -c " + quoteJobArg("printf 'parent-pid:%s\\n' $$; "+command+"; result=$?; echo AFTER; exit $result")
	}
	terminal.send(t, command+"\n")
	parent := 0
	if scenario != "bg-fg" {
		pid := terminalPID(t, terminal, "parent-pid:")
		cleanOwnedGroup(t, pid)
		parent = pid
	}
	child := terminalPID(t, terminal, "child-pid:")
	if mode == "baton" {
		job := terminalJob(t)
		cleanOwnedGroup(t, job.OwnerPID)
		cleanOwnedGroup(t, job.PID)
	} else if scenario == "bg-fg" {
		cleanOwnedGroup(t, child)
	}

	if scenario == "outside-stop" || scenario == "supervisor-continue" {
		// An external debugger/owner may stop only the child. The waiter must
		// remain runnable, then reap it without requiring a separate CONT.
		if err := syscall.Kill(child, syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		awaitProcessStop(t, child)
		time.Sleep(parentStateSettle)
		if mode == "baton" {
			owner := terminalJob(t).OwnerPID
			if strings.Contains(processState(t, owner), "T") {
				t.Fatalf("outside SIGSTOP also stopped baton PID %d", owner)
			}
		}

		if scenario == "supervisor-continue" {
			// A late CONT for the supervisor must not override a foreground
			// child's independent stop; only its owner may resume it.
			owner := parent
			if mode == "baton" {
				owner = terminalJob(t).OwnerPID
			}
			if err := syscall.Kill(owner, syscall.SIGCONT); err != nil {
				t.Fatal(err)
			}
			time.Sleep(parentStateSettle)
			if !strings.Contains(processState(t, child), "T") {
				t.Fatal("supervisor CONT resumed its independently stopped foreground child")
			}
		}

		if err := syscall.Kill(child, syscall.SIGCONT); err != nil {
			t.Fatal(err)
		}
	} else {
		// A single keystroke must stop the whole shell job, including parents.
		terminal.send(t, controlZ)
		terminal.await(t, shellPrompt)
		awaitProcessStop(t, child)
		if mode == "baton" {
			assertStoppedStatus(t, binary)
		}
		if scenario == "bg-fg" {
			terminal.send(t, "bg\n")
			terminal.await(t, shellPrompt)
		}
		terminal.send(t, "fg\n")
		foreground := child
		if mode == "bare" && parent != 0 {
			foreground = parent
		}
		awaitForeground(t, terminal, foreground)
		if scenario == "bg-fg" {
			terminal.send(t, controlZ)
			terminal.await(t, shellPrompt)
			awaitProcessStop(t, child)
			if mode == "baton" {
				assertStoppedStatus(t, binary)
			}
			terminal.send(t, "fg\n")
			awaitForeground(t, terminal, child)
			awaitProcessRunning(t, child)
		}
	}

	// Interrupt the owned child directly to make the outside-stop case fail
	// on a stranded supervisor, even if it still owns the foreground tty.
	if err := syscall.Kill(child, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	terminal.await(t, "child-interrupt")
	if scenario != "bg-fg" {
		terminal.await(t, "AFTER")
	}
	terminal.await(t, shellPrompt)
	terminal.send(t, "printf '\\ncontrol-exit:%s\\n' \"$?\"\n")
	terminal.await(t, fmt.Sprintf("control-exit:%d", trapExitCode))
	terminal.await(t, shellPrompt)
	if mode == "baton" {
		terminal.send(t, quoteJobArg(binary)+" run --lane jc -- /bin/echo control-slot-released\n")
		terminal.await(t, "control-slot-released")
		terminal.await(t, shellPrompt)
		jobs := assertHistoryCount(t, 2)
		if jobs[0].ExitCode != trapExitCode || jobs[1].ExitCode != 0 {
			t.Fatalf("history exits: %+v", jobs)
		}
	}
	t.Log("prompt, child state, exit 23 and slot release match the bare command")
}

func terminalPID(t *testing.T, terminal *testTerminal, prefix string) int {
	t.Helper()
	pid, err := strconv.Atoi(terminalPrefix(t, terminal, prefix))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func processState(t *testing.T, pid int) string {
	t.Helper()
	output, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		t.Fatalf("owned PID %d state: %v: %s", pid, err, output)
	}
	return strings.TrimSpace(string(output))
}

func awaitProcessStop(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(terminalTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(processState(t, pid), "T") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("owned child %d did not stop", pid)
}

func awaitForeground(t *testing.T, terminal *testTerminal, pid int) {
	t.Helper()
	deadline := time.Now().Add(terminalTimeout)
	var foreground int32
	for time.Now().Before(deadline) {
		if err := terminalGroup(terminal.master.Fd(), syscall.TIOCGPGRP, &foreground); err != nil {
			t.Fatal(err)
		}
		if int(foreground) == pid {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fg left terminal with group %d; child group is %d", foreground, pid)
}

// TestParentTrapProcess is a bounded, owned terminal child without tty reads.
func TestParentTrapProcess(t *testing.T) {
	if os.Getenv(parentTrapHelper) != "1" {
		return
	}
	// Reset inherited shell stop dispositions so the child behaves like a
	// terminal application even below a shell without interactive job control.
	signal.Reset(syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU)
	caught := make(chan os.Signal, 1)
	signal.Notify(caught, syscall.SIGINT)
	fmt.Printf("child-pid:%d\n", os.Getpid())
	select {
	case <-caught:
		fmt.Println("child-interrupt")
		os.Exit(trapExitCode)
	case <-time.After(processTimeout):
		os.Exit(exitFailure)
	}
}

func awaitProcessRunning(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(terminalTimeout)
	for time.Now().Before(deadline) {
		if !strings.Contains(processState(t, pid), "T") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("owned child %d did not continue", pid)
}
