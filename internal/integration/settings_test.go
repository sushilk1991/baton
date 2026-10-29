package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsBytes(t *testing.T) {
	// Existing commands, escaped strings, ordering, and whitespace belong to
	// the user. Adding one hook must leave those bytes intact at every depth.
	for _, original := range []string{
		"{\r\n\t\"z\": \"echo a && echo b > out < in\",\r\n\t\"a\" : 1e2\r\n}\r\n",
		` { "z":false, "hooks" : { "Stop" : [], "PreToolUse" : [ { "matcher":"Bash", "hooks":[{"command":"echo a && echo b > out", "type":"command"}] } ] }, "a":"\u003e" } `,
		"{\n  \"z\":0, \"hooks\" : { \"Stop\": [] }, \"a\":1\n}\n",
		"{\n\t\"hooks\" : { \"PreToolUse\" : [ \t ] }, \"z\": 0\n}\n",
		"{ \n\t }\n",
	} {
		t.Run(original, func(t *testing.T) {
			isolate(t)
			t.Setenv("TMPDIR", t.TempDir())
			requireLinks(t)
			i, err := newInstaller()
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(i.home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, name, []byte(original))
			if err := i.manage(installAgents, applyChanges, withClaudeHook, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			installed, err := os.ReadFile(name)
			if err != nil || !json.Valid(installed) {
				t.Fatalf("installed JSON: %q (%v)", installed, err)
			}
			// A single insertion can add the owned entry and necessary parents.
			// Removing that insertion must reproduce the entire original file.
			start := 0
			for start < len(original) && start < len(installed) && original[start] == installed[start] {
				start++
			}
			if !bytes.HasSuffix(installed, []byte(original[start:])) {
				t.Errorf("install rewrote user bytes:\noriginal %q\ninstalled %q", original, installed)
			}

			// Unrelated edits force manifest reconciliation instead of the exact
			// snapshot fast path. Re-init and removal must preserve those edits.
			prefix := []byte("\"userKey\" : \"keep && >\", ")
			at := bytes.IndexByte(installed, '{') + 1
			edited := append(append(append([]byte{}, installed[:at]...), prefix...), installed[at:]...)
			mustWrite(t, name, edited)
			if err := i.manage(installAgents, applyChanges, withClaudeHook, &bytes.Buffer{}); err != nil {
				t.Fatalf("re-init: %v", err)
			}
			if got, err := os.ReadFile(name); err != nil || !bytes.Equal(got, edited) {
				t.Fatalf("re-init changed bytes: %q (%v)", got, err)
			}
			if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			at = bytes.IndexByte([]byte(original), '{') + 1
			var fields map[string]any
			if err := json.Unmarshal([]byte(original), &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) == 0 {
				prefix = bytes.TrimSuffix(prefix, []byte(", "))
			}
			want := original[:at] + string(prefix) + original[at:]
			if got, err := os.ReadFile(name); err != nil || string(got) != want {
				t.Fatalf("uninstall changed user bytes:\ngot  %q (%v)\nwant %q", got, err, want)
			}
		})
	}
}

// A pretty-printed settings.json gets the hook on its own lines in the
// file's indentation and line endings, not one minified line spliced
// after the last member. Uninstall still restores the exact bytes.
func TestSettingsKeepsIndentation(t *testing.T) {
	for _, test := range []struct {
		name, original, member, nested string
	}{
		{"spaces", "{\n  \"theme\": \"dark\",\n  \"permissions\": {\n    \"allow\": [\"Bash(ls:*)\"]\n  }\n}\n", "\n  \"hooks\": {\n", "\n    \"PreToolUse\": [\n"},
		{"tabs", "{\n\t\"theme\": \"dark\"\n}\n", "\n\t\"hooks\": {\n", "\n\t\t\"PreToolUse\": [\n"},
		{"crlf", "{\r\n  \"theme\": \"dark\"\r\n}\r\n", "\r\n  \"hooks\": {\r\n", "\r\n    \"PreToolUse\": [\r\n"},
		{"existing hooks", "{\n  \"hooks\": {\n    \"Stop\": []\n  }\n}\n", "\n    \"PreToolUse\": [\n", "\n      {\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolate(t)
			t.Setenv("TMPDIR", t.TempDir())
			requireLinks(t)
			i, err := newInstaller()
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(i.home, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, name, []byte(test.original))
			if err := i.manage(installAgents, applyChanges, withClaudeHook, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			installed, err := os.ReadFile(name)
			if err != nil || !json.Valid(installed) {
				t.Fatalf("installed JSON: %q (%v)", installed, err)
			}
			if !bytes.Contains(installed, []byte(test.member)) || !bytes.Contains(installed, []byte(test.nested)) || bytes.Contains(installed, []byte(`},"`)) {
				t.Fatalf("hook not indented like the file:\n%s", installed)
			}
			if err := i.manage(removeAgents, applyChanges, withoutHook, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(name); err != nil || string(got) != test.original {
				t.Fatalf("uninstall: %q (%v)", got, err)
			}
		})
	}
}
