package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
	"github.com/sushilk1991/baton/internal/queue"
)

func TestAutoLane(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"/usr/bin/ffmpeg", "-i", "in"}, "encode"},
		{[]string{"npx", "remotion", "render"}, "render"},
		{[]string{"pnpm", "exec", "playwright", "test"}, "render"},
		{[]string{"python", "-m", "mlx_whisper"}, "ml"},
		{[]string{"whisper", "audio.wav"}, "ml"},
		{[]string{"npm", "i"}, "install"}, {[]string{"pnpm", "ci"}, "install"},
		{[]string{"uv", "pip", "install"}, "install"}, {[]string{"uv", "sync"}, "install"},
		{[]string{"pip3", "install", "thing"}, "install"}, {[]string{"brew", "install", "thing"}, "install"},
		{[]string{"cargo", "install", "thing"}, "install"},
		{[]string{"go", "build", "./..."}, "build"}, {[]string{"xcodebuild", "test"}, "build"},
		{[]string{"cargo", "build"}, "build"}, {[]string{"gradle", "build"}, "build"},
		{[]string{"git", "status"}, ""}, {[]string{"npm", "info"}, ""},
		{[]string{"sh", "-c", "ffmpeg -i in"}, ""},
	} {
		if got := autoLane(test.args); got != test.want {
			t.Errorf("%v = %q, want %q", test.args, got, test.want)
		}
	}
}

func TestMemoryBudget(t *testing.T) {
	for _, test := range []struct {
		value string
		want  uint64
	}{
		{"4G", 4 << 30}, {"1.5GiB", 3 << 29}, {"512M", 512 << 20}, {"2k", 2048}, {"123B", 123}, {"7", 7},
	} {
		got, err := parseMemory(test.value)
		if err != nil || got != test.want {
			t.Errorf("%s = %d, %v; want %d", test.value, got, err, test.want)
		}
	}
	for _, text := range []string{"", "0", "-1G", "NaN", "Inf", "18446744073709551616", "4T", "0.1B"} {
		if _, err := parseMemory(text); err == nil {
			t.Errorf("accepted invalid memory %q", text)
		}
	}
}

func TestDefaultSlots(t *testing.T) {
	for name, want := range map[string]int{"render": 2, "encode": 2, "build": 3, "ml": 1, "install": 1, "x": 1} {
		if got := laneSlots(name, 12, 36*gib); got != want {
			t.Errorf("%s slots %d, want %d", name, got, want)
		}
		if got := laneSlots(name, 1, gib); got != 1 {
			t.Errorf("small machine %s slots %d, want 1", name, got)
		}
	}
}

