package lane

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestObserveCapacityDamage(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	lane, err := New(root, "x", 1)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := lane.TryAcquire(1)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := os.WriteFile(filepath.Join(lane.dir, capacityFile), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Observe(context.Background(), root, "x", 1); err == nil {
		t.Fatal("accepted corrupt capacity")
	}
}
