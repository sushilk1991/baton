//go:build darwin || linux

package gc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func realTemp(t *testing.T) string {
	t.Helper()
	name, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func oldFile(t *testing.T, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(name, old, old); err != nil {
		t.Fatal(err)
	}
}

func openTestRoot(t *testing.T, name string) *fileRoot {
	t.Helper()
	root, err := openRoot(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(root.close)
	return root
}

func oneCandidate(t *testing.T, root *fileRoot) candidate {
	t.Helper()
	rows, err := discover(root, root.name, []string{"**"}, time.Now().Add(-24*time.Hour))
	if err != nil || len(rows) != 1 {
		t.Fatalf("discover: %v, %v", rows, err)
	}
	return rows[0]
}

// Revalidation must reject each replacement without deleting its contents.
func TestChangedCandidate(t *testing.T) {
	for _, change := range []string{"leaf", "fresh", "rewrite", "symlink", "parent", "root", "gone"} {
		t.Run(change, func(t *testing.T) {
			base := filepath.Join(realTemp(t), "root")
			name := filepath.Join(base, "frames", "1.png")
			oldFile(t, name)
			root := openTestRoot(t, base)
			row := oneCandidate(t, root)
			outside := realTemp(t)
			sentinel := filepath.Join(outside, "1.png")
			oldFile(t, sentinel)
			switch change {
			case "leaf":
				if err := os.Rename(name, name+".saved"); err != nil {
					t.Fatal(err)
				}
				oldFile(t, name)
			case "fresh":
				now := time.Now()
				if err := os.Chtimes(name, now, now); err != nil {
					t.Fatal(err)
				}
			case "rewrite":
				if err := os.WriteFile(name, []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(name, row.info.modified, row.info.modified); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(sentinel, name); err != nil {
					t.Fatal(err)
				}
			case "parent":
				parent := filepath.Dir(name)
				if err := os.Rename(parent, parent+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, parent); err != nil {
					t.Fatal(err)
				}
			case "root":
				if err := os.Rename(base, base+".saved"); err != nil {
					t.Fatal(err)
				}
				oldFile(t, name)
			case "gone":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
			}
			if err := root.remove(row.entry); err == nil {
				t.Fatal("changed candidate was removed")
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "old" {
				t.Fatalf("outside file changed: %q, %v", got, err)
			}
			if change != "gone" {
				if _, err := os.Lstat(name); err != nil {
					t.Fatalf("replacement was deleted: %v", err)
				}
			}
		})
	}
}

func TestRootAndRelativeGuards(t *testing.T) {
	base := realTemp(t)
	root := openTestRoot(t, base)
	alias := filepath.Join(realTemp(t), "alias")
	if err := os.Symlink(base, alias); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{alias, filepath.Join(alias, "child"), "relative", base + "\x00"} {
		if file, err := openRoot(name); err == nil {
			file.close()
			t.Fatalf("unsafe root accepted: %q", name)
		}
	}
	for _, name := range []string{"../outside", "/absolute", "a/../../outside", "a//b", "."} {
		if err := root.remove(entry{name: name}); err == nil {
			t.Fatalf("invalid deletion accepted: %q", name)
		}
		if name == "." {
			continue
		}
		if file, err := root.directory(name); err == nil {
			file.Close()
			t.Fatalf("unsafe traversal accepted: %q", name)
		}
	}
}

// Special files and symlink cycles are neither opened for I/O nor selected.
func TestSpecialAndAgeGuards(t *testing.T) {
	base := realTemp(t)
	oldFile(t, filepath.Join(base, "old.png"))
	cutoff := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	for name, modified := range map[string]time.Time{"boundary.png": cutoff, "future.png": time.Now().Add(time.Hour)} {
		file := filepath.Join(base, name)
		oldFile(t, file)
		if err := os.Chtimes(file, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(base, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base, filepath.Join(base, "cycle")); err != nil {
		t.Fatal(err)
	}
	root := openTestRoot(t, base)
	rows, err := discover(root, base, []string{"**"}, cutoff)
	if err != nil || len(rows) != 1 || rows[0].name != "old.png" {
		t.Fatalf("age or special selection: %v, %v", rows, err)
	}
	rows, err = discover(root, base, nil, cutoff)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty pattern selection: %v, %v", rows, err)
	}
}

// A stop after partial deletion reports the real count and preserves later files.
func TestPartialDeletion(t *testing.T) {
	base := realTemp(t)
	oldFile(t, filepath.Join(base, "1.png"))
	oldFile(t, filepath.Join(base, "2.png"))
	root := openTestRoot(t, base)
	rows, err := discover(root, base, []string{"**"}, time.Now().Add(-time.Hour))
	if err != nil || len(rows) != 2 {
		t.Fatalf("candidates: %v %v", rows, err)
	}
	changed := rows[1]
	if err := os.WriteFile(changed.path, []byte("updated"), 0600); err != nil {
		t.Fatal(err)
	}
	err = deleteRows(&bytes.Buffer{}, rows)
	if err == nil || !strings.Contains(err.Error(), "deleted 1 file(s), 3 bytes") {
		t.Fatalf("partial failure: %v", err)
	}
	if _, err := os.Stat(rows[0].path); !os.IsNotExist(err) {
		t.Fatalf("first candidate not removed: %v", err)
	}
	if got, err := os.ReadFile(changed.path); err != nil || string(got) != "updated" {
		t.Fatalf("changed candidate removed: %q %v", got, err)
	}
}