func TestIdentity(t *testing.T) {
	for _, test := range []struct {
		env            []string
		override, want string
	}{
		{[]string{"CLAUDECODE=1", "CODEX_TEST=1"}, "", "claude-code"},
		{[]string{"CODEX_TEST=1", "CURSOR_TRACE=1"}, "", "codex"},
		{[]string{"CURSOR_TRACE=1"}, "", "cursor"},
		{[]string{"TERM_PROGRAM=tmux"}, "", "tmux"},
		{[]string{"CLAUDECODE="}, "", "terminal"},
		{[]string{"CLAUDECODE=1"}, "explicit", "explicit"},
	} {
		if got := detectAgent(test.override, test.env); got != test.want {
			t.Errorf("%v: %q, want %q", test, got, test.want)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: elsewhere"), fileMode); err != nil {
		t.Fatal(err)
	}
	if got := repoRoot(filepath.Join(root, "sub", "deeper")); got != root {
		t.Fatalf("repo root %q, want %q", got, root)
	}
}

func TestSignature(t *testing.T) {
	isolateHome(t)
	opts := options{args: []string{"tool", "a b", "c"}}
	first, err := newJob(opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.args = []string{"tool", "a", "b c"}
	second, err := newJob(opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.Signature == second.Signature || first.ID == second.ID {
		t.Fatal("argv boundaries or job identity collapsed")
	}
}

func TestHistoryLearning(t *testing.T) {
	isolateHome(t)
	store, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, peak := range []uint64{1000, 2000, 250} {
		if err := store.finish(context.Background(), jobRecord{Signature: "test", PeakRSSBytes: peak}); err != nil {
			t.Fatal(err)
		}
	}
	prediction, err := store.predict(context.Background(), "test")
	if err != nil || prediction != 1000 {
		t.Fatalf("EWMA %d, %v; want 1000", prediction, err)
	}
	data, err := os.ReadFile(filepath.Join(store.root, "history.jsonl"))
	if err != nil || len(bytes.Split(bytes.TrimSpace(data), []byte("\n"))) != 3 {
		t.Fatalf("history %s, %v", data, err)
	}
	// Refute overflow in the first observation and the EWMA update.
	for _, peak := range []uint64{math.MaxUint64, math.MaxUint64 - 4} {
		if err := store.finish(context.Background(), jobRecord{Signature: "huge", PeakRSSBytes: peak}); err != nil {
			t.Fatal(err)
		}
	}
	prediction, err = store.predict(context.Background(), "huge")
	if err != nil || prediction != math.MaxUint64-1 {
		t.Fatalf("overflow: %d, %v", prediction, err)
	}
}

func TestConcurrentHistory(t *testing.T) {
	isolateHome(t)
	store, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errors := make(chan error, 20)
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			errors <- store.finish(context.Background(), jobRecord{ID: fmt.Sprint(i), Signature: fmt.Sprint(i), PeakRSSBytes: 1000})
		}(i)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(store.root, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var job jobRecord
		if err := json.Unmarshal(line, &job); err != nil {
			t.Fatal(err)
		}
		seen[job.ID] = true
	}
	learned, err := store.readLearned()
	if err != nil || len(learned) != 20 || len(seen) != 20 {
		t.Fatalf("learned %d records %d error %v", len(learned), len(seen), err)
	}
}

func TestHistoryRotation(t *testing.T) {
	isolateHome(t)
	store, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, "history.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(historyLimit); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.finish(context.Background(), jobRecord{ID: "new"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path + ".1")
	if err != nil || info.Size() != historyLimit {
		t.Fatalf("rotated segment %v, %v", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte(`"id":"new"`)) {
		t.Fatalf("new history %s, %v", data, err)
	}
}

func TestSumTree(t *testing.T) {
	rows := []processRow{{1, 0, 100}, {2, 1, 200}, {3, 2, 300}, {4, 0, 9000}, {5, 3, 400}}
	if got := sumTree(rows, 2); got != 900 {
		t.Fatalf("tree RSS %d, want 900", got)
	}
	if got := sumTree([]processRow{{1, 2, math.MaxUint64}, {2, 1, 1}}, 1); got != math.MaxUint64 {
		t.Fatalf("cyclic/overflow tree RSS %d", got)
	}
}

func TestNotices(t *testing.T) {
	var out bytes.Buffer
	notice := noticeProvider{out: &out, lane: "x"}
	sample := pressure.Snapshot{Level: pressure.Warn}
	if err := notice.wait(sample, nil); err != nil {
		t.Fatal(err)
	}
	if err := notice.wait(sample, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "waiting") != 1 {
		t.Fatalf("not rate limited: %q", out.String())
	}
}

func TestStateRoot(t *testing.T) {
	isolateHome(t)
	t.Setenv("XDG_STATE_HOME", "")
	got, err := stateRoot()
	want := filepath.Join(os.Getenv("HOME"), ".local", "state", "baton")
	if err != nil || got != want {
		t.Fatalf("%q %v, want %s", got, err, want)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	if _, err := stateRoot(); err == nil {
		t.Fatal("relative state root accepted")
	}
}

func isolateHome(t *testing.T) {
	t.Helper()
	keys := map[string]bool{"HOME": true, "XDG_CONFIG_HOME": true, "XDG_STATE_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true, "XDG_RUNTIME_DIR": true, "XDG_CONFIG_DIRS": true, "XDG_DATA_DIRS": true}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = true
		}
	}
	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
	t.Setenv("BATON", "")
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
}

// A controlled provider keeps process tests independent of machine pressure.
type healthyProvider struct{}

func (healthyProvider) Sample(context.Context) (pressure.Snapshot, error) {
	return pressure.Snapshot{Level: pressure.Normal, TotalBytes: 8 * gib, AvailableBytes: 6 * gib}, nil
}

func baseOptions(args []string) options {
	return options{args: args, lane: "x", weight: 1, slots: 2, priority: queue.Normal, maxWait: time.Second, provider: healthyProvider{}}
}

func TestJobJSON(t *testing.T) {
	job := jobRecord{ID: "test", MemoryBytes: 123, Queued: time.Now().UTC()}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var restored jobRecord
	if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(job, restored) {
		t.Fatalf("round trip: %v, %v", restored, err)
	}
}

func TestCorruptLearnedMemory(t *testing.T) {
	isolateHome(t)
	store, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"null", "{partial", `{"test":-1}`} {
		if err := os.WriteFile(filepath.Join(store.root, "learned.json"), []byte(data), fileMode); err != nil {
			t.Fatal(err)
		}
		if _, err := store.predict(context.Background(), "test"); err == nil {
			t.Fatalf("accepted corrupt memory data %q", data)
		}
	}
}
