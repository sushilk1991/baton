package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	runHelper      = "BATON_RUN_TEST_HELPER"
	processTimeout = 15 * time.Second
)

type runChild struct {
	command *exec.Cmd
	input   io.WriteCloser
	lines   <-chan string
	err     bytes.Buffer
	waited  bool
}

func TestRunProcess(t *testing.T) {
	if os.Getenv(runHelper) != "1" {
		return
	}
	args := os.Args[len(os.Args)-3:]
	root, role, mode := args[0], args[1], args[2]
	if role == "baton" {
		executable, _ := os.Executable()
		jobArgs := []string{executable, "-test.run=^TestRunProcess$", "--", root, "job", mode}
		opts := baseOptions(jobArgs)
		ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
		defer cancel()
		code, err := runJob(ctx, opts, streams{os.Stdin, os.Stdout, os.Stderr})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(code)
	}
	if mode == "memory" {
		if err := memoryJob(root); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if err := countJob(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func countJob(root string) error {
	if err := changeCount(root, 1); err != nil {
		return err
	}
	fmt.Println("admitted")
	if !bufio.NewScanner(os.Stdin).Scan() {
		return errors.New("release barrier closed")
	}
	return changeCount(root, -1)
}

func changeCount(root string, delta int) error {
	var guard *lockfile.Lock
	for {
		var err error
		guard, err = lockfile.Try(filepath.Join(root, "count.lock"))
		if errors.Is(err, lockfile.ErrBusy) {
			time.Sleep(time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	defer guard.Close()
	var state struct {
		Active int
		Peak   int
	}
	path := filepath.Join(root, "count.json")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return err
		}
	}
	state.Active += delta
	if state.Active > state.Peak {
		state.Peak = state.Active
	}
	return writeJSON(path, state)
}

func TestTwentyJobs(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	jobs := make([]*runChild, 20)
	for i := range jobs {
		jobs[i] = startRunChild(t, root, "count")
	}
	// Each admitted job holds until released, so the jobs themselves expose
	// overlap. Releasing every input allows the full queue to drain.
	for _, job := range jobs {
		fmt.Fprintln(job.input, "release")
	}
	for _, job := range jobs {
		awaitLine(t, job, "admitted")
		finishChild(t, job)
	}
	data, err := os.ReadFile(filepath.Join(root, "count.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Active int
		Peak   int
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Active != 0 || state.Peak > 2 || state.Peak < 1 {
		t.Fatalf("job-measured counts: %s", data)
	}
	t.Logf("20 real commands, two slots; job-measured counts: %s", bytes.TrimSpace(data))
	assertHistoryCount(t, 20)
}

func TestProcessMemoryLearning(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	for i := 0; i < 2; i++ {
		child := startRunChild(t, root, "memory")
		awaitLine(t, child, "memory-ready")
		// The 100 ms sampler must see the descendant's touched allocation.
		time.Sleep(350 * time.Millisecond)
		fmt.Fprintln(child.input, "release")
		finishChild(t, child)
	}
	jobs := assertHistoryCount(t, 2)
	if jobs[0].PeakRSSBytes < 24<<20 || jobs[1].MemoryBytes != jobs[0].PeakRSSBytes || jobs[1].PeakRSSBytes < 24<<20 {
		t.Fatalf("tree sample and reused prediction: %+v", jobs)
	}
	t.Logf("tree peak %d bytes; next run budget %d bytes", jobs[0].PeakRSSBytes, jobs[1].MemoryBytes)
}

func memoryJob(root string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, "-test.run=^TestMemoryChild$", "--", root)
	command.Env = append(os.Environ(), "BATON_MEMORY_TEST_HELPER=1")
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	return command.Run()
}

func TestMemoryChild(t *testing.T) {
	if os.Getenv("BATON_MEMORY_TEST_HELPER") != "1" {
		return
	}
	memory := make([]byte, 32<<20)
	for i := range memory {
		memory[i] = byte(i)
	}
	fmt.Println("memory-ready")
	bufio.NewScanner(os.Stdin).Scan()
	runtime.KeepAlive(memory)
	os.Exit(0)
}

func startRunChild(t *testing.T, root, mode string) *runChild {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	t.Cleanup(cancel)
	child := &runChild{}
	child.command = exec.CommandContext(ctx, executable, "-test.run=^TestRunProcess$", "--", root, "baton", mode)
	child.command.Env = append(os.Environ(), runHelper+"=1", "GORACE=atexit_sleep_ms=0")
	child.command.Stderr = &child.err
	child.input, err = child.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.command.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 4)
	child.lines = lines
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	t.Cleanup(func() {
		_ = child.input.Close()
		if !child.waited {
			_ = child.command.Process.Kill()
			_ = child.command.Wait()
		}
	})
	return child
}

func awaitLine(t *testing.T, child *runChild, want string) {
	t.Helper()
	select {
	case line := <-child.lines:
		if line != want {
			t.Fatalf("line %q, want %q", line, want)
		}
	case <-time.After(processTimeout):
		t.Fatalf("timed out awaiting %q", want)
	}
}

func finishChild(t *testing.T, child *runChild) {
	t.Helper()
	err := child.command.Wait()
	child.waited = true
	if err != nil {
		t.Fatalf("command failed: %v (%s)", err, child.err.String())
	}
}

func assertHistoryCount(t *testing.T, count int) []jobRecord {
	t.Helper()
	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "history.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var jobs []jobRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var job jobRecord
		if err := json.Unmarshal([]byte(line), &job); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	if len(jobs) != count {
		t.Fatalf("history count %d, want %d", len(jobs), count)
	}
	return jobs
}

func TestRunOff(t *testing.T) {
	isolateHome(t)
	t.Setenv("BATON", "off")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	code, err := runJob(context.Background(), baseOptions([]string{executable, "-test.run=^TestNothing$"}), streams{nil, io.Discard, io.Discard})
	if err != nil || code != 0 {
		t.Fatalf("bypass code %d error %v", code, err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")); !os.IsNotExist(err) {
		t.Fatalf("bypass touched state: %v", err)
	}
}

func TestGuardCancellation(t *testing.T) {
	isolateHome(t)
	store, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	guard, err := store.guard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := store.predict(ctx, strconv.Itoa(1)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("guard ignored cancellation: %v", err)
	}
}
