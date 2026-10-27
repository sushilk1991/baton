package queue

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	helperEnv  = "BATON_QUEUE_TEST_HELPER"
	crashLimit = time.Second
)

type childProcess struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	lines  <-chan string
	stderr *bytes.Buffer
	waited bool
}

func TestFIFOProcesses(t *testing.T) {
	for _, priority := range []Priority{High, Normal, Low} {
		t.Run(string(priority), func(t *testing.T) {
			isolateHome(t)
			root := t.TempDir()
			q := newQueue(t, root, "render", 1)
			held := acquire(t, enqueue(t, q, 1, Normal))
			jobs := make([]*childProcess, 0, 3)
			for _, name := range []string{"first", "second", "third"} {
				job := startChild(t, root, name, priority)
				awaitLine(t, job, "queued")
				jobs = append(jobs, job)
			}

			// Reverse polling order deliberately: registration defines FIFO.
			for i := len(jobs) - 1; i >= 0; i-- {
				sendLine(t, jobs[i], "go")
			}
			release(t, held)
			for _, job := range jobs {
				awaitLine(t, job, "acquired")
				finishChild(t, job)
			}
			assertOrder(t, root, []string{"first", "second", "third"})
		})
	}
}

func TestPriorityProcesses(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	q := newQueue(t, root, "render", 1)
	held := acquire(t, enqueue(t, q, 1, Normal))
	low := startChild(t, root, "low", Low)
	awaitLine(t, low, "queued")
	normal := startChild(t, root, "normal", Normal)
	awaitLine(t, normal, "queued")
	high := startChild(t, root, "high", High)
	awaitLine(t, high, "queued")
	for _, job := range []*childProcess{low, normal, high} {
		sendLine(t, job, "go")
	}
	release(t, held)
	for _, job := range []*childProcess{high, normal, low} {
		awaitLine(t, job, "acquired")
		finishChild(t, job)
	}
	assertOrder(t, root, []string{"high", "normal", "low"})
}

func TestDeadProcessTicket(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	q := newQueue(t, root, "render", 1)
	held := acquire(t, enqueue(t, q, 1, Normal))
	dead := startChild(t, root, "dead", Normal)
	awaitLine(t, dead, "queued")
	survivor := startChild(t, root, "survivor", Normal)
	awaitLine(t, survivor, "queued")
	sendLine(t, survivor, "go")

	// SIGKILL skips defers. The survivor must prune only the dead owner.
	if err := dead.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := dead.cmd.Wait(); err == nil {
		t.Fatal("killed child exited successfully")
	}
	dead.waited = true
	start := time.Now()
	release(t, held)
	awaitLine(t, survivor, "acquired")
	elapsed := time.Since(start)
	if elapsed >= crashLimit {
		t.Fatalf("dead ticket blocked admission for %s, want < %s", elapsed, crashLimit)
	}
	finishChild(t, survivor)
	assertOrder(t, root, []string{"survivor"})
	files, err := os.ReadDir(q.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if ticketBase(file.Name()) != "" {
			t.Fatalf("ticket file remains after exit: %s", file.Name())
		}
	}
	t.Logf("dead ticket pruned; next process admitted in %s", elapsed)
}

func TestQueueProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	if err := runChild(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runChild() error {
	args := os.Args[len(os.Args)-3:]
	root, name, priority := args[0], args[1], Priority(args[2])
	q, err := New(root, "render", 1)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	ticket, err := q.Enqueue(ctx, 1, priority)
	if err != nil {
		return err
	}
	defer ticket.Close()
	input := bufio.NewScanner(os.Stdin)
	fmt.Println("queued")
	if !input.Scan() {
		return errors.New("start barrier closed")
	}
	lease, err := ticket.Acquire(ctx)
	if err != nil {
		return err
	}
	defer lease.Release()

	// The admitted job itself records order while owning the single slot.
	file, err := os.OpenFile(filepath.Join(root, "order"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(file, name)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	fmt.Println("acquired")
	if !input.Scan() {
		return errors.New("release barrier closed")
	}
	return lease.Release()
}

func startChild(t *testing.T, root, name string, priority Priority) *childProcess {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestQueueProcess$", "--", root, name, string(priority))
	cmd.Env = append(os.Environ(), helperEnv+"=1", "GORACE=atexit_sleep_ms=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 2)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	child := &childProcess{cmd: cmd, input: input, lines: lines, stderr: &stderr}
	t.Cleanup(func() {
		_ = input.Close()
		if child.waited {
			return
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return child
}

func awaitLine(t *testing.T, child *childProcess, want string) {
	t.Helper()
	select {
	case got, ok := <-child.lines:
		if !ok || got != want {
			t.Fatalf("child line = %q (open %t), want %q", got, ok, want)
		}
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %q", want)
	}
}

func sendLine(t *testing.T, child *childProcess, line string) {
	t.Helper()
	if _, err := fmt.Fprintln(child.input, line); err != nil {
		t.Fatal(err)
	}
}

func finishChild(t *testing.T, child *childProcess) {
	t.Helper()
	sendLine(t, child, "release")
	err := child.cmd.Wait()
	child.waited = true
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, child.stderr.String())
	}
}

func assertOrder(t *testing.T, root string, want []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "order"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(data))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("process order = %v, want %v", got, want)
	}
	t.Logf("process order = %v", got)
}
