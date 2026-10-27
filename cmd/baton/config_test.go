package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCommands(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml")
	var out, diagnostics bytes.Buffer
	if code := run([]string{"config", "path"}, &out, &diagnostics); code != exitOK || strings.TrimSpace(out.String()) != path {
		t.Fatalf("path: exit=%d stdout=%q stderr=%q", code, &out, &diagnostics)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("path created state: %v", err)
	}

	// Showing defaults must work before initialization, without writing files.
	out.Reset()
	if code := run([]string{"config", "show"}, &out, &diagnostics); code != exitOK || !strings.Contains(out.String(), "[lanes.render]") {
		t.Fatalf("show: exit=%d stdout=%q stderr=%q", code, &out, &diagnostics)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("show created config: %v", err)
	}
	out.Reset()
	if code := run([]string{"config", "init"}, &out, &diagnostics); code != exitOK || !strings.Contains(out.String(), path) {
		t.Fatalf("init: exit=%d stdout=%q stderr=%q", code, &out, &diagnostics)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"config", "validate"}, &bytes.Buffer{}, &diagnostics); code != exitOK {
		t.Fatalf("validate: exit=%d stderr=%q", code, &diagnostics)
	}

	// A second init must preserve the exact file, including user comments.
	before = append(before, []byte("\n# keep this comment\n")...)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"config", "init"}, &bytes.Buffer{}, &diagnostics); code == exitOK {
		t.Fatal("second init overwrote existing configuration")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("init changed existing bytes: %v", err)
	}
}

func TestConfigRejectsInvalid(t *testing.T) {
	for _, body := range []string{
		"[lanes.render]\nslots = 0\n",
		"[lanes.render]\nslotz = 2\n",
		"[lanes.render]\nmatchers = ['[']\n",
		"[lanes.render]\nmax_wait = '0s'\n",
		"[lanes.render]\nmax_weight = 999999\n",
		"[broken\n",
	} {
		t.Run(body, func(t *testing.T) {
			isolateHome(t)
			path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			var out, diagnostics bytes.Buffer
			if code := run([]string{"config", "validate"}, &out, &diagnostics); code == exitOK || !strings.Contains(diagnostics.String(), path) {
				t.Fatalf("invalid config: exit=%d stdout=%q stderr=%q", code, &out, &diagnostics)
			}
		})
	}
}

func TestConfigOutputAndUsage(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{{"config"}, {"config", "unknown"}, {"config", "path", "extra"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != exitUsage {
			t.Fatalf("%v: exit=%d, want %d", args, code, exitUsage)
		}
	}
	for _, action := range []string{"path", "show", "validate"} {
		if code := run([]string{"config", action}, failedWriter{}, &bytes.Buffer{}); code != exitFailure {
			t.Fatalf("%s with closed stdout: exit=%d", action, code)
		}
	}
}
