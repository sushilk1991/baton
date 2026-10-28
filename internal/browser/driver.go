package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const shutdownGrace = 3 * time.Second

type ending uint8

const (
	graceful ending = iota
	forced
)

type child struct {
	command *exec.Cmd
	done    chan struct{}
}

func launch(executable, profile string, flags []string, log io.Writer) (*child, error) {
	if err := checkFlags(flags); err != nil {
		return nil, err
	}
	// Invariants follow user flags: all sessions are headless, loopback-only,
	// and isolated from interactive Chrome. GPU remains enabled by default.
	args := []string{"--enable-gpu"}
	args = append(args, flags...)
	args = append(args, "--headless=new", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--user-data-dir="+profile, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "about:blank")
	command := exec.Command(executable, args...)
	browserGroup(command)
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		return nil, err
	}
	c := &child{command: command, done: make(chan struct{})}
	go func() {
		_ = command.Wait()
		close(c.done)
	}()
	return c, nil
}

func checkFlags(flags []string) error {
	for _, flag := range flags {
		if !strings.HasPrefix(flag, "--") || strings.ContainsAny(flag, "\x00\r\n") {
			return fmt.Errorf("browser.flags: expected --flag or --flag=value, got %q", flag)
		}
		name, _, _ := strings.Cut(flag, "=")
		if strings.HasPrefix(name, "--remote-debugging-") || name == "--user-data-dir" || name == "--headless" || name == "--no-startup-window" || name == "--app" {
			return fmt.Errorf("browser.flags: %s is managed by baton", name)
		}
	}
	return nil
}

func (c *child) stop() {
	select {
	case <-c.done:
		return
	default:
	}
	endGroup(c.command, graceful)
	select {
	case <-c.done:
	case <-time.After(shutdownGrace):
		endGroup(c.command, forced)
		<-c.done
	}
}

func (c *child) endpoint(ctx context.Context, profile string) (*url.URL, string, error) {
	for {
		data, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) == 2 {
				port, err := strconv.ParseUint(lines[0], 10, 16)
				if err == nil && port != 0 && strings.HasPrefix(lines[1], "/devtools/browser/") && !strings.ContainsAny(lines[1], "?#\r\n") {
					address := "http://" + net.JoinHostPort("127.0.0.1", lines[0])
					request, _ := http.NewRequestWithContext(ctx, http.MethodGet, address+"/json/version", nil)
					if _, err := fetch(request); err == nil {
						u, _ := url.Parse(address)
						return u, lines[1], nil
					}
				}
			}
		}
		select {
		case <-c.done:
			return nil, "", errors.New("browser exited before CDP became ready")
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(pollTime):
		}
	}
}

func discover(configured string) (string, error) {
	if configured != "" {
		return executableFile(configured)
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "microsoft-edge-stable", "chrome-headless-shell", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	for _, path := range systemPaths(runtime.GOOS, home) {
		if path, err := executableFile(path); err == nil {
			return path, nil
		}
	}
	cache := os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	if cache == "" {
		cache = playwrightPath(runtime.GOOS, home)
	}
	if cache != "0" {
		if path, err := cachedBrowser(cache); err == nil {
			return path, nil
		}
	}
	return "", errors.New("no Chrome, Chromium, Edge, or chrome-headless-shell found; set browser.executable")
}

func executableFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0) {
		return "", fmt.Errorf("browser is not executable: %s", path)
	}
	return path, nil
}

func systemPaths(platform, home string) []string {
	if platform == "darwin" {
		var paths []string
		for _, base := range []string{"/Applications", filepath.Join(home, "Applications")} {
			for _, name := range []string{"Google Chrome", "Chromium", "Microsoft Edge", "Google Chrome for Testing"} {
				paths = append(paths, filepath.Join(base, name+".app", "Contents", "MacOS", name))
			}
		}
		return paths
	}
	if platform == "windows" {
		var paths []string
		for _, base := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if base == "" {
				continue
			}
			for _, name := range []string{"Google/Chrome/Application/chrome.exe", "Chromium/Application/chrome.exe", "Microsoft/Edge/Application/msedge.exe"} {
				paths = append(paths, filepath.Join(base, filepath.FromSlash(name)))
			}
		}
		return paths
	}
	return []string{"/opt/google/chrome/chrome", "/opt/microsoft/msedge/msedge"}
}

func playwrightPath(platform, home string) string {
	if platform == "darwin" {
		return filepath.Join(home, "Library", "Caches", "ms-playwright")
	}
	if platform == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "ms-playwright")
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "ms-playwright")
}

func cachedBrowser(root string) (string, error) {
	// Search only known executable layouts; never walk arbitrary home trees.
	patterns := []string{"chromium_headless_shell-*/chrome-*/headless_shell", "chromium_headless_shell-*/chrome-*/headless_shell.exe", "chromium_headless_shell-*/chrome-*/chrome-headless-shell", "chromium_headless_shell-*/chrome-*/chrome-headless-shell.exe", "chromium-*/chrome-*/chrome", "chromium-*/chrome-*/chrome.exe", "chromium-*/chrome-*/Chromium.app/Contents/MacOS/Chromium", "chromium-*/chrome-*/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"}
	for _, pattern := range patterns {
		paths, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		for i := len(paths) - 1; i >= 0; i-- {
			if path, err := executableFile(paths[i]); err == nil {
				return path, nil
			}
		}
	}
	return "", os.ErrNotExist
}
