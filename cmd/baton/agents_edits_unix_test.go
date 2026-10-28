//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitOutsideEdits(t *testing.T) {
	isolateHome(t)
	binary := buildShimCLI(t)
	home := os.Getenv("HOME")
	settings := filepath.Join(home, ".claude", "settings.json")
	rules := filepath.Join(home, ".codex", "AGENTS.md")
	writeShimFile(t, settings, `{"env":{"FIXTURE":"original"},"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[]}]}}`, 0640)
	writeShimFile(t, rules, "original rules without final newline", 0640)
	shimCLI(t, binary, 0, "init", "--yes", "--claude-hook")

	// Changes outside baton ownership must survive preview, reinstall, and
	// removal. A second user hook must survive alongside an existing one.
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]any
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatal(err)
	}
	current["env"].(map[string]any)["FIXTURE"] = "edited"
	current["userKey"] = "keep"
	hooks := current["hooks"].(map[string]any)
	hooks["PostToolUse"] = []any{map[string]any{"matcher": "Write", "hooks": []any{}}}
	hooks["PreToolUse"] = append(hooks["PreToolUse"].([]any), map[string]any{"matcher": "Edit", "hooks": []any{}})
	data, err = json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	writeShimFile(t, settings, string(data), 0640)
	installed, err := os.ReadFile(rules)
	if err != nil {
		t.Fatal(err)
	}
	edited := append([]byte("user above\n"), installed...)
	edited = append(edited, []byte("user below\n")...)
	writeShimFile(t, rules, string(edited), 0640)
	for _, args := range [][]string{
		{"init", "--dry-run", "--claude-hook"},
		{"init", "--uninstall", "--dry-run"},
		{"init", "--yes", "--claude-hook"},
		{"init", "--yes", "--claude-hook"},
	} {
		shimCLI(t, binary, 0, args...)
		for name, want := range map[string][]byte{settings: data, rules: edited} {
			got, err := os.ReadFile(name)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("reinstall changed %s: %q (%v)", name, got, err)
			}
		}
	}

	// A later edit must still work after the manifest has been reconciled;
	// user hooks can leave baton's entry in the middle of the recorded array.
	data = bytes.Replace(data, []byte(`"userKey":"keep"`), []byte(`"userKey":"keep","largeInteger":9007199254740993`), 1)
	writeShimFile(t, settings, string(data), 0640)
	shimCLI(t, binary, 0, "init", "--yes", "--claude-hook")

	// Uninstall removes only the installed block and single hook entry.
	shimCLI(t, binary, 0, "init", "--yes", "--uninstall")
	got, err := os.ReadFile(rules)
	if want := "user above\noriginal rules without final newlineuser below\n"; err != nil || string(got) != want {
		t.Fatalf("rules: %q (%v), want %q", got, err, want)
	}
	got, err = os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("9007199254740993")) {
		t.Fatalf("changed unrelated integer: %s", got)
	}
	var restored map[string]any
	if err := json.Unmarshal(got, &restored); err != nil {
		t.Fatal(err)
	}
	pre := restored["hooks"].(map[string]any)["PreToolUse"].([]any)
	if restored["userKey"] != "keep" || restored["env"].(map[string]any)["FIXTURE"] != "edited" || len(pre) != 2 || pre[0].(map[string]any)["matcher"] != "Read" || pre[1].(map[string]any)["matcher"] != "Edit" || restored["hooks"].(map[string]any)["PostToolUse"] == nil {
		t.Fatalf("lost user settings: %s", got)
	}
	for _, name := range []string{settings, rules} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0640 {
			t.Fatalf("mode %s: %v (%v)", name, info, err)
		}
	}
	t.Log("CLI kept outside edits, removed baton block/hook, retained user hooks and modes")
}
