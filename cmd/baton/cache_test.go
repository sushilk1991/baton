package main

import (
	"bytes"
	"testing"
)

// Half-configured caching must fail before the child can run.
func TestCacheFlagPair(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{
		{"run", "--cache-key", "src/**", "--", "never-run"},
		{"run", "--output", "result", "--", "never-run"},
		{"cache", "ls", "extra"}, {"cache", "unknown"},
	} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != exitUsage {
			t.Fatalf("%v: exit=%d stderr=%s", args, code, &diagnostics)
		}
	}
}
