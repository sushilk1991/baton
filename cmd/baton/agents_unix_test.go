//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/queue"
)

const hookShellTail = `; result=$?; printf '\ncaller:%s\n' "$PWD"; exit "$result"`

// Claude's shell must parse the untouched original and retain its snapshot.
func TestHookShellFlow(t *testing.T) {
	isolateShim(t)
	t.Setenv("TMPDIR", t.TempDir())
	binary := buildShimCLI(t)
	writeNestedConfig(t)
	tools := t.TempDir()
	writeShimFile(t, filepath.Join(tools, "npm"), "#!/bin/sh\nprintf 'out:%s\\n' \"${PWD##*/}\"\nprintf 'err\\n' >&2\nexit \"${NPM_EXIT:-0}\"\n", 0700)
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); err != nil {
			continue
		}
		for _, login := range []string{"/bin/tcsh", ""} {
			for _, original := range []string{
				"for d in web; do (cd $d && npm ci); done",
				"npm ci &> log.txt; result=$?; cat log.txt; exit $result",
				"nvm use 20 && npm ci",
				"cd web && npm ci; pwd",
			} {
				t.Run(shell+"/"+login+"/"+original, func(t *testing.T) {
					t.Setenv("SHELL", login)
					if login == "" {
						if err := os.Unsetenv("SHELL"); err != nil {
							t.Fatal(err)
						}
					}
					wrapped := hookCommand(t, binary, original)
					// The original must remain shell syntax visible to permission checks.
					if !strings.HasSuffix(wrapped, " && "+original) || strings.Contains(wrapped, " -c ") {
						t.Errorf("original hidden in wrapper: %s", wrapped)
					}
					for _, code := range []int{0, 3} {
						t.Setenv("NPM_EXIT", strconv.Itoa(code))
						dir := t.TempDir()
						if err := os.Mkdir(filepath.Join(dir, "web"), 0700); err != nil {
							t.Fatal(err)
						}
						prefix := "nvm() { printf 'function:%s\\n' \"$*\"; }; "
						want, wantCode := hookShell(t, shell, dir, prefix+original+hookShellTail)
						got, gotCode := hookShell(t, shell, dir, prefix+wrapped+hookShellTail)
						if got != want || gotCode != wantCode {
							t.Errorf("got exit %d output %q; original exit %d output %q", gotCode, got, wantCode, want)
						}
						awaitHoldSlots(t, 0)
					}
				})
			}
		}
	}
}

func hookCommand(t *testing.T, binary, original string) string {
	t.Helper()
	input, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": original}})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "init", "_hook")
	command.Stdin = bytes.NewReader(input)
	result, err := command.Output()
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
		t.Fatalf("hook output %s: %v", result, err)
	}
	return response.Output.Input.Command
}

func hookShell(t *testing.T, shell, dir, text string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, shell, "-c", text)
	command.Dir = dir
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("shell timed out: %s", output)
	}
	if err == nil {
		return string(output), 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return string(output), exit.ExitCode()
}

// A hold must retain capacity until its exact shell exits, even after SIGKILL.
func TestHookHoldLifetime(t *testing.T) {
	isolateShim(t)
	t.Setenv("TMPDIR", t.TempDir())
	binary := buildShimCLI(t)
	writeNestedConfig(t)
	for _, killed := range []bool{false, true} {
		t.Run(strconv.FormatBool(killed), func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			text := hookCommand(t, binary, "npm ci")
			if !strings.HasSuffix(text, " --pid $$ && npm ci") {
				t.Fatalf("missing shell-lifetime prefix: %s", text)
			}
			// Replace only the original payload: the real prefix still owns $$.
			text = strings.TrimSuffix(text, "npm ci") + "printf ready > \"$1\"; read -r done; :"
			command := exec.Command("/bin/bash", "-c", text, "bash", ready)
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})
			awaitFile(t, ready)
			awaitHoldSlots(t, 1)
			if killed {
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := io.WriteString(input, "done\n"); err != nil {
					t.Fatal(err)
				}
			}
			err = command.Wait()
			if !killed && err != nil {
				t.Fatal(err)
			}
			awaitHoldSlots(t, 0)
			t.Logf("shell killed=%t: slot held while alive, released after exit", killed)
		})
	}
}

// Refute a no-op prefix: capacity blocks execution, and dead queued shells
// cancel their ticket instead of acquiring a reservation later.
func TestHookHoldQueue(t *testing.T) {
	isolateShim(t)
	t.Setenv("TMPDIR", t.TempDir())
	binary := buildShimCLI(t)
	writeNestedConfig(t)
	root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")
	q, err := queue.New(root, "install", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, killed := range []bool{false, true} {
		t.Run(strconv.FormatBool(killed), func(t *testing.T) {
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
			ready := filepath.Join(t.TempDir(), "ready")
			text := hookCommand(t, binary, "npm ci")
			if !strings.HasSuffix(text, " --pid $$ && npm ci") {
				t.Fatalf("missing shell-lifetime prefix: %s", text)
			}
			text = strings.TrimSuffix(text, "npm ci") + "printf ready > \"$1\"; read -r done; :"
			command := exec.Command("/bin/bash", "-c", text, "bash", ready)
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})
			deadline := time.Now().Add(3 * time.Second)
			waiting := false
			for time.Now().Before(deadline) {
				view, err := queue.Observe(context.Background(), root, "install", 1)
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
				t.Fatal("hold never queued behind occupied slot")
			}
			if _, err := os.Stat(ready); !os.IsNotExist(err) {
				t.Fatalf("payload ran before admission: %v", err)
			}
			if killed {
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = command.Wait()
				awaitHoldSlots(t, 1)
			} else {
				if err := lease.Release(); err != nil {
					t.Fatal(err)
				}
				awaitFile(t, ready)
				awaitHoldSlots(t, 1)
				if _, err := io.WriteString(input, "done\n"); err != nil {
					t.Fatal(err)
				}
				if err := command.Wait(); err != nil {
					t.Fatal(err)
				}
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			awaitHoldSlots(t, 0)
		})
	}
}

func awaitHoldSlots(t *testing.T, used int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := queue.Observe(context.Background(), filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton"), "install", 1)
		if err != nil {
			t.Fatal(err)
		}
		if view.Used == used && len(view.Waiting) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("install slots did not reach %d", used)
}

// Run the installed hook command and its returned shell text using a real CLI.
func TestInstalledHookFlow(t *testing.T) {
	isolateHome(t)
	t.Setenv("SHELL", "/bin/sh")
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
	input, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "[ -n \"$PATH\" ] && ffmpeg 'argv with spaces and a quote: '\"'\"''"}})
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
