package process

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Losing the kernel birth identity would let a reused PID pass revalidation.
func TestReadSelf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("reap discovery is not implemented on Windows")
	}
	isolateHome(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := Read(ctx, os.Getpid())
	if err != nil || info.PID != os.Getpid() || info.Start == "" || info.Started.IsZero() || len(info.Args) == 0 || info.Dead {
		t.Fatalf("self: %+v, %v", info, err)
	}
	rows, err := List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.PID == info.PID && row.Start == info.Start {
			return
		}
	}
	t.Fatalf("self %d missing from snapshot", info.PID)
}

func isolateHome(t *testing.T) {
	t.Helper()
	keys := map[string]bool{"HOME": true, "USERPROFILE": true, "XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true, "XDG_RUNTIME_DIR": true, "XDG_CONFIG_DIRS": true, "XDG_DATA_DIRS": true}
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
