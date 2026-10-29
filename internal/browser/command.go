// Package browser manages one owned headless Chrome through a local CDP proxy.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	exitFailure    = 1
	exitUsage      = 2
	privateDir     = 0700
	privateFile    = 0600
	pollTime       = 25 * time.Millisecond
	startTimeout   = 15 * time.Second
	requestTimeout = time.Second
	stopTimeout    = 8 * time.Second
	maxResponse    = 4 << 20
	usage          = "usage: baton browser start|stop|status|endpoint"
	controlHeader  = "X-Baton-Token"
)

var errStopped = errors.New("browser not running; use baton browser start")
var errOrphan = errors.New("browser supervisor gone; use baton browser start or stop to recover")

// These fields are exported only for local JSON encoding, never a Go API.
type session struct {
	URL      string `json:"url"`
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}

type snapshot struct {
	Endpoint string `json:"endpoint"`
	Clients  int    `json:"clients"`
	PID      int    `json:"pid"`
}

// Run dispatches browser lifecycle commands; only start creates a supervisor.
func Run(args []string, out, diagnostics io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return printResult(out, diagnostics, usage)
	}
	if len(args) != 1 || !validAction(args[0]) {
		fmt.Fprintln(diagnostics, usage)
		return exitUsage
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitFailure
	}
	root := filepath.Join(paths.StateDir, "browser")
	if args[0] == "_serve" {
		err = serve(root, diagnostics)
	} else {
		var result string
		result, err = manage(root, args[0])
		if err == nil {
			return printResult(out, diagnostics, result)
		}
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitFailure
	}
	return 0
}

func validAction(action string) bool {
	switch action {
	case "start", "stop", "status", "endpoint", "_serve":
		return true
	default:
		return false
	}
}

func printResult(out, diagnostics io.Writer, result string) int {
	if _, err := fmt.Fprintln(out, result); err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitFailure
	}
	return 0
}

func manage(root, action string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	// Serialize start/stop through readiness and complete shutdown. The
	// supervisor owns a separate lifetime lock, so observations never start it.
	if action == "start" || action == "stop" {
		if err := os.MkdirAll(root, privateDir); err != nil {
			return "", err
		}
		guard, err := acquire(ctx, filepath.Join(root, "control.lock"))
		if err != nil {
			return "", err
		}
		defer guard.Close()
	}
	s, view, err := observeReady(ctx, root)
	if action == "stop" && errors.Is(err, errOrphan) {
		lease, lockErr := lockfile.Try(filepath.Join(root, "browser.lock"))
		if lockErr != nil {
			return "", lockErr
		}
		defer lease.Close()
		if err := recoverChild(root); err != nil {
			return "", err
		}
		return "browser stopped", nil
	}
	if action == "stop" && errors.Is(err, errStopped) {
		return "browser stopped", nil
	}
	if action == "status" && errors.Is(err, errStopped) {
		return "browser stopped; clients 0", nil
	}
	if action == "start" && (errors.Is(err, errStopped) || errors.Is(err, errOrphan)) {
		s, view, err = start(ctx, root)
	}
	if err != nil {
		return "", err
	}
	if action == "stop" {
		if _, err := control(s, "stop"); err != nil {
			return "", err
		}
		deadline := time.Now().Add(stopTimeout)
		for time.Now().Before(deadline) {
			if _, _, err := observe(root); errors.Is(err, errStopped) {
				return "browser stopped", nil
			}
			time.Sleep(pollTime)
		}
		return "", errors.New("browser shutdown timed out")
	}
	if action == "endpoint" || action == "start" {
		return view.Endpoint, nil
	}
	return fmt.Sprintf("browser running; pid %d; clients %d; %s", view.PID, view.Clients, view.Endpoint), nil
}

func acquire(ctx context.Context, path string) (*lockfile.Lock, error) {
	for {
		guard, err := lockfile.Try(path)
		if !errors.Is(err, lockfile.ErrBusy) {
			return guard, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollTime):
		}
	}
}

func observeReady(ctx context.Context, root string) (session, snapshot, error) {
	// Startup and shutdown briefly hold the lifetime lock without a session,
	// with a session Windows will not open yet, or with a session whose
	// control port is closing. Reobserve until ownership settles; corrupt
	// metadata still fails at once.
	for {
		s, view, err := observe(root)
		if !errors.Is(err, os.ErrNotExist) && !sessionBusy(err) && !transport(err) {
			return s, view, err
		}

		select {
		case <-ctx.Done():
			return s, view, fmt.Errorf("%w: %v", ctx.Err(), err)
		case <-time.After(pollTime):
		}
	}
}

// transport reports that the control request never got an HTTP response:
// a refused dial, or a connection dropped mid-request (EOF). observe only
// calls the control port while the lifetime lock is held, so this is the
// shutdown gap: session.json was read just before the supervisor removed it
// and closed its listener. HTTP status and JSON errors are not transport
// errors and still fail at once.
func transport(err error) bool {
	var request *url.Error
	return errors.As(err, &request)
}

func observe(root string) (session, snapshot, error) {
	guard, err := lockfile.TryExisting(filepath.Join(root, "browser.lock"))
	if err == nil {
		state := orphaned(root)
		guard.Close()
		return session{}, snapshot{}, state
	}
	if os.IsNotExist(err) {
		return session{}, snapshot{}, errStopped
	}
	if !errors.Is(err, lockfile.ErrBusy) {
		return session{}, snapshot{}, err
	}
	data, err := os.ReadFile(filepath.Join(root, "session.json"))
	if err != nil {
		return session{}, snapshot{}, fmt.Errorf("browser starting or unavailable: %w", err)
	}
	var s session
	if err := json.Unmarshal(data, &s); err != nil {
		return s, snapshot{}, err
	}
	data, err = control(s, "status")
	if err != nil {
		return s, snapshot{}, err
	}
	var view snapshot
	err = json.Unmarshal(data, &view)
	return s, view, err
}

func control(s session, action string) ([]byte, error) {
	address, err := url.Parse(s.URL)
	if err != nil || address.Scheme != "http" || address.Hostname() != "127.0.0.1" || address.Port() == "" || s.Token == "" {
		return nil, errors.New("invalid local browser session")
	}
	request, err := http.NewRequest(http.MethodPost, s.URL+"/baton/"+action, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(controlHeader, s.Token)
	return fetch(request)
}

func fetch(request *http.Request) ([]byte, error) {
	// Disable environment proxies and redirects: CDP/control stays loopback.
	client := &http.Client{Timeout: requestTimeout, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("browser HTTP status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if len(data) > maxResponse {
		return nil, errors.New("browser response exceeds limit")
	}
	return data, err
}

func start(ctx context.Context, root string) (session, snapshot, error) {
	executable, err := os.Executable()
	if err != nil {
		return session{}, snapshot{}, err
	}
	logPath := filepath.Join(root, "browser.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, privateFile)
	if err != nil {
		return session{}, snapshot{}, err
	}
	defer log.Close()
	command := exec.Command(executable, "browser", "_serve")
	detach(command)
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		return session{}, snapshot{}, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	for {
		s, view, err := observe(root)
		if err == nil {
			return s, view, nil
		}
		select {
		case err := <-done:
			return s, view, fmt.Errorf("browser startup failed (%v); see %s", err, logPath)
		case <-ctx.Done():
			// Signal only the supervisor this invocation started. It owns and
			// cleans up its browser; no persisted PID is ever signalled.
			_ = command.Process.Signal(stopSignal())
			return s, view, fmt.Errorf("browser startup timed out; see %s", logPath)
		case <-time.After(pollTime):
		}
	}
}
