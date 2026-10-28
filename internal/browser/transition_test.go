package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

// Read-only observations must not make a new supervisor fail its startup.
func TestStartWaitsForObserver(t *testing.T) {
	isolate(t)
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	missing := filepath.Join(root, "missing-browser")
	configDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton")
	if err := os.MkdirAll(configDir, privateDir); err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf("[browser]\nexecutable = %q\n", missing)
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(settings), privateFile); err != nil {
		t.Fatal(err)
	}

	// Hold observe's temporary probe across supervisor startup. The missing
	// executable stops the test after acquisition without launching a browser.
	// observer holds lock -> supervisor waits -> probe ends -> discovery runs
	probe, err := lockfile.Try(filepath.Join(root, "browser.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	completed := make(chan error, 1)
	go func() { completed <- serve(root, io.Discard) }()
	select {
	case err := <-completed:
		t.Fatalf("startup failed during observation: %v", err)
	case <-time.After(4 * pollTime):
	}

	// Once the observer releases its probe, startup must reach discovery.
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), missing) {
			t.Fatalf("startup did not reach configured discovery: %v", err)
		}
	case <-time.After(requestTimeout):
		t.Fatal("startup did not continue after observation")
	}
	t.Log("startup waited for the observer, then reached configured discovery")
}

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

// A session read just before shutdown points at a control port that is
// closing: the dial is refused, or the connection drops mid-request (EOF).
// Either is transitional while the supervisor still holds its lock.
func TestObserveTransportDuringShutdown(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		// Bind a port, record it, close it: dials are refused.
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		listener.Close()
		observeStaleSession(t, address)
	})
	t.Run("eof", func(t *testing.T) {
		// Accept and close at once: requests end in EOF, as during Shutdown.
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			for {
				connection, err := listener.Accept()
				if err != nil {
					return
				}
				connection.Close()
			}
		}()
		observeStaleSession(t, listener.Addr().String())
	})
}

// observeStaleSession holds the lifetime lock with session.json pointing at
// address, checks status waits, then finishes shutdown and expects "stopped".
//
//	session.json -> address -> transport error -> wait -> lock released -> stopped
func observeStaleSession(t *testing.T, address string) {
	t.Helper()
	const exitSuccess = 0

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

	stale, err := json.Marshal(session{URL: "http://" + address, Endpoint: "ws://" + address + "/devtools/browser/x", Token: "00"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "session.json"), stale, privateFile); err != nil {
		t.Fatal(err)
	}

	type result struct {
		code        int
		output      string
		diagnostics string
	}
	completed := make(chan result, 1)
	go func() {
		var out, diagnostics bytes.Buffer
		code := Run([]string{"status"}, &out, &diagnostics)
		completed <- result{code, out.String(), diagnostics.String()}
	}()
	select {
	case got := <-completed:
		t.Fatalf("returned during shutdown: exit %d, output %q, error %q", got.code, got.output, got.diagnostics)
	case <-time.After(4 * pollTime):
	}

	// The supervisor finishes: session removed, lifetime lock released.
	if err := os.Remove(filepath.Join(root, "session.json")); err != nil {
		t.Fatal(err)
	}
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
}
