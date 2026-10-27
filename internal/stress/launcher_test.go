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

// Fake only the compiler boundary: the real launcher must isolate HOME/XDG,
// clean its module cache, and preserve the helper's exit status.
func TestLauncherBoundary(t *testing.T) {
	for _, code := range []int{0, 7} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			isolateTest(t)
			root := t.TempDir()
			tools := filepath.Join(root, "tools")
			if err := os.Mkdir(tools, 0700); err != nil {
				t.Fatal(err)
			}
			goStub := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$HOME" > '%s/scratch'
env > '%s/environment'
writable=no
output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -modcacherw) writable=yes ;;
    -o) shift; output=$1 ;;
  esac
  shift
done
mkdir -p "$HOME/go/pkg/mod/fixture"
printf fixture > "$HOME/go/pkg/mod/fixture/source.go"
if [ "$writable" = no ]; then chmod 555 "$HOME/go/pkg/mod/fixture"; fi
printf '#!/bin/sh\nexit %d\n' > "$output"
chmod +x "$output"
`, root, root, code)
			if err := os.WriteFile(filepath.Join(tools, "go"), []byte(goStub), 0700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("sh", "../../scripts/stress.sh")
			command.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH"), "TMPDIR=" + root, "XDG_UNSAFE=outside", "UNSAFE_AGENT_SETTING=outside"}
			out, err := command.CombinedOutput()
			got := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					got = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			data, readErr := os.ReadFile(filepath.Join(root, "scratch"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			scratch := strings.TrimSpace(string(data))
			if _, statErr := os.Stat(scratch); !os.IsNotExist(statErr) || got != code {
				// This fixture alone created the directory. Restore write access
				// before test cleanup when reproducing the read-only-cache bug.
				_ = os.Chmod(filepath.Join(scratch, "go", "pkg", "mod", "fixture"), 0700)
				_ = os.RemoveAll(scratch)
				t.Fatalf("launcher code=%d want=%d; scratch remains=%v\n%s", got, code, statErr, out)
			}
			environment, err := os.ReadFile(filepath.Join(root, "environment"))
			if err != nil || strings.Contains(string(environment), "UNSAFE") || !strings.Contains(string(environment), "HOME="+scratch) {
				t.Fatalf("environment leaked or missing: %v\n%s", err, environment)
			}
		})
	}
}
