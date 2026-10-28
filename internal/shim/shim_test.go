package shim

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sushilk1991/baton/internal/lockfile"
)

func TestManagement(t *testing.T) {
	isolateHome(t)
	var out, diagnostics bytes.Buffer
	root := filepath.Join(os.Getenv("XDG_DATA_HOME"), "baton")
	if code := Run([]string{"list"}, &out, &diagnostics); code != 0 || out.Len() != 0 || diagnostics.Len() != 0 {
		t.Fatalf("empty list: %d, %q, %q", code, &out, &diagnostics)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("list created data: %v", err)
	}
	for _, args := range [][]string{nil, {"bad"}, {"list", "extra"}, {"uninstall", "extra"}} {
		if code := Run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != exitUsage {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
	if code := Run([]string{"--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), "baton shim") {
		t.Fatalf("help: %d, %q", code, &out)
	}
	if code := Run([]string{"--help"}, failedWriter{}, &diagnostics); code != exitFailure {
		t.Fatalf("closed output: %d", code)
	}
	// Default installation skips unavailable tools rather than installing
	// wrappers that can never reach a real executable.
	t.Setenv("PATH", t.TempDir())
	out.Reset()
	if code := Run([]string{"install"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), "skipped ffmpeg") {
		t.Fatalf("defaults: %d, %s, %s", code, &out, &diagnostics)
	}
	if code := Run([]string{"install", "missing"}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitFailure {
		t.Fatalf("missing explicit: %d", code)
	}
	for _, name := range []string{"../outside", "baton", "baton.exe", "bad\x1bname", ".", "..", ""} {
		if code := Run([]string{"install", name}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitFailure {
			t.Fatalf("accepted %q: %d", name, code)
		}
	}
	guard, err := lockfile.Try(filepath.Join(root, lockName))
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if code := Run([]string{"uninstall"}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitFailure {
		t.Fatalf("ignored management lock: %d", code)
	}
}

func TestPreserveUserFiles(t *testing.T) {
	for _, mode := range []string{"conflict", "modified", "symlink", "bad manifest", "directory symlink", "output closed"} {
		t.Run(mode, func(t *testing.T) {
			install := fixtureInstall(t)
			path := filepath.Join(install.dir, shimName("tool"))
			body, err := script(install.binary, "tool")
			if err != nil {
				t.Fatal(err)
			}
			owned := map[string]string{}
			if mode == "conflict" {
				writeFile(t, path, "user data", executableMode)
				if err := install.install([]string{"tool"}, owned, &bytes.Buffer{}, explicit); err == nil {
					t.Fatal("overwrote conflict")
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "user data" {
					t.Fatalf("conflict changed: %q, %v", got, err)
				}
				return
			}
			if err := install.install([]string{"tool"}, owned, &bytes.Buffer{}, explicit); err != nil {
				t.Fatal(err)
			}
			if err := install.install([]string{"tool", "tool"}, owned, &bytes.Buffer{}, explicit); err != nil {
				t.Fatal(err)
			}
			if mode == "output closed" {
				if err := install.list(owned, failedWriter{}); err == nil {
					t.Fatal("ignored closed list output")
				}
				return
			}
			if mode == "bad manifest" {
				writeFile(t, filepath.Join(install.root, manifestName), `{"../outside":"bad"}`, fileMode)
				if _, err := install.readManifest(); err == nil {
					t.Fatal("accepted corrupt manifest")
				}
				return
			}
			if mode == "directory symlink" {
				moved := install.dir + "-moved"
				if err := os.Rename(install.dir, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, install.dir); err != nil {
					t.Skip(err)
				}
				if _, err := install.readManifest(); err == nil {
					t.Fatal("followed shim directory symlink")
				}
				return
			}
			if mode == "modified" {
				writeFile(t, path, string(body)+"# user edit\n", executableMode)
			}
			if mode == "symlink" {
				target := filepath.Join(t.TempDir(), "target")
				writeFile(t, target, string(body), executableMode)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skip(err)
				}
			}
			if err := install.uninstall(owned, &bytes.Buffer{}); err == nil {
				t.Fatal("removed changed shim")
			}
			if err := install.install([]string{"tool"}, owned, &bytes.Buffer{}, explicit); err == nil {
				t.Fatal("replaced changed shim")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("changed shim disappeared: %v", err)
			}
		})
	}
}

func TestInterruptedInstall(t *testing.T) {
	install := fixtureInstall(t)
	body, err := script(install.binary, "tool")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(install.dir, shimName("tool"))
	// A crash between publication and manifest update is recoverable only
	// when the existing bytes exactly match what this installer would write.
	writeFile(t, path, string(body), executableMode)
	if err := install.install([]string{"tool"}, map[string]string{}, &bytes.Buffer{}, explicit); err != nil {
		t.Fatal(err)
	}
	owned, err := install.readManifest()
	if err != nil || owned["tool"] != digest(body) {
		t.Fatalf("manifest recovery: %v, %v", owned, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := install.uninstall(owned, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestChainGuard(t *testing.T) {
	isolateHome(t)
	t.Setenv(chainKey, "")
	if err := guardChain("one", nil); err != nil {
		t.Fatal(err)
	}
	if err := guardChain("two", nil); err != nil {
		t.Fatal(err)
	}
	if err := guardChain("one", nil); !errors.Is(err, errRecursion) {
		t.Fatalf("missed indirect recursion: %v", err)
	}
	if err := guardChain("one", []string{"different"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(chainKey, "not JSON")
	if err := guardChain("tool", nil); !errors.Is(err, errRecursion) {
		t.Fatalf("invalid chain: %v", err)
	}
}

func TestResolve(t *testing.T) {
	install := fixtureInstall(t)
	path := os.Getenv("PATH")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.MkdirAll(install.dir, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(install.dir, alias); err != nil {
		t.Skip(err)
	}
	t.Setenv("PATH", strings.Join([]string{alias, install.dir, path}, string(os.PathListSeparator)))
	real, err := install.resolve("tool")
	if err != nil || filepath.Dir(real) != path {
		t.Fatalf("resolution: %q, %v", real, err)
	}
	// A different PATH entry containing a generated wrapper is also excluded.
	wrapperDir := t.TempDir()
	body, err := script(install.binary, "tool")
	if err != nil {
		t.Fatal(err)
	}
	candidate := "tool"
	if runtime.GOOS == "windows" {
		candidate += ".exe"
	}
	writeFile(t, filepath.Join(wrapperDir, candidate), string(body), executableMode)
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+path)
	if got, err := install.resolve("tool"); err != nil || got != real {
		t.Fatalf("selected wrapper: %q, %v", got, err)
	}
	t.Setenv("PATH", wrapperDir)
	if _, err := install.resolve("tool"); !errors.Is(err, errRecursion) {
		t.Fatalf("missed wrapper recursion: %v", err)
	}
}

func fixtureInstall(t *testing.T) installation {
	t.Helper()
	isolateHome(t)
	root, bin := t.TempDir(), t.TempDir()
	name := "tool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	writeFile(t, filepath.Join(bin, name), "real tool", executableMode)
	t.Setenv("PATH", bin)
	return installation{root, filepath.Join(root, "shims"), filepath.Join(root, "baton")}
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
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
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("BATON", "")
	t.Setenv(chainKey, "")
}

type failedWriter struct{}

// Write simulates a failed output stream without touching a real terminal.
func (failedWriter) Write([]byte) (int, error) {
	return 0, errors.New("closed output")
}
