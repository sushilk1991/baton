package lane

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testTimeout = 15 * time.Second

func TestWeightedSlots(t *testing.T) {
	isolateHome(t)
	lane := newTestLane(t, t.TempDir(), "render", 3)
	heavy := takeSlots(t, lane, 2)

	// A failed two-slot request must leave the last slot usable.
	assertBusy(t, lane, 2)
	light := takeSlots(t, lane, 1)
	assertBusy(t, lane, 1)
	releaseSlots(t, heavy)
	replacement := takeSlots(t, lane, 2)
	assertBusy(t, lane, 1)
	releaseSlots(t, replacement)
	releaseSlots(t, light)

	all := takeSlots(t, lane, 3)
	releaseSlots(t, all)
}

func TestIndependentLanes(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	first := newTestLane(t, root, "render", 1)
	alias := newTestLane(t, root, "render", 1)
	other := newTestLane(t, root, "encode", 1)
	lease := takeSlots(t, first, 1)
	assertBusy(t, alias, 1)
	releaseSlots(t, takeSlots(t, other, 1))
	releaseSlots(t, lease)
	releaseSlots(t, takeSlots(t, alias, 1))
}

func TestInvalidRequests(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()

	// Impossible weights must fail promptly instead of waiting forever.
	for _, slots := range []int{0, -1} {
		if _, err := New(root, "render", slots); err == nil {
			t.Fatalf("capacity %d accepted", slots)
		}
	}
	if _, err := New(root, "", 1); err == nil {
		t.Fatal("empty lane name accepted")
	}
	if _, err := New("", "render", 1); err == nil {
		t.Fatal("empty state path accepted")
	}
	lane := newTestLane(t, root, "render", 2)
	for _, weight := range []int{0, -1, 3} {
		if _, err := lane.TryAcquire(weight); err == nil || errors.Is(err, ErrBusy) {
			t.Fatalf("weight %d: want validation error, got %v", weight, err)
		}
	}
}

func TestAcquireCancellation(t *testing.T) {
	isolateHome(t)
	lane := newTestLane(t, t.TempDir(), "render", 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if lease, err := lane.Acquire(ctx, 1); !errors.Is(err, context.Canceled) || lease != nil {
		t.Fatalf("canceled acquisition = %v, %v", lease, err)
	}

	held := takeSlots(t, lane, 2)
	ctx, cancel = context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if lease, err := lane.Acquire(ctx, 2); !errors.Is(err, context.DeadlineExceeded) || lease != nil {
		t.Fatalf("waiting acquisition = %v, %v", lease, err)
	}
	releaseSlots(t, held)
	releaseSlots(t, takeSlots(t, lane, 2))
}

func TestCapacityChange(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	first := newTestLane(t, root, "render", 3)
	held := takeSlots(t, first, 3)
	smaller := newTestLane(t, root, "render", 2)

	// Different capacities cannot coexist; a resize waits for an idle lane.
	assertBusy(t, smaller, 1)
	releaseSlots(t, held)
	releaseSlots(t, takeSlots(t, smaller, 2))
	held = takeSlots(t, smaller, 1)
	assertBusy(t, first, 1)
	releaseSlots(t, held)
	releaseSlots(t, takeSlots(t, first, 3))
}

func TestAcquireRollback(t *testing.T) {
	isolateHome(t)
	lane := newTestLane(t, t.TempDir(), "render", 2)
	lease := takeSlots(t, lane, 2)
	releaseSlots(t, lease)

	// An I/O failure after slot zero must release that partial reservation.
	path := filepath.Join(lane.dir, "slot-1.lock")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := lane.TryAcquire(2); err == nil || errors.Is(err, ErrBusy) {
		t.Fatalf("want slot I/O error, got %v", err)
	}
	releaseSlots(t, takeSlots(t, lane, 1))
}

func TestCorruptCapacity(t *testing.T) {
	isolateHome(t)
	lane := newTestLane(t, t.TempDir(), "render", 2)

	// Damaged metadata must fail closed rather than admit excess jobs.
	for _, value := range []string{"", "0", "-1", "broken"} {
		if err := os.WriteFile(filepath.Join(lane.dir, capacityFile), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := lane.TryAcquire(1); err == nil || errors.Is(err, ErrBusy) {
			t.Fatalf("capacity %q: want metadata error, got %v", value, err)
		}
	}
}

func TestReleaseConcurrent(t *testing.T) {
	isolateHome(t)
	lane := newTestLane(t, t.TempDir(), "render", 2)
	lease := takeSlots(t, lane, 2)
	var callers sync.WaitGroup

	// A lease may be released by competing cancellation and exit paths.
	for i := 0; i < 10; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if err := lease.Release(); err != nil {
				t.Error(err)
			}
		}()
	}
	callers.Wait()
	releaseSlots(t, takeSlots(t, lane, 2))
}

func TestLanePaths(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()

	// Names are identifiers, never filesystem paths, including Unicode.
	for _, name := range []string{"../outside", "a/b", "a\\b", "..", "编译"} {
		lane := newTestLane(t, root, name, 1)
		rel, err := filepath.Rel(root, lane.dir)
		if err != nil || strings.Contains(rel, string(filepath.Separator)) || rel == ".." {
			t.Fatalf("unsafe lane directory %q: %v", rel, err)
		}
		releaseSlots(t, takeSlots(t, lane, 1))
	}
}

func newTestLane(t *testing.T, root, name string, slots int) *Lane {
	t.Helper()
	lane, err := New(root, name, slots)
	if err != nil {
		t.Fatal(err)
	}
	return lane
}

func takeSlots(t *testing.T, lane *Lane, weight int) *Lease {
	t.Helper()
	lease, err := lane.TryAcquire(weight)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	return lease
}

func releaseSlots(t *testing.T, lease *Lease) {
	t.Helper()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func assertBusy(t *testing.T, lane *Lane, weight int) {
	t.Helper()
	lease, err := lane.TryAcquire(weight)
	if lease != nil {
		_ = lease.Release()
	}
	if !errors.Is(err, ErrBusy) || lease != nil {
		t.Fatalf("acquisition = %v, %v; want nil, ErrBusy", lease, err)
	}
}

func isolateHome(t *testing.T) {
	t.Helper()

	// Child processes inherit only temporary HOME/XDG paths from these tests.
	keys := map[string]struct{}{
		"HOME": {}, "XDG_CONFIG_HOME": {}, "XDG_CACHE_HOME": {},
		"XDG_DATA_HOME": {}, "XDG_STATE_HOME": {}, "XDG_RUNTIME_DIR": {},
		"XDG_CONFIG_DIRS": {}, "XDG_DATA_DIRS": {},
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = struct{}{}
		}
	}
	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
}
