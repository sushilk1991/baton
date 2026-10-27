package queue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/lane"
	"github.com/sushilk1991/baton/internal/lockfile"
)

const testTimeout = 15 * time.Second

func TestFIFO(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	first := enqueue(t, q, 1, Normal)
	second := enqueue(t, q, 1, Normal)
	third := enqueue(t, q, 1, Normal)

	// A later caller cannot jump a ticket whose owner has not polled yet.
	assertBusy(t, third)
	assertBusy(t, second)
	lease := acquire(t, first)
	assertBusy(t, second)
	release(t, lease)
	release(t, acquire(t, second))
	release(t, acquire(t, third))
}

func TestPriorities(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	low := enqueue(t, q, 1, Low)
	normal := enqueue(t, q, 1, Normal)
	high := enqueue(t, q, 1, High)

	// Fresh priority wins over arrival order; FIFO breaks ties.
	assertBusy(t, low)
	assertBusy(t, normal)
	release(t, acquire(t, high))
	assertBusy(t, low)
	release(t, acquire(t, normal))
	release(t, acquire(t, low))
}

func TestAging(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	now := time.Date(2026, time.October, 27, 0, 0, 0, 0, time.UTC)
	q.now = func() time.Time { return now }
	low := enqueue(t, q, 1, Low)
	now = now.Add(agingInterval)
	normal := enqueue(t, q, 1, Normal)
	now = now.Add(agingInterval)
	high := enqueue(t, q, 1, High)

	// Continuous new high-priority arrivals cannot starve promoted tickets.
	assertBusy(t, high)
	assertBusy(t, normal)
	release(t, acquire(t, low))
	assertBusy(t, high)
	release(t, acquire(t, normal))
	release(t, acquire(t, high))
}

func TestAgingBoundary(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	now := time.Now()
	q.now = func() time.Time { return now }
	old := enqueue(t, q, 1, Normal)
	now = now.Add(agingInterval - time.Nanosecond)
	fresh := enqueue(t, q, 1, High)
	assertBusy(t, old)

	// Promotion starts at the full interval, with older sequence first.
	now = now.Add(time.Nanosecond)
	assertBusy(t, fresh)
	release(t, acquire(t, old))
	release(t, acquire(t, fresh))
}

func TestWeightedHead(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 2)
	held := acquire(t, enqueue(t, q, 1, Normal))
	heavy := enqueue(t, q, 2, Normal)
	light := enqueue(t, q, 1, Normal)

	// Do not backfill a free slot: bypassing heavy tickets can starve them.
	assertBusy(t, heavy)
	assertBusy(t, light)
	release(t, held)
	held = acquire(t, heavy)
	assertBusy(t, light)
	release(t, held)
	release(t, acquire(t, light))
}

func TestCanceledTicket(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	first := enqueue(t, q, 1, Normal)
	second := enqueue(t, q, 1, Normal)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if lease, err := first.Acquire(ctx); lease != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission = %v, %v", lease, err)
	}
	release(t, acquire(t, second))
	if lease, err := first.TryAcquire(); lease != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("closed ticket = %v, %v", lease, err)
	}
}

func TestWaitingCancellation(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	held := acquire(t, enqueue(t, q, 1, Normal))
	waiter := enqueue(t, q, 1, Normal)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if lease, err := waiter.Acquire(ctx); lease != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting admission = %v, %v", lease, err)
	}
	release(t, held)
	release(t, acquire(t, enqueue(t, q, 1, Normal)))
}

