//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeBrowserEnv = "BATON_TEST_FAKE_BROWSER"

func cliBrowser(baton, action string) *exec.Cmd {
	return exec.Command(baton, "browser", action)
}

// This owned subprocess exposes just enough CDP transport to test lifecycle.
func TestBrowserFake(t *testing.T) {
	if os.Getenv(fakeBrowserEnv) != "1" {
		return
	}
	profile := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--user-data-dir=") {
			profile = strings.TrimPrefix(arg, "--user-data-dir=")
		}
	}
	if profile == "" {
		os.Exit(9)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		os.Exit(10)
	}
	calls, err := os.OpenFile(os.Getenv("BATON_TEST_BROWSER_CALLS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(11)
	}
	_, _ = fmt.Fprintln(calls, "start")
	_ = calls.Close()
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	if err := os.WriteFile(filepath.Join(profile, "DevToolsActivePort"), []byte(port+"\n/devtools/browser/owned-test\n"), 0600); err != nil {
		os.Exit(12)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": "ws://" + listener.Addr().String() + "/devtools/browser/owned-test"})
			return
		}
		if r.URL.Path != "/devtools/browser/owned-test" {
			http.NotFound(w, r)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = fmt.Fprint(connection, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_, _ = io.Copy(io.Discard, connection)
	})
	_ = http.Serve(listener, handler)
	os.Exit(0)
}

// Real CLI processes prove singleton startup, connections, idle expiry, and stop.
func TestBrowserLifecycleCLI(t *testing.T) {
	isolateHome(t)
	baton := buildShimCLI(t)
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "fake-chrome")
	quoted := "'" + strings.ReplaceAll(testBinary, "'", "'\\''") + "'"
	writeShimFile(t, fake, "#!/bin/sh\nexec "+quoted+" -test.run=^TestBrowserFake$ -- \"$@\"\n", 0700)
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv(fakeBrowserEnv, "1")
	t.Setenv("BATON_TEST_BROWSER_CALLS", calls)
	writeBrowserConfig(t, fake, "1s")
	t.Cleanup(func() {
		// Only the supervisor in this test's private state root can receive stop.
		shimCLI(t, baton, exitOK, "browser", "stop")
	})
	const callers = 6
	type result struct {
		output []byte
		err    error
	}
	results := make(chan result, callers)
	for i := 0; i < callers; i++ {
		go func() {
			output, err := cliBrowser(baton, "start").CombinedOutput()
			results <- result{output, err}
		}()
	}
	endpoint := ""
	for i := 0; i < callers; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("start: %v: %s", r.err, r.output)
		}
		got := strings.TrimSpace(string(r.output))
		if endpoint != "" && got != endpoint {
			t.Fatalf("different browsers: %s, %s", endpoint, got)
		}
		endpoint = got
	}
	data, err := os.ReadFile(calls)
	if err != nil || string(data) != "start\n" {
		t.Fatalf("browser launches=%q: %v", data, err)
	}
	if got := strings.TrimSpace(shimCLI(t, baton, exitOK, "browser", "endpoint")); got != endpoint {
		t.Fatalf("endpoint=%s, want %s", got, endpoint)
	}
	first := browserSocket(t, endpoint)
	defer first.Close()
	second := browserSocket(t, endpoint)
	defer second.Close()
	waitBrowserStatus(t, baton, "clients 2")
	time.Sleep(1100 * time.Millisecond)
	waitBrowserStatus(t, baton, "clients 2")
	first.Close()
	waitBrowserStatus(t, baton, "clients 1")
	second.Close()
	waitBrowserStatus(t, baton, "clients 0")
	waitBrowserStatus(t, baton, "browser stopped")
	shimCLI(t, baton, exitFailure, "browser", "endpoint")
	endpoint = strings.TrimSpace(shimCLI(t, baton, exitOK, "browser", "start"))
	active := browserSocket(t, endpoint)
	defer active.Close()
	waitBrowserStatus(t, baton, "clients 1")
	stopped := shimCLI(t, baton, exitOK, "browser", "stop")
	_ = active.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := active.Read(make([]byte, 1)); err == nil {
		t.Fatal("stop left active transport open")
	}
	entries, err := os.ReadDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton", "browser"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "profile-") || entry.Name() == "session.json" {
			t.Fatalf("shutdown retained %s", entry.Name())
		}
	}
	t.Log("6 concurrent starts launched 1 browser; clients 2 -> 1 -> 0; active clients survived idle timeout; idle shutdown and restart passed")
	t.Log(strings.TrimSpace(stopped))
}

func writeBrowserConfig(t *testing.T, executable, idle string) {
	t.Helper()
	writeShimFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml"), fmt.Sprintf("[browser]\nexecutable=%q\nidle_timeout=%q\n", executable, idle), 0600)
}

func waitBrowserStatus(t *testing.T, baton, want string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		got = shimCLI(t, baton, exitOK, "browser", "status")
		if strings.Contains(got, want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("status=%q, want %q", got, want)
}

func browserSocket(t *testing.T, endpoint string) net.Conn {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", u.RequestURI(), u.Host)
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
		connection.Close()
		t.Fatalf("WebSocket upgrade: %v, %v", response, err)
	}
	_ = connection.SetDeadline(time.Time{})
	return connection
}

