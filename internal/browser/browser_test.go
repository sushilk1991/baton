package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isolate(t *testing.T) {
	t.Helper()
	keys := map[string]bool{"HOME": true, "USERPROFILE": true, "LOCALAPPDATA": true, "XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_STATE_HOME": true, "XDG_DATA_HOME": true, "XDG_RUNTIME_DIR": true, "XDG_CONFIG_DIRS": true, "XDG_DATA_DIRS": true}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = true
		}
	}
	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
}

// Known binary layouts work without depending on the host's installed browser.
func TestDiscovery(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	file := filepath.Join(root, "chromium_headless_shell-1234", "chrome-headless-shell-linux64", "chrome-headless-shell")
	if err := os.MkdirAll(filepath.Dir(file), privateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("fake"), 0700); err != nil {
		t.Fatal(err)
	}
	// Resource files can share the executable prefix. Exact basenames also
	// work on Windows, where permission bits cannot identify executables.
	if err := os.WriteFile(file+".pak", []byte("resource"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := cachedBrowser(root)
	if err != nil || got != file {
		t.Fatalf("cached=%s, %v; want %s", got, err, file)
	}
	got, err = discover(file)
	if err != nil || got != file {
		t.Fatalf("configured=%s, %v", got, err)
	}
	if _, err := discover(root); err == nil {
		t.Fatal("directory accepted as executable")
	}
	if _, err := cachedBrowser(t.TempDir()); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, platform := range []string{"darwin", "linux", "windows"} {
		if !strings.Contains(playwrightPath(platform, os.Getenv("HOME")), "ms-playwright") {
			t.Fatal(platform)
		}
	}
	t.Setenv("PROGRAMFILES", t.TempDir())
	if len(systemPaths("windows", root)) == 0 || len(systemPaths("darwin", root)) == 0 {
		t.Fatal("missing OS discovery paths")
	}
}

func TestManagedFlags(t *testing.T) {
	isolate(t)
	for _, flag := range []string{"--user-data-dir=/tmp/real", "--remote-debugging-port=9222", "--remote-debugging-pipe", "--headless=false", "https://example.com", "--flag\nvalue"} {
		if err := checkFlags([]string{flag}); err == nil {
			t.Fatalf("accepted %q", flag)
		}
	}
	if err := checkFlags([]string{"--disable-gpu", "--window-size=800,600", "--no-sandbox"}); err != nil {
		t.Fatal(err)
	}
}

// Count successful upgrades, including page sockets, rather than page targets.
func TestProxyClients(t *testing.T) {
	isolate(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": "ws://127.0.0.1:9/devtools/browser/fake"})
			return
		}
		if r.URL.Path != "/devtools/browser/fake" && r.URL.Path != "/devtools/page/fake" {
			http.NotFound(w, r)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = fmt.Fprint(connection, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		var buf [1]byte
		_, _ = connection.Read(buf[:])
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	p := &proxy{upstream: target, last: time.Now(), stop: make(chan struct{}), transport: &http.Transport{Proxy: nil}}
	server := httptest.NewUnstartedServer(p)
	p.session = session{URL: "http://" + server.Listener.Addr().String(), Endpoint: "ws://" + server.Listener.Addr().String() + "/devtools/browser/fake", Token: "test-private-token"}
	server.Start()
	defer server.Close()
	defer p.transport.CloseIdleConnections()
	request, _ := http.NewRequest(http.MethodGet, p.session.URL+"/json/version", nil)
	data, err := fetch(request)
	if err != nil || !strings.Contains(string(data), p.session.Endpoint) || strings.Contains(string(data), ":9/") {
		t.Fatalf("rewritten discovery=%s, %v", data, err)
	}
	first := connectTestWS(t, p.session.Endpoint)
	defer first.Close()
	second := connectTestWS(t, strings.Replace(p.session.Endpoint, "/browser/", "/page/", 1))
	defer second.Close()
	waitClients(t, p, 2)
	first.Close()
	waitClients(t, p, 1)
	second.Close()
	waitClients(t, p, 0)
	// Rejected upgrades never acquire a client, and web pages cannot reach CDP.
	request, _ = http.NewRequest(http.MethodGet, p.session.URL+"/devtools/missing", nil)
	if _, err := fetch(request); err == nil {
		t.Fatal("missing upstream path succeeded")
	}
	request, _ = http.NewRequest(http.MethodGet, p.session.URL+"/json/version", nil)
	request.Header.Set("Origin", "https://untrusted.invalid")
	if _, err := fetch(request); err == nil {
		t.Fatal("cross-origin discovery succeeded")
	}
	request, _ = http.NewRequest(http.MethodPost, p.session.URL+"/baton/stop", nil)
	if _, err := fetch(request); err == nil {
		t.Fatal("unauthenticated shutdown succeeded")
	}
	if _, err := control(session{URL: "http://example.com", Token: "x"}, "status"); err == nil {
		t.Fatal("remote control endpoint accepted")
	}
	if _, err := control(p.session, "status"); err != nil {
		t.Fatal(err)
	}
}

func connectTestWS(t *testing.T, endpoint string) net.Conn {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", u.Host, requestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Now().Add(requestTimeout))
	_, err = fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", u.RequestURI(), u.Host)
	if err != nil {
		connection.Close()
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
		connection.Close()
		t.Fatalf("upgrade: %v, %v", response, err)
	}
	_ = connection.SetDeadline(time.Time{})
	return connection
}

func waitClients(t *testing.T, p *proxy, want int) {
	t.Helper()
	deadline := time.Now().Add(requestTimeout)
	for time.Now().Before(deadline) {
		p.Lock()
		got := p.clients
		p.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("clients did not become %d", want)
}

// Active sockets prevent idle expiry; the last disconnect begins a new interval.
func TestIdleLifetime(t *testing.T) {
	isolate(t)
	p := &proxy{clients: 1, last: time.Now().Add(-time.Hour), stop: make(chan struct{})}
	c := &child{done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.wait(ctx, c, 30*time.Millisecond, make(chan error)) }()
	select {
	case err := <-done:
		t.Fatalf("active client expired: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	p.Lock()
	p.clients = 0
	p.last = time.Now()
	p.Unlock()
	select {
	case err := <-done:
		t.Fatal("expired before disconnect idle interval", err)
	case <-time.After(10 * time.Millisecond):
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("did not expire")
	}
}
