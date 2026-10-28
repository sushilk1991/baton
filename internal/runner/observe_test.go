package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/lockfile"
	"github.com/sushilk1991/baton/internal/pressure"
	"github.com/sushilk1991/baton/internal/queue"
)

// Stop state stays visible while the process still holds its owner lock.
func TestStoppedStatus(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	store, err := newStore(root)
	if err != nil {
		t.Fatal(err)
	}
	job := jobRecord{ID: "stopped", OwnerPID: os.Getpid(), PID: os.Getpid(), Phase: "stopped", Start: time.Now().UTC()}
	owner, err := lockfile.Try(filepath.Join(root, "jobs", job.ID+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := store.publish(job); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	// A stopped process still owns its reservation and remains an active job.
	view, err := readStatus(context.Background(), root, cfg, healthyProvider{}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Running) != 1 || view.Running[0].Phase != "stopped" {
		t.Fatalf("stopped job hidden: %+v", view)
	}
	var out bytes.Buffer
	if err := printStatus(&out, view, plainOutput); err != nil || !strings.Contains(out.String(), "stopped") {
		t.Fatalf("plain status hides state: %v\n%s", err, &out)
	}
}

// Job metadata may precede ticket publication; the queue owns its sequence.
func TestQueuedPublicationSequence(t *testing.T) {
	job := jobRecord{Lane: "build", OwnerPID: 42, Phase: "queued", Label: "waiting"}
	ticket := queue.Waiting{PID: 42, Sequence: 7}
	got := queueJob([]jobRecord{job}, "build", ticket)
	if got.Label != "waiting" || got.QueueSequence != ticket.Sequence {
		t.Fatalf("unpublished sequence: label=%q sequence=%d, want %d", got.Label, got.QueueSequence, ticket.Sequence)
	}
}

// A ticket published after the initial job snapshot still needs its identity.
func TestQueuedMetadataRefresh(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	store, err := newStore(root)
	if err != nil {
		t.Fatal(err)
	}
	job := jobRecord{ID: "new-waiter", Lane: "build", OwnerPID: os.Getpid(), Phase: "queued", Label: "compile", Agent: "codex"}
	owner, err := lockfile.Try(filepath.Join(root, "jobs", job.ID+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := store.publish(job); err != nil {
		t.Fatal(err)
	}
	// The observer's earlier snapshot is empty; queue discovery happened later.
	ticket := queue.Waiting{PID: os.Getpid(), Sequence: 7}
	got, err := queuedJob(context.Background(), root, nil, "build", ticket)
	if err != nil || got.Label != "compile" || got.Agent != "codex" || got.QueueSequence != ticket.Sequence {
		t.Fatalf("new ticket lost identity: label=%q agent=%q sequence=%d error=%v", got.Label, got.Agent, got.QueueSequence, err)
	}
}

func TestStatusLiveAndStale(t *testing.T) {
	isolateHome(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := newStore(root)
	if err != nil {
		t.Fatal(err)
	}
	q, err := queue.New(root, "ad-hoc", 2)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := q.Enqueue(context.Background(), 2, queue.Normal)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := holder.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	now := time.Now().UTC()
	running := jobRecord{ID: "running", Lane: "ad-hoc", Label: "build", Agent: "codex", Repo: "/repo", OwnerPID: os.Getpid(), PID: os.Getpid(), Phase: "running", Weight: 2, Start: now.Add(-time.Minute), RSSBytes: 4096}
	owner, err := lockfile.Try(filepath.Join(root, "jobs", running.ID+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := store.publish(running); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id       string
		priority queue.Priority
	}{{"normal", queue.Normal}, {"high", queue.High}} {
		ticket, err := q.Enqueue(context.Background(), 1, item.priority)
		if err != nil {
			t.Fatal(err)
		}
		defer ticket.Close()
		job := jobRecord{ID: item.id, Lane: "ad-hoc", OwnerPID: os.Getpid(), Phase: "queued", Queued: now.Add(-time.Second), QueueSequence: ticket.Sequence(), Label: item.id, Agent: "claude-code", Repo: "/repo"}
		lock, err := lockfile.Try(filepath.Join(root, "jobs", job.ID+".lock"))
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		if err := store.publish(job); err != nil {
			t.Fatal(err)
		}
	}
	// Stale records must neither inflate occupancy nor be deleted by readers.
	if err := store.publish(jobRecord{ID: "dead", Lane: "ad-hoc", Phase: "running", Weight: 100}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "jobs", "dead.lock"), nil, fileMode); err != nil {
		t.Fatal(err)
	}
	view, err := readStatus(context.Background(), root, cfg, healthyProvider{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Running) != 1 || view.Running[0].RuntimeNanos != int64(time.Minute) || view.Running[0].RSSBytes != 4096 || len(view.Queue) != 2 || view.Queue[0].Label != "high" || view.Queue[0].Position != 1 || view.Queue[1].Position != 2 || view.Queue[0].WaitNanos != int64(time.Second) {
		t.Fatalf("bad snapshot: %+v", view)
	}
	found := false
	for _, lane := range view.Lanes {
		if lane.Name == "ad-hoc" {
			found = true
			if lane.Used != 2 || lane.Total != 2 {
				t.Fatalf("lane=%+v", lane)
			}
		}
	}
	if !found {
		t.Fatal("missing ad-hoc lane")
	}
	if _, err := os.Stat(filepath.Join(root, "jobs", "dead.json")); err != nil {
		t.Fatal("reader removed stale job", err)
	}
	var out bytes.Buffer
	if err := printStatus(&out, view, plainOutput); err != nil || !strings.Contains(out.String(), "codex") || !strings.Contains(out.String(), "/repo") || !strings.Contains(out.String(), "claude-code") {
		t.Fatalf("text=%q err=%v", &out, err)
	}
	// A malformed live record is an error, while unlocked garbage is ignored.
	if err := os.WriteFile(filepath.Join(root, "jobs", "running.json"), []byte("{"), fileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := readStatus(context.Background(), root, cfg, healthyProvider{}, now); err == nil {
		t.Fatal("accepted damaged live record")
	}
}

func TestHistoryReadAndFilter(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	store, err := newStore(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entries := []jobRecord{
		{ID: "old", Lane: "render", Start: now.Add(-48 * time.Hour), End: now.Add(-47 * time.Hour)},
		{ID: "recent", Lane: "render", Label: "frame", Agent: "codex", Repo: "/repo", Start: now.Add(-time.Hour), End: now, WaitNanos: int64(time.Second), ExitCode: 7, PeakRSSBytes: 8192, CacheHit: true},
		{ID: "other", Lane: "encode", Start: now, End: now},
	}
	data, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "history.jsonl.1"), append(data, '\n'), fileMode); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries[1:] {
		if err := store.finish(context.Background(), entry); err != nil {
			t.Fatal(err)
		}
	}
	all, err := readHistory(context.Background(), root, time.Time{}, "")
	if err != nil || len(all) != 3 || all[0].ID != "old" {
		t.Fatalf("history=%+v err=%v", all, err)
	}
	filtered, err := readHistory(context.Background(), root, now.Add(-24*time.Hour), "render")
	if err != nil || len(filtered) != 1 || filtered[0].ID != "recent" || !filtered[0].CacheHit {
		t.Fatalf("filtered=%+v err=%v", filtered, err)
	}
	var out, diagnostics bytes.Buffer
	// Use the real CLI parser against these same rotated records.
	t.Setenv("XDG_STATE_HOME", filepath.Dir(root))
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, paths.StateDir); err != nil {
		t.Fatal(err)
	}
	if code := History([]string{"--json", "--since", "24h", "--lane", "render"}, &out, &diagnostics); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, &diagnostics)
	}
	var record jobRecord
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil || record.ID != "recent" {
		t.Fatalf("JSONL=%s err=%v", &out, err)
	}
	out.Reset()
	if code := History(nil, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), "frame") || !strings.Contains(out.String(), "codex") || !strings.Contains(out.String(), "/repo") {
		t.Fatalf("text=%s exit=%d", &out, code)
	}
	for _, body := range []string{"{\n", "null\n", "{}\n", "\n"} {
		if err := os.WriteFile(filepath.Join(paths.StateDir, "history.jsonl"), []byte(body), fileMode); err != nil {
			t.Fatal(err)
		}
		if _, err := readHistory(context.Background(), paths.StateDir, time.Time{}, "no-match"); err == nil {
			t.Fatalf("accepted malformed history %q", body)
		}
	}
}

func TestStatusWatch(t *testing.T) {
	isolateHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	out := &watchWriter{cancel: cancel}
	if err := watchStatus(ctx, out, func() (statusView, error) {
		return statusView{Lanes: []laneView{}, Running: []jobView{}, Queue: []jobView{}}, nil
	}, jsonOutput, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if out.count != 2 {
		t.Fatalf("watch emitted %d frames, want 2", out.count)
	}
}

type watchWriter struct {
	sync.Mutex
	count  int
	cancel context.CancelFunc
}

func (w *watchWriter) Write(data []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	w.count++
	if w.count == 2 {
		w.cancel()
	}
	return len(data), nil
}

func TestObserveFailures(t *testing.T) {
	isolateHome(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := readStatus(context.Background(), root, cfg, failedPressure{}, time.Now()); err == nil || !strings.Contains(err.Error(), "pressure unavailable") {
		t.Fatalf("failed pressure hidden: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "jobs"), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "jobs", "stale.json"), []byte("{"), fileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := readStatus(context.Background(), root, cfg, healthyProvider{}, time.Now()); err != nil {
		t.Fatalf("unowned garbage blocked status: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "jobs", "stale.lock")); !os.IsNotExist(err) {
		t.Fatalf("observer created owner file: %v", err)
	}
	guard, err := lockfile.Try(filepath.Join(root, "history.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := readHistory(ctx, root, time.Time{}, ""); err == nil {
		t.Fatal("history ignored cancellation while locked")
	}
}

type failedPressure struct{}

func (failedPressure) Sample(context.Context) (pressure.Snapshot, error) {
	return pressure.Snapshot{}, errors.New("pressure unavailable")
}

func TestHistoryConcurrentRead(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	store, err := newStore(root)
	if err != nil {
		t.Fatal(err)
	}
	// Force real rotation with valid records, rather than a corrupt sparse file.
	now := time.Now().UTC()
	job := jobRecord{ID: "before", Start: now, End: now, Label: strings.Repeat("x", historyLimit)}
	if err := store.finish(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		for i := 0; i < 20; i++ {
			entry := jobRecord{ID: fmt.Sprint(i), Start: now, End: now}
			if err := store.finish(ctx, entry); err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	// A reader must see a complete prefix spanning old and current segments.
	for {
		jobs, err := readHistory(context.Background(), root, time.Time{}, "")
		if err != nil || len(jobs) == 0 || jobs[0].ID != "before" {
			t.Fatalf("rotation read: len=%d err=%v", len(jobs), err)
		}
		for i, job := range jobs[1:] {
			if job.ID != fmt.Sprint(i) {
				t.Fatalf("incomplete prefix: index=%d id=%q", i, job.ID)
			}
		}
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
			jobs, err = readHistory(context.Background(), root, time.Time{}, "")
			if err != nil || len(jobs) != 21 {
				t.Fatalf("final history len=%d err=%v", len(jobs), err)
			}
			return
		default:
		}
	}
}

func TestStatusTextSafety(t *testing.T) {
	view := statusView{Memory: memoryView{Pressure: pressure.Normal}, Running: []jobView{{jobRecord: jobRecord{Label: "evil\x1b[2J\nforged\trow"}}}}
	var out bytes.Buffer
	if err := printStatus(&out, view, plainOutput); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "\nforged") {
		t.Fatalf("unsafe label: %q", &out)
	}
}
