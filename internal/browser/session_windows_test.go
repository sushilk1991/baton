package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

// While the supervisor holds its lifetime lock, Windows can refuse to open
// session.json for a moment: mid-rename it is a sharing violation, and once
// removed while another handle (a virus scan) has it open it stays
// delete-pending and opens fail with ERROR_ACCESS_DENIED. Both are the
// same startup/shutdown gap as a missing session, so observation waits.
//
//	lock held + session busy -> wait -> handle closed, lock released -> stopped
func TestObserveBusySession(t *testing.T) {
	t.Run("sharing", func(t *testing.T) {
		observeBusySession(t, 0, false, sharingViolation)
	})
	t.Run("delete-pending", func(t *testing.T) {
		share := uint32(syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE | syscall.FILE_SHARE_DELETE)
		observeBusySession(t, share, true, syscall.ERROR_ACCESS_DENIED)
	})
}

func observeBusySession(t *testing.T, share uint32, remove bool, want syscall.Errno) {
	t.Helper()
	root := t.TempDir()
	lease, err := lockfile.Try(filepath.Join(root, "browser.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	path := filepath.Join(root, "session.json")
	if err := os.WriteFile(path, []byte("{}"), privateFile); err != nil {
		t.Fatal(err)
	}

	// Another process's handle on the session, as the OS sees it.
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, share, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	release := func() { _ = syscall.CloseHandle(handle) }
	if remove {
		if err := os.Remove(path); err != nil {
			release()
			t.Fatal(err)
		}
	}
	_, err = os.ReadFile(path)
	if remove && errors.Is(err, os.ErrNotExist) {
		release()
		t.Skip("POSIX delete semantics remove the name at once")
	}
	if !errors.Is(err, want) {
		release()
		t.Fatalf("read of a busy session: %v, want %v", err, want)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		_, _, err := observeReady(ctx, root)
		completed <- err
	}()
	select {
	case err := <-completed:
		release()
		t.Fatalf("returned while the session was busy: %v", err)
	case <-time.After(4 * pollTime):
	}

	// The other handle closes and the supervisor releases its lock.
	release()
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		if !errors.Is(err, errStopped) {
			t.Fatalf("after shutdown: %v", err)
		}
	case <-time.After(requestTimeout):
		t.Fatal("observation did not finish after shutdown")
	}
	t.Logf("observation waited out a busy session (%v)", want)
}
