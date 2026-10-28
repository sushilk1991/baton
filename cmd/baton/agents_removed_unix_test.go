//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInitHandRemoved(t *testing.T) {
	for _, removal := range []string{"hook entry", "PreToolUse", "hooks", "instruction", "both"} {
		for _, action := range []string{"uninstall", "reinstall"} {
			t.Run(removal+"/"+action, func(t *testing.T) {
				isolateHome(t)
				t.Setenv("TMPDIR", t.TempDir())
				binary := buildShimCLI(t)
				home := os.Getenv("HOME")
				settings := filepath.Join(home, ".claude", "settings.json")
				rules := filepath.Join(home, ".claude", "CLAUDE.md")
				original := "user rules\n"
				writeShimFile(t, settings, `{"env":{"FIXTURE":"keep"},"note":"<!-- baton:start -->","hooks":{"PreToolUse":[{"matcher":"Read","hooks":[]}],"PostToolUse":[]}}`, 0640)
				writeShimFile(t, rules, original, 0640)
				shimCLI(t, binary, 0, "init", "--yes", "--claude-hook")
				// An unrelated JSON string can contain an instruction marker.
				// Reconcile literal editor bytes before removing the owned entry.
				edited := bytes.ReplaceAll(readRemovedFile(t, settings), []byte(`\u003c`), []byte("<"))
				edited = bytes.ReplaceAll(edited, []byte(`\u003e`), []byte(">"))
				writeShimFile(t, settings, string(edited), 0640)
				shimCLI(t, binary, 0, "init", "--yes")

				// Remove only baton's entry or block, as an editor would. Keep
				// blank lines and unrelated settings for later restoration.
				var current map[string]any
				if err := json.Unmarshal(readRemovedFile(t, settings), &current); err != nil {
					t.Fatal(err)
				}
				hooks := current["hooks"].(map[string]any)
				switch removal {
				case "hook entry", "both":
					entries := hooks["PreToolUse"].([]any)
					hooks["PreToolUse"] = entries[:len(entries)-1]
				case "PreToolUse":
					delete(hooks, "PreToolUse")
				case "hooks":
					delete(current, "hooks")
				}
				data, err := json.Marshal(current)
				if err != nil {
					t.Fatal(err)
				}
				writeShimFile(t, settings, string(data), 0640)
				wantRules := original
				if removal == "instruction" || removal == "both" {
					lines := strings.Split(string(readRemovedFile(t, rules)), "\n")
					var kept []string
					inside := false
					for _, line := range lines {
						if line == "<!-- baton:start -->" {
							inside = true
						}
						if !inside {
							kept = append(kept, line)
						}
						if line == "<!-- baton:end -->" {
							inside = false
						}
					}
					wantRules = strings.Join(kept, "\n")
					writeShimFile(t, rules, wantRules, 0640)
				}
				beforeRules := readRemovedFile(t, rules)
				for _, args := range [][]string{{"init", "--dry-run"}, {"init", "--uninstall", "--dry-run"}} {
					shimCLI(t, binary, 0, args...)
					if !bytes.Equal(readRemovedFile(t, settings), data) || !bytes.Equal(readRemovedFile(t, rules), beforeRules) {
						t.Fatal("preview changed hand-edited files")
					}
				}

				// Re-init retains the recorded opt-in even without --claude-hook.
				// A second re-init must not duplicate or rewrite either edit.
				if action == "reinstall" {
					shimCLI(t, binary, 0, "init", "--yes")
					installedRules := readRemovedFile(t, rules)
					installedSettings := readRemovedFile(t, settings)
					if bytes.Count(installedRules, []byte("<!-- baton:start -->")) != 1 || bytes.Count(installedSettings, []byte("init _hook")) != 1 {
						t.Fatalf("missing or duplicate integration: %s\n%s", installedRules, installedSettings)
					}
					shimCLI(t, binary, 0, "init", "--yes")
					if !bytes.Equal(readRemovedFile(t, rules), installedRules) || !bytes.Equal(readRemovedFile(t, settings), installedSettings) {
						t.Fatal("re-init rewrote installed entries")
					}
				}
				shimCLI(t, binary, 0, "init", "--yes", "--uninstall")
				if got := readRemovedFile(t, rules); string(got) != wantRules {
					t.Fatalf("rules: %q, want %q", got, wantRules)
				}
				var got map[string]any
				if err := json.Unmarshal(readRemovedFile(t, settings), &got); err != nil {
					t.Fatal(err)
				}
				if removal == "instruction" {
					entries := hooks["PreToolUse"].([]any)
					hooks["PreToolUse"] = entries[:len(entries)-1]
				}
				if !reflect.DeepEqual(got, current) {
					t.Fatalf("settings: %v, want %v", got, current)
				}
				for _, name := range []string{settings, rules} {
					if info, err := os.Stat(name); err != nil || info.Mode().Perm() != 0640 {
						t.Fatalf("mode of %s: %v (%v)", name, info, err)
					}
				}
				for _, name := range []string{filepath.Join(home, ".agents", "skills", "baton"), filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "integration-manifest.json")} {
					if _, err := os.Lstat(name); !os.IsNotExist(err) {
						t.Fatalf("remaining integration %s: %v", name, err)
					}
				}
				t.Log("preview preserved edits; uninstall removed the rest; re-init restored only baton")
			})
		}
	}
}

func readRemovedFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
