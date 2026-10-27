package lane

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
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	parallelJobs = 20
	testSlots    = 2
	jobDuration  = 80 * time.Millisecond
	crashLimit   = time.Second
	helperEnv    = "BATON_LANE_TEST_HELPER"
)

type process struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	lines  <-chan string
	stderr *bytes.Buffer
	waited bool
}

type counts struct {
	Active   int
	Peak     int
	Started  int
	Finished int
}

func TestParallelProcesses(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	jobs := make([]*process, 0, parallelJobs)

	// Every process reaches the start barrier before any job can acquire.
	for i := 0; i < parallelJobs; i++ {
		job := startProcess(t, root, testSlots, 1, "job")
		awaitLine(t, job, "ready")
		jobs = append(jobs, job)
	}
	for _, job := range jobs {
		if _, err := io.WriteString(job.input, "go\n"); err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range jobs {
		awaitLine(t, job, "acquired")
		waitProcess(t, job)
	}

	// Jobs measure overlap themselves under a separate measurement lock.
	result := readCounts(t, root)
	if result.Active != 0 || result.Peak != testSlots || result.Started != parallelJobs || result.Finished != parallelJobs {
		t.Fatalf("unexpected concurrency counts: %+v", result)
	}
	t.Logf("jobs=%d capacity=%d peak=%d finished=%d", parallelJobs, testSlots, result.Peak, result.Finished)
}

func TestWeightedProcesses(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	heavy := startProcess(t, root, 3, 2, "hold")
	awaitLine(t, heavy, "acquired")
	waiter := startProcess(t, root, 3, 2, "wait")
	awaitLine(t, waiter, "waiting")
	light := startProcess(t, root, 3, 1, "hold")
	awaitLine(t, light, "acquired")

	// A blocked heavy job must not reserve the remaining single slot.
	stopProcess(t, heavy)
	awaitLine(t, waiter, "acquired")
	stopProcess(t, waiter)
	stopProcess(t, light)
}

func TestCrashReleasesSlots(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	holder := startProcess(t, root, testSlots, testSlots, "hold")
	awaitLine(t, holder, "acquired")
	waiter := startProcess(t, root, testSlots, testSlots, "wait")
	awaitLine(t, waiter, "waiting")

	// Kill only our child. Unix Process.Kill is SIGKILL, without cleanup.
	start := time.Now()
	if err := holder.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	awaitLine(t, waiter, "acquired")
	elapsed := time.Since(start)
	if elapsed >= crashLimit {
		t.Fatalf("crash admission took %s, want < %s", elapsed, crashLimit)
	}
	if err := holder.cmd.Wait(); err == nil {
		t.Fatal("killed holder exited successfully")
	}
	holder.waited = true
	stopProcess(t, waiter)
	t.Logf("all %d slots recovered after kill in %s", testSlots, elapsed)
}

func TestLaneProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}

	// Re-exec the test binary to exercise kernel locks across process bounds.
	if err := runHelper(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runHelper() error {
	args := os.Args[len(os.Args)-4:]
	root, mode := args[0], args[3]
	slots, err := strconv.Atoi(args[1])
	if err != nil {
		return err
	}
	weight, err := strconv.Atoi(args[2])
	if err != nil {
		return err
	}
	lane, err := New(root, "x", slots)
	if err != nil {
		return err
	}
	input := bufio.NewScanner(os.Stdin)
	if mode == "job" {
		fmt.Println("ready")
		if !input.Scan() {
			return errors.New("start barrier closed")
		}
	}
	if mode == "wait" {
		lease, err := lane.TryAcquire(weight)
		if lease != nil {
			_ = lease.Release()
		}
		if !errors.Is(err, ErrBusy) {
			return fmt.Errorf("expected occupied lane, got %v", err)
		}
		fmt.Println("waiting")
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	lease, err := lane.Acquire(ctx, weight)
	if err != nil {
		return err
	}
	defer lease.Release()
	fmt.Println("acquired")
	if mode != "job" {
		if !input.Scan() {
			return errors.New("release barrier closed")
		}
		return lease.Release()
	}

	// The workload records active weight before sleeping and after finishing.
	if err := measure(root, weight, slots); err != nil {
		return err
	}
	time.Sleep(jobDuration)
	if err := measure(root, -weight, slots); err != nil {
		return err
	}
	return lease.Release()
}

func measure(root string, change, capacity int) error {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	var lock *lockfile.Lock

	// Serialize only the observer; it never holds the lane allocation guard.
	for {
		var err error
		lock, err = lockfile.Try(filepath.Join(root, "measurement.lock"))
		if err == nil {
			break
		}
		if !errors.Is(err, lockfile.ErrBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	defer lock.Close()
	path := filepath.Join(root, "counts.json")
	var count counts
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(data, &count); err != nil {
			return err
		}
	}
	count.Active += change
	if change > 0 {
		count.Started++
	} else {
		count.Finished++
	}
	if count.Active > count.Peak {
		count.Peak = count.Active
	}
	if count.Active < 0 || count.Active > capacity {
		return fmt.Errorf("capacity violated: %+v", count)
	}
	data, err = json.Marshal(count)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func startProcess(t *testing.T, root string, slots, weight int, mode string) *process {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestLaneProcess$", "--", root, strconv.Itoa(slots), strconv.Itoa(weight), mode)
	cmd.Env = append(os.Environ(), helperEnv+"=1")
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
	lines := make(chan string, 4)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	child := &process{cmd: cmd, input: input, lines: lines, stderr: &stderr}

	// Cleanup never signals anything except the exact process we started.
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

func awaitLine(t *testing.T, child *process, want string) {
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

func waitProcess(t *testing.T, child *process) {
	t.Helper()
	err := child.cmd.Wait()
	child.waited = true
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, child.stderr.String())
	}
}

func stopProcess(t *testing.T, child *process) {
	t.Helper()
	if _, err := io.WriteString(child.input, "release\n"); err != nil {
		t.Fatal(err)
	}
	waitProcess(t, child)
}

func readCounts(t *testing.T, root string) counts {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "counts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result counts
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
