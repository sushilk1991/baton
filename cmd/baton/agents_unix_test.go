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
)

// Run the installed hook command and its returned shell text using a real CLI.
func TestInstalledHookFlow(t *testing.T) {
	isolateHome(t)
	binary := buildShimCLI(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton")
	if err := os.MkdirAll(configRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), []byte("[lanes.encode]\nslots = 1\nmemory_gate = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	fake := filepath.Join(tools, "ffmpeg")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s' \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	shim := exec.Command(binary, "shim", "install", "ffmpeg")
	if output, err := shim.CombinedOutput(); err != nil {
		t.Fatalf("shim install: %v %s", err, output)
	}
	shimDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "shims")
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	install := exec.Command(binary, "init", "--yes", "--claude-hook")
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install: %v %s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks struct {
			Pre []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	hook := exec.Command("sh", "-c", settings.Hooks.Pre[0].Hooks[0].Command)
	input, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "[[ -n \"$PATH\" ]] && ffmpeg 'argv with spaces and a quote: '\"'\"''"}})
	hook.Stdin = bytes.NewReader(input)
	result, err := hook.Output()
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output struct {
			Input struct {
				Command string `json:"command"`
			} `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-c", response.Output.Input.Command)
	// TERM lets our runner forward cancellation to its own queued descendants.
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = time.Second
	var out, diag bytes.Buffer
	command.Stdout, command.Stderr = &out, &diag
	if err := command.Run(); err != nil {
		t.Fatalf("wrapped command: %v %s", err, &diag)
	}
	if out.String() != "argv with spaces and a quote: '" {
		t.Fatalf("argv changed: %q", out.String())
	}
	hook = exec.Command("sh", "-c", settings.Hooks.Pre[0].Hooks[0].Command)
	input, _ = json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": response.Output.Input.Command}})
	hook.Stdin = bytes.NewReader(input)
	result, err = hook.Output()
	if err != nil || len(strings.TrimSpace(string(result))) != 0 {
		t.Fatalf("recursive wrapper: %s %v", result, err)
	}
	uninstall := exec.Command(binary, "init", "--yes", "--uninstall")
	if out, err := uninstall.CombinedOutput(); err != nil {
		t.Fatalf("uninstall: %v %s", err, out)
	}
	t.Log("installed hook preserved quoted argv, ran through encode lane, and uninstalled")
}
