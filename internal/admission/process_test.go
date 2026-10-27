package admission

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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
	"github.com/sushilk1991/baton/internal/queue"
)

const (
	processTimeout  = 5 * time.Second
	processRecovery = 100 * time.Millisecond
	helperEnv       = "BATON_ADMISSION_TEST_HELPER"
)

type gateChild struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	lines  <-chan string
	stderr bytes.Buffer
	waited bool
}

func TestPressureProcesses(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	warnFile := writeSample(t, root, "first", pressure.Warn)
	normalFile := writeSample(t, root, "second", pressure.Normal)
	first := startGateChild(t, root, warnFile, processTimeout)
	awaitLine(t, first, "queued")
	awaitLine(t, first, "sample warn")
	assertWaiting(t, first)
	second := startGateChild(t, root, normalFile, processTimeout)
	awaitLine(t, second, "queued")
	awaitLine(t, second, "sample normal")
	assertWaiting(t, second)

	// A healthy follower cannot pass the head while its memory gate recovers.
	writeSample(t, root, "first", pressure.Normal)
	awaitLine(t, first, "sample normal")
	awaitLine(t, first, "acquired")
	assertWaiting(t, second)
	finishGateChild(t, first)
	awaitLine(t, second, "acquired")
	finishGateChild(t, second)
	if first.stderr.Len() != 0 || second.stderr.Len() != 0 {
		t.Fatalf("unexpected warning: %s %s", first.stderr.String(), second.stderr.String())
	}
	t.Log("warn waited; stable normal admitted; follower retained FIFO position")
}

func TestTimeoutProcess(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	file := writeSample(t, root, "timeout", pressure.Critical)
	child := startGateChild(t, root, file, 100*time.Millisecond)
	awaitLine(t, child, "queued")
	awaitLine(t, child, "sample critical")
	awaitLine(t, child, "acquired")
	finishGateChild(t, child)
	if !strings.Contains(child.stderr.String(), "WARNING: max_wait 100ms reached") {
		t.Fatalf("missing timeout warning: %q", child.stderr.String())
	}
	t.Log(strings.TrimSpace(child.stderr.String()))
}

func TestAdmissionProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	if err := runGateChild(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runGateChild() error {
	args := os.Args[len(os.Args)-3:]
	maxWait, err := time.ParseDuration(args[2])
	if err != nil {
		return err
	}
	q, err := queue.New(args[0], "render", 1)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	ticket, err := q.Enqueue(ctx, 1, queue.Normal)
	if err != nil {
		return err
	}
	defer ticket.Close()
	provider := &fileProvider{file: args[1]}
	gate, err := New(provider, Options{
		MaxWait: maxWait, PollInterval: 5 * time.Millisecond, RecoveryWindow: processRecovery,
	})
	if err != nil {
		return err
	}
	fmt.Println("queued")
	started := time.Now()
	lease, err := gate.Acquire(ctx, ticket, 1<<30, os.Stderr)
	if err != nil {
		return err
	}
	defer lease.Release()
	// Measure inside the waiter so parent scheduling cannot falsify timing.
	if time.Since(started) < maxWait && provider.last != pressure.Normal {
		return errors.New("pressure admission preceded max_wait")
	}
	if time.Since(started) < maxWait && provider.blocked && time.Since(provider.normalSince) < processRecovery {
		return errors.New("pressure admission preceded stable recovery")
	}
	fmt.Println("acquired")
	if !bufio.NewScanner(os.Stdin).Scan() {
		return errors.New("release barrier closed")
	}
	return lease.Release()
}

type fileProvider struct {
	file        string
	last        pressure.Level
	blocked     bool
	normalSince time.Time
}

func (provider *fileProvider) Sample(ctx context.Context) (pressure.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return pressure.Snapshot{}, err
	}
	data, err := os.ReadFile(provider.file)
	if err != nil {
		return pressure.Snapshot{}, err
	}
	var sample pressure.Snapshot
	if err := json.Unmarshal(data, &sample); err != nil {
		return pressure.Snapshot{}, err
	}
	if sample.Level != provider.last {
		fmt.Println("sample", sample.Level)
		provider.last = sample.Level
		provider.normalSince = time.Now()
	}
	if sample.Level != pressure.Normal {
		provider.blocked = true
	}
	return sample, nil
}

func writeSample(t *testing.T, root, name string, level pressure.Level) string {
	t.Helper()
	file := filepath.Join(root, name+".json")
	data, err := json.Marshal(pressure.Snapshot{Level: level, TotalBytes: 8 << 30, AvailableBytes: 6 << 30})
	if err != nil {
		t.Fatal(err)
	}
	// Publish complete observations so concurrent reads never see partial JSON.
	temporary := file + ".new"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, file); err != nil {
		t.Fatal(err)
	}
	return file
}

func startGateChild(t *testing.T, root, file string, maxWait time.Duration) *gateChild {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	t.Cleanup(cancel)
	child := &gateChild{}
	child.cmd = exec.CommandContext(ctx, executable, "-test.run=^TestAdmissionProcess$", "--", root, file, maxWait.String())
	child.cmd.Env = append(os.Environ(), helperEnv+"=1", "GORACE=atexit_sleep_ms=0")
	child.cmd.Stderr = &child.stderr
	stdout, err := child.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.input, err = child.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 8)
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
			_ = child.cmd.Process.Kill()
			_ = child.cmd.Wait()
		}
	})
	return child
}

func awaitLine(t *testing.T, child *gateChild, want string) {
	t.Helper()
	select {
	case line, ok := <-child.lines:
		if !ok || line != want {
			t.Fatalf("child line = %s (open %t), want %s", strconv.Quote(line), ok, want)
		}
	case <-time.After(processTimeout):
		t.Fatalf("timed out awaiting %s", want)
	}
}

func assertWaiting(t *testing.T, child *gateChild) {
	t.Helper()
	select {
	case line := <-child.lines:
		t.Fatalf("child ran while blocked: %q", line)
	case <-time.After(processRecovery / 2):
	}
}

func finishGateChild(t *testing.T, child *gateChild) {
	t.Helper()
	if _, err := fmt.Fprintln(child.input, "release"); err != nil {
		t.Fatal(err)
	}
	err := child.cmd.Wait()
	child.waited = true
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, child.stderr.String())
	}
}
