package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/admission"
	"github.com/sushilk1991/baton/internal/lockfile"
	"github.com/sushilk1991/baton/internal/pressure"
)

// Inherited environment alone cannot bypass a lane after its owner exits.
func TestNestedInheritance(t *testing.T) {
	for _, test := range []struct {
		name, phase, marker string
		nested, released    bool
		want                map[string]string
	}{
		{"running", "running", `{"install":"parent"}`, false, false, map[string]string{"install": "parent"}},
		{"child startup", "queued", `{"install":"parent"}`, false, false, map[string]string{"install": "parent"}},
		{"nested", "running", `{"install":"parent"}`, true, false, map[string]string{}},
		{"released", "running", `{"install":"parent"}`, false, true, map[string]string{}},
		{"wrong lane", "running", `{"encode":"parent"}`, false, false, map[string]string{}},
		{"wrong state", "running", `{"install":"other"}`, false, false, map[string]string{}},
		{"malformed", "running", `{`, false, false, map[string]string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateHome(t)
			t.Setenv(lanesEnv, test.marker)
			store, err := newStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			owner, err := lockfile.Try(filepath.Join(store.root, "jobs", "parent.lock"))
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			if err := store.publish(jobRecord{ID: "parent", Lane: "install", OwnerPID: os.Getpid(), Phase: test.phase, Nested: test.nested}); err != nil {
				t.Fatal(err)
			}
			if test.released {
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			}
			got, err := inheritedLanes(context.Background(), store)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("inherited %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestNestedChildEnvironment(t *testing.T) {
	isolateHome(t)
	t.Setenv(lanesEnv, "unchanged")
	lanes := map[string]string{"install": "parent"}
	for _, job := range []jobRecord{
		{ID: "nested", Lane: "install", Nested: true},
		{ID: "unmatched"},
		{ID: "encode", Lane: "encode"},
	} {
		env, err := childEnvironment(lanes, job)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		entry := env[len(env)-1]
		if err := json.Unmarshal([]byte(entry[len(lanesEnv)+1:]), &got); err != nil {
			t.Fatal(err)
		}
		if got["install"] != "parent" || got[""] != "" || (job.Lane == "encode" && got["encode"] != "encode") {
			t.Fatalf("lost ancestor or added an unheld lane: %v", got)
		}
		if os.Getenv(lanesEnv) != "unchanged" {
			t.Fatal("runner environment changed")
		}
	}
}

// Sharing an ancestor's slots must retain bounded memory admission, including
// the descendant's own budget and hysteresis after a pressure warning.
func TestNestedMemoryGate(t *testing.T) {
	const (
		recovery = 20 * time.Millisecond
		maxWait  = 80 * time.Millisecond
	)
	normal := pressure.Snapshot{Level: pressure.Normal, TotalBytes: 8 * gib, AvailableBytes: 6 * gib}
	warn := normal
	warn.Level = pressure.Warn
	for _, name := range []string{"recovery", "timeout", "headroom", "disabled", "cancel"} {
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			store, err := newStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			opts := baseOptions(nil)
			opts.memory, opts.maxWait = 6*gib, maxWait
			opts.gate = admission.Options{PollInterval: time.Millisecond, RecoveryWindow: recovery, MarginBytes: gib}
			provider := &nestedPressure{samples: []pressure.Snapshot{warn}}
			minimum := maxWait
			limit := time.Second
			switch name {
			case "recovery":
				opts.memory, opts.maxWait = gib, time.Second
				provider.samples = append(provider.samples, normal)
				minimum = recovery
			case "headroom":
				provider.samples = []pressure.Snapshot{normal}
			case "disabled":
				opts.gate.MemoryGate = admission.GateDisabled
				minimum = 0
			case "cancel":
				opts.maxWait = time.Second
				limit, minimum = 20*time.Millisecond, 20*time.Millisecond
			}
			opts.provider = provider
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			defer cancel()
			var diagnostics bytes.Buffer
			started := time.Now()
			lease, err := admit(ctx, opts, store, &jobRecord{ID: "nested", Nested: true}, &diagnostics)
			elapsed := time.Since(started)
			if lease != nil {
				_ = lease.Release()
				t.Fatal("nested job acquired another reservation")
			}
			if name == "cancel" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("canceled memory wait: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if elapsed < minimum {
				t.Errorf("nested memory admission took %s; must wait at least %s", elapsed, minimum)
			}
			wantWarning := name == "timeout" || name == "headroom"
			if got := strings.Count(diagnostics.String(), "WARNING"); (wantWarning && got != 1) || (!wantWarning && got != 0) {
				t.Errorf("memory timeout warning count %d: %s", got, &diagnostics)
			}
			if name == "disabled" && provider.calls != 0 {
				t.Errorf("disabled gate sampled pressure %d times", provider.calls)
			}
			if name != "disabled" && provider.calls == 0 {
				t.Error("nested memory policy never sampled pressure")
			}
			if _, err := os.Stat(filepath.Join(store.root, "queue")); !os.IsNotExist(err) {
				t.Fatalf("nested job created a queue: %v", err)
			}
			t.Logf("%s: waited %s, pressure samples %d, warning=%t, no ticket or lease", name, elapsed, provider.calls, wantWarning)
		})
	}
}

// Repeat the final sample so recovery, timeout, and cancellation are explicit.
type nestedPressure struct {
	samples []pressure.Snapshot
	calls   int
}

// Sample advances the fixture, repeating its final observation until timeout.
func (provider *nestedPressure) Sample(context.Context) (pressure.Snapshot, error) {
	index := provider.calls
	provider.calls++
	if index >= len(provider.samples) {
		index = len(provider.samples) - 1
	}
	return provider.samples[index], nil
}
