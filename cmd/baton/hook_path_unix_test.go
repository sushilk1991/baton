//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookUpgradePath(t *testing.T) {
	isolateShim(t)
	t.Setenv("TMPDIR", t.TempDir())
	built := buildShimCLI(t)
	data, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}

	// Install through each supported path spelling. The saved hook and its
	// hold prefix must retain the link that a package manager updates.
	for _, invocation := range []string{"absolute", "PATH", "relative", "relative PATH"} {
		t.Run(invocation, func(t *testing.T) {
			isolateShim(t)
			t.Setenv("TMPDIR", t.TempDir())
			old := filepath.Join(t.TempDir(), "version-1")
			writeShimFile(t, old, string(data), 0700)
			installed := filepath.Join(t.TempDir(), "baton's cli")
			if err := os.Symlink(old, installed); err != nil {
				t.Fatal(err)
			}
			writeShimFile(t, filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"), "{}\n", 0600)
			writeShimFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml"), "[lanes.encode]\nslots = 1\nmemory_gate = false\n", 0600)
			tools := t.TempDir()
			writeShimFile(t, filepath.Join(tools, "ffmpeg"), "#!/bin/sh\nprintf path-ok\n", 0700)
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			path := installed
			switch invocation {
			case "PATH":
				path = filepath.Base(installed)
				t.Setenv("PATH", filepath.Dir(installed)+string(os.PathListSeparator)+os.Getenv("PATH"))
			case "relative":
				path = "./" + filepath.Base(installed)
			case "relative PATH":
				path = filepath.Base(installed)
				t.Setenv("PATH", "."+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			command := exec.Command("/bin/sh", "-c", `exec "$1" init --yes --claude-hook`, "sh", path)
			command.Dir = filepath.Dir(installed)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("install: %v\n%s", err, output)
			}
			var settings struct {
				Hooks map[string][]struct {
					Hooks []struct{ Command string }
				}
			}
			body, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body, &settings); err != nil {
				t.Fatal(err)
			}
			hook := settings.Hooks["PreToolUse"][0].Hooks[0].Command
			quoted := "'" + strings.ReplaceAll(installed, "'", "'\"'\"'") + "'"
			if want := quoted + " init _hook"; hook != want {
				t.Fatalf("hook path: got %q, want %q", hook, want)
			}

			// Remove the old version, retarget the link, then execute the
			// saved hook -> hold prefix -> fake tool from another directory.
			updated := old + "-upgraded"
			if err := os.Rename(old, updated); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(installed); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(updated, installed); err != nil {
				t.Fatal(err)
			}
			input := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ffmpeg"}}`
			command = exec.Command("/bin/sh", "-c", hook)
			command.Stdin = strings.NewReader(input)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("saved hook after upgrade: %v", err)
			}
			var response struct {
				Output struct {
					Input struct{ Command string } `json:"updatedInput"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(output, &response); err != nil {
				t.Fatal(err)
			}
			wrapped := response.Output.Input.Command
			if want := quoted + " hold --lane 'encode' --pid $$ && ffmpeg"; wrapped != want {
				t.Fatalf("hold path: got %q, want %q", wrapped, want)
			}
			if got, code := hookShell(t, "/bin/bash", t.TempDir(), wrapped); got != "path-ok" || code != exitOK {
				t.Fatalf("upgraded command: exit %d, output %q", code, got)
			}

			// Shell exit precedes the detached keeper's final writes. The fixture
			// must wait for that keeper before removing its temporary state.
			root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")
			jobs, err := filepath.Glob(filepath.Join(root, "jobs", "*.json"))
			if err != nil || len(jobs) != 0 {
				t.Errorf("shell fixture returned before keeper cleanup: %v (%v)", jobs, err)
			}

			history, err := os.ReadFile(filepath.Join(root, "history.jsonl"))
			if err != nil || bytes.Count(history, []byte("\n")) != 1 {
				t.Errorf("shell fixture returned before hold history: %s (%v)", history, err)
			}

			input = strings.Replace(input, `"ffmpeg"`, string(mustPathJSON(t, wrapped)), 1)
			command = exec.Command("/bin/sh", "-c", hook)
			command.Stdin = strings.NewReader(input)
			if output, err := command.Output(); err != nil || len(bytes.TrimSpace(output)) != 0 {
				t.Fatalf("repeated hook: %s (%v)", output, err)
			}
			t.Log("saved hook and hold retain invoked link; upgraded command exits 0; repeated hook unchanged")
		})
	}
}

func mustPathJSON(t *testing.T, value string) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
