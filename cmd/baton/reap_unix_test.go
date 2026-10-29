//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/process"
)

type fakeParent uint8

const (
	ownedParent fakeParent = iota
	orphanParent
)

type fakeExit uint8

const (
	exitsOnTerm fakeExit = iota
	ignoresTerm
)

// Fakes run the test executable through a named symlink. Their own launcher
// exits, giving real init/launchd adoption without touching existing jobs.
func TestMain(m *testing.M) {
	if os.Getenv("BATON_REAP_HELPER") != "" {
		reapHelper()
	}

	// One shared CLI build for buildShimCLI, removed after every test ran.
	dir, err := os.MkdirTemp("", "baton-cli-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cliBuild.dir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func reapHelper() {
	role := os.Getenv("BATON_REAP_HELPER")
	if role == "" {
		return
	}
	if role == "launcher" {
		child := exec.Command(os.Args[1], os.Args[2:]...)
		child.Env = append(os.Environ(), "BATON_REAP_HELPER=child")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(child.Process.Pid)
		os.Exit(0)
	}
	root := os.Getenv("BATON_REAP_READY")
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	if err := os.WriteFile(root+".ready", []byte("ready"), 0600); err != nil {
		os.Exit(1)
	}
	deadline := time.NewTimer(30 * time.Second)
	for {
		select {
		case <-ch:
			_ = os.WriteFile(root+".term", []byte("TERM"), 0600)
			if os.Getenv("BATON_REAP_IGNORE") != "1" {
				os.Exit(0)
			}
		case <-deadline.C:
			os.Exit(0)
		}
	}
}

func TestReapCLI(t *testing.T) {
	isolateHome(t)
	baton := buildShimCLI(t)
	root := t.TempDir()
	config := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml")
	writeShimFile(t, config, "[reap]\ngrace_period='80ms'\nmatchers=['^"+regexp.QuoteMeta(root)+"/']\n", 0600)
	headless := startReapFake(t, root, "chrome", "headless", orphanParent, exitsOnTerm, "--headless=new")
	remote := startReapFake(t, root, "chromium", "remote", orphanParent, exitsOnTerm, "--remote-debugging-port=0", "--user-data-dir="+filepath.Join(root, "private"))
	stubborn := startReapFake(t, root, "ffmpeg", "stubborn", orphanParent, ignoresTerm)
	interactive := startReapFake(t, root, "chrome", "interactive", orphanParent, exitsOnTerm)
	active := startReapFake(t, root, "ffmpeg", "active", ownedParent, exitsOnTerm)
	profile := startReapFake(t, root, "chrome", "profile", orphanParent, exitsOnTerm, "--headless", "--user-data-dir="+filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Google", "Chrome"))
	unrecorded := startReapFake(t, root, "ffmpeg", "unrecorded", orphanParent, exitsOnTerm)
	all := []process.Info{headless, remote, stubborn, interactive, active, profile, unrecorded}
	// Encoders need provenance everywhere. Containers also need it for
	// headless browsers; headed browsers remain protected with provenance.
	writeReapOrigin(t, stubborn)
	writeReapOrigin(t, remote)
	if runtime.GOOS == "linux" {
		writeReapOrigin(t, headless)
	} else {
		// A stale birth token must not mislabel host-init adoption as ownership.
		stale := headless
		stale.Start = "stale-birth"
		writeReapOrigin(t, stale)
	}

	// The default two-hour threshold must exclude newly orphaned jobs.
	if out := shimCLI(t, baton, 0, "reap"); !strings.Contains(out, "0 reap candidate") {
		t.Fatalf("ignored default age: %s", out)
	}
	out := shimCLI(t, baton, 0, "reap", "--older-than", "0s")
	assertReapPIDs(t, out, []process.Info{headless, stubborn}, []process.Info{remote, interactive, active, profile, unrecorded})
	// Each selected row must explain its own proof before --yes is used.
	headlessReason := "adopted orphan of the system init"
	if runtime.GOOS == "linux" {
		headlessReason = "started by baton, parent dead"
	}
	assertReapReason(t, out, headless, headlessReason)
	assertReapReason(t, out, stubborn, "started by baton, parent dead")
	for _, row := range all {
		assertReapAlive(t, row)
	}
	// A failed listing must abort before any signals, even with --yes.
	if code := run([]string{"reap", "--older-than", "0s", "--yes"}, failedWriter{}, &strings.Builder{}); code != 1 {
		t.Fatalf("closed stdout exit %d", code)
	}
	for _, row := range all {
		assertReapAlive(t, row)
	}
	t.Logf("dry-run selected only two owned orphan fakes:\n%s", out)
	out = shimCLI(t, baton, 0, "reap", "--older-than", "0s", "--yes")
	assertReapReason(t, out, headless, headlessReason)
	assertReapReason(t, out, stubborn, "started by baton, parent dead")
	for _, row := range []process.Info{headless, stubborn} {
		current, err := process.Read(context.Background(), row.PID)
		if err == nil && process.Same(row, current) {
			t.Fatalf("selected process remains alive: %d", row.PID)
		}
	}
	for _, name := range []string{"headless", "stubborn"} {
		if _, err := os.Stat(filepath.Join(root, name) + ".term"); err != nil {
			t.Fatalf("TERM was not observed by %s: %v", name, err)
		}
	}
	for _, row := range []process.Info{remote, interactive, active, profile, unrecorded} {
		assertReapAlive(t, row)
	}
	t.Logf("--yes removed selected identities, including TERM-ignoring fake; five protected fakes remain alive:\n%s", out)
}

// Simulate a crashed baton owner with a real, already-exited helper PID.
func writeReapOrigin(t *testing.T, row process.Info) {
	t.Helper()
	owner := exec.Command("/bin/sh", "-c", "exit 0")
	if err := owner.Run(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"pid": row.PID, "owner_pid": owner.Process.Pid, "process_start": row.Start})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "jobs", strconv.Itoa(row.PID)+".json")
	writeShimFile(t, path, string(data), 0600)
}

