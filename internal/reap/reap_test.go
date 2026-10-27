package reap

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/process"
)

// Removing any guard below risks terminating an active or interactive job.
func TestCandidateGuards(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	base := process.Info{PID: 42, Parent: 1, UID: 501, Start: "birth", Started: now.Add(-3 * time.Hour)}
	home := t.TempDir()
	private := filepath.Join(t.TempDir(), "private")
	policy := options{older: 2 * time.Hour, uid: 501, self: 99, home: home, config: filepath.Join(home, ".config", "baton", "config.toml"), match: func([]string) bool { return true }}
	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{"headless", []string{"/opt/Google Chrome", "--headless=new"}, true},
		{"shell", []string{"chrome-headless-shell", "--headless"}, true},
		{"remote-private", []string{"chromium", "--remote-debugging-port=0", "--user-data-dir", "/tmp/private"}, true},
		{"interactive", []string{"chrome", "https://playwright.dev"}, false},
		{"remote-default", []string{"chrome", "--remote-debugging-port=9222"}, false},
		{"explicit-default", []string{"chromium", "--headless", "--user-data-dir=/home/test/.config/chromium"}, false},
		{"case-profile", []string{"chromium", "--headless", "--user-data-dir=/home/test/.config/CHROMIUM"}, false},
		{"duplicate-profile", []string{"chromium", "--headless", "--user-data-dir=/home/test/.config/chromium", "--user-data-dir=/tmp/private"}, false},
		{"after-delimiter", []string{"chrome", "--", "--headless"}, false},
		{"ffmpeg", []string{"/usr/bin/ffmpeg", "-i", "in", "out"}, true},
		{"substring", []string{"my-ffmpeg-script"}, false},
		{"innocent-arg", []string{"sleep", "ffmpeg"}, false},
		{"script-text", []string{"sh", "-c", "ffmpeg -i in out"}, false},
		{"mcp", []string{"node", "/tmp/node_modules/chrome-devtools-mcp/build/src/index.js"}, true},
		{"playwright-driver", []string{"node", "/tmp/node_modules/playwright-core/lib/cli/driver.js"}, true},
		{"puppeteer-driver", []string{"node", "/tmp/node_modules/puppeteer/lib/cjs/puppeteer/node/cli.js"}, true},
		{"remotion-render", []string{"node", "/tmp/node_modules/@remotion/cli/remotion-cli.js", "render"}, true},
		{"hyperframes-render", []string{"hyperframes", "render", "scene"}, true},
		{"remotion-preview", []string{"remotion", "studio"}, false},
		{"node-eval", []string{"node", "--eval=console.log(1)", "/tmp/node_modules/playwright/cli.js"}, false},
		{"project-name", []string{"node", "/work/playwright/server.js"}, false},
		{"bin-driver", []string{"node", "/tmp/node_modules/.bin/playwright", "run-driver"}, true},
		{"unrelated-node", []string{"node", "/work/playwright-notes/server.js"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := base
			row.Args = append([]string{}, test.args...)
			for i, arg := range row.Args {
				row.Args[i] = strings.ReplaceAll(strings.ReplaceAll(arg, "/home/test", home), "/tmp/private", private)
			}
			if got := eligible(row, policy, now, nil); got != test.want {
				t.Fatalf("eligible %v: %t, want %t", test.args, got, test.want)
			}
		})
	}
	base.Args = []string{"ffmpeg"}
	for _, change := range []func(*process.Info){
		func(p *process.Info) { p.Parent = 77 },
		func(p *process.Info) { p.UID = 502 },
		func(p *process.Info) { p.PID = 1 },
		func(p *process.Info) { p.PID = 99 },
		func(p *process.Info) { p.Started = now.Add(-time.Hour) },
		func(p *process.Info) { p.Dead = true },
		func(p *process.Info) { p.Start = "" },
	} {
		row := base
		change(&row)
		if eligible(row, policy, now, nil) {
			t.Fatalf("unsafe candidate: %+v", row)
		}
	}
	policy.match = func([]string) bool { return false }
	if eligible(base, policy, now, nil) {
		t.Fatal("ignored configured matcher restriction")
	}
}

type fakeDriver struct {
	rows    map[int]process.Info
	signals []process.Signal
	mode    string
}

func (d *fakeDriver) Read(_ context.Context, pid int) (process.Info, error) {
	row, ok := d.rows[pid]
	if !ok {
		return process.Info{}, process.ErrGone
	}
	return row, nil
}

func (d *fakeDriver) Send(_ context.Context, row process.Info, sig process.Signal) error {
	d.signals = append(d.signals, sig)
	if d.mode == "fail" {
		return errors.New("denied")
	}
	if d.mode == "exec" {
		row.Args = []string{"sleep"}
		d.rows[row.PID] = row
	}
	if sig == process.Kill || d.mode == "term" {
		delete(d.rows, row.PID)
	}
	return nil
}

// These cases catch PID reuse, lost orphan status, and failed escalation.
func TestTerminateRevalidates(t *testing.T) {
	row := process.Info{PID: 42, Parent: 1, UID: 501, Start: "one", Started: time.Now().Add(-3 * time.Hour), Args: []string{"ffmpeg"}}
	opts := options{older: 2 * time.Hour, grace: time.Millisecond, uid: 501, self: 99, match: func([]string) bool { return true }}
	for _, test := range []struct {
		name, mode string
		changed    process.Info
		want       []process.Signal
		failed     bool
	}{
		{"term", "term", row, []process.Signal{process.Terminate}, false},
		{"kill", "ignore", row, []process.Signal{process.Terminate, process.Kill}, false},
		{"exec", "exec", row, []process.Signal{process.Terminate}, false},
		{"active-parent", "ignore", process.Info{PID: 42, Parent: 7, UID: 501, Start: "one", Started: row.Started, Args: []string{"ffmpeg"}}, nil, false},
		{"reused", "ignore", process.Info{PID: 42, Start: "two"}, nil, false},
		{"gone", "ignore", process.Info{}, nil, false},
		{"denied", "fail", row, []process.Signal{process.Terminate}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := &fakeDriver{rows: map[int]process.Info{42: test.changed}, mode: test.mode}
			err := terminate(context.Background(), driver, []process.Info{row}, opts, nil, io.Discard)
			if (err != nil) != test.failed || !reflect.DeepEqual(driver.signals, test.want) {
				t.Fatalf("signals %v, error %v", driver.signals, err)
			}
		})
	}
}

// Aliases of the normal browser profile retain the same protection.
func TestProfileAlias(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config", "chromium")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	opts := options{home: home, config: filepath.Join(home, ".config", "baton", "config.toml")}
	if safeBrowser([]string{"--headless", "--user-data-dir=" + alias}, opts) {
		t.Fatal("profile alias passed browser guard")
	}
}
