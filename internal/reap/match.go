package reap

import (
	"path/filepath"
	"strings"
)

type provenance uint8

const (
	recordedJob provenance = iota
	adoptedJob
)

func heavy(args []string, opts options, proof provenance) bool {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(args[0])), ".exe")
	if browserName(name) {
		// The dedicated shell is headless even without a headless flag.
		if name == "chrome-headless-shell" {
			return safeBrowser(append([]string{"--headless"}, args[1:]...), opts)
		}
		return safeBrowser(args[1:], opts)
	}
	if name == "ffmpeg" {
		return proof == recordedJob
	}
	if name == "chrome-devtools-mcp" || name == "playwright" || name == "puppeteer" {
		return true
	}
	if name == "remotion" || name == "hyperframes" {
		return proof == recordedJob && hasRender(args[1:])
	}
	if name != "node" && name != "nodejs" {
		return false
	}
	// Only inspect the Node entry script, never arbitrary arguments, eval
	// text, URLs, or paths passed to an unrelated application.
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "-") {
			if arg == "-e" || arg == "--eval" || arg == "-p" || arg == "--print" || strings.HasPrefix(arg, "--eval=") || strings.HasPrefix(arg, "--print=") || (strings.HasPrefix(arg, "-e") && !strings.HasPrefix(arg, "--")) || (strings.HasPrefix(arg, "-p") && !strings.HasPrefix(arg, "--")) {
				return false
			}
			continue
		}
		return driverScript(arg, args[1:], proof)
	}
	return false
}

func driverScript(path string, args []string, proof provenance) bool {
	path = "/" + strings.Trim(strings.ToLower(filepath.ToSlash(path)), "/")
	if strings.Contains(path, "/node_modules/.bin/") {
		name := filepath.Base(path)
		if name == "chrome-devtools-mcp" || name == "playwright" || name == "puppeteer" {
			return true
		}
		return proof == recordedJob && (name == "remotion" || name == "hyperframes") && hasRender(args)
	}
	for _, segment := range []string{"/node_modules/chrome-devtools-mcp/", "/node_modules/playwright/", "/node_modules/playwright-core/", "/node_modules/puppeteer/", "/node_modules/puppeteer-core/", "/playwright/driver/"} {
		if strings.Contains(path, segment) && (strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".mjs")) {
			return true
		}
	}
	if proof != recordedJob {
		return false
	}
	for _, segment := range []string{"/node_modules/@remotion/cli/", "/node_modules/remotion/", "/node_modules/hyperframes/", "/node_modules/@hyperframes/"} {
		if strings.Contains(path, segment) && hasRender(args) {
			return true
		}
	}
	return false
}

func hasRender(args []string) bool {
	for _, arg := range args {
		if arg == "render" || arg == "render-media" || arg == "render-still" {
			return true
		}
	}
	return false
}

func browserName(name string) bool {
	switch name {
	case "chrome", "google chrome", "google chrome canary", "google-chrome", "google-chrome-stable", "google-chrome-beta", "chromium", "chromium-browser", "chrome-headless-shell", "microsoft edge", "msedge":
		return true
	default:
		return false
	}
}

func safeBrowser(args []string, opts options) bool {
	var headless bool
	var profile string
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--headless" || strings.HasPrefix(arg, "--headless=") {
			headless = true
		}
		if strings.HasPrefix(arg, "--user-data-dir=") {
			profile = strings.TrimPrefix(arg, "--user-data-dir=")
			if profile == "" {
				return false
			}
		}
		if arg == "--user-data-dir" {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return false
			}
			profile = args[i+1]
		}
		if profile != "" && (!filepath.IsAbs(profile) || defaultProfile(profile, opts)) {
			return false
		}
	}
	return headless
}

func defaultProfile(profile string, opts options) bool {
	roots := []string{
		filepath.Join(opts.home, "Library", "Application Support", "Google", "Chrome"),
		filepath.Join(opts.home, "Library", "Application Support", "Google", "Chrome Canary"),
		filepath.Join(opts.home, "Library", "Application Support", "Chromium"),
		filepath.Join(opts.home, "Library", "Application Support", "Microsoft Edge"),
	}
	for _, base := range []string{filepath.Join(opts.home, ".config"), filepath.Dir(filepath.Dir(opts.config))} {
		for _, name := range []string{"google-chrome", "google-chrome-beta", "google-chrome-unstable", "chromium", "microsoft-edge"} {
			roots = append(roots, filepath.Join(base, name))
		}
	}
	for _, root := range roots {
		if within(profile, root) {
			return true
		}
	}
	return false
}

func within(path, root string) bool {
	// Resolve existing aliases too, so a temporary symlink cannot disguise a
	// normal user profile. Missing paths still get lexical protection.
	// Case folding conservatively protects case-insensitive filesystem aliases.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	rel, err := filepath.Rel(strings.ToLower(filepath.Clean(root)), strings.ToLower(filepath.Clean(path)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