// A configured executable failure must be nonzero and must release ownership.
func TestBrowserStartupFailureCLI(t *testing.T) {
	isolateHome(t)
	baton := buildShimCLI(t)
	fake := filepath.Join(t.TempDir(), "exit-chrome")
	writeShimFile(t, fake, "#!/bin/sh\nexit 23\n", 0700)
	writeBrowserConfig(t, fake, "1s")
	failed := shimCLI(t, baton, exitFailure, "browser", "start")
	if !strings.Contains(failed, "browser.log") {
		t.Fatal(failed)
	}
	waitBrowserStatus(t, baton, "browser stopped")
	var out, diagnostic bytes.Buffer
	if code := run([]string{"browser", "status"}, failedWriter{}, &diagnostic); code != exitFailure {
		t.Fatalf("closed stdout: exit %d, %s", code, &out)
	}
}

// Opt-in proof uses only an explicitly named browser and a disposable profile.
func TestBrowserRealCLI(t *testing.T) {
	executable := os.Getenv("BATON_TEST_REAL_BROWSER")
	if executable == "" {
		t.Skip("set BATON_TEST_REAL_BROWSER to an absolute Chrome executable")
	}
	isolateHome(t)
	baton := buildShimCLI(t)
	writeBrowserConfig(t, executable, "10s")
	t.Cleanup(func() { shimCLI(t, baton, exitOK, "browser", "stop") })
	endpoint := strings.TrimSpace(shimCLI(t, baton, exitOK, "browser", "start"))
	connection := browserSocket(t, endpoint)
	defer connection.Close()
	waitBrowserStatus(t, baton, "clients 1")
	// A real masked WebSocket frame proves CDP request/response forwarding.
	message := []byte(`{"id":1,"method":"Browser.getVersion"}`)
	mask := []byte{1, 2, 3, 4}
	frame := append([]byte{0x81, 0x80 | byte(len(message))}, mask...)
	for i, value := range message {
		frame = append(frame, value^mask[i%len(mask)])
	}
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := connection.Write(frame); err != nil {
		t.Fatal(err)
	}
	var header [2]byte
	if _, err := io.ReadFull(connection, header[:]); err != nil {
		t.Fatal(err)
	}
	length := uint64(header[1] & 0x7f)
	if length == 126 {
		var size [2]byte
		if _, err := io.ReadFull(connection, size[:]); err != nil {
			t.Fatal(err)
		}
		length = uint64(binary.BigEndian.Uint16(size[:]))
	}
	if length > 4096 || length == 127 {
		t.Fatalf("unexpected CDP frame length %d", length)
	}
	response := make([]byte, length)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	var version struct {
		ID     int `json:"id"`
		Result struct {
			Product string `json:"product"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &version); err != nil || version.ID != 1 || version.Result.Product == "" {
		t.Fatalf("CDP response=%s: %v", response, err)
	}
	t.Logf("Browser.getVersion -> %s", response)
	connection.Close()
	waitBrowserStatus(t, baton, "clients 0")
	t.Log(strings.TrimSpace(shimCLI(t, baton, exitOK, "browser", "stop")))
}

// SIGKILL cannot run cleanup; the next start must retire the recorded child.
func TestBrowserCrashRecoveryCLI(t *testing.T) {
	isolateHome(t)
	baton := buildShimCLI(t)
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "fake-chrome")
	quoted := "'" + strings.ReplaceAll(testBinary, "'", "'\\''") + "'"
	writeShimFile(t, fake, "#!/bin/sh\nexec "+quoted+" -test.run=^TestBrowserFake$ -- \"$@\"\n", 0700)
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv(fakeBrowserEnv, "1")
	t.Setenv("BATON_TEST_BROWSER_CALLS", calls)
	writeBrowserConfig(t, fake, "10s")
	supervisor := cliBrowser(baton, "_serve")
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = supervisor.Process.Kill()
		shimCLI(t, baton, exitOK, "browser", "stop")
	})
	// Wait without assuming the asynchronous server has published its state.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := cliBrowser(baton, "status").CombinedOutput()
		if strings.Contains(string(data), "browser running") {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	old := strings.TrimSpace(shimCLI(t, baton, exitOK, "browser", "endpoint"))
	if err := supervisor.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = supervisor.Wait()
	if out := shimCLI(t, baton, exitFailure, "browser", "status"); !strings.Contains(out, "supervisor gone") {
		t.Fatal(out)
	}
	newEndpoint := strings.TrimSpace(shimCLI(t, baton, exitOK, "browser", "start"))
	if old == newEndpoint {
		t.Fatal("restart reused stale endpoint")
	}
	waitBrowserStatus(t, baton, "clients 0")
	data, err := os.ReadFile(calls)
	if err != nil || string(data) != "start\nstart\n" {
		t.Fatalf("launches=%s, %v", data, err)
	}
	t.Log("SIGKILL supervisor -> orphan detected -> verified old browser terminated -> one replacement started")
}
