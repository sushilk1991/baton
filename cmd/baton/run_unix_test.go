//go:build darwin || linux

package main

import (
	"bytes"
	"context"
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

	"github.com/sushilk1991/baton/internal/queue"
)

func TestRunStdioExit(t *testing.T) {
	isolateHome(t)
	command := cliCommand(t, "run", "--agent", "fixture", "--label", "stdio", "--", "/bin/sh", "-c", `read -r line; printf 'out:%s' "$line"; printf 'err' >&2; exit 37`)
	command.Stdin = strings.NewReader("untouched\n")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitCode(t, command, 37)
	if stdout.String() != "out:untouched" || stderr.String() != "err" {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "history.jsonl"))
	if err != nil || !bytes.Contains(data, []byte(`"exit_code":37`)) || !bytes.Contains(data, []byte(`"agent":"fixture"`)) {
		t.Fatalf("history: %s (%v)", data, err)
	}
}

func TestRunWaitNotices(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(fmt.Sprint(quiet), func(t *testing.T) {
			isolateHome(t)
			root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")
			q, err := queue.New(root, "x", 1)
			if err != nil {
				t.Fatal(err)
			}
			ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := ticket.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lease.Release() }()
			marker := filepath.Join(t.TempDir(), "ran")
			args := []string{"run", "--lane", "x", "--max-wait", "10ms"}
			if quiet {
				args = append(args, "--quiet")
			}
			args = append(args, "--", "/bin/sh", "-c", `printf ran > "$1"`, "sh", marker)
			command := cliCommand(t, args...)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})

			// Observe admission before timing the occupied-slot assertion.
			// slow CLI startup -> live ticket -> held slot -> release
			awaitQueuedRun(t, root, command.Process.Pid)
			time.Sleep(400 * time.Millisecond)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("job ran through occupied slot: %v", err)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			waitCode(t, command, 0)
			// A slot wait with healthy memory never spends max_wait, so it
			// ends without the bypass warning even past --max-wait.
			notice := strings.Contains(stderr.String(), "baton: waiting for x")
			if notice == quiet || strings.Contains(stderr.String(), "WARNING: max_wait") {
				t.Fatalf("quiet %t, stderr %q", quiet, stderr.String())
			}
		})
	}
}

func awaitQueuedRun(t *testing.T, root string, pid int) {
	t.Helper()
	// Startup time must not consume the interval that tests queue blocking.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		view, err := queue.Observe(ctx, root, "x", 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Waiting) == 1 && view.Waiting[0].PID == pid {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("run never queued behind occupied slot")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRunSignalGroup(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(signal.String(), func(t *testing.T) {
			isolateHome(t)
			root := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := cliCommand(t, "run", "--", executable, "-test.run=^TestSignalChild$", "--", root, "parent")
			command.Env = append(command.Env, "BATON_SIGNAL_TEST_HELPER=1")
			var stderr bytes.Buffer
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if command.ProcessState != nil {
					return
				}
				_ = command.Process.Kill()
				data, err := os.ReadFile(filepath.Join(root, "group"))
				if err == nil {
					pid, _ := strconv.Atoi(string(data))
					if pid > 0 {
						_ = syscall.Kill(-pid, syscall.SIGKILL)
					}
				}
			})
			awaitFile(t, filepath.Join(root, "ready"))
			if err := command.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			waitCode(t, command, 23)
			awaitFile(t, filepath.Join(root, "parent"))
			awaitFile(t, filepath.Join(root, "child"))
			for _, role := range []string{"parent", "child"} {
				data, err := os.ReadFile(filepath.Join(root, role))
				if err != nil || string(data) != signal.String() {
					t.Fatalf("%s caught %q (%v), want %s", role, data, err, signal)
				}
			}
		})
	}
}

func awaitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func TestSignalChild(t *testing.T) {
	if os.Getenv("BATON_SIGNAL_TEST_HELPER") != "1" {
		return
	}
	root, role := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	caught := make(chan os.Signal, 1)
	signal.Notify(caught, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	if role == "parent" {
		_ = os.WriteFile(filepath.Join(root, "group"), []byte(strconv.Itoa(os.Getpid())), 0600)
		executable, _ := os.Executable()
		child := exec.Command(executable, "-test.run=^TestSignalChild$", "--", root, "child")
		child.Env = os.Environ()
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		sig := <-caught
		_ = os.WriteFile(filepath.Join(root, "parent"), []byte(sig.String()), 0600)
		// Wait for the descendant so failed group delivery cannot look green.
		_ = child.Wait()
		os.Exit(23)
	}
	_ = os.WriteFile(filepath.Join(root, "ready"), []byte("ready"), 0600)
	sig := <-caught
	_ = os.WriteFile(filepath.Join(root, "child"), []byte(sig.String()), 0600)
	os.Exit(0)
}

func TestRunWaitCancellation(t *testing.T) {
	isolateHome(t)
	root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")
	q, err := queue.New(root, "x", 1)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := ticket.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	marker := filepath.Join(t.TempDir(), "unexpected")
	command := cliCommand(t, "run", "--lane", "x", "--", "/bin/sh", "-c", `printf ran > "$1"`, "sh", marker)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	deadline := time.Now().Add(5 * time.Second)
	var jobs []string
	for time.Now().Before(deadline) {
		jobs, _ = filepath.Glob(filepath.Join(root, "jobs", "*.json"))
		if len(jobs) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(jobs) != 1 {
		t.Fatal("queued identity missing")
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitCode(t, command, 143)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("canceled job executed: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, err := q.Enqueue(ctx, 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := next.Acquire(ctx)
	if err != nil {
		t.Fatalf("cancellation stranded queue: %v", err)
	}
	if err := acquired.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestRunSignalExit(t *testing.T) {
	isolateHome(t)
	command := cliCommand(t, "run", "--lane", "x", "--max-wait", "1ms", "--", "/bin/sh", "-c", "kill -TERM $$")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitCode(t, command, 143)
	// A failed child must release the lane before the next invocation.
	next := cliCommand(t, "run", "--lane", "x", "--max-wait", "1ms", "--", "/usr/bin/true")
	if err := next.Start(); err != nil {
		t.Fatal(err)
	}
	waitCode(t, next, 0)
}
