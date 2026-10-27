package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	// Each spelling must show usable help without writing diagnostics.
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			isolateHome(t)
			var stdout, stderr bytes.Buffer

			if code := run(args, &stdout, &stderr); code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}

			if !strings.Contains(stdout.String(), "Usage: baton") {
				t.Fatalf("help lacks usage: %q", stdout.String())
			}

			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

func TestUnknownCommand(t *testing.T) {
	isolateHome(t)
	var stdout, stderr bytes.Buffer

	// Unknown commands must fail instead of implying work was done.
	if code := run([]string{"not-a-command"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}

	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}

	if !strings.Contains(stderr.String(), `unknown command "not-a-command"`) {
		t.Fatalf("missing diagnostic: %q", stderr.String())
	}
}

func TestOutputFailure(t *testing.T) {
	isolateHome(t)

	// A closed output stream must not produce a successful exit status.
	if code := run(nil, failedWriter{}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("help exit code = %d, want 1", code)
	}

	if code := run([]string{"not-a-command"}, &bytes.Buffer{}, failedWriter{}); code != 1 {
		t.Fatalf("diagnostic exit code = %d, want 1", code)
	}
}

type failedWriter struct{}

// Write simulates a closed stream to check failure exit codes.
func (failedWriter) Write([]byte) (int, error) {
	return 0, errors.New("output closed")
}

func isolateHome(t *testing.T) {
	t.Helper()

	// Override inherited XDG keys too, so future file access stays in temp dirs.
	keys := map[string]struct{}{
		"HOME": {}, "XDG_CONFIG_HOME": {}, "XDG_CACHE_HOME": {},
		"XDG_DATA_HOME": {}, "XDG_STATE_HOME": {}, "XDG_RUNTIME_DIR": {},
		"XDG_CONFIG_DIRS": {}, "XDG_DATA_DIRS": {},
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = struct{}{}
		}
	}

	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
}
