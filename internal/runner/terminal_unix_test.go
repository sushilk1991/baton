//go:build darwin || linux

package runner

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestTerminalStdin(t *testing.T) {
	isolateHome(t)
	master, slave := openTestPTY(t)
	defer master.Close()
	defer slave.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestTerminalProcess$")
	command.Env = append(os.Environ(), "BATON_TTY_TEST_HELPER=1", "GORACE=atexit_sleep_ms=0")
	command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = command.Process.Kill() })
	lines := make(chan string, 8)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(master)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	var output []string
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("terminal closed early: %v", output)
			}
			output = append(output, line)
			if strings.TrimSpace(line) == "tty-ready" {
				fmt.Fprintln(master, "tty-input")
			}
			if strings.TrimSpace(line) != "restored" {
				continue
			}
			if err := command.Wait(); err != nil {
				t.Fatalf("terminal child: %v (%v)", err, output)
			}
			if !strings.Contains(strings.Join(output, "\n"), "read:tty-input") {
				t.Fatalf("terminal input changed: %v", output)
			}
			t.Log("terminal input reached child; caller foreground group restored")
			return
		case <-ctx.Done():
			t.Fatalf("terminal read stopped or hung: %v", output)
		}
	}
}

func TestTerminalProcess(t *testing.T) {
	if os.Getenv("BATON_TTY_TEST_HELPER") != "1" {
		return
	}
	opts := baseOptions([]string{"/bin/sh", "-c", `printf 'tty-ready\n'; read -r text; printf 'read:%s\n' "$text"`})
	opts.lane = ""
	code, err := runJob(context.Background(), opts, streams{os.Stdin, os.Stdout, os.Stderr})
	if code != 0 || err != nil {
		fmt.Fprintln(os.Stderr, "terminal run failed", code, err)
		os.Exit(1)
	}
	var foreground int32
	if err := terminalGroup(os.Stdin.Fd(), syscall.TIOCGPGRP, &foreground); err != nil || int(foreground) != syscall.Getpgrp() {
		fmt.Fprintln(os.Stderr, "foreground not restored", foreground, err)
		os.Exit(1)
	}
	fmt.Println("restored")
	os.Exit(0)
}
