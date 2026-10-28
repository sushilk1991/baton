//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/queue"
)

const nestedDeadline = 4 * time.Second

// Installed recipes must finish even when their tools invoke another shim.
func TestNestedInstalledRecipes(t *testing.T) {
	isolateShim(t)
	binary := buildShimCLI(t)
	tools := t.TempDir()
	writeShimFile(t, filepath.Join(tools, "baton"), "#!/bin/sh\nexec \"$BATON_TEST_BINARY\" \"$@\"\n", 0700)
	t.Setenv("BATON_TEST_BINARY", binary)
	for _, name := range []string{"ffmpeg", "whisper", "npm", "go"} {
		// Different argv permits a legitimate recursive tool call; identical
		// argv would correctly trigger the shim's recursion guard.
		body := "#!/bin/sh\nif [ \"$BATON_TEST_NESTED\" = 1 ]; then printf nested; exit 0; fi\nexport BATON_TEST_NESTED=1\nexec " + name + " \"$@\" nested\n"
		writeShimFile(t, filepath.Join(tools, name), body, 0700)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	shimCLI(t, binary, 0, "shim", "install", "ffmpeg", "whisper", "npm", "go")
	shimDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "shims")
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	shimCLI(t, binary, 0, "init", "--yes")
	skill, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".agents", "skills", "baton", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	snippet, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".agents", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	recipes := []string{"baton run --lane install -- npm ci"}
	for _, line := range strings.Split(string(skill), "\n") {
		if strings.HasPrefix(line, "baton run ") {
			recipes = append(recipes, line)
		}
	}
	prefix := "Queue heavy commands with "
	for _, line := range strings.Split(string(snippet), "\n") {
		if strings.HasPrefix(line, prefix) {
			recipes = append(recipes, strings.ReplaceAll(strings.TrimPrefix(line, prefix), "<command> [args...].", "npm ci"))
		}
	}
	if len(recipes) != 6 {
		t.Fatalf("expected explicit command, four skill recipes, and snippet: %q", recipes)
	}
	for _, recipe := range recipes {
		t.Run(recipe, func(t *testing.T) {
			// Each case gets native temporary lock state, including in Docker.
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			writeNestedConfig(t)
			fields := strings.Fields(recipe)
			if got := runNestedCLI(t, binary, 0, fields[1:]...); got != "nested" {
				t.Fatalf("nested tool stdout %q", got)
			}
			jobs := nestedHistory(t)
			if len(jobs) != 3 || !jobs[0].Nested || !jobs[1].Nested || jobs[2].Nested {
				t.Fatalf("want two nested jobs and one reservation: %+v", jobs)
			}
			t.Logf("%s: completed; 3 history records, 2 nested", recipe)
		})
	}
}

// Lane inheritance must survive a different-lane child and preserve exits.
func TestNestedLaneChain(t *testing.T) {
	isolateShim(t)
	binary := buildShimCLI(t)
	writeNestedConfig(t)
	runNestedCLI(t, binary, 3, "run", "--lane", "install", "--", binary,
		"run", "--lane", "encode", "--", binary,
		"run", "--lane", "install", "--", "/bin/sh", "-c", "exit 3")
	jobs := nestedHistory(t)
	if len(jobs) != 3 || !jobs[0].Nested || jobs[0].Lane != "install" || jobs[1].Nested || jobs[1].Lane != "encode" || jobs[2].Nested {
		t.Fatalf("want inherited install and separate encode reservation: %+v", jobs)
	}
	for _, job := range jobs {
		if job.ExitCode != 3 {
			t.Fatalf("lost child exit: %+v", job)
		}
	}
	t.Log("install -> encode -> install completed; only last install nested; all exits 3")
	// All reservations must be available again after the failing job ends.
	for _, lane := range []string{"install", "encode"} {
		view, err := queue.Observe(context.Background(), filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton"), lane, 1)
		if err != nil || view.Used != 0 || len(view.Waiting) != 0 {
			t.Fatalf("stranded %s: %+v, %v", lane, view, err)
		}
	}
}

// A shim for a different lane must still respect an occupied reservation.
func TestNestedOtherLaneQueues(t *testing.T) {
	isolateShim(t)
	binary := buildShimCLI(t)
	writeNestedConfig(t)
	tools := t.TempDir()
	writeShimFile(t, filepath.Join(tools, "ffmpeg"), "#!/bin/sh\nprintf finished\n", 0700)
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	shimCLI(t, binary, 0, "shim", "install", "ffmpeg")
	shim := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "shims", "ffmpeg")
	root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")
	q, err := queue.New(root, "encode", 1)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	defer ticket.Close()
	lease, err := ticket.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	ctx, cancel := context.WithTimeout(context.Background(), nestedDeadline)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "run", "--lane", "install", "--", shim, "-i", "a", "b")
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = time.Second
	var out, diag bytes.Buffer
	command.Stdout, command.Stderr = &out, &diag
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Signal(syscall.SIGTERM)
			_ = command.Wait()
		}
	})
	var waiting bool
	deadline := time.Now().Add(nestedDeadline / 2)
	for time.Now().Before(deadline) {
		view, err := queue.Observe(ctx, root, "encode", 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Waiting) == 1 {
			waiting = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("encode shim bypassed its occupied lane inside install")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil || out.String() != "finished" {
		t.Fatalf("different-lane child: %v, stdout %q, stderr %q", err, &out, &diag)
	}
	jobs := nestedHistory(t)
	if len(jobs) != 2 || jobs[0].Nested || jobs[1].Nested {
		t.Fatalf("different lane must own its reservation: %+v", jobs)
	}
	t.Log("encode shim waited inside install, then completed after encode release")
}

func writeNestedConfig(t *testing.T) {
	t.Helper()
	var body strings.Builder
	for _, name := range []string{"install", "encode", "ml", "build"} {
		body.WriteString("[lanes." + name + "]\nslots = 1\nmemory_gate = false\n")
	}
	writeShimFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml"), body.String(), 0600)
}

func runNestedCLI(t *testing.T, binary string, want int, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), nestedDeadline)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	// Cancel only this owned runner; it forwards TERM to nested descendants.
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = time.Second
	var out, diag bytes.Buffer
	command.Stdout, command.Stderr = &out, &diag
	err := command.Run()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%v: %v\n%s", args, err, &diag)
		}
		code = exit.ExitCode()
	}
	if ctx.Err() != nil || code != want {
		t.Fatalf("%v: exit %d, want %d, context %v\n%s", args, code, want, ctx.Err(), &diag)
	}
	return out.String()
}

type nestedRecord struct {
	Lane     string `json:"lane"`
	Nested   bool   `json:"nested"`
	ExitCode int    `json:"exit_code"`
}

func nestedHistory(t *testing.T) []nestedRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var jobs []nestedRecord
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var job nestedRecord
		if err := json.Unmarshal(line, &job); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	return jobs
}
