package admission

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/lane"
	"github.com/sushilk1991/baton/internal/pressure"
	"github.com/sushilk1991/baton/internal/queue"
)

func TestRecoveryHysteresis(t *testing.T) {
	isolateHome(t)
	now := time.Now()
	var state waitState
	normal := pressure.Snapshot{Level: pressure.Normal, TotalBytes: 1000, AvailableBytes: 800}
	warn := normal
	warn.Level = pressure.Warn
	checks := []struct {
		sample pressure.Snapshot
		after  time.Duration
		allow  bool
	}{
		{warn, 0, false},
		{normal, time.Second, false},
		{normal, 2*time.Second - time.Nanosecond, false},
		{warn, 2 * time.Second, false},
		{normal, 3 * time.Second, false},
		{normal, 4 * time.Second, true},
	}
	for _, check := range checks {
		if got := state.ready(check.sample, nil, 500, 100, time.Second, now.Add(check.after)); got != check.allow {
			t.Fatalf("at %s: admission = %t, want %t", check.after, got, check.allow)
		}
	}
}

func TestMemoryHeadroom(t *testing.T) {
	isolateHome(t)
	for _, test := range []struct {
		name      string
		level     pressure.Level
		available uint64
		memory    uint64
		margin    uint64
		sampleErr error
		want      bool
	}{
		{"initial normal", pressure.Normal, 800, 500, 100, nil, true},
		{"equal is insufficient", pressure.Normal, 600, 500, 100, nil, false},
		{"one byte spare", pressure.Normal, 601, 500, 100, nil, true},
		{"undeclared memory", pressure.Normal, 100, 0, 100, nil, false},
		{"critical", pressure.Critical, 800, 0, 100, nil, false},
		{"unavailable probe", pressure.Normal, 800, 0, 100, errors.New("probe failed"), false},
		{"invalid sample", pressure.Normal, 1001, 0, 100, nil, false},
		{"overflow", pressure.Normal, 800, math.MaxUint64, 100, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var state waitState
			sample := pressure.Snapshot{Level: test.level, TotalBytes: 1000, AvailableBytes: test.available}
			if got := state.ready(sample, test.sampleErr, test.memory, test.margin, time.Second, time.Now()); got != test.want {
				t.Fatalf("admission = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCancelRemovesTicket(t *testing.T) {
	isolateHome(t)
	q, err := queue.New(t.TempDir(), "render", 1)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	gate := newGate(t, fakeProvider{level: pressure.Warn}, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var warning bytes.Buffer
	lease, err := gate.Acquire(ctx, ticket, 0, &warning)
	if lease != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled admission = %v, %v", lease, err)
	}
	if _, err := ticket.TryAcquire(); !errors.Is(err, queue.ErrClosed) {
		t.Fatalf("canceled ticket remains: %v", err)
	}
}

func TestProbeFailureTimesOut(t *testing.T) {
	isolateHome(t)
	q, err := queue.New(t.TempDir(), "render", 1)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	gate := newGate(t, fakeProvider{err: errors.New("probe unavailable")}, Options{
		MaxWait: 20 * time.Millisecond, PollInterval: time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var warning bytes.Buffer
	lease, err := gate.Acquire(ctx, ticket, 0, &warning)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	if !strings.Contains(warning.String(), "WARNING") || !strings.Contains(warning.String(), "probe unavailable") {
		t.Fatalf("missing timeout/probe warning: %q", warning.String())
	}
}

func TestInvalidOptions(t *testing.T) {
	isolateHome(t)
	for _, options := range []Options{
		{MaxWait: -time.Second}, {PollInterval: -time.Second}, {RecoveryWindow: -time.Second},
	} {
		if gate, err := New(fakeProvider{}, options); gate != nil || err == nil {
			t.Fatalf("accepted invalid options: %v, %v", gate, err)
		}
	}
	if gate, err := New(nil, Options{}); gate != nil || err == nil {
		t.Fatalf("accepted missing provider: %v, %v", gate, err)
	}
}

func TestTimeoutKeepsSlots(t *testing.T) {
	isolateHome(t)
	q, err := queue.New(t.TempDir(), "render", 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	first, err := q.Enqueue(ctx, 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	held, err := first.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	ticket, err := q.Enqueue(ctx, 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	gate := newGate(t, fakeProvider{level: pressure.Warn}, Options{MaxWait: 10 * time.Millisecond, PollInterval: time.Millisecond})
	type result struct {
		lease *lane.Lease
		err   error
	}
	results := make(chan result, 1)
	var warnings bytes.Buffer
	go func() {
		lease, err := gate.Acquire(ctx, ticket, 0, &warnings)
		results <- result{lease, err}
	}()
	select {
	case got := <-results:
		t.Fatalf("timeout bypassed occupied slot: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	got := <-results
	if got.err != nil || got.lease == nil {
		t.Fatalf("released slot not admitted: %+v", got)
	}
	defer func() { _ = got.lease.Release() }()
	if strings.Count(warnings.String(), "WARNING") != 1 {
		t.Fatalf("timeout warning count: %q", warnings.String())
	}
}

func TestWarningFailureReleases(t *testing.T) {
	isolateHome(t)
	q, err := queue.New(t.TempDir(), "render", 1)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	gate := newGate(t, fakeProvider{level: pressure.Warn}, Options{MaxWait: time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if lease, err := gate.Acquire(ctx, ticket, 0, failedWriter{}); lease != nil || err == nil {
		t.Fatalf("failed warning admitted silently: %v, %v", lease, err)
	}
	next, err := q.Enqueue(ctx, 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := next.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed warning leaked a slot: %v", err)
	}
	defer func() { _ = lease.Release() }()
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) {
	return 0, errors.New("warning stream closed")
}

type fakeProvider struct {
	level pressure.Level
	err   error
}

func (fake fakeProvider) Sample(context.Context) (pressure.Snapshot, error) {
	return pressure.Snapshot{Level: fake.level, TotalBytes: 8 << 30, AvailableBytes: 6 << 30}, fake.err
}

func newGate(t *testing.T, provider pressure.Provider, options Options) *Gate {
	t.Helper()
	gate, err := New(provider, options)
	if err != nil {
		t.Fatal(err)
	}
	return gate
}

func isolateHome(t *testing.T) {
	t.Helper()
	// All inherited XDG paths must remain temporary, including future keys.
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

func TestWaitObserverFailure(t *testing.T) {
	isolateHome(t)
	q, err := queue.New(t.TempDir(), "x", 1)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	closed := errors.New("notice stream closed")
	gate := newGate(t, fakeProvider{level: pressure.Warn}, Options{
		OnWait: func(reason WaitReason, sample pressure.Snapshot, err error) error {
			if reason != WaitMemory || err != nil || sample.Level != pressure.Warn {
				t.Fatalf("observer sample %+v, %v", sample, err)
			}
			return closed
		},
	})
	lease, err := gate.Acquire(context.Background(), ticket, 0, &bytes.Buffer{})
	if lease != nil || !errors.Is(err, closed) {
		t.Fatalf("failed notice: lease %v, error %v", lease, err)
	}
	if _, err := ticket.TryAcquire(); !errors.Is(err, queue.ErrClosed) {
		t.Fatalf("failed notice retained ticket: %v", err)
	}
}

// max_wait bounds memory delay only. Waiting for an occupied slot while
// memory is healthy must not spend it, so admission carries no warning.
func TestSlotWaitKeepsMemoryBudget(t *testing.T) {
	isolateHome(t)
	q, held := heldLane(t)
	ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	gate := newGate(t, fakeProvider{level: pressure.Normal}, Options{MaxWait: 10 * time.Millisecond, PollInterval: time.Millisecond})
	var warnings bytes.Buffer
	results := acquireLater(gate, ticket, &warnings)
	time.Sleep(50 * time.Millisecond)
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	got := <-results
	if got.err != nil || got.lease == nil {
		t.Fatalf("released slot not admitted: %+v", got)
	}
	defer func() { _ = got.lease.Release() }()
	if strings.Contains(warnings.String(), "WARNING") {
		t.Fatalf("slot wait spent the memory budget: %q", warnings.String())
	}
}

// Memory is checked again when a slot frees after a long slot wait: a job
// that queued behind other work still waits out pressure, up to max_wait.
//
//	slot busy, memory healthy (> max_wait) -> slot frees, memory critical
//	-> waits about max_wait -> admitted with the warning
func TestMemoryCheckedAfterSlotWait(t *testing.T) {
	isolateHome(t)
	q, held := heldLane(t)
	ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	provider := &switchProvider{}
	provider.set(pressure.Normal)
	const maxWait = 300 * time.Millisecond
	gate := newGate(t, provider, Options{MaxWait: maxWait, PollInterval: time.Millisecond})
	var warnings lockedBuffer
	results := acquireLater(gate, ticket, &warnings)
	time.Sleep(2 * maxWait)
	provider.set(pressure.Critical)
	time.Sleep(10 * time.Millisecond)
	released := time.Now()
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-results:
		if got.lease != nil {
			_ = got.lease.Release()
		}
		t.Fatalf("admitted under critical pressure after a slot wait: %+v %q", got, warnings.String())
	case <-time.After(maxWait / 2):
	}
	got := <-results
	if got.err != nil || got.lease == nil {
		t.Fatalf("memory wait never ended: %+v", got)
	}
	defer func() { _ = got.lease.Release() }()
	if waited := time.Since(released); waited < maxWait/2 {
		t.Fatalf("memory wait %s, want about %s", waited, maxWait)
	}
	if !strings.Contains(warnings.String(), "WARNING") || !strings.Contains(warnings.String(), "pressure critical, 6.0 GiB available") {
		t.Fatalf("missing max_wait warning: %q", warnings.String())
	}
}

type acquired struct {
	lease *lane.Lease
	err   error
}

func acquireLater(gate *Gate, ticket *queue.Ticket, warnings io.Writer) <-chan acquired {
	results := make(chan acquired, 1)
	go func() {
		lease, err := gate.Acquire(context.Background(), ticket, 0, warnings)
		results <- acquired{lease, err}
	}()
	return results
}

// heldLane returns a one-slot lane whose slot a first job already holds.
func heldLane(t *testing.T) (*queue.Queue, *lane.Lease) {
	t.Helper()
	q, err := queue.New(t.TempDir(), "render", 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	held, err := first.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Release() })
	return q, held
}

// switchProvider reports a level the test changes while admission runs.
type switchProvider struct {
	level atomic.Value
}

func (provider *switchProvider) set(level pressure.Level) {
	provider.level.Store(level)
}

func (provider *switchProvider) Sample(context.Context) (pressure.Snapshot, error) {
	level, _ := provider.level.Load().(pressure.Level)
	return pressure.Snapshot{Level: level, TotalBytes: 8 << 30, AvailableBytes: 6 << 30}, nil
}

// lockedBuffer collects warnings written by the admission goroutine.
type lockedBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.data.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.data.String()
}

// A budget that runs out while memory recovers must say so; a bare
// "pressure normal" reads as if the gate were bypassed for no reason.
func TestWarningNamesRecovery(t *testing.T) {
	isolateHome(t)
	q, err := queue.New(t.TempDir(), "render", 1)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := q.Enqueue(context.Background(), 1, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	provider := &switchProvider{}
	provider.set(pressure.Warn)
	gate := newGate(t, provider, Options{MaxWait: 50 * time.Millisecond, PollInterval: time.Millisecond, RecoveryWindow: time.Hour})
	var warnings lockedBuffer
	results := acquireLater(gate, ticket, &warnings)
	time.Sleep(10 * time.Millisecond)
	provider.set(pressure.Normal)
	got := <-results
	if got.err != nil || got.lease == nil {
		t.Fatalf("not admitted: %+v", got)
	}
	defer func() { _ = got.lease.Release() }()
	if !strings.Contains(warnings.String(), "pressure normal") || !strings.Contains(warnings.String(), "recovery window") {
		t.Fatalf("warning %q", warnings.String())
	}
}
