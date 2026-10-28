//go:build darwin || linux

package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/process"
)

const backgroundRead = "stty -echo; read x; echo GOT=$x"

func TestBackgroundTTY(t *testing.T) {
	isolateHome(t)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "modules"))
	// Build the actual CLI; a private writable module cache keeps cleanup local.
	binary := filepath.Join(t.TempDir(), "baton")
	build := exec.Command("go", "build", "-modcacherw", "-o", binary, "../../cmd/baton")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build baton: %v\n%s", err, output)
	}
	for _, shell := range []string{"/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); os.IsNotExist(err) {
			continue
		}
		for _, mode := range []string{"bare", "baton"} {
			t.Run(filepath.Base(shell)+"/"+mode, func(t *testing.T) {
				testBackgroundTTY(t, binary, shell, mode)
			})
		}
	}
}

func testBackgroundTTY(t *testing.T, binary, shell, mode string) {
	t.Helper()
	isolateHome(t)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("PS1", "baton-test> ")
	t.Setenv("TERM", "dumb")
	configRoot := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton")
	if err := os.MkdirAll(configRoot, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), []byte("[lanes.jc]\nslots = 1\nmemory_gate = false\n"), fileMode); err != nil {
		t.Fatal(err)
	}
	terminal := startJobShell(t, shell)
	if filepath.Base(shell) == "bash" {
		// Disable history expansion so the fixture can capture $! literally.
		terminal.send(t, "set +H\n")
		terminal.await(t, shellPrompt)
	}
	command := "/bin/sh -c " + quoteJobArg(backgroundRead)
	if mode == "baton" {
		command = quoteJobArg(binary) + " run --lane jc -- " + command
	}
	// The shell owns the background job. Capture only its newly started PID.
	terminal.send(t, command+" & background_pid=$!; printf '\\nbackground-pid:%s\\n' \"$background_pid\"\n")
	pidText := terminalPrefix(t, terminal, "background-pid:")
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		t.Fatal(err)
	}
	cleanOwnedGroup(t, pid)
	if mode == "baton" {
		cleanOwnedGroup(t, terminalJob(t).PID)
	}
	terminal.await(t, shellPrompt)
	awaitBackgroundStop(t, terminal)
	if mode == "baton" {
		assertStoppedStatus(t, binary)
	}
	t.Log("shell reports stopped; terminal remains with the interactive shell")

	// fg must transfer the terminal before resuming the stopped read/stty.
	terminal.send(t, "fg\n")
	terminalContains(t, terminal, backgroundRead)
	terminal.send(t, "hello\n")
	terminal.await(t, "GOT=hello")
	terminal.await(t, shellPrompt)
	terminal.send(t, "printf '\\nbackground-exit:%s\\n' \"$?\"\n")
	terminal.await(t, "background-exit:0")
	terminal.await(t, shellPrompt)
	t.Log("fg delivered hello to the child; GOT=hello and exit 0 match the bare command")
	if mode != "baton" {
		return
	}

	// A second job proves that completion released the one-slot reservation.
	terminal.send(t, quoteJobArg(binary)+" run --lane jc -- /bin/echo background-slot-released\n")
	terminal.await(t, "background-slot-released")
	terminal.await(t, shellPrompt)
	jobs := assertHistoryCount(t, 2)
	if jobs[0].ExitCode != 0 || jobs[1].ExitCode != 0 {
		t.Fatalf("background history: %+v", jobs)
	}
	t.Log("next job acquired the released slot; both history exits are 0")
}

func terminalPrefix(t *testing.T, terminal *testTerminal, prefix string) string {
	t.Helper()
	timer := time.NewTimer(terminalTimeout)
	defer timer.Stop()
	var output []string
	for {
		select {
		case line, ok := <-terminal.lines:
			if !ok {
				t.Fatalf("terminal closed awaiting %q: %q", prefix, output)
			}
			output = append(output, line)
			if strings.HasPrefix(line, prefix) {
				return strings.TrimPrefix(line, prefix)
			}
		case <-timer.C:
			t.Fatalf("terminal timed out awaiting %q: %q", prefix, output)
		}
	}
}

func terminalContains(t *testing.T, terminal *testTerminal, want string) {
	t.Helper()
	timer := time.NewTimer(terminalTimeout)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-terminal.lines:
			if !ok {
				t.Fatalf("terminal closed awaiting %q", want)
			}
			if strings.Contains(line, want) {
				return
			}
		case <-timer.C:
			t.Fatalf("terminal timed out awaiting %q", want)
		}
	}
}

func awaitBackgroundStop(t *testing.T, terminal *testTerminal) {
	t.Helper()
	deadline := time.Now().Add(terminalTimeout)
	var listing string
	for time.Now().Before(deadline) {
		// jobs is the interactive shell's source of truth, including on zsh.
		terminal.send(t, "printf '\\nbackground-jobs:%s\\n' \"$(jobs -l)\"\n")
		listing = terminalPrefix(t, terminal, "background-jobs:")
		terminal.await(t, shellPrompt)
		state := strings.ToLower(listing)
		if strings.Contains(state, "stopped") || strings.Contains(state, "suspended") {
			t.Log(listing)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("shell never reported background job stopped: %q", listing)
}

func assertStoppedStatus(t *testing.T, binary string) {
	t.Helper()
	// Query public status in both formats while the supervisor is stopped.
	output, err := exec.Command(binary, "status", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("status: %v\n%s", err, output)
	}
	var view statusView
	if err := json.Unmarshal(output, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Running) != 1 || view.Running[0].Phase != "stopped" {
		t.Fatalf("stopped job missing from status: %s", output)
	}
	for _, lane := range view.Lanes {
		if lane.Name == "jc" && lane.Used != 1 {
			t.Fatalf("stopped job lost its slot: %s", output)
		}
	}
	text, err := exec.Command(binary, "status").CombinedOutput()
	if err != nil || !strings.Contains(string(text), "stopped") {
		t.Fatalf("plain status hides stopped job: %v\n%s", err, text)
	}
	t.Logf("public status reports stopped with its slot held: %s", output)
}

func cleanOwnedGroup(t *testing.T, pid int) {
	t.Helper()
	// Darwin can reject procargs while the just-forked shell is still execing.
	var info process.Info
	var err error
	deadline := time.Now().Add(terminalTimeout)
	for time.Now().Before(deadline) {
		info, err = process.Read(context.Background(), pid)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("owned process identity: %v", err)
	}
	t.Cleanup(func() {
		// Check birth identity so cleanup cannot signal a reused PID.
		current, err := process.Read(context.Background(), pid)
		if err == nil && current.Start == info.Start {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	})
}
