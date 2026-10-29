package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolate(t *testing.T) {
	t.Helper()
	keys := map[string]bool{"HOME": true, "USERPROFILE": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true, "XDG_CACHE_HOME": true, "XDG_RUNTIME_DIR": true, "XDG_CONFIG_DIRS": true, "XDG_DATA_DIRS": true}
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = true
		}
	}
	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("BATON", "")
}

func TestJournalRecovery(t *testing.T) {
	isolate(t)
	requireLinks(t)
	i, err := newInstaller()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(i.home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte("old\n")
	name := filepath.Join(i.home, ".claude", "CLAUDE.md")
	if err := os.WriteFile(name, old, 0600); err != nil {
		t.Fatal(err)
	}
	j, err := i.load()
	if err != nil {
		t.Fatal(err)
	}
	j, err = i.plan(j, withoutHook, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(i.root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := i.save(j); err != nil {
		t.Fatal(err)
	}
	// A crash after journal publication but before any agent write is replayed.
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(name)
	if err != nil || !bytes.Contains(got, []byte(startMarker)) {
		t.Fatalf("replay %q %v", got, err)
	}
	j, err = i.load()
	if err != nil {
		t.Fatal(err)
	}
	j.Removing = true
	if err := i.save(j); err != nil {
		t.Fatal(err)
	}
	// A crash midway through uninstall leaves mixed original/installed bytes.
	if err := os.WriteFile(name, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(name)
	if err != nil || !bytes.Equal(got, old) {
		t.Fatalf("restore %q %v", got, err)
	}
}

func TestSymlinkInstructions(t *testing.T) {
	isolate(t)
	i, err := newInstaller()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(i.home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(i.home, "rules.md")
	old := []byte("linked rules")
	if err := os.WriteFile(target, old, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(i.home, ".codex", "AGENTS.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("link=%s %v", got, err)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, old) {
		t.Fatalf("target=%q %v", got, err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err == nil {
		t.Fatal("followed outside link")
	}
	if got, _ := os.ReadFile(outside); !bytes.Equal(got, old) {
		t.Fatal("changed outside sentinel")
	}
}

func TestHook(t *testing.T) {
	for _, tc := range []struct {
		command string
		wrapped bool
	}{
		{"ffmpeg -i 'a b.mp4' out.mp4", true},
		{"cd project && npm ci", true},
		{"echo hello", false},
		{"baton run --lane encode -- ffmpeg -i a b", false},
		{"env FIXTURE=1 baton run -- ffmpeg -i a b", false},
		{"cd project && baton run -- ffmpeg -i a b", false},
		{"echo baton; ffmpeg -i a b", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			isolate(t)
			t.Setenv("SHELL", "/bin/sh")
			input, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": tc.command, "timeout": 123}})
			var out, diag bytes.Buffer
			if code := Run([]string{"_hook"}, bytes.NewReader(input), &out, &diag); code != 0 {
				t.Fatalf("code=%d %s", code, &diag)
			}
			if !tc.wrapped || !holdSupported {
				if out.Len() != 0 {
					t.Fatalf("unexpected wrapper: %s", &out)
				}
				return
			}
			var response struct {
				Output struct {
					Event    string         `json:"hookEventName"`
					Decision string         `json:"permissionDecision"`
					Input    map[string]any `json:"updatedInput"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(out.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			updated, _ := response.Output.Input["command"].(string)
			if response.Output.Event != "PreToolUse" || response.Output.Decision == "allow" || !strings.Contains(updated, "hold --lane") || !strings.HasSuffix(updated, " --pid $$ && "+tc.command) || response.Output.Input["timeout"] != float64(123) {
				t.Fatalf("wrapper: %s", &out)
			}
			input, _ = json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": response.Output.Input})
			out.Reset()
			if code := Run([]string{"_hook"}, bytes.NewReader(input), &out, &diag); code != 0 || out.Len() != 0 {
				t.Fatalf("recursion: %d %s %s", code, &out, &diag)
			}
		})
	}
}

func TestConflictPreflight(t *testing.T) {
	for _, conflict := range []string{"skill", "markers", "settings"} {
		t.Run(conflict, func(t *testing.T) {
			isolate(t)
			i, err := newInstaller()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(i.home, ".claude"), 0700); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(i.home, ".claude", "CLAUDE.md")
			body := startMarker + "unowned" + endMarker
			if conflict == "skill" {
				name = filepath.Join(i.home, ".agents", "skills", "baton", "SKILL.md")
				body = "unowned skill"
			}
			if conflict == "settings" {
				name = filepath.Join(i.home, ".claude", "settings.json")
				body = "{invalid"
			}
			if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(name, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := i.manage(installAgents, applyChanges, withClaudeHook, &bytes.Buffer{}); err == nil {
				t.Fatal("conflict accepted")
			}
			if got, _ := os.ReadFile(name); string(got) != body {
				t.Fatal("conflict modified")
			}
			if _, err := os.Stat(filepath.Join(i.root, manifestName)); !os.IsNotExist(err) {
				t.Fatalf("wrote manifest on failed plan: %v", err)
			}
		})
	}
}

func TestSharedSkillAlias(t *testing.T) {
	isolate(t)
	i, err := newInstaller()
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(i.home, ".agents", "skills")
	if err := os.MkdirAll(canonical, 0700); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(i.home, ".codex")
	if err := os.MkdirAll(codex, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonical, filepath.Join(codex, "skills")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(codex, "skills")); err != nil {
		t.Fatalf("removed existing alias: %v", err)
	}
}

func TestAiderConventions(t *testing.T) {
	isolate(t)
	i, err := newInstaller()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(i.home, ".aider.conf.yml")
	original := "model: fixture\nread: [CONVENTIONS.md, 'other notes.md'] # keep these\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, aiderEntry(t, i.home)) {
		t.Fatalf("Aider was not wired: %s %v", data, err)
	}
	if !bytes.Contains(data, []byte("CONVENTIONS.md")) || !bytes.Contains(data, []byte("other notes.md")) {
		t.Fatal("lost existing read files")
	}
	if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("restore: %s %v", data, err)
	}
}

func TestAiderFormats(t *testing.T) {
	isolate(t)
	path := "/tmp/rules with spaces.md"
	for _, tc := range []struct{ input, contains string }{
		{"model: fixture\n", `read: ["/tmp/rules with spaces.md"]`},
		{"read: CONVENTIONS.md # preserve\nmodel: fixture\n", "  - CONVENTIONS.md # preserve\n  - \"/tmp/rules with spaces.md\""},
		{"read:\n  - one.md\n  - 'two notes.md'\nmodel: fixture\n", "  - 'two notes.md'\n  - \"/tmp/rules with spaces.md\"\nmodel:"},
		{"read:\n- one.md\nmodel: fixture\n", "- one.md\n- \"/tmp/rules with spaces.md\"\nmodel:"},
		{"read: [] # empty\n", `read: ["/tmp/rules with spaces.md"] # empty`},
		{`{"model":"fixture","read":"one.md"}`, `"one.md"`},
	} {
		got, err := aiderRead([]byte(tc.input), path)
		if err != nil || !strings.Contains(string(got), tc.contains) {
			t.Fatalf("%q -> %q (%v)", tc.input, got, err)
		}
	}
	for _, input := range []string{"---\nread: one.md\n", "read: &other one.md\n", "read: [\n  one.md\n]\n", "read: one\nread: two\n"} {
		if _, err := aiderRead([]byte(input), path); err == nil {
			t.Fatalf("unsupported YAML accepted: %s", input)
		}
	}
}

func TestPrivateManifest(t *testing.T) {
	isolate(t)
	i, err := newInstaller()
	if err != nil {
		t.Fatal(err)
	}
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(i.root, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator != '\\' && info.Mode().Perm() != 0600 {
		t.Fatalf("manifest mode: %v", info.Mode())
	}
	sentinel := filepath.Join(i.home, ".agents", "skills", "baton", "keep.txt")
	if err := os.WriteFile(sentinel, []byte("new user file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "new user file" {
		t.Fatalf("sentinel %q %v", got, err)
	}
}

func TestMalformedManifest(t *testing.T) {
	for _, body := range []string{"null", "{}", `{"version":1,"home":"/somewhere"}`} {
		t.Run(body, func(t *testing.T) {
			isolate(t)
			i, err := newInstaller()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(i.root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(i.root, manifestName), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := i.load(); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestBlockingHook(t *testing.T) {
	isolate(t)
	requireLinks(t)
	if !holdSupported {
		t.Skip("the Claude Code hook requires baton hold")
	}
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := Run([]string{"--yes", "--claude-hook-block"}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatalf("install block hook: %d %s", code, &diag)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || !bytes.Contains(data, []byte("init _hook-block")) {
		t.Fatalf("block hook command: %s %v", data, err)
	}
	out.Reset()
	diag.Reset()
	input := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ffmpeg -i a b"}}`
	if code := Run([]string{"_hook-block"}, strings.NewReader(input), &out, &diag); code != 2 || out.Len() != 0 || !strings.Contains(diag.String(), "hold --lane") || !strings.Contains(diag.String(), "ffmpeg -i a b") {
		t.Fatalf("block hook: %d out=%s diag=%s", code, &out, &diag)
	}
	if code := Run([]string{"--yes", "--uninstall"}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatalf("uninstall block hook: %d %s", code, &diag)
	}
}

func requireLinks(t *testing.T) {
	t.Helper()
	if err := os.Symlink(t.TempDir(), filepath.Join(t.TempDir(), "link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("agent links need symlink permission: %v", err)
		}
		t.Fatal(err)
	}
}
