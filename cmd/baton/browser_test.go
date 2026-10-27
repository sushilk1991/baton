package main

import (
	"bytes"
	"strings"
	"testing"
)

// An absent browser is observable without starting a background process.
func TestBrowserCommands(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{{"browser", "status"}, {"browser", "stop"}, {"browser", "--help"}} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != exitOK {
			t.Fatalf("%v: exit=%d: %s", args, code, &diagnostics)
		}
		if out.Len() == 0 {
			t.Fatalf("%v: empty result", args)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"browser", "endpoint"}, &out, &diagnostics); code != exitFailure || !strings.Contains(diagnostics.String(), "not running") {
		t.Fatalf("endpoint: exit=%d out=%s error=%s", code, &out, &diagnostics)
	}
	for _, args := range [][]string{{"browser"}, {"browser", "unknown"}, {"browser", "start|stop"}, {"browser", "start", "extra"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != exitUsage {
			t.Fatalf("%v: exit=%d", args, code)
		}
	}
}
