//go:build darwin || linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A real CLI must remove only aged matching files below configured roots.
func TestGCCLI(t *testing.T) {
	isolateHome(t)
	baton := buildShimCLI(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	files := map[string]string{
		"out/slice-1.mp4": "slice", "pkg/out/slice-2.mp4": "nested",
		".remotion/deep/frame.png": "frame", "out/master.mp4": "keep",
		"out/slice-new.mp4": "fresh", "out/slice-boundary.mp4.bak": "keep",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		writeShimFile(t, path, body, 0600)
		if name == "out/slice-new.mp4" {
			continue
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := filepath.Join(outside, "out", "slice-outside.mp4")
	writeShimFile(t, sentinel, "outside", 0600)
	if err := os.Chtimes(sentinel, old, old); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"linked": outside, "out/slice-link.mp4": sentinel, "cycle": root} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml")
	writeShimFile(t, path, fmt.Sprintf("[gc]\nroots=[%q,%q,%q]\nolder_than='24h'\n", root, root, filepath.Join(root, "pkg")), 0600)
	listed := shimCLI(t, baton, exitOK, "gc")
	if !strings.Contains(listed, "3 gc candidate(s); 16 bytes") || !strings.Contains(listed, "dry-run") {
		t.Fatalf("wrong selection or sizes: %s", listed)
	}
	for name := range files {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("dry-run changed %s: %v", name, err)
		}
	}
	removed := shimCLI(t, baton, exitOK, "gc", "--yes")
	if !strings.Contains(removed, "Deleted 3 file(s); 16 bytes") {
		t.Fatalf("missing deletion result: %s", removed)
	}
	for _, name := range []string{"out/slice-1.mp4", "pkg/out/slice-2.mp4", ".remotion/deep/frame.png"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("candidate remains %s: %v", name, err)
		}
	}
	for _, name := range []string{"out/master.mp4", "out/slice-new.mp4", "out/slice-boundary.mp4.bak", "linked", "out/slice-link.mp4", "cycle"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err != nil {
			t.Fatalf("protected path changed %s: %v", name, err)
		}
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "outside" {
		t.Fatalf("outside sentinel changed: %q, %v", got, err)
	}
	t.Log(strings.TrimSpace(listed))
	t.Log(strings.TrimSpace(removed))
}

// Output or root-discovery failure must happen before the first deletion.
func TestGCAbortsBeforeDeletion(t *testing.T) {
	isolateHome(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "out", "slice-1.mp4")
	writeShimFile(t, file, "keep", 0600)
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml")
	writeShimFile(t, config, fmt.Sprintf("[gc]\nroots=[%q]\nolder_than='24h'\n", root), 0600)
	if code := run([]string{"gc", "--yes"}, failedWriter{}, &strings.Builder{}); code != exitFailure {
		t.Fatalf("closed output: exit=%d", code)
	}
	// The first root has a valid candidate; the second root must still abort
	// the complete command before any candidate from the first is removed.
	for _, body := range []string{
		fmt.Sprintf("[gc]\nroots=[%q,%q]\nolder_than='24h'\n", root, filepath.Join(root, "missing")),
		"[gc]\npatterns=['../outside']\n",
		"[gc]\nolder_than='0s'\n",
	} {
		writeShimFile(t, config, body, 0600)
		if code := run([]string{"gc", "--yes"}, &strings.Builder{}, &strings.Builder{}); code != exitFailure {
			t.Fatalf("invalid gc policy: exit=%d, %s", code, body)
		}
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "keep" {
		t.Fatalf("failure removed candidate: %q, %v", got, err)
	}
}
