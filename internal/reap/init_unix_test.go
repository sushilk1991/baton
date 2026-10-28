//go:build darwin || linux

package reap

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A detector failure must never count as proof that this is a host.
func TestHostInitProbe(t *testing.T) {
	keys := map[string]struct{}{"HOME": {}, "XDG_CONFIG_HOME": {}, "XDG_CACHE_HOME": {}, "XDG_STATE_HOME": {}, "XDG_DATA_HOME": {}, "XDG_RUNTIME_DIR": {}, "XDG_CONFIG_DIRS": {}, "XDG_DATA_DIRS": {}}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = struct{}{}
		}
	}
	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
	noContainer := exec.Command("/bin/sh", "-c", "exit 1").Run()
	failed := exec.Command("/bin/sh", "-c", "exit 2").Run()
	for _, test := range []struct {
		name, output string
		err          error
		want         bool
	}{
		{"host", "none\n", noContainer, true},
		{"container", "docker\n", nil, false},
		{"container-systemd", "systemd-nspawn\n", nil, false},
		{"missing-detector", "", errors.New("not found"), false},
		{"failed-detector", "none\n", failed, false},
		{"unknown", "", noContainer, false},
		{"false-success", "none\n", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := outsideContainer([]byte(test.output), test.err); got != test.want {
				t.Fatalf("host proof %t, want %t", got, test.want)
			}
		})
	}
	for _, name := range []string{"go", "tini", "docker-init", "launchd", "init-wrapper"} {
		if linuxInit(name) {
			t.Fatalf("non-system-init %q accepted", name)
		}
	}
	for _, name := range []string{"init\n", "systemd\n"} {
		if !linuxInit(name) {
			t.Fatalf("system init %q rejected", name)
		}
	}
}
