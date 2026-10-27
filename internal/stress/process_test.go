//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
)

// Exercise real independent workers and a compiled baton; observations and
// event boundaries are faked here so unrelated host pressure cannot abort CI.
func TestPhaseProcesses(t *testing.T) {
	bench := processBench(t)
	bench.opts.baton = filepath.Join(t.TempDir(), "baton")
	build := exec.Command("go", "build", "-modcacherw", "-o", bench.opts.baton, "../../cmd/baton")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build baton: %v\n%s", err, out)
	}
	for _, mode := range []string{"sequential", "unqueued", "queued"} {
		p := bench.runPhase(context.Background(), mode)
		if p.Error != "" || p.Completed != bench.opts.jobs || p.Failed != 0 || p.Samples < 2 || p.WallSeconds <= 0 {
			t.Fatalf("%s: %+v", mode, p)
		}
		want := 1
		if mode == "unqueued" {
			want = bench.opts.jobs
		}
		if p.MaxActive != want {
			t.Fatalf("%s overlap %d, want %d", mode, p.MaxActive, want)
		}
		t.Logf("%s: completed=%d overlap=%d samples=%d", mode, p.Completed, p.MaxActive, p.Samples)
	}
	// Inspect the policy actually given to the compiled CLI, including gate on.
	policy, err := os.ReadFile(filepath.Join(bench.opts.output, "queued", "home", "XDG_CONFIG_HOME", "baton", "config.toml"))
	if err != nil || !strings.Contains(string(policy), "memory_gate = true") {
		t.Fatalf("policy: %s %v", policy, err)
	}
}

// A timeout must stop and reap only this phase's children. An independently
// started sentinel stays alive until this test closes its own stdin pipe.
func TestAbortPreservesOther(t *testing.T) {
	bench := processBench(t)
	bench.opts.hold, bench.opts.timeout = time.Minute, 400*time.Millisecond
	root := t.TempDir()
	gate := filepath.Join(root, "go")
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(root, "sentinel.json")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := append([]string{}, bench.prefix...)
	args = append(args, "--worker", "--bytes", "1048576", "--hold", "1m", "--record", record, "--gate", gate)
	sentinel := exec.CommandContext(ctx, bench.worker, args...)
	sentinel.Env = os.Environ()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	sentinel.Stdin = read
	if err := sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = write.Close()
		cancel()
		_ = sentinel.Wait()
	})
	awaitRecord(t, record)
	start := time.Now()
	p := bench.runPhase(context.Background(), "unqueued")
	if p.Error == "" || p.Completed != 0 || time.Since(start) > 6*time.Second {
		t.Fatalf("timeout not enforced: %+v", p)
	}
	var job jobRecord
	data, err := os.ReadFile(record)
	if err != nil || json.Unmarshal(data, &job) != nil || !job.Ended.IsZero() {
		t.Fatalf("sentinel stopped by unrelated phase: %s %v", data, err)
	}
	for i := 0; i < bench.opts.jobs; i++ {
		data, err := os.ReadFile(jobPath(filepath.Join(bench.opts.output, "unqueued"), i))
		if err != nil || json.Unmarshal(data, &job) != nil || job.Ended.IsZero() || job.Completed {
			t.Fatalf("owned worker %d did not exit on cancellation: %s %v", i, data, err)
		}
	}
	t.Logf("owned timeout returned in %s; independent sentinel still running", time.Since(start))
}

// Critical samples and unavailable event sources are failures, never zeroes.
func TestObservationFailure(t *testing.T) {
	bench := processBench(t)
	bench.events = func(context.Context) (endEvents, error) { return nil, errors.New("events unavailable") }
	p := bench.runPhase(context.Background(), "unqueued")
	if !strings.Contains(p.Error, "events unavailable") || p.Completed != 0 {
		t.Fatalf("lost event failure: %+v", p)
	}
	bench = processBench(t)
	bench.opts.hold = time.Minute
	count := 0
	bench.probe = func(context.Context) (pressure.Snapshot, error) {
		count++
		sample := normalSample()
		if count > 1 {
			sample.Level = pressure.Critical
		}
		return sample, nil
	}
	p = bench.runPhase(context.Background(), "unqueued")
	if !strings.Contains(p.Error, "critical pressure") || p.Worst != pressure.Critical || p.Completed != 0 {
		t.Fatalf("critical observation ignored: %+v", p)
	}
}

