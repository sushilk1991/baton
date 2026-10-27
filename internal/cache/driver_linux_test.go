//go:build linux

package cache

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Linux's tmpfs provides a real EXDEV failure for the copy fallback.
func TestCrossDeviceRestore(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	put(t, source, "cross-device")
	root, err := os.MkdirTemp("/dev/shm", "baton-cache-test-")
	if err != nil {
		t.Skipf("tmpfs unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Sys().(*syscall.Stat_t).Dev == targetInfo.Sys().(*syscall.Stat_t).Dev {
		t.Skip("no second filesystem")
	}
	target := filepath.Join(root, "output")
	if err := restoreFile(context.Background(), source, target, fileMode); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "cross-device" {
		t.Fatalf("copy=%q: %v", data, err)
	}
	after, err := os.Stat(target)
	if err != nil || os.SameFile(before, after) {
		t.Fatalf("fallback did not create a separate file: %v", err)
	}
	t.Log("EXDEV hardlink failure restored identical bytes by copying")
}
