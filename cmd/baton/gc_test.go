package main

import (
	"bytes"
	"strings"
	"testing"
)

// Help and an unconfigured collector must be usable without creating state.
func TestGCHelpAndDefaults(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{{"gc", "--help"}, {"gc"}, {"gc", "--yes"}} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != exitOK || !strings.Contains(out.String(), "gc") {
			t.Fatalf("%v: exit=%d stdout=%q stderr=%q", args, code, &out, &diagnostics)
		}
	}
}

func TestGCUsage(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{{"gc", "extra"}, {"gc", "--unknown"}} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != exitUsage || out.Len() != 0 {
			t.Fatalf("%v: exit=%d stdout=%q stderr=%q", args, code, &out, &diagnostics)
		}
	}
}
