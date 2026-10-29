package runner

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const shareRelease = 50 * time.Millisecond

// A record held open by another reader must still be replaceable once that
// reader closes, as when twenty jobs publish while others list them.
func TestReplaceRecordShared(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "job.json")
	next := filepath.Join(dir, ".record-next")
	for path, body := range map[string]string{target: "old", next: "new"} {
		if err := os.WriteFile(path, []byte(body), fileMode); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := os.Rename(next, target); !sharedBusy(err) {
		t.Fatalf("rename over an open record: %v, want a sharing error", err)
	}

	time.AfterFunc(shareRelease, func() { _ = reader.Close() })
	if err := replaceRecord(next, target); err != nil {
		t.Fatalf("replace after reader closed: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("replaced record %q (%v)", data, err)
	}
	t.Log("rename collided with an open reader, then replaced the record")
}

// A record opened mid-replace must still be readable once the writer's
// handle closes.
func TestReadRecordShared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.json")
	if err := os.WriteFile(path, []byte("record"), fileMode); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// Share mode 0 stands in for the writer: no other open may proceed.
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); !sharedBusy(err) {
		_ = syscall.CloseHandle(handle)
		t.Fatalf("read of an exclusive record: %v, want a sharing error", err)
	}

	time.AfterFunc(shareRelease, func() { _ = syscall.CloseHandle(handle) })
	data, err := readRecord(path)
	if err != nil || string(data) != "record" {
		t.Fatalf("read after writer closed %q (%v)", data, err)
	}
	t.Log("read collided with an exclusive handle, then read the record")
}
