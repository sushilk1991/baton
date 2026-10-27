package browser

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

// A removed session is transitional until the supervisor releases its lock.
func TestObserveDuringShutdown(t *testing.T) {
	const exitSuccess = 0

	for _, action := range []string{"status", "stop"} {
		t.Run(action, func(t *testing.T) {
			isolate(t)
			root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "browser")
			if err := os.MkdirAll(root, privateDir); err != nil {
				t.Fatal(err)
			}
			lease, err := lockfile.Try(filepath.Join(root, "browser.lock"))
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()

			// Hold the exact shutdown gap open: no session, lifetime still held.
			// session removed -> status/stop waits -> lock released -> stopped
			type result struct {
				code        int
				output      string
				diagnostics string
			}
			completed := make(chan result, 1)
			go func() {
				var out, diagnostics bytes.Buffer
				code := Run([]string{action}, &out, &diagnostics)
				completed <- result{code, out.String(), diagnostics.String()}
			}()
			select {
			case got := <-completed:
				t.Fatalf("returned during shutdown: exit %d, output %q, error %q", got.code, got.output, got.diagnostics)
			case <-time.After(4 * pollTime):
			}

			// Releasing ownership must expose a stopped browser without an error.
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-completed:
				if got.code != exitSuccess || !strings.Contains(got.output, "browser stopped") || got.diagnostics != "" {
					t.Fatalf("after shutdown: exit %d, output %q, error %q", got.code, got.output, got.diagnostics)
				}
			case <-time.After(requestTimeout):
				t.Fatal("observation did not finish after shutdown")
			}
		})
	}
}

// A damaged or stalled supervisor must remain an error, never appear stopped.
func TestObserveTransitionErrors(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	lease, err := lockfile.Try(filepath.Join(root, "browser.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()

	// A permanently missing session cannot keep an observer past its deadline.
	ctx, cancel := context.WithTimeout(context.Background(), pollTime)
	defer cancel()
	if _, _, err := observeReady(ctx, root); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing session: %v", err)
	}

	// Only missing sessions are transitional; malformed JSON fails immediately.
	if err := os.WriteFile(filepath.Join(root, "session.json"), []byte("{"), privateFile); err != nil {
		t.Fatal(err)
	}
	_, _, err = observeReady(ctx, root)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("corrupt session: %v", err)
	}
}
