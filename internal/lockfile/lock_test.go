package lockfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestExclusiveLock(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "slot.lock")
	first, err := Try(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })

	// Independent opens must conflict even within one process.
	if second, err := Try(path); !errors.Is(err, ErrBusy) || second != nil {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second lock = %v, %v; want nil, ErrBusy", second, err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Try(path)
	if err != nil {
		t.Fatalf("relock after close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	// Lock paths remain stable; unlinking would allow two separate inodes.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock path removed: %v", err)
	}
}

func TestCloseConcurrent(t *testing.T) {
	isolateHome(t)
	lock, err := Try(filepath.Join(t.TempDir(), "slot.lock"))
	if err != nil {
		t.Fatal(err)
	}

	// Cleanup callers may race; all must observe the same release result.
	var callers sync.WaitGroup
	for i := 0; i < 10; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if err := lock.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	callers.Wait()
}

func TestOpenFailure(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "missing", "slot.lock")
	if lock, err := Try(path); err == nil || lock != nil {
		t.Fatalf("lock = %v, %v; want open error", lock, err)
	}
}

func isolateHome(t *testing.T) {
	t.Helper()

	// Replace all HOME/XDG paths so these tests cannot touch agent settings.
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

func TestExistingNeverCreates(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "missing.lock")
	if _, err := TryExisting(path); !os.IsNotExist(err) {
		t.Fatalf("missing existing lock: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("probe created lock: %v", err)
	}
	held, err := Try(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := TryExisting(path); !errors.Is(err, ErrBusy) {
		t.Fatalf("held lock: %v", err)
	}
}
