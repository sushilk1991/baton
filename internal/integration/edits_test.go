package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnippetOutsideEdits(t *testing.T) {
	for _, original := range []string{"", "existing rules\n"} {
		t.Run(original, func(t *testing.T) {
			isolate(t)
			name := filepath.Join(os.Getenv("HOME"), ".agents", "AGENTS.md")
			if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
				t.Fatal(err)
			}
			if original != "" {
				mustWrite(t, name, []byte(original))
			}
			i, err := newInstaller()
			if err != nil {
				t.Fatal(err)
			}
			if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			data = append(append([]byte("above\n"), data...), []byte("below\n")...)
			mustWrite(t, name, data)
			if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
				t.Fatalf("uninstall with surrounding text: %v", err)
			}
			got, err := os.ReadFile(name)
			if want := "above\n" + original + "below\n"; err != nil || string(got) != want {
				t.Fatalf("got %q (%v), want %q", got, err, want)
			}
		})
	}
}

func TestSnippetAdjacentEdits(t *testing.T) {
	isolate(t)
	name := filepath.Join(os.Getenv("HOME"), ".agents", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, name, []byte("existing rules\n"))
	i, err := newInstaller()
	if err != nil {
		t.Fatal(err)
	}
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	// Inserting directly above the marker changes the original separator,
	// while leaving every byte of baton's block intact.
	data = bytes.Replace(data, []byte(startMarker), []byte("user directly above\n"+startMarker), 1)
	data = append(data, []byte("user directly below\n")...)
	mustWrite(t, name, data)
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got, err := os.ReadFile(name)
	if want := bytes.Replace(data, []byte(instructions), nil, 1); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got %q (%v), want %q", got, err, want)
	}
}

func TestHookOutsideEdits(t *testing.T) {
	for _, original := range []string{"", `{}`, `{"hooks":{}}`, `{"hooks":{"PreToolUse":[]}}`} {
		t.Run(original, func(t *testing.T) {
			isolate(t)
			requireLinks(t)
			i, err := newInstaller()
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(i.home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
				t.Fatal(err)
			}
			if original != "" {
				mustWrite(t, name, []byte(original))
			}
			if err := i.manage(installAgents, applyChanges, withClaudeHook, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.Replace(data, []byte("{"), []byte("{\n  \"userKey\": \"keep\",\n"), 1)
			mustWrite(t, name, data)
			if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
				t.Fatalf("uninstall with unrelated key: %v", err)
			}
			got, err := os.ReadFile(name)
			if err != nil || !bytes.Contains(got, []byte(`"userKey": "keep"`)) || bytes.Contains(got, []byte("init _hook")) {
				t.Fatalf("settings: %s (%v)", got, err)
			}
		})
	}
}

