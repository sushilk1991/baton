package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const cliHelper = "BATON_CLI_TEST_HELPER"

func TestBatonCLI(t *testing.T) {
	if os.Getenv(cliHelper) != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(run(os.Args[i+1:], os.Stdout, os.Stderr))
		}
	}
	os.Exit(exitUsage)
}

func TestRunInvalid(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{
		{"run"}, {"run", "--priority", "urgent", "--", "echo"},
		{"run", "--weight", "0", "--", "echo"},
		{"run", "--mem", "garbage", "--", "echo"},
		{"run", "--max-wait", "-1s", "--", "echo"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitUsage {
			t.Fatalf("%v: code %d, want %d (%s)", args, code, exitUsage, stderr.String())
		}
	}
}

func TestRunMissing(t *testing.T) {
	isolateHome(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr)
	if code != 127 || !strings.Contains(stderr.String(), "missing") {
		t.Fatalf("code %d, stderr %q; want 127 and command error", code, stderr.String())
	}
}

func cliCommand(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, append([]string{"-test.run=^TestBatonCLI$", "--"}, args...)...)
	command.Env = append(os.Environ(), cliHelper+"=1", "GORACE=atexit_sleep_ms=0")
	return command
}

func waitCode(t *testing.T, command *exec.Cmd, want int) {
	t.Helper()
	err := command.Wait()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	if code != want {
		t.Fatalf("exit %d, want %d (%v)", code, want, err)
	}
}

// Flag-based commands print baton's usage line, --options, and for run an
// example; a bad flag is reported once, not again after the usage text.
func TestFlagUsage(t *testing.T) {
	for _, test := range []struct {
		command, synopsis string
	}{
		{"run", "usage: baton run [options] -- command [args...]\n"},
		{"status", "usage: baton status [--json | --line] [--watch]\n"},
		{"history", "usage: baton history [--json] [--since 24h] [--lane L]\n"},
	} {
		t.Run(test.command, func(t *testing.T) {
			isolateHome(t)
			var stdout, stderr bytes.Buffer
			if code := run([]string{test.command, "-h"}, &stdout, &stderr); code != 0 {
				t.Fatalf("help exit %d", code)
			}
			if !strings.HasPrefix(stderr.String(), test.synopsis) || !strings.Contains(stderr.String(), "\n  --") || strings.Contains(stderr.String(), "Usage of") {
				t.Fatalf("help %q", stderr.String())
			}
		})
	}

	isolateHome(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"run", "-h"}, &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "example:\n  baton run --lane render") {
		t.Fatalf("run help lacks an example: %q", stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"run", "--lanes", "x", "--", "true"}, &stdout, &stderr); code != exitUsage || strings.Count(stderr.String(), "flag provided but not defined") != 1 {
		t.Fatalf("bad flag: exit %d, %q", code, stderr.String())
	}
}
