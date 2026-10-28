//go:build darwin || linux

package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/sushilk1991/baton/internal/queue"
)

const (
	jobControlHelper = "BATON_JOB_CONTROL_HELPER"
	terminalTimeout  = 5 * time.Second
	trapExitCode     = 23
	controlZ         = "\x1a"
	controlC         = "\x03"
	shellPrompt      = "baton-shell-ready"
)

type testTerminal struct {
	master *os.File
	lines  <-chan string
}

func TestJobControlPTY(t *testing.T) {
	isolateHome(t)
	// A fresh module cache exposes cleanup failures hidden by a warm cache.
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "modules"))

	// Keep downloaded module directories writable for TempDir cleanup.
	binary := filepath.Join(t.TempDir(), "baton")
	build := exec.Command("go", "build", "-modcacherw", "-o", binary, "../../cmd/baton")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build baton: %v\n%s", err, output)
	}

	// Each real shell owns a fresh terminal and private scheduler state.
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); os.IsNotExist(err) {
			continue
		}
		t.Run(filepath.Base(shell), func(t *testing.T) {
			testJobControl(t, binary, shell)
		})
	}
}

func testJobControl(t *testing.T, binary, shell string) {
	t.Helper()
	isolateHome(t)
	t.Setenv(jobControlHelper, "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("PS1", "baton-test> ")
	t.Setenv("TERM", "dumb")
	configRoot := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton")
	if err := os.MkdirAll(configRoot, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), []byte("[lanes.x]\nslots = 1\nmemory_gate = false\n"), fileMode); err != nil {
		t.Fatal(err)
	}

	terminal := startJobShell(t, shell)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Readiness comes from the trapping child, after admission and exec.
	terminal.send(t, fmt.Sprintf("%s run --lane x --label pty -- %s -test.run=^TestJobTrapProcess$\n", quoteJobArg(binary), quoteJobArg(executable)))
	terminal.await(t, "trap-ready")
	job := terminalJob(t)
	t.Cleanup(func() {
		// This private record identifies only the child/group started above.
		if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "jobs", job.ID+".json")); err == nil {
			_ = syscall.Kill(-job.PID, syscall.SIGKILL)
			_ = syscall.Kill(job.OwnerPID, syscall.SIGKILL)
		}
	})

	// Repeat on one child to detect stale stop notifications after fg.
	for cycle := 0; cycle < 2; cycle++ {
		// Ctrl-Z must return control to the shell without surrendering the slot.
		terminal.send(t, controlZ)
		terminal.await(t, shellPrompt)
		terminal.send(t, "printf '\\nshell-returned\\n'\n")
		terminal.await(t, "shell-returned")
		terminal.await(t, shellPrompt)
		root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")
		usage, err := queue.Observe(context.Background(), root, "x", 1)
		if err != nil || usage.Used != 1 {
			t.Fatalf("stopped job lost its slot: used %d, error %v", usage.Used, err)
		}
		t.Log("Ctrl-Z returned terminal control to the shell; stopped job retains its slot")

		terminal.send(t, "fg\n")
		terminal.await(t, "trap-resumed")
		terminal.send(t, "resume-input\n")
		terminal.await(t, "trap-read:resume-input")
	}

	// Ctrl-C must reach the handler after both stop/resume cycles.
	terminal.send(t, controlC)
	terminal.await(t, "trap-interrupt")
	terminal.await(t, shellPrompt)
	terminal.send(t, "printf '\\njob-exit:%s\\n' \"$?\"\n")
	terminal.await(t, fmt.Sprintf("job-exit:%d", trapExitCode))
	terminal.await(t, shellPrompt)
	t.Logf("fg resumed child input; Ctrl-C reached its trap; exit %d passed through", trapExitCode)

	// A second CLI run in the one-slot lane proves release after the exit.
	terminal.send(t, fmt.Sprintf("%s run --lane x --label after-pty -- /bin/echo slot-released\n", quoteJobArg(binary)))
	terminal.await(t, "slot-released")
	terminal.await(t, shellPrompt)
	terminal.send(t, "printf '\\nnext-exit:%s\\n' \"$?\"\n")
	terminal.await(t, "next-exit:0")
	jobs := assertHistoryCount(t, 2)
	if jobs[0].ExitCode != trapExitCode || jobs[1].Label != "after-pty" {
		t.Fatalf("job history: %+v", jobs)
	}
	t.Log("next job acquired the released slot; both completions are in history")

	// Failed exec has no Process object but must still restore terminal access.
	terminal.await(t, shellPrompt)
	missing := filepath.Join(t.TempDir(), "missing")
	terminal.send(t, fmt.Sprintf("%s run --lane x -- %s\n", quoteJobArg(binary), quoteJobArg(missing)))
	terminal.await(t, shellPrompt)
	terminal.send(t, "printf '\\nmissing-exit:%s\\n' \"$?\"\n")
	terminal.await(t, fmt.Sprintf("missing-exit:%d", exitNotFound))
	jobs = assertHistoryCount(t, 3)
	if jobs[2].ExitCode != exitNotFound {
		t.Fatalf("failed exec history: %+v", jobs[2])
	}
	t.Log("failed exec restored the terminal and preserved exit 127")
}

