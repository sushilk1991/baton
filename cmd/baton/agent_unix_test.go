//go:build darwin || linux

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRunAgentLabel(t *testing.T) {
	for _, test := range []struct {
		name, marker, terminal, override, want string
	}{
		{name: "API key only", want: "terminal"},
		{name: "CLI configuration", marker: "GEMINI_CLI_TRUST_WORKSPACE=true", want: "terminal"},
		{name: "CLI marker", marker: "GEMINI_CLI=1", want: "gemini"},
		{name: "terminal fallback", terminal: "tmux", want: "tmux"},
		{name: "explicit override", override: "fixture", want: "fixture"},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateHome(t)
			// Clear inherited identities so the caller cannot mask a false label.
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				for _, prefix := range []string{"CLAUDECODE", "CODEX_", "CURSOR_", "GEMINI_", "AIDER_", "OPENCODE_", "GOOSE_", "TERM_PROGRAM"} {
					if strings.HasPrefix(key, prefix) {
						t.Setenv(key, "")
					}
				}
			}
			t.Setenv("GEMINI_API_KEY", "x")
			t.Setenv("TERM_PROGRAM", test.terminal)
			if test.marker != "" {
				key, value, _ := strings.Cut(test.marker, "=")
				t.Setenv(key, value)
			}

			// Exercise run -> persisted history -> public history output.
			args := []string{"run"}
			if test.override != "" {
				args = append(args, "--agent", test.override)
			}
			args = append(args, "--", "true")
			if output, err := cliCommand(t, args...).CombinedOutput(); err != nil {
				t.Fatalf("run: %v: %s", err, output)
			}

			output, err := cliCommand(t, "history", "--json").CombinedOutput()
			if err != nil {
				t.Fatalf("history: %v: %s", err, output)
			}
			var job struct {
				Agent    string
				ExitCode int `json:"exit_code"`
			}
			if err := json.Unmarshal(output, &job); err != nil || job.Agent != test.want || job.ExitCode != exitOK {
				t.Fatalf("history %s: error %v; want agent %q, exit %d", output, err, test.want, exitOK)
			}
		})
	}
}
