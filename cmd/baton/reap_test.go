package main

import (
	"bytes"
	"strings"
	"testing"
)

// A missing dispatch must fail before any process discovery is implemented.
func TestReapHelp(t *testing.T) {
	isolateHome(t)
	var out, diagnostics bytes.Buffer
	if code := run([]string{"reap", "--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), "older-than") {
		t.Fatalf("reap help: exit=%d stdout=%q stderr=%q", code, &out, &diagnostics)
	}
}

func TestReapUsage(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{{"reap", "extra"}, {"reap", "--unknown"}, {"reap", "--older-than", "-1s"}, {"reap", "--older-than", "broken"}, {"reap", "--older-than", ""}} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 {
			t.Fatalf("%v: exit=%d out=%q diagnostics=%q", args, code, &out, &diagnostics)
		}
	}
}