func TestCloseConcurrent(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	first := enqueue(t, q, 1, Normal)
	var calls sync.WaitGroup
	for i := 0; i < 10; i++ {
		calls.Add(1)
		go func() {
			defer calls.Done()
			if err := first.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	calls.Wait()
	release(t, acquire(t, enqueue(t, q, 1, Normal)))
}

func TestSharedQueue(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	first := newQueue(t, root, "render", 1)
	alias := newQueue(t, root, "render", 1)
	other := newQueue(t, root, "encode", 1)
	ticket := enqueue(t, first, 1, Normal)
	waiter := enqueue(t, alias, 1, Normal)
	assertBusy(t, waiter)
	release(t, acquire(t, enqueue(t, other, 1, Normal)))
	release(t, acquire(t, ticket))
	release(t, acquire(t, waiter))
}

func TestValidation(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	for _, name := range []string{"../render", "a/b", "a\\b", "编译"} {
		q := newQueue(t, root, name, 1)
		rel, err := filepath.Rel(root, q.dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("queue escaped root: %q, %v", rel, err)
		}
	}
	for _, capacity := range []int{0, -1} {
		if _, err := New(root, "render", capacity); err == nil {
			t.Fatalf("accepted capacity %d", capacity)
		}
	}
	if _, err := New("", "render", 1); err == nil {
		t.Fatal("accepted empty root")
	}
	if _, err := New(root, "", 1); err == nil {
		t.Fatal("accepted empty lane")
	}
	q := newQueue(t, root, "render", 2)
	for _, weight := range []int{0, -1, 3} {
		if ticket, err := q.Enqueue(context.Background(), weight, Normal); ticket != nil || err == nil {
			t.Fatalf("accepted weight %d: %v, %v", weight, ticket, err)
		}
	}
	if ticket, err := q.Enqueue(context.Background(), 1, Priority("urgent")); ticket != nil || err == nil {
		t.Fatalf("accepted invalid priority: %v, %v", ticket, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ticket, err := q.Enqueue(ctx, 1, Normal); ticket != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled enqueue = %v, %v", ticket, err)
	}
}

func TestDeadPartialTicket(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	ticket := enqueue(t, q, 1, Normal)

	// A crash before publishing valid JSON leaves only an unlocked owner.
	dead := filepath.Join(q.dir, "00000000000000000000")
	owner, err := lockfile.Try(dead + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dead+".json", []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	release(t, acquire(t, ticket))
	for _, suffix := range []string{".lock", ".json"} {
		if _, err := os.Stat(dead + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dead ticket %s remains: %v", suffix, err)
		}
	}
}

func TestInvalidLiveTicket(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	first := enqueue(t, q, 1, Normal)
	second := enqueue(t, q, 1, Normal)
	if err := os.WriteFile(first.path+".json", []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if lease, err := second.TryAcquire(); lease != nil || err == nil || errors.Is(err, ErrBusy) {
		t.Fatalf("corrupt live ticket admitted or hidden: %v, %v", lease, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	release(t, acquire(t, second))
}

func TestGuardCancellation(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	ticket := enqueue(t, q, 1, Normal)
	guard, err := q.tryGuard()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	assertBusy(t, ticket)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if next, err := q.Enqueue(ctx, 1, Normal); next != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("guard wait = %v, %v", next, err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	release(t, acquire(t, ticket))
}

func TestSequenceRecovery(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	first := enqueue(t, q, 1, Normal)
	sequence := first.entry.Sequence
	release(t, acquire(t, first))
	stale := filepath.Join(q.dir, ".sequence-crashed")
	if err := os.WriteFile(stale, []byte("partial"), fileMode); err != nil {
		t.Fatal(err)
	}

	// An empty queue must not reuse owner paths; interrupted writes are safe.
	second := enqueue(t, q, 1, Normal)
	if second.entry.Sequence <= sequence {
		t.Fatalf("reused sequence: %d after %d", second.entry.Sequence, sequence)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale sequence file remains: %v", err)
	}
	release(t, acquire(t, second))
}

func TestCorruptSequence(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 1)
	for _, value := range []string{"", "broken", "-1", "18446744073709551615"} {
		if err := os.WriteFile(filepath.Join(q.dir, sequenceFile), []byte(value), fileMode); err != nil {
			t.Fatal(err)
		}
		if ticket, err := q.Enqueue(context.Background(), 1, Normal); ticket != nil || err == nil {
			t.Fatalf("accepted sequence %q: %v, %v", value, ticket, err)
		}
	}
}

func TestConcurrentEnqueue(t *testing.T) {
	isolateHome(t)
	q := newQueue(t, t.TempDir(), "render", 2)
	const jobs = 20
	tickets := make(chan *Ticket, jobs)
	errorsFound := make(chan error, jobs)
	var calls sync.WaitGroup
	for i := 0; i < jobs; i++ {
		calls.Add(1)
		go func() {
			defer calls.Done()
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			ticket, err := q.Enqueue(ctx, 1, Normal)
			if err != nil {
				errorsFound <- err
				return
			}
			tickets <- ticket
		}()
	}
	calls.Wait()
	close(tickets)
	close(errorsFound)
	for ticket := range tickets {
		t.Cleanup(func() { _ = ticket.Close() })
	}
	for err := range errorsFound {
		t.Fatal(err)
	}

	// Ticket order is total even when registrations race in one process.
	guard, err := q.lockGuard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entries, readErr := q.liveTickets()
	if err := errors.Join(readErr, guard.Close()); err != nil {
		t.Fatal(err)
	}
	if len(entries) != jobs {
		t.Fatalf("live tickets = %d, want %d", len(entries), jobs)
	}
	for i, entry := range entries {
		if entry.Sequence != uint64(i+1) {
			t.Fatalf("ticket %d has sequence %d", i, entry.Sequence)
		}
	}
}

func newQueue(t *testing.T, root, name string, slots int) *Queue {
	t.Helper()
	q, err := New(root, name, slots)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func enqueue(t *testing.T, q *Queue, weight int, priority Priority) *Ticket {
	t.Helper()
	ticket, err := q.Enqueue(context.Background(), weight, priority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ticket.Close() })
	return ticket
}

func acquire(t *testing.T, ticket *Ticket) *lane.Lease {
	t.Helper()
	lease, err := ticket.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	return lease
}

func release(t *testing.T, lease *lane.Lease) {
	t.Helper()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func assertBusy(t *testing.T, ticket *Ticket) {
	t.Helper()
	lease, err := ticket.TryAcquire()
	if lease != nil {
		_ = lease.Release()
	}
	if lease != nil || !errors.Is(err, ErrBusy) {
		t.Fatalf("admission = %v, %v; want nil, ErrBusy", lease, err)
	}
}

func isolateHome(t *testing.T) {
	t.Helper()
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
