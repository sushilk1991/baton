package browser

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/lockfile"
)

const tokenBytes = 32

type proxy struct {
	sync.Mutex
	session   session
	pid       int
	clients   int
	last      time.Time
	closing   bool
	stop      chan struct{}
	once      sync.Once
	upstream  *url.URL
	transport *http.Transport
}

func serve(root string, diagnostics io.Writer) error {
	if err := os.MkdirAll(root, privateDir); err != nil {
		return err
	}
	lease, err := lockfile.Try(filepath.Join(root, "browser.lock"))
	if err != nil {
		return err
	}
	defer lease.Close()
	if err := recoverChild(root); err != nil {
		return err
	}
	settings, err := config.Load()
	if err != nil {
		return err
	}
	executable, flags, idle := settings.BrowserPolicy()
	executable, err = discover(executable)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), signals()...)
	defer cancel()
	profile, err := os.MkdirTemp(root, "profile-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)
	c, err := launch(executable, profile, flags, diagnostics)
	if err != nil {
		return err
	}
	defer func() {
		c.stop()
		_ = os.Remove(filepath.Join(root, "owner.json"))
	}()
	if err := saveOwner(root, profile, c); err != nil {
		return err
	}
	ready, readyCancel := context.WithTimeout(ctx, startTimeout-requestTimeout)
	defer readyCancel()
	upstream, path, err := c.endpoint(ready, profile)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	secret := make([]byte, tokenBytes)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	p := &proxy{
		session: session{"http://" + listener.Addr().String(), "ws://" + listener.Addr().String() + path, hex.EncodeToString(secret)},
		pid:     c.command.Process.Pid, last: time.Now(), stop: make(chan struct{}), upstream: upstream,
		transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: requestTimeout},
	}
	defer p.transport.CloseIdleConnections()
	server := &http.Server{Handler: p, ReadHeaderTimeout: requestTimeout, IdleTimeout: requestTimeout, ErrorLog: log.New(diagnostics, "browser: ", 0)}
	defer func() {
		// Finish control replies before closing HTTP connections; browser
		// termination below also closes any hijacked WebSocket connections.
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = server.Close()
	}()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	// Publish only a ready proxy. Atomic rename prevents partial session data;
	// the lifetime lock, not a persisted PID, determines whether it is live.
	data, _ := json.Marshal(p.session)
	staging := filepath.Join(root, "session.tmp")
	if err := os.WriteFile(staging, data, privateFile); err != nil {
		return err
	}
	if err := os.Rename(staging, filepath.Join(root, "session.json")); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(root, "session.json"))
	return p.wait(ctx, c, idle, done)
}

func (p *proxy) wait(ctx context.Context, c *child, idle time.Duration, done <-chan error) error {
	interval := idle / 4
	if interval > pollTime {
		interval = pollTime
	}
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.stop:
			return nil
		case <-c.done:
			return errors.New("browser process exited")
		case err := <-done:
			return err
		case now := <-ticker.C:
			p.Lock()
			expired := p.clients == 0 && now.Sub(p.last) >= idle
			if expired {
				p.closing = true
			}
			p.Unlock()
			if expired {
				return nil
			}
		}
	}
}

// ServeHTTP keeps control private and proxies CDP without interpreting frames.
func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != strings.TrimPrefix(p.session.URL, "http://") || r.Header.Get("Origin") != "" {
		http.Error(w, "local clients only", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/baton/") {
		p.control(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/json") {
		p.discovery(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/devtools/") {
		http.NotFound(w, r)
		return
	}
	// A successful 101 adds one connection; ServeHTTP returns on disconnect.
	// Client --ws--> proxy (count) --ws--> private Chrome
	counted := false
	reverse := httputil.NewSingleHostReverseProxy(p.upstream)
	reverse.Transport = p.transport
	reverse.Director = func(request *http.Request) {
		request.URL.Scheme, request.URL.Host = p.upstream.Scheme, p.upstream.Host
		request.Host = p.upstream.Host
	}
	reverse.ModifyResponse = func(response *http.Response) error {
		if response.StatusCode != http.StatusSwitchingProtocols {
			return nil
		}
		p.Lock()
		defer p.Unlock()
		if p.closing {
			return errors.New("browser shutting down")
		}
		p.clients++
		counted = true
		return nil
	}
	reverse.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "browser upstream unavailable", http.StatusBadGateway)
	}
	defer func() {
		if counted {
			p.Lock()
			p.clients--
			p.last = time.Now()
			p.Unlock()
		}
	}()
	reverse.ServeHTTP(w, r)
}

func (p *proxy) control(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || subtle.ConstantTimeCompare([]byte(r.Header.Get(controlHeader)), []byte(p.session.Token)) != 1 {
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}
	p.Lock()
	view := snapshot{p.session.Endpoint, p.clients, p.pid}
	p.Unlock()
	switch r.URL.Path {
	case "/baton/status":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(view)
	case "/baton/stop":
		_, _ = fmt.Fprintln(w, "stopping")
		p.once.Do(func() { close(p.stop) })
	default:
		http.NotFound(w, r)
	}
}

func (p *proxy) discovery(w http.ResponseWriter, r *http.Request) {
	request, err := http.NewRequestWithContext(r.Context(), r.Method, p.upstream.String()+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, "invalid CDP request", http.StatusBadRequest)
		return
	}
	data, err := fetch(request)
	if err != nil {
		http.Error(w, "browser discovery unavailable", http.StatusBadGateway)
		return
	}
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		http.Error(w, "invalid browser discovery", http.StatusBadGateway)
		return
	}
	p.rewrite(value)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (p *proxy) rewrite(value interface{}) {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if key == "webSocketDebuggerUrl" {
				if address, ok := child.(string); ok {
					u, err := url.Parse(address)
					if err == nil {
						v[key] = strings.Replace(p.session.URL, "http://", "ws://", 1) + u.RequestURI()
					}
				}
				continue
			}
			p.rewrite(child)
		}
	case []interface{}:
		for _, child := range v {
			p.rewrite(child)
		}
	}
}
