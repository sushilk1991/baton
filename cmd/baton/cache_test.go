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

// cache and shim accept both list spellings, so neither one is a guess.
func TestListAliases(t *testing.T) {
	for _, args := range [][]string{{"cache", "ls"}, {"cache", "list"}, {"shim", "list"}, {"shim", "ls"}} {
		isolateHome(t)
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != exitOK {
			t.Errorf("%v: exit=%d stderr=%s", args, code, &diagnostics)
		}
	}
}
