package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
)

func TestConfiguredRunPolicy(t *testing.T) {
	isolateHome(t)
	writeRunConfig(t, `[lanes.video]
slots = 3
max_weight = 2
memory_gate = false
max_wait = '41ms'
matchers = ['^custom-render( |$)']
`)
	opts, err := parse([]string{"--weight", "2", "--", "custom-render", "input"}, &bytes.Buffer{})
	if err != nil || opts.lane != "video" || opts.slots != 3 || opts.maxWait != 41*time.Millisecond {
		t.Fatalf("configured policy: %+v, %v", opts, err)
	}
	if _, err := parse([]string{"--lane", "video", "--weight", "3", "--", "tool"}, &bytes.Buffer{}); err == nil {
		t.Fatal("configured max_weight was ignored")
	}
	opts, err = parse([]string{"--lane", "video", "--max-wait", "2s", "--", "tool"}, &bytes.Buffer{})
	if err != nil || opts.maxWait != 2*time.Second {
		t.Fatalf("CLI max-wait override: %+v, %v", opts, err)
	}

	// Disabled memory gating must still reserve queue slots without probing.
	opts.provider = forbiddenProvider{t: t}
	opts.weight = 2
	opts.maxWait = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	root := t.TempDir()
	lease, err := admit(ctx, opts, root, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil {
		t.Fatal("disabled gate bypassed lane slots")
	}
	// Waiting beyond max_wait must still respect capacity, without emitting
	// a memory timeout warning when memory policy was explicitly disabled.
	waitCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	var warnings bytes.Buffer
	next, waitErr := admit(waitCtx, opts, root, &warnings)
	if next != nil || waitErr == nil || strings.Contains(warnings.String(), "WARNING") {
		t.Fatalf("disabled gate wait: lease=%v error=%v stderr=%q", next, waitErr, &warnings)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestBypassIgnoresConfig(t *testing.T) {
	isolateHome(t)
	writeRunConfig(t, "this is not TOML")
	if _, err := parse([]string{"--", "tool"}, &bytes.Buffer{}); err == nil {
		t.Fatal("run ignored corrupt configuration")
	}
	t.Setenv("BATON", "off")
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := parse([]string{"--", "tool"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("BATON=off did not bypass config: %v", err)
	}
}

func writeRunConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), fileMode); err != nil {
		t.Fatal(err)
	}
}

type forbiddenProvider struct {
	t *testing.T
}

func (provider forbiddenProvider) Sample(context.Context) (pressure.Snapshot, error) {
	provider.t.Error("disabled gate sampled memory pressure")
	return pressure.Snapshot{}, nil
}
