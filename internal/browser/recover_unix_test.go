//go:build darwin || linux

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushilk1991/baton/internal/process"
)

// A reused PID or mismatched argv must never become a termination target.
func TestRecoveryIdentity(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	info, err := process.Read(context.Background(), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	saved := owner{PID: info.PID, Start: "wrong-birth", Profile: filepath.Join(root, "profile-test")}
	write := func() {
		t.Helper()
		data, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "owner.json"), data, privateFile); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, err := orphan(root); !errors.Is(err, process.ErrGone) {
		t.Fatal(err)
	}
	if err := recoverChild(root); err != nil {
		t.Fatal(err)
	}
	saved.Start = info.Start
	write()
	if err := recoverChild(root); err == nil {
		t.Fatal("mismatched argv accepted")
	}
	saved.Profile = filepath.Join(t.TempDir(), "profile-other")
	write()
	if err := recoverChild(root); err == nil {
		t.Fatal("outside profile accepted")
	}
}
