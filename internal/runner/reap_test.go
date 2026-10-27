//go:build darwin || linux

package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A stale PID alone must not let reap identify a later, unrelated process.
func TestRecordProcessBirth(t *testing.T) {
	isolateHome(t)
	child := startRunChild(t, t.TempDir(), "count")
	awaitLine(t, child, "admitted")
	t.Cleanup(func() { fmt.Fprintln(child.input, "release"); finishChild(t, child) })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "jobs"))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "jobs", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var record struct {
				Start string `json:"process_start"`
				Phase string `json:"phase"`
			}
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.Phase != "running" {
				continue
			}
			if record.Start == "" {
				t.Fatalf("running job lacks kernel birth identity: %s, %v", data, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("running record not published")
}
