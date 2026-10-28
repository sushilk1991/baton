//go:build darwin || linux

package runner

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const selfStopHelper = "BATON_SELF_STOP_HELPER"

// The kernel's caught-signal mask must tell a handler from the default.
func TestCatchesSignal(t *testing.T) {
	// A fresh sleep keeps SIGTSTP's default action. This process cannot be
	// the negative case: Go keeps its handler installed after signal.Reset.
	sleeper := exec.Command("/bin/sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sleeper.Process.Kill()
		_ = sleeper.Wait()
	}()
	caught, err := catchesSignal(sleeper.Process.Pid, syscall.SIGTSTP)
	if err != nil {
		t.Fatal(err)
	}
	if caught {
		t.Fatal("SIGTSTP reported caught for sleep")
	}

	stops := make(chan os.Signal, 1)
	signal.Notify(stops, syscall.SIGTSTP)
	defer signal.Reset(syscall.SIGTSTP)
	caught, err = catchesSignal(os.Getpid(), syscall.SIGTSTP)
	if err != nil || !caught {
		t.Fatalf("SIGTSTP not reported caught after signal.Notify: %v", err)
	}
	t.Log("caught-signal mask follows signal.Notify for SIGTSTP")
}

// Ctrl-Z in a program that catches SIGTSTP and stops itself with SIGSTOP,
// as nano and mutt do, must return the prompt exactly as without baton.
// Ctrl-Z -> TSTP handler -> kill(0, SIGSTOP) -> baton suspends the job
func TestJobControlSelfStop(t *testing.T) {
	isolateHome(t)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "modules"))
	binary := filepath.Join(t.TempDir(), "baton")
	build := exec.Command("go", "build", "-modcacherw", "-o", binary, "../../cmd/baton")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build baton: %v\n%s", err, output)
	}
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); os.IsNotExist(err) {
			continue
		}
		for _, mode := range []string{"bare", "baton"} {
			t.Run(filepath.Base(shell)+"/"+mode, func(t *testing.T) {
				testSelfStop(t, binary, shell, mode)
			})
		}
	}
}

func testSelfStop(t *testing.T, binary, shell, mode string) {
	t.Helper()
	isolateHome(t)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("PS1", "baton-test> ")
	t.Setenv("TERM", "dumb")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv(selfStopHelper, "1")
	configRoot := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton")
	if err := os.MkdirAll(configRoot, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), []byte("[lanes.jc]\nslots = 1\nmemory_gate = false\n"), fileMode); err != nil {
		t.Fatal(err)
	}

	terminal := startJobShell(t, shell)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := quoteJobArg(executable) + " -test.run=^TestSelfStopProcess$"
	if mode == "baton" {
		command = quoteJobArg(binary) + " run --lane jc -- " + command
	}
	terminal.send(t, command+"\n")
	child := terminalPID(t, terminal, "selfstop-pid:")
	cleanOwnedGroup(t, child)
	if mode == "baton" {
		cleanOwnedGroup(t, terminalJob(t).OwnerPID)
	}

	// One Ctrl-Z must return the prompt with the child stopped.
	terminal.send(t, controlZ)
	terminal.await(t, shellPrompt)
	awaitProcessStop(t, child)
	t.Log("Ctrl-Z returned the prompt after the child stopped itself")

	// fg resumes the child with the terminal, so it can still read input.
	terminal.send(t, "fg\n")
	terminal.await(t, "selfstop-resumed")
	terminal.send(t, "done\n")
	terminal.await(t, "selfstop-read:done")
	terminal.await(t, shellPrompt)
	terminal.send(t, "printf '\\nselfstop-exit:%s\\n' \"$?\"\n")
	terminal.await(t, "selfstop-exit:0")
	t.Log("fg resumed the child, which read input and exited 0")
}

// TestSelfStopProcess answers SIGTSTP like nano's do_suspend: it stops its
// whole process group with SIGSTOP instead of taking the default action.
func TestSelfStopProcess(t *testing.T) {
	if os.Getenv(selfStopHelper) != "1" {
		return
	}
	signal.Reset(syscall.SIGTTIN, syscall.SIGTTOU)
	stops := make(chan os.Signal, 1)
	signal.Notify(stops, syscall.SIGTSTP)
	fmt.Printf("selfstop-pid:%d\n", os.Getpid())
	select {
	case <-stops:
	case <-time.After(processTimeout):
		os.Exit(exitFailure)
	}
	// Linux can return from kill before every thread stops, so report
	// resumption only after the CONT that fg sends.
	continued := make(chan os.Signal, 1)
	signal.Notify(continued, syscall.SIGCONT)
	if err := syscall.Kill(0, syscall.SIGSTOP); err != nil {
		os.Exit(exitFailure)
	}
	select {
	case <-continued:
	case <-time.After(processTimeout):
		os.Exit(exitFailure)
	}

	fmt.Println("selfstop-resumed")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		os.Exit(exitFailure)
	}
	fmt.Println("selfstop-read:" + strings.TrimSpace(line))
	os.Exit(0)
}