func startJobShell(t *testing.T, shell string) *testTerminal {
	t.Helper()
	master, slave := openTestPTY(t)
	t.Cleanup(func() { _ = slave.Close() })
	// Give line editors a real terminal size rather than the PTY's zero size.
	size := [4]uint16{24, 80}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		t.Fatal(errno)
	}
	args := []string{"--noprofile", "--norc", "-i"}
	if filepath.Base(shell) == "zsh" {
		args = []string{"-d", "-f", "-o", "no_zle", "-i"}
	}
	command := exec.Command(shell, args...)
	command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	lines := make(chan string, 128)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(master)
		for scanner.Scan() {
			lines <- strings.TrimSpace(scanner.Text())
		}
	}()
	terminal := &testTerminal{master, lines}
	terminal.send(t, "PS1=$'\\n"+shellPrompt+"\\n'\n")
	terminal.await(t, shellPrompt)
	return terminal
}

func (terminal *testTerminal) send(t *testing.T, text string) {
	t.Helper()
	if _, err := fmt.Fprint(terminal.master, text); err != nil {
		t.Fatal(err)
	}
}

func (terminal *testTerminal) await(t *testing.T, want string) {
	t.Helper()
	timer := time.NewTimer(terminalTimeout)
	defer timer.Stop()
	var output []string
	for {
		select {
		case line, ok := <-terminal.lines:
			if !ok {
				t.Fatalf("terminal closed awaiting %q: %q", want, output)
			}
			output = append(output, line)
			if line == want {
				return
			}
		case <-timer.C:
			t.Fatalf("terminal timed out awaiting %q: %q", want, output)
		}
	}
}

func terminalJob(t *testing.T) jobRecord {
	t.Helper()
	root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "jobs")
	deadline := time.Now().Add(terminalTimeout)
	for time.Now().Before(deadline) {
		files, err := filepath.Glob(filepath.Join(root, "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			var job jobRecord
			if err == nil && json.Unmarshal(data, &job) == nil && job.PID > 0 {
				return job
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("terminal job identity was not published")
	return jobRecord{}
}

func quoteJobArg(text string) string {
	return "'" + strings.ReplaceAll(text, "'", "'\\''") + "'"
}

func TestJobControlTTYLoss(t *testing.T) {
	isolateHome(t)
	file, err := os.CreateTemp(t.TempDir(), "lost-terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	command := exec.Command("/bin/sh", "-c", fmt.Sprintf("kill -TSTP $$; exit %d", trapExitCode))
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})

	// Establish the stopped-child precondition before invalidating its tty.
	awaitProcessStop(t, command.Process.Pid)

	// Model loss of the controlling tty after startup. A stopped child must
	// still be resumed and reaped when the terminal handoff fails.
	command.SysProcAttr.Foreground = true
	command.SysProcAttr.Ctty = int(file.Fd())
	expired := make(chan struct{})
	timer := time.AfterFunc(terminalTimeout, func() {
		_ = command.Process.Kill()
		close(expired)
	})
	defer timer.Stop()
	err = waitGroup(command, func(string) {})
	select {
	case <-expired:
		t.Fatalf("terminal failure stranded the stopped child: %v", err)
	default:
	}
	if !errors.Is(err, syscall.ENOTTY) || command.ProcessState.ExitCode() != trapExitCode {
		t.Fatalf("terminal failure %v, child status %v", err, command.ProcessState)
	}
	t.Log("terminal failure returned ENOTTY and reaped the resumed child with exit 23")
}

func TestJobTrapProcess(t *testing.T) {
	if os.Getenv(jobControlHelper) != "1" {
		return
	}
	// The test's owned process traps INT and reads after resumption, like a
	// terminal application. A deadline also bounds unexpected runner failures.
	caught := make(chan os.Signal, 1)
	signal.Notify(caught, syscall.SIGINT, syscall.SIGCONT)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			fmt.Println("trap-read:" + scanner.Text())
		}
	}()
	fmt.Println("trap-ready")
	timer := time.NewTimer(processTimeout)
	for {
		select {
		case sig := <-caught:
			if sig == syscall.SIGCONT {
				fmt.Println("trap-resumed")
				continue
			}
			fmt.Println("\ntrap-interrupt")
			os.Exit(trapExitCode)
		case <-timer.C:
			os.Exit(exitFailure)
		}
	}
}