func TestReapRecordedOwner(t *testing.T) {
	isolateHome(t)
	baton := buildShimCLI(t)
	root := t.TempDir()
	writeShimFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml"), "[reap]\ngrace_period='50ms'\nmatchers=['^"+regexp.QuoteMeta(root)+"/']\n", 0600)
	row := startReapFake(t, root, "ffmpeg", "recorded", ownedParent, exitsOnTerm)
	owner := exec.Command("/bin/sh", "-c", "exit 0")
	if err := owner.Run(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "jobs", "owned.json")
	writeRecord := func(start string, pid int) {
		data, err := json.Marshal(map[string]any{"pid": row.PID, "owner_pid": pid, "process_start": start})
		if err != nil {
			t.Fatal(err)
		}
		writeShimFile(t, path, string(data), 0600)
	}
	// A stale record and a live owner must not create an orphan candidate.
	writeRecord("unrelated-birth", owner.Process.Pid)
	assertReapPIDs(t, shimCLI(t, baton, 0, "reap", "--older-than", "0s"), nil, []process.Info{row})
	writeRecord(row.Start, os.Getpid())
	assertReapPIDs(t, shimCLI(t, baton, 0, "reap", "--older-than", "0s"), nil, []process.Info{row})
	writeRecord(row.Start, owner.Process.Pid)
	out := shimCLI(t, baton, 0, "reap", "--older-than", "0s")
	assertReapPIDs(t, out, []process.Info{row}, nil)
	assertReapReason(t, out, row, "started by baton, parent dead")
	shimCLI(t, baton, 0, "reap", "--older-than", "0s", "--yes")
	current, err := process.Read(context.Background(), row.PID)
	if err == nil && process.Same(row, current) {
		t.Fatalf("recorded orphan %d remains alive", row.PID)
	}
}

