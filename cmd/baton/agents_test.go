package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Installation must preserve exact original bytes, even without a final newline.
func TestInitLifecycle(t *testing.T) {
	isolateHome(t)
	requireAgentLinks(t)
	home := os.Getenv("HOME")
	originals := map[string]string{
		".claude/CLAUDE.md":     "existing Claude rules\r\nno final newline",
		".codex/AGENTS.md":      "existing Codex rules\n",
		".claude/settings.json": "{\"env\": {\"FIXTURE\": \"keep\"}, \"hooks\": {\"PreToolUse\": []}}\n",
	}
	for path, body := range originals {
		name := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0640); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{".cursor", ".gemini", ".aider", ".agents/skills"} {
		if err := os.MkdirAll(filepath.Join(home, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "--dry-run", "--claude-hook"}, {"init"}} {
		var out, diag bytes.Buffer
		if code := run(args, &out, &diag); code != 0 {
			t.Fatalf("%v: code=%d %s", args, code, &diag)
		}
		if !strings.Contains(out.String(), "SKILL.md") {
			t.Fatalf("missing plan: %s", &out)
		}
		if _, err := os.Stat(filepath.Join(home, ".agents/skills/baton/SKILL.md")); !os.IsNotExist(err) {
			t.Fatalf("preview wrote skill: %v", err)
		}
	}
	var out, diag bytes.Buffer
	if code := run([]string{"init", "--yes", "--claude-hook"}, &out, &diag); code != 0 {
		t.Fatalf("install: code=%d %s", code, &diag)
	}
	skill, err := os.ReadFile(filepath.Join(home, ".agents/skills/baton/SKILL.md"))
	if err != nil || !bytes.Contains(skill, []byte("name: baton")) {
		t.Fatalf("skill=%s error=%v", skill, err)
	}
	for _, agent := range []string{".claude", ".codex", ".cursor", ".gemini"} {
		data, err := os.ReadFile(filepath.Join(home, agent, "skills/baton/SKILL.md"))
		if err != nil || !bytes.Equal(data, skill) {
			t.Fatalf("%s skill link: %v", agent, err)
		}
	}
	installed := map[string][]byte{}
	for path := range originals {
		name := filepath.Join(home, path)
		installed[path], err = os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	var settings map[string]any
	if err := json.Unmarshal(installed[".claude/settings.json"], &settings); err != nil {
		t.Fatal(err)
	}
	if settings["env"].(map[string]any)["FIXTURE"] != "keep" {
		t.Fatal("lost settings")
	}
	if code := run([]string{"init", "--yes", "--claude-hook"}, &out, &diag); code != 0 {
		t.Fatalf("repeat: %d %s", code, &diag)
	}
	for path, want := range installed {
		got, _ := os.ReadFile(filepath.Join(home, path))
		if !bytes.Equal(got, want) {
			t.Fatalf("non-idempotent %s", path)
		}
	}
	if code := run([]string{"init", "--yes", "--uninstall"}, &out, &diag); code != 0 {
		t.Fatalf("uninstall: %d %s", code, &diag)
	}
	for path, want := range originals {
		name := filepath.Join(home, path)
		got, err := os.ReadFile(name)
		if err != nil || string(got) != want {
			t.Fatalf("restore %s: %q %v", path, got, err)
		}
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0640 && os.PathSeparator != '\\' {
			t.Fatalf("mode %s: %v", path, info.Mode())
		}
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents/skills/baton")); !os.IsNotExist(err) {
		t.Fatalf("owned skill remains: %v", err)
	}
	if code := run([]string{"init", "--yes", "--uninstall"}, &out, &diag); code != 0 {
		t.Fatalf("repeat uninstall: %d %s", code, &diag)
	}
}

func TestInitPreservesEdits(t *testing.T) {
	isolateHome(t)
	requireAgentLinks(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"init", "--yes"}, &out, &diag); code != 0 {
		t.Fatalf("install: %d %s", code, &diag)
	}
	name := filepath.Join(home, ".claude/CLAUDE.md")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("Queue heavy commands"), []byte("User-edited commands"), 1)
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"init", "--yes", "--uninstall"}, &out, &diag); code != 1 {
		t.Fatalf("must preserve edits: %d %s", code, &diag)
	}
	got, _ := os.ReadFile(name)
	if !bytes.Equal(got, data) {
		t.Fatal("user edits changed")
	}
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/baton/SKILL.md")); err != nil {
		t.Fatalf("partial removal: %v", err)
	}
}

func requireAgentLinks(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("agent links need symlink permission: %v", err)
		}
		t.Fatal(err)
	}
}
