//go:build darwin || linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShimFallback(t *testing.T) {
	isolateShim(t)
	built := buildShimCLI(t)
	data, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	// A missing or disabled scheduler must leave installed tools usable,
	// including BATON=off and the real tool's nonzero exit status.
	for _, failure := range []string{"moved", "not executable"} {
		for _, bypass := range []string{"", "off"} {
			for _, code := range []int{0, 3} {
				t.Run(fmt.Sprintf("%s/off=%s/exit=%d", failure, bypass, code), func(t *testing.T) {
					isolateShim(t)
					baton := filepath.Join(t.TempDir(), "baton's binary")
					writeShimFile(t, baton, string(data), 0700)
					realDir := t.TempDir()
					writeShimFile(t, filepath.Join(realDir, "ffmpeg"), "#!/bin/sh\ncase \":$PATH:\" in *\":$SHIM_DIR:\"*|*\":$SHIM_ALIAS:\"*) exit 99 ;; esac\nprintf '%s\\n' \"$@\"\nexit "+fmt.Sprint(code)+"\n", 0700)
					t.Setenv("PATH", realDir)
					shimCLI(t, baton, 0, "shim", "install", "ffmpeg")
					dir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "shims")
					alias := filepath.Join(t.TempDir(), "shim alias")
					if err := os.Symlink(dir, alias); err != nil {
						t.Fatal(err)
					}
					copied := t.TempDir()
					wrapper, err := os.ReadFile(filepath.Join(dir, "ffmpeg"))
					if err != nil {
						t.Fatal(err)
					}
					writeShimFile(t, filepath.Join(copied, "ffmpeg"), string(wrapper), 0700)
					t.Setenv("SHIM_DIR", dir)
					t.Setenv("SHIM_ALIAS", alias)
					t.Setenv("PATH", strings.Join([]string{alias, dir, copied, realDir, alias}, string(os.PathListSeparator)))
					t.Setenv("BATON", bypass)
					writeShimFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml"), "invalid = [", 0600)
					if failure == "moved" {
						if err := os.Rename(baton, baton+"-moved"); err != nil {
							t.Fatal(err)
						}
					} else if err := os.Chmod(baton, 0600); err != nil {
						t.Fatal(err)
					}
					got := shimCLI(t, "/bin/sh", code, "-c", `exec ffmpeg "$@"`, "sh", "one argument", "", "$(untouched)", "a'b")
					if want := "one argument\n\n$(untouched)\na'b\n"; got != want {
						t.Fatalf("fallback argv: got %q, want %q", got, want)
					}
					if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")); !os.IsNotExist(err) {
						t.Fatalf("fallback touched scheduler state: %v", err)
					}
					// Exhausting wrapper-only PATH entries must fail promptly, not
					// re-enter a shim or implicitly search the working directory.
					t.Setenv("PATH", strings.Join([]string{alias, dir, copied}, string(os.PathListSeparator)))
					shimCLI(t, "/bin/sh", 127, "-c", `cd "$1" && exec "$2"`, "sh", realDir, filepath.Join(alias, "ffmpeg"))
				})
			}
		}
	}
}

func TestShimUpgradePath(t *testing.T) {
	isolateShim(t)
	old := buildShimCLI(t)
	installed := filepath.Join(t.TempDir(), "baton")
	if err := os.Symlink(old, installed); err != nil {
		t.Fatal(err)
	}
	realDir := t.TempDir()
	writeShimFile(t, filepath.Join(realDir, "ffmpeg"), "#!/bin/sh\nprintf executed\n", 0700)
	writeShimFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml"), "[lanes.encode]\nslots = 1\nmemory_gate = false\n", 0600)
	// Exercise bare PATH lookup and a relative symlink without binding the
	// wrapper to the versioned target that an upgrade removes.
	for _, invocation := range []string{"PATH", "relative", "relative PATH"} {
		t.Run(invocation, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Setenv("PATH", filepath.Dir(installed)+string(os.PathListSeparator)+realDir)
			if invocation == "PATH" {
				shimCLI(t, "baton", 0, "shim", "install", "ffmpeg")
			} else {
				command := exec.Command("./baton", "shim", "install", "ffmpeg")
				if invocation == "relative PATH" {
					t.Setenv("PATH", "."+string(os.PathListSeparator)+realDir)
					command = exec.Command("/bin/sh", "-c", "exec baton shim install ffmpeg")
				}
				command.Dir = filepath.Dir(installed)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("relative install: %v\n%s", err, output)
				}
			}
			shim := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "shims", "ffmpeg")
			body, err := os.ReadFile(shim)
			if err != nil || !strings.Contains(string(body), installed) {
				t.Fatalf("shim lost installed symlink %q: %s (%v)", installed, body, err)
			}
		})
	}
	// The same installed link must keep scheduling after its old target moves.
	dir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton", "shims")
	t.Setenv("PATH", realDir)
	shimCLI(t, installed, 0, "shim", "install", "ffmpeg")
	updated := old + "-upgraded"
	if err := os.Rename(old, updated); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(updated, installed); err != nil {
		t.Fatal(err)
	}
	if got := shimCLI(t, filepath.Join(dir, "ffmpeg"), 0); got != "executed" {
		t.Fatalf("upgraded shim: %q", got)
	}
	history := shimCLI(t, installed, 0, "history", "--json")
	if !strings.Contains(history, `"lane":"encode"`) {
		t.Fatalf("upgrade bypassed scheduling: %s", history)
	}
}
