//go:build darwin || linux

package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestKilledRunnerAdmits(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	first := startRunChild(t, root, "count")
	second := startRunChild(t, root, "count")
	awaitLine(t, first, "admitted")
	awaitLine(t, second, "admitted")
	waiter := startRunChild(t, root, "count")
	var running jobRecord
	deadline := time.Now().Add(processTimeout)
	for time.Now().Before(deadline) {
		files, err := filepath.Glob(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "jobs", "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			var job jobRecord
			if json.Unmarshal(data, &job) == nil && job.OwnerPID == first.command.Process.Pid && job.PID != 0 {
				running = job
			}
		}
		if running.PID != 0 && len(files) == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if running.PID == 0 {
		t.Fatal("running identity not published")
	}
	// The recorded child is test-owned. SIGKILL of baton cannot execute
	// cleanup in its descendants; explicitly reap this fake after the check.
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = syscall.Kill(-running.PID, syscall.SIGKILL)
		}
	})
	started := time.Now()
	if err := first.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-waiter.lines:
		if line != "admitted" {
			t.Fatalf("waiter returned %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("SIGKILL did not release the slot within one second")
	}
	t.Logf("SIGKILL to next command admission: %s", time.Since(started))
	if err := syscall.Kill(-running.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	reaped = true
	// Cmd.Wait also drains inherited stderr pipes. Wait only after closing
	// the orphan's group, so pipe lifetime cannot mask slot-release latency.
	if err := first.command.Wait(); err == nil {
		t.Fatal("killed runner returned success")
	}
	first.waited = true
	fmt.Fprintln(second.input, "release")
	fmt.Fprintln(waiter.input, "release")
	finishChild(t, second)
	finishChild(t, waiter)
}