func TestLinkedSettingsEdits(t *testing.T) {
	isolate(t)
	requireLinks(t)
	i, err := newInstaller()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(i.home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(i.home, "user-config.json")
	mustWrite(t, target, []byte("{}"))
	link := filepath.Join(i.home, ".claude", "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := i.manage(installAgents, applyChanges, withClaudeHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("{"), []byte("{\n  \"userKey\": \"keep\",\n"), 1)
	mustWrite(t, target, data)
	if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatalf("uninstall linked settings: %v", err)
	}
	got, err := os.ReadFile(link)
	if err != nil || !bytes.Contains(got, []byte(`"userKey": "keep"`)) || bytes.Contains(got, []byte("init _hook")) {
		t.Fatalf("settings: %s (%v)", got, err)
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("replaced link: %s (%v)", got, err)
	}
}

func TestInitOwnEdits(t *testing.T) {
	for _, edit := range []string{"snippet", "hook", "duplicate marker", "duplicate hook"} {
		for _, operation := range []string{"reinstall", "uninstall"} {
			t.Run(edit+"/"+operation, func(t *testing.T) {
				isolate(t)
				requireLinks(t)
				i, err := newInstaller()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(i.home, ".claude"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := i.manage(installAgents, applyChanges, withClaudeHook, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(i.home, ".claude", "CLAUDE.md")
				if strings.Contains(edit, "hook") {
					name = filepath.Join(i.home, ".claude", "settings.json")
				}
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				switch edit {
				case "snippet":
					data = bytes.Replace(data, []byte("Queue heavy commands"), []byte("Edited commands"), 1)
				case "hook":
					data = bytes.Replace(data, []byte("init _hook"), []byte("init _hook-block"), 1)
				case "duplicate marker":
					data = append(data, []byte(instructions)...)
				case "duplicate hook":
					var settings map[string]any
					if err := json.Unmarshal(data, &settings); err != nil {
						t.Fatal(err)
					}
					hooks := settings["hooks"].(map[string]any)
					pre := hooks["PreToolUse"].([]any)
					hooks["PreToolUse"] = append(pre, pre[0])
					data, err = json.Marshal(settings)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(name, data, 0600); err != nil {
					t.Fatal(err)
				}
				action := installAgents
				if operation == "uninstall" {
					action = removeAgents
				}
				// A changed or ambiguous owned block fails before any removal.
				err = i.manage(action, applyChanges, withClaudeHook, &bytes.Buffer{})
				if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "preserved") {
					t.Fatalf("missing conflict warning: %v", err)
				}
				got, readErr := os.ReadFile(name)
				if readErr != nil || !bytes.Equal(got, data) {
					t.Fatalf("changed edited block: %s (%v)", got, readErr)
				}
				if _, err := os.Stat(filepath.Join(i.home, ".agents", "skills", "baton", "SKILL.md")); err != nil {
					t.Fatalf("partially removed installation: %v", err)
				}
			})
		}
	}
}

func TestAiderOutsideEdits(t *testing.T) {
	for _, original := range []string{
		"model: original\n",
		"model: original\nread: CONVENTIONS.md\n",
		"model: original\nread: [CONVENTIONS.md]\n",
		"model: original\nread:\n  - CONVENTIONS.md\n",
		`{"model":"original","read":["CONVENTIONS.md"]}`,
	} {
		t.Run(original, func(t *testing.T) {
			isolate(t)
			mustWrite(t, filepath.Join(os.Getenv("HOME"), ".aider.conf.yml"), []byte(original))
			i, err := newInstaller()
			if err != nil {
				t.Fatal(err)
			}
			if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(i.home, ".aider.conf.yml")
			installed, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			edited := bytes.Replace(installed, []byte("model: original"), []byte("model: edited"), 1)
			edited = bytes.Replace(edited, []byte(`"model": "original"`), []byte(`"model": "edited"`), 1)
			mustWrite(t, name, edited)
			if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
				t.Fatalf("reinstall: %v", err)
			}
			if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			got, err := os.ReadFile(name)
			if err != nil || bytes.Contains(got, aiderEntry(t, i.home)) || !bytes.Contains(got, []byte("edited")) || (strings.Contains(original, "CONVENTIONS.md") && !bytes.Contains(got, []byte("CONVENTIONS.md"))) {
				t.Fatalf("restore: %s (%v)", got, err)
			}
		})
	}
}

// Aider's read entry is a JSON-quoted path, so a home that JSON escapes
// (Windows backslashes, or & here) must still be recognised as owned.
// C:\Users\me -> "C:\\Users\\me"; /tmp/a&b -> "/tmp/a\u0026b"
func TestAiderEscapedHome(t *testing.T) {
	isolate(t)
	home := filepath.Join(os.Getenv("HOME"), `a&b\c`)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Skipf("escaped home unavailable: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	name := filepath.Join(home, ".aider.conf.yml")
	mustWrite(t, name, []byte("model: original\n"))
	i, err := newInstaller()
	if err != nil {
		t.Fatal(err)
	}
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(name)
	if err != nil || !bytes.Contains(installed, aiderEntry(t, i.home)) {
		t.Fatalf("Aider was not wired: %s (%v)", installed, err)
	}

	// An outside edit beside the owned entry survives reinstall and removal.
	mustWrite(t, name, bytes.Replace(installed, []byte("original"), []byte("edited"), 1))
	if err := i.manage(installAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got, err := os.ReadFile(name)
	if err != nil || string(got) != "model: edited\n" {
		t.Fatalf("restore: %q (%v)", got, err)
	}
	t.Log("escaped-home Aider entry reinstalled and removed")
}

// aiderEntry is the read entry baton writes: the instruction path, JSON-quoted.
func aiderEntry(t *testing.T, home string) []byte {
	t.Helper()
	entry, err := json.Marshal(filepath.Join(home, ".agents", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func mustWrite(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}
