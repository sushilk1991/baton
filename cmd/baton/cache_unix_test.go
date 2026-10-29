//go:build darwin || linux

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real subprocesses prove that hits restore bytes without rerunning the tool.
func TestCacheCLI(t *testing.T) {
	isolateHome(t)
	baton := buildShimCLI(t)
	root := t.TempDir()
	writeShimFile(t, filepath.Join(root, "src", "nested", "input"), "first", 0600)
	writeShimFile(t, filepath.Join(root, "render"), "#!/bin/sh\nprintf x >> calls\ncat src/nested/input > result\n", 0700)
	configPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml")
	writeShimFile(t, configPath, "[cache]\nenv=['BATON_CACHE_TEST']\n", 0600)
	invoke := func(extra ...string) string {
		t.Helper()
		args := append([]string{"run", "--cache-key", "src/**", "--output", "result", "--", "./render"}, extra...)
		command := exec.Command(baton, args...)
		command.Dir = root
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v: %s", args, err, data)
		}
		return string(data)
	}
	check := func(want string, calls string) {
		t.Helper()
		for name, value := range map[string]string{"result": want, "calls": calls} {
			data, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || string(data) != value {
				t.Fatalf("%s=%q, %v; want %q", name, data, err, value)
			}
		}
	}
	if out := invoke(); strings.Contains(out, "cache hit") {
		t.Fatalf("miss reported a hit: %q", out)
	}
	check("first", "x")
	if err := os.Remove(filepath.Join(root, "result")); err != nil {
		t.Fatal(err)
	}
	// A hit skips the command, so it says so instead of finishing silently.
	if out := invoke(); out != "baton: cache hit; restored result without running the command\n" {
		t.Fatalf("hit output %q", out)
	}
	check("first", "x")
	// A consumer may truncate a hardlinked output. Detect that corruption
	// before reusing it, and execute the command to regenerate correct bytes.
	writeShimFile(t, filepath.Join(root, "result"), "corrupt", 0600)
	invoke()
	check("first", "xx")
	writeShimFile(t, filepath.Join(root, "src", "nested", "input"), "second", 0600)
	invoke()
	check("second", "xxx")
	t.Setenv("BATON_CACHE_TEST", "changed")
	invoke()
	check("second", "xxxx")
	t.Setenv("BATON_CACHE_IGNORED", "ignored")
	invoke()
	check("second", "xxxx")
	invoke("different-argv")
	check("second", "xxxxx")
	listed := shimCLI(t, baton, exitOK, "cache", "ls")
	if !strings.Contains(listed, "cache entry(s)") {
		t.Fatal(listed)
	}
	history := shimCLI(t, baton, exitOK, "history", "--json")
	hits := 0
	for _, line := range strings.Split(strings.TrimSpace(history), "\n") {
		var job struct {
			CacheHit bool `json:"cache_hit"`
		}
		if err := json.Unmarshal([]byte(line), &job); err != nil {
			t.Fatal(err)
		}
		if job.CacheHit {
			hits++
		}
	}
	if hits != 2 {
		t.Fatalf("cache hit history=%d, want 2: %s", hits, history)
	}
	writeShimFile(t, configPath, "[cache]\nmax_size='1B'\n", 0600)
	pruned := shimCLI(t, baton, exitOK, "cache", "prune")
	if !strings.Contains(shimCLI(t, baton, exitOK, "cache", "ls"), "0 cache entry(s)") {
		t.Fatal(pruned)
	}
	t.Log("miss -> restore hit -> corruption miss -> input/env/argv invalidation; 2 recorded hits")
	t.Log(strings.TrimSpace(listed))
	t.Log(strings.TrimSpace(pruned))
}

// Identical callers serialize the miss so only one process generates output.
func TestCacheConcurrentCLI(t *testing.T) {
	isolateHome(t)
	baton := buildShimCLI(t)
	root := t.TempDir()
	writeShimFile(t, filepath.Join(root, "input"), "payload", 0600)
	writeShimFile(t, filepath.Join(root, "render"), "#!/bin/sh\nprintf x >> calls\nsleep 0.1\ncat input > output\n", 0700)
	commands := make([]*exec.Cmd, 8)
	for i := range commands {
		commands[i] = exec.Command(baton, "run", "--cache-key", "input", "--output", "output", "--", "./render")
		commands[i].Dir = root
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		waitCode(t, command, exitOK)
	}
	data, err := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil || string(data) != "x" {
		t.Fatalf("render calls=%q: %v", data, err)
	}
	t.Log("8 concurrent callers executed the renderer once")
}

// Failed commands and unusable caches must preserve execution and exit codes.
func TestCacheFailureCLI(t *testing.T) {
	isolateHome(t)
	baton := buildShimCLI(t)
	root := t.TempDir()
	writeShimFile(t, filepath.Join(root, "input"), "input", 0600)
	writeShimFile(t, filepath.Join(root, "render"), "#!/bin/sh\nprintf x >> calls\nprintf partial > output\nexit 7\n", 0700)
	invoke := func(want int) string {
		t.Helper()
		command := exec.Command(baton, "run", "--cache-key", "input", "--output", "output", "--", "./render")
		command.Dir = root
		data, err := command.CombinedOutput()
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want {
			t.Fatalf("exit=%d want=%d: %s", code, want, data)
		}
		return string(data)
	}
	invoke(7)
	invoke(7)
	if listed := shimCLI(t, baton, exitOK, "cache", "ls"); !strings.Contains(listed, "0 cache entry(s)") {
		t.Fatal(listed)
	}
	// A successful job lacking its declared output still returns its own status.
	writeShimFile(t, filepath.Join(root, "render"), "#!/bin/sh\nprintf x >> calls\nrm -f output\n", 0700)
	if out := invoke(exitOK); !strings.Contains(out, "cache store") {
		t.Fatal(out)
	}
	// Cache path failure cannot prevent the useful job from executing.
	blocked := filepath.Join(root, "blocked")
	writeShimFile(t, blocked, "regular file", 0600)
	writeShimFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml"), fmt.Sprintf("[cache]\ndir=%q\n", blocked), 0600)
	if out := invoke(exitOK); !strings.Contains(out, "cache lookup") {
		t.Fatal(out)
	}
	data, err := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil || string(data) != "xxxx" {
		t.Fatalf("executions=%q: %v", data, err)
	}
	t.Setenv("BATON", "off")
	if out := invoke(exitOK); strings.Contains(out, "cache") {
		t.Fatalf("bypass touched cache: %s", out)
	}
}
