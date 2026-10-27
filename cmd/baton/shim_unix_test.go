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
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/queue"
)

func TestShimFlow(t *testing.T) {
	isolateShim(t)
	baton := buildShimCLI(t)
	writeShimFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml"), "[lanes.encode]\nslots = 1\nmemory_gate = false\n", 0600)
	realDir := t.TempDir()
	real := filepath.Join(realDir, "ffmpeg")
	writeShimFile(t, real, "#!/bin/sh\nprintf '%s\\n' \"$@\"\nexit 37\n", 0700)
	t.Setenv("PATH", realDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", "/bin/zsh")
	installed := shimCLI(t, baton, 0, "shim", "install", "ffmpeg")
	dir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "shims")
	if !strings.Contains(installed, "export PATH=") || !strings.Contains(installed, "zsh") {
		t.Fatalf("missing shell/PATH guidance: %s", installed)
	}
	shimCLI(t, baton, 0, "shim", "install", "ffmpeg")
	if listed := shimCLI(t, baton, 0, "shim", "list"); !strings.Contains(listed, "ffmpeg") {
		t.Fatalf("missing installed shim: %s", listed)
	}
	// Skip the shim directory even through a symlink alias and resolve at call
	// time: the original executable can be replaced after installation.
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{alias, dir, realDir}, string(os.PathListSeparator)))
	shim := filepath.Join(dir, "ffmpeg")
	out := shimCLI(t, "/bin/sh", 37, "-c", `exec ffmpeg "$@"`, "sh", "one argument", "", "$(untouched)", "a'b")
	if out != "one argument\n\n$(untouched)\na'b\n" {
		t.Fatalf("argv changed: %q", out)
	}
	history := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "history.jsonl")
	data, err := os.ReadFile(history)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Lane string `json:"lane"`
		Exit int    `json:"exit_code"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil || record.Lane != "encode" || record.Exit != 37 {
		t.Fatalf("shim did not use auto lane: %s (%v)", data, err)
	}
	writeShimFile(t, real, "#!/bin/sh\nprintf replacement\n", 0700)
	if got := shimCLI(t, shim, 0); got != "replacement" {
		t.Fatalf("stale resolution: %q", got)
	}
	// A real admitted tool can invoke its shim with different arguments.
	// The child shares the parent's reservation instead of queueing again.
	writeShimFile(t, real, "#!/bin/sh\ncase \"$1\" in outer) exec ffmpeg inner ;; *) printf nested ;; esac\n", 0700)
	if got := shimCLI(t, shim, 0, "outer"); got != "nested" {
		t.Fatalf("nested invocation: %q", got)
	}
	data, err = os.ReadFile(history)
	if err != nil || bytes.Count(data, []byte("\n")) != 3 {
		t.Fatalf("nested child acquired separate telemetry: %s, %v", data, err)
	}

	// Uninstall removes owned files and leaves unrelated directory entries.
	unrelated := filepath.Join(dir, "keep")
	writeShimFile(t, unrelated, "user data", 0600)
	shimCLI(t, baton, 0, "shim", "uninstall")
	if _, err := os.Stat(shim); !os.IsNotExist(err) {
		t.Fatalf("shim remains: %v", err)
	}
	if got, err := os.ReadFile(unrelated); err != nil || string(got) != "user data" {
		t.Fatalf("unrelated file: %q, %v", got, err)
	}
	shimCLI(t, baton, 0, "shim", "uninstall")
}

func TestShimBypassAndQueue(t *testing.T) {
	isolateShim(t)
	baton := buildShimCLI(t)
	realDir := t.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		writeShimFile(t, filepath.Join(realDir, name), "#!/bin/sh\nprintf executed\n", 0700)
	}
	t.Setenv("PATH", realDir)
	shimCLI(t, baton, 0, "shim", "install", "ffmpeg", "ffprobe")
	dir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "shims")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+realDir)
	config := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml")
	writeShimFile(t, config, "[lanes.encode]\nslots = 1\nmemory_gate = false\n", 0600)
	root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")
	q, err := queue.New(root, "encode", 1)
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
	defer lease.Release()
	// Fast paths must pass an occupied lane without history or job records.
	for _, args := range [][]string{{"ffmpeg", "-version"}, {"ffmpeg", "-f", "null", "-"}, {"ffprobe", "movie.mp4"}} {
		if got := shimCLI(t, filepath.Join(dir, args[0]), 0, args[1:]...); got != "executed" {
			t.Fatal(got)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "history.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("fast path recorded history: %v", err)
	}
	// A descendant already belongs to an admitted process tree. Scheduling
	// it again could wait forever on the parent's occupied lane.
	t.Setenv("_BATON_SHIM_JOB", "1")
	if got := shimCLI(t, filepath.Join(dir, "ffmpeg"), 0, "-i", "input"); got != "executed" {
		t.Fatalf("nested job: %q", got)
	}
	t.Setenv("_BATON_SHIM_JOB", "")
	// A non-fast invocation must actually wait, then run after release.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(dir, "ffmpeg"), "-i", "input")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	deadline := time.Now().Add(3 * time.Second)
	var waiting int
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(filepath.Join(root, "jobs"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") {
				waiting++
			}
		}
		if waiting > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waiting != 1 {
		t.Fatalf("shim never queued; stderr not read until child exits")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	waitCode(t, command, 0)
	if stdout.String() != "executed" {
		t.Fatalf("queued stdout %q, stderr %q", &stdout, &stderr)
	}
	// BATON=off must bypass even invalid TOML and create no new history.
	before, err := os.ReadFile(filepath.Join(root, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeShimFile(t, config, "invalid = [", 0600)
	t.Setenv("BATON", "off")
	if got := shimCLI(t, filepath.Join(dir, "ffmpeg"), 0, "-i", "input"); got != "executed" {
		t.Fatal(got)
	}
	after, err := os.ReadFile(filepath.Join(root, "history.jsonl"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("bypass wrote history: %v", err)
	}
}

func TestShimRecursion(t *testing.T) {
	isolateShim(t)
	baton := buildShimCLI(t)
	realDir := t.TempDir()
	real := filepath.Join(realDir, "ffmpeg")
	writeShimFile(t, real, "#!/bin/sh\nexec ffmpeg \"$@\"\n", 0700)
	t.Setenv("PATH", realDir)
	shimCLI(t, baton, 0, "shim", "install", "ffmpeg")
	dir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "shims")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+realDir)
	t.Setenv("BATON", "off")
	out := shimCLI(t, filepath.Join(dir, "ffmpeg"), 126)
	if !strings.Contains(out, "recursion") {
		t.Fatalf("missing recursion error: %s", out)
	}
	// A symlink back to the shim cannot be selected as the real executable.
	if err := os.Remove(real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "ffmpeg"), real); err != nil {
		t.Fatal(err)
	}
	out = shimCLI(t, filepath.Join(dir, "ffmpeg"), 126)
	if !strings.Contains(out, "recursion") {
		t.Fatalf("missing symlink recursion error: %s", out)
	}
}

func buildShimCLI(t *testing.T) string {
	t.Helper()
	// Build before replacing PATH; all subprocess home/XDG values are isolated.
	binary := filepath.Join(t.TempDir(), "baton's binary")
	command := exec.Command("go", "build", "-modcacherw", "-o", binary, ".")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return binary
}

func shimCLI(t *testing.T, binary string, want int, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	data, err := command.CombinedOutput()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%v: %v (%s)", args, err, data)
		}
		code = exit.ExitCode()
	}
	if ctx.Err() != nil || code != want {
		t.Fatalf("%v: exit %d, want %d, context %v\n%s", args, code, want, ctx.Err(), data)
	}
	return string(data)
}

func writeShimFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func isolateShim(t *testing.T) {
	t.Helper()
	isolateHome(t)
	t.Setenv("_BATON_SHIM_CHAIN", "")
	t.Setenv("_BATON_SHIM_JOB", "")
}
