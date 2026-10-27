package queue

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObserve(t *testing.T) {
	isolateHome(t)
	root := filepath.Join(t.TempDir(), "absent")
	view, err := Observe(context.Background(), root, "x", 3)
	if err != nil || view.Used != 0 || view.Total != 3 || len(view.Waiting) != 0 {
		t.Fatalf("empty=%+v %v", view, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("observe created state: %v", err)
	}
	q := newQueue(t, root, "x", 2)
	held := enqueue(t, q, 2, Normal)
	lease, err := held.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	normal := enqueue(t, q, 1, Normal)
	defer normal.Close()
	high := enqueue(t, q, 1, High)
	defer high.Close()
	// On-disk capacity stays authoritative while a config resize waits.
	view, err = Observe(context.Background(), root, "x", 1)
	if err != nil || view.Total != 2 || view.Used != 2 || len(view.Waiting) != 2 || view.Waiting[0].Sequence != high.Sequence() || view.Waiting[1].Sequence != normal.Sequence() {
		t.Fatalf("busy=%+v %v", view, err)
	}
	// Dead tickets are skipped without unlinking either half.
	dead := enqueue(t, q, 1, Low)
	if err := dead.owner.Close(); err != nil {
		t.Fatal(err)
	}
	view, err = Observe(context.Background(), root, "x", 2)
	if err != nil || len(view.Waiting) != 2 {
		t.Fatalf("dead included: %+v %v", view, err)
	}
	if _, err := os.Stat(dead.path + ".json"); err != nil {
		t.Fatal("observer deleted dead ticket", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	guard, err := q.tryGuard()
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if _, err := Observe(ctx, root, "x", 2); err == nil {
		t.Fatal("ignored snapshot cancellation")
	}
}

func TestObserveDamagedLiveTicket(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	q := newQueue(t, root, "x", 1)
	ticket := enqueue(t, q, 1, Normal)
	defer ticket.Close()
	if err := os.Remove(ticket.path + ".json"); err != nil {
		t.Fatal(err)
	}
	if _, err := Observe(context.Background(), root, "x", 1); err == nil {
		t.Fatal("missing live ticket metadata was silently omitted")
	}
}