// Container PID 1 adoption cannot authorize cleanup of unrecorded work.
func TestReapContainerInit(t *testing.T) {
	isolateHome(t)
	if runtime.GOOS != "linux" {
		t.Skip("requires a Linux container with a non-system-init PID 1")
	}
	name, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(name)) == "systemd" || strings.TrimSpace(string(name)) == "init" {
		t.Skip("requires a non-system-init PID 1")
	}
	t.Logf("PID 1 executable: %s", strings.TrimSpace(string(name)))
	baton := buildShimCLI(t)
	root := t.TempDir()
	writeShimFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml"), "[reap]\ngrace_period='50ms'\nmatchers=['^"+regexp.QuoteMeta(root)+"/']\n", 0600)
	headless := startReapFake(t, root, "chrome", "headless", orphanParent, exitsOnTerm, "--headless")
	driver := startReapFake(t, root, "playwright", "driver", orphanParent, exitsOnTerm, "run-driver")
	encoder := startReapFake(t, root, "ffmpeg", "encoder", orphanParent, exitsOnTerm)
	rows := []process.Info{headless, driver, encoder}
	assertReapPIDs(t, shimCLI(t, baton, 0, "reap", "--older-than", "0s", "--yes"), nil, rows)
	for _, row := range rows {
		assertReapAlive(t, row)
	}
}

func startReapFake(t *testing.T, root, name, label string, parent fakeParent, exit fakeExit, args ...string) process.Info {
	t.Helper()
	orphan := parent == orphanParent
	ignore := exit == ignoresTerm
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if err := os.Symlink(executable, path); err != nil {
			t.Fatal(err)
		}
	}
	argv := args
	command := exec.Command(path, argv...)
	if orphan {
		launcher := append([]string{path}, argv...)
		command = exec.Command(executable, launcher...)
	}
	role := "child"
	if orphan {
		role = "launcher"
	}
	ignoreValue := "0"
	if ignore {
		ignoreValue = "1"
	}
	command.Env = append(os.Environ(), "BATON_REAP_HELPER="+role, "BATON_REAP_READY="+filepath.Join(root, label), "BATON_REAP_IGNORE="+ignoreValue, "GORACE=atexit_sleep_ms=0")
	var pid int
	if orphan {
		data, err := command.Output()
		if err != nil {
			t.Fatalf("launch fake: %v, %s", err, data)
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatal(err)
		}
	} else {
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		pid = command.Process.Pid
	}
	var row process.Info
	t.Cleanup(func() {
		if row.Start != "" {
			_ = process.Send(context.Background(), row, process.Kill)

		}
		if !orphan {
			_ = command.Wait()
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, err := process.Read(context.Background(), pid)
		_, ready := os.Stat(filepath.Join(root, label) + ".ready")
		if err == nil {
			row = info
		}
		if err == nil && ready == nil && (!orphan || info.Parent == 1) {
			return row
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fake %d not ready/adopted: %+v", pid, row)
	return row
}

func assertReapPIDs(t *testing.T, output string, included, excluded []process.Info) {
	t.Helper()
	for _, row := range included {
		if !strings.Contains(output, fmt.Sprintf("\n%d\t", row.PID)) {
			t.Fatalf("missing %d: %s", row.PID, output)
		}
	}
	for _, row := range excluded {
		if strings.Contains(output, fmt.Sprintf("\n%d\t", row.PID)) {
			t.Fatalf("unsafe selection %d: %s", row.PID, output)
		}
	}
}

// Check the candidate's row, so a reason elsewhere cannot satisfy the test.
func assertReapReason(t *testing.T, output string, row process.Info, reason string) {
	t.Helper()
	if !strings.Contains(output, "PID\tPPID\tAGE\tCOMMAND\tREASON\n") {
		t.Errorf("missing reason column: %s", output)
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, strconv.Itoa(row.PID)+"\t") {
			continue
		}
		if !strings.HasSuffix(line, "\t"+reason) {
			t.Errorf("PID %d: want reason %q, got %q", row.PID, reason, line)
		}
		return
	}
	t.Fatalf("missing candidate %d: %s", row.PID, output)
}

func assertReapAlive(t *testing.T, row process.Info) {
	t.Helper()
	current, err := process.Read(context.Background(), row.PID)
	if err != nil || !process.Same(row, current) {
		t.Fatalf("protected process %d changed: %+v, %v", row.PID, current, err)
	}
}
