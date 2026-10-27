//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"syscall"
	"testing"
)

func TestWatchStopsOnSignal(t *testing.T) {
	isolateHome(t)
	command := cliCommand(t, "status", "--json", "--watch")
	pipe, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	line, err := bufio.NewReader(pipe).ReadBytes('\n')
	if err != nil || !json.Valid(line) {
		t.Fatalf("watch=%q err=%v stderr=%s", line, err, &diagnostics)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitCode(t, command, exitOK)
}