// The report must retain missing/failed observations and return nonzero.
func TestReportFailure(t *testing.T) {
	isolateTest(t)
	var output bytes.Buffer
	dir := t.TempDir()
	result := report{Jobs: 2, Phases: []phase{{Mode: "sequential", Error: "events unavailable"}}}
	if err := finishReport(dir, result, &output); err == nil || strings.Contains(output.String(), "PASS:") {
		t.Fatalf("false success: %v %s", err, &output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil || !strings.Contains(string(data), `"passed": false`) || !strings.Contains(string(data), "events unavailable") {
		t.Fatalf("missing failure evidence: %s %v", data, err)
	}
}

// Output can be a Docker Desktop bind mount. Kernel locks must stay on the
// native temporary filesystem; completed evidence is copied out afterward.
func TestStateOutsideArtifacts(t *testing.T) {
	bench := processBench(t)
	bench.opts.jobs = 1
	bench.opts.baton = filepath.Join(t.TempDir(), "baton")
	program := "#!/bin/sh\nprintf '%s\\n' \"$HOME\" \"$XDG_STATE_HOME\"\nexit 9\n"
	if err := os.WriteFile(bench.opts.baton, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	p := bench.runPhase(context.Background(), "queued")
	if !strings.Contains(p.Error, "exit status 9") {
		t.Fatalf("fixture did not execute: %+v", p)
	}
	data, err := os.ReadFile(filepath.Join(bench.opts.output, "queued", "jobs.log"))
	lines := strings.Fields(string(data))
	if err != nil || len(lines) != 2 {
		t.Fatalf("missing private environment: %q %v", data, err)
	}
	for _, path := range lines {
		if strings.HasPrefix(path, bench.opts.output+string(os.PathSeparator)) {
			t.Fatalf("scheduler state uses artifact filesystem: %s", path)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary state not removed: %s %v", path, err)
		}
	}
	policy := filepath.Join(bench.opts.output, "queued", "home", "XDG_CONFIG_HOME", "baton", "config.toml")
	if _, err := os.Stat(policy); err != nil {
		t.Fatalf("configuration evidence not retained: %v", err)
	}
}

func processBench(t *testing.T) benchmark {
	t.Helper()
	isolateTest(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return benchmark{
		opts:   options{jobs: 2, bytes: 1 << 20, hold: 250 * time.Millisecond, maxWait: time.Millisecond, timeout: 20 * time.Second, output: t.TempDir()},
		worker: executable, prefix: []string{"-test.run=^TestWorkerChild$", "--"}, progress: io.Discard,
		probe: func(context.Context) (pressure.Snapshot, error) { return normalSample(), nil },
		events: func(context.Context) (endEvents, error) {
			return func(context.Context, time.Time, time.Time, string) (uint64, error) { return 0, nil }, nil
		},
	}
}

func normalSample() pressure.Snapshot {
	return pressure.Snapshot{TotalBytes: 16 << 30, AvailableBytes: 8 << 30, Level: pressure.Normal}
}

func isolateTest(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("BATON", "off")
	t.Setenv("GOCACHE", filepath.Join(home, "go-cache"))
	t.Setenv("GOPATH", filepath.Join(home, "go"))
	t.Setenv("GOMODCACHE", filepath.Join(home, "go", "pkg", "mod"))
	keys := map[string]bool{"XDG_CONFIG_HOME": true, "XDG_STATE_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true, "XDG_RUNTIME_DIR": true, "XDG_CONFIG_DIRS": true, "XDG_DATA_DIRS": true}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = true
		}
	}
	for key := range keys {
		t.Setenv(key, filepath.Join(home, key))
	}
}

func awaitRecord(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), `"pid"`) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no worker record: %s", path)
}

func TestWorkerChild(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--worker" {
			continue
		}
		if err := entry(os.Args[i:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}
