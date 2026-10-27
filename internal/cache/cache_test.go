package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), fileMode); err != nil {
		t.Fatal(err)
	}
}

func testRecipe(t *testing.T) recipe {
	t.Helper()
	cwd := t.TempDir()
	put(t, filepath.Join(cwd, "src", "input"), "one")
	return recipe{cwd: cwd, root: t.TempDir(), output: filepath.Join(cwd, "out", "result"), patterns: []string{"src/**"}, args: []string{"tool", "a b"}}
}

func keyOf(t *testing.T, r recipe) string {
	t.Helper()
	key, err := r.key(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// Content, membership, names, permissions, argv boundaries, and allowlisted
// environment all affect reuse; timestamps and unrelated env do not.
func TestKeyInvalidation(t *testing.T) {
	r := testRecipe(t)
	first := keyOf(t, r)
	name := filepath.Join(r.cwd, "src", "input")
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	put(t, name, "two")
	if err := os.Chtimes(name, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if keyOf(t, r) == first {
		t.Fatal("same-size rewrite with restored mtime reused key")
	}
	put(t, name, "one")
	if keyOf(t, r) != first {
		t.Fatal("content-equivalent input changed key")
	}
	put(t, filepath.Join(r.cwd, "src", "added"), "one")
	if keyOf(t, r) == first {
		t.Fatal("added input reused key")
	}
	if err := os.Remove(filepath.Join(r.cwd, "src", "added")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(name, filepath.Join(r.cwd, "src", "renamed")); err != nil {
		t.Fatal(err)
	}
	if keyOf(t, r) == first {
		t.Fatal("renamed input reused key")
	}
	if err := os.Rename(filepath.Join(r.cwd, "src", "renamed"), name); err != nil {
		t.Fatal(err)
	}
	r.args = []string{"tool", "a", "b"}
	if keyOf(t, r) == first {
		t.Fatal("argv boundaries collapsed")
	}
	r.args = []string{"tool", "a b"}
	r.env = []string{"BATON_CACHE_UNIT_ENV"}
	t.Setenv("BATON_CACHE_UNIT_ENV", "")
	empty := keyOf(t, r)
	if err := os.Unsetenv("BATON_CACHE_UNIT_ENV"); err != nil {
		t.Fatal(err)
	}
	if keyOf(t, r) == empty {
		t.Fatal("unset and empty environment collapsed")
	}
	t.Setenv("BATON_CACHE_UNIT_ENV", "value")
	if keyOf(t, r) == empty {
		t.Fatal("allowlisted value ignored")
	}
	t.Setenv("BATON_CACHE_UNIT_IGNORED", "other")
	value := keyOf(t, r)
	t.Setenv("BATON_CACHE_UNIT_IGNORED", "changed")
	if keyOf(t, r) != value {
		t.Fatal("unlisted env affected key")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(name, 0700); err != nil {
			t.Fatal(err)
		}
		if keyOf(t, r) == value {
			t.Fatal("executable input mode ignored")
		}
	}
}

func TestPatternValidation(t *testing.T) {
	for _, pattern := range []string{"", "../outside", "src/../outside", "/absolute", `src\file`, "[bad", "src/**,"} {
		if _, err := parsePatterns(pattern); err == nil {
			t.Errorf("accepted %q", pattern)
		}
	}
	for _, test := range []struct {
		pattern, name string
		want          bool
	}{
		{"src/**", "src/a", true}, {"src/**", "src/deep/a", true},
		{"**/*.wav", "audio.wav", true}, {"**/*.wav", "a/b/audio.wav", true},
		{"audio/*.wav", "audio/b/a.wav", false}, {"a/**/c", "a/c", true},
	} {
		if got := matchParts(strings.Split(test.pattern, "/"), strings.Split(test.name, "/")); got != test.want {
			t.Errorf("%+v = %v", test, got)
		}
	}
	r := testRecipe(t)
	r.patterns = []string{"missing*"}
	if _, err := r.key(context.Background()); err == nil {
		t.Fatal("unmatched pattern accepted")
	}
}

func TestKeyExcludesOutputAndCache(t *testing.T) {
	r := testRecipe(t)
	r.patterns = []string{"**"}
	r.root = filepath.Join(r.cwd, "cache")
	first := keyOf(t, r)
	put(t, r.output, "output")
	put(t, filepath.Join(r.root, "entries", "anything"), "cached")
	if keyOf(t, r) != first {
		t.Fatal("generated output/cache invalidated key")
	}
}

func TestInputSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary")
	}
	r := testRecipe(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(r.cwd, "src", "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.key(context.Background()); err == nil {
		t.Fatal("input symlink silently omitted")
	}
}

func testDriver(t *testing.T, limit uint64) *driver {
	t.Helper()
	d, err := openDriver(t.TempDir(), limit)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStoreRestoreAndCorruption(t *testing.T) {
	ctx := context.Background()
	d := testDriver(t, 100)
	output := filepath.Join(t.TempDir(), "output")
	put(t, output, "payload")
	key := strings.Repeat("a", 64)
	if err := d.store(ctx, key, output); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	hit, err := d.restore(ctx, key, output)
	if err != nil || !hit {
		t.Fatalf("restore hit=%v: %v", hit, err)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "payload" {
		t.Fatalf("restored %q: %v", data, err)
	}
	cached, err := os.Stat(filepath.Join(d.entry(key), "output"))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := os.Stat(output)
	if err != nil || !os.SameFile(cached, restored) {
		t.Fatalf("same-filesystem restore did not hardlink: %v", err)
	}
	// Refute a hit after a consumer edits the hardlinked output in place.
	put(t, output, "changed")
	hit, err = d.restore(ctx, key, output)
	if err != nil || hit {
		t.Fatalf("corruption hit=%v: %v", hit, err)
	}
	if _, err := os.Stat(d.entry(key)); !os.IsNotExist(err) {
		t.Fatalf("corrupt entry remains: %v", err)
	}
	if err := d.store(ctx, key, output); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(d.entry(key), "metadata.json"), "{bad")
	hit, err = d.restore(ctx, key, output)
	if err != nil || hit {
		t.Fatalf("metadata corruption hit=%v: %v", hit, err)
	}
}

func TestLRUAndCap(t *testing.T) {
	ctx := context.Background()
	d := testDriver(t, 6)
	output := filepath.Join(t.TempDir(), "output")
	keys := []string{strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)}
	for _, key := range keys[:2] {
		put(t, output, "abc")
		if err := d.store(ctx, key, output); err != nil {
			t.Fatal(err)
		}
	}
	// Force deterministic ages, then a hit must keep A ahead of B for eviction.
	for i, key := range keys[:2] {
		r, err := readRecord(d.entry(key))
		if err != nil {
			t.Fatal(err)
		}
		r.Used = time.Unix(int64(i+1), 0)
		if err := writeRecord(d.entry(key), r); err != nil {
			t.Fatal(err)
		}
	}
	if hit, err := d.restore(ctx, keys[0], output); err != nil || !hit {
		t.Fatalf("hit=%v: %v", hit, err)
	}
	if err := d.store(ctx, keys[2], output); err != nil {
		t.Fatal(err)
	}
	rows, err := d.records()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v: %v", rows, err)
	}
	if _, err := os.Stat(d.entry(keys[1])); !os.IsNotExist(err) {
		t.Fatal("least recently used entry survived")
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	put(t, output, "too large")
	if err := d.store(ctx, keys[1], output); err == nil {
		t.Fatal("oversized output stored")
	}
	// Crash staging is not visible and is reclaimed by the next transaction.
	put(t, filepath.Join(d.root, "entries", ".pending-crash", "output"), "partial")
	d.limit = 1
	count, size, err := d.prune()
	if err != nil || count != 2 || size != 6 {
		t.Fatalf("prune=%d/%d: %v", count, size, err)
	}
	if _, err := os.Stat(filepath.Join(d.root, "entries", ".pending-crash")); !os.IsNotExist(err) {
		t.Fatal("abandoned staging remains")
	}
}

func TestSessionChangedInput(t *testing.T) {
	r := testRecipe(t)
	d, err := openDriver(r.root, 100)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{driver: d, recipe: r, key: keyOf(t, r)}
	put(t, r.output, "generated")
	put(t, filepath.Join(r.cwd, "src", "input"), "changed during job")
	if err := s.Store(context.Background()); err == nil {
		t.Fatal("cached output after input mutation")
	}
	if hit, err := s.Restore(context.Background()); err == nil || hit {
		t.Fatal("restored after input mutation")
	}
	rows, err := d.records()
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%v: %v", rows, err)
	}
}

func TestLockCancellation(t *testing.T) {
	name := filepath.Join(t.TempDir(), "key.lock")
	owner, err := acquire(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := acquire(ctx, name); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error=%v", err)
	}
}

func TestOutputProtection(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source")
	put(t, source, "cached")
	directory := t.TempDir()
	if err := restoreFile(ctx, source, directory, fileMode); err == nil {
		t.Fatal("directory output accepted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	sentinel := filepath.Join(t.TempDir(), "sentinel")
	put(t, sentinel, "outside")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(sentinel, link); err != nil {
		t.Fatal(err)
	}
	if err := restoreFile(ctx, source, link, fileMode); err == nil {
		t.Fatal("symlink destination accepted")
	}
	if err := copyFile(ctx, source, link, fileMode); err == nil {
		t.Fatal("copy overwrote symlink destination")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "outside" {
		t.Fatalf("sentinel=%q: %v", data, err)
	}
}

func isolateHome(t *testing.T) {
	t.Helper()
	keys := map[string]bool{"HOME": true, "USERPROFILE": true, "XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_STATE_HOME": true, "XDG_DATA_HOME": true, "XDG_RUNTIME_DIR": true, "XDG_CONFIG_DIRS": true, "XDG_DATA_DIRS": true}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = true
		}
	}
	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
}

func TestCommandUsage(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{nil, {"unknown"}, {"ls", "extra"}} {
		if code := Run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != exitUsage {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
	for _, args := range [][]string{{"--help"}, {"ls"}, {"prune"}} {
		var out, diagnostics bytes.Buffer
		if code := Run(args, &out, &diagnostics); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, &diagnostics)
		}
	}
}

func TestNoPlaintextMetadata(t *testing.T) {
	r := testRecipe(t)
	r.args = append(r.args, "secret-argument")
	t.Setenv("BATON_CACHE_SECRET", "secret-value")
	r.env = []string{"BATON_CACHE_SECRET"}
	key := keyOf(t, r)
	d, err := openDriver(r.root, 100)
	if err != nil {
		t.Fatal(err)
	}
	put(t, r.output, "artifact")
	if err := d.store(context.Background(), key, r.output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(d.entry(key), "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("secret")) {
		t.Fatalf("plaintext metadata: %s", data)
	}
	var row record
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
}

// Prune is also recovery from malformed generated metadata; a damaged entry
// must not prevent healthy writes from bringing the cache below its cap.
func TestPruneCorruptEntry(t *testing.T) {
	d := testDriver(t, 1)
	key := strings.Repeat("d", 64)
	put(t, filepath.Join(d.entry(key), "metadata.json"), "{interrupted")
	put(t, filepath.Join(d.entry(key), "output"), "bad")
	if _, _, err := d.prune(); err != nil {
		t.Fatalf("corrupt entry blocked recovery: %v", err)
	}
	if _, err := os.Stat(d.entry(key)); !os.IsNotExist(err) {
		t.Fatalf("corrupt entry remains: %v", err)
	}
}
