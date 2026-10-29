package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAutoLane(t *testing.T) {
	settings, err := compile(defaults(12, 36*gib, Paths{CacheDir: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"/usr/bin/ffmpeg", "-i", "in"}, "encode"},
		{[]string{"npx", "remotion", "render"}, "render"},
		{[]string{"pnpm", "exec", "playwright", "test"}, "render"},
		{[]string{"python", "-m", "mlx_whisper"}, "ml"},
		{[]string{"whisper", "audio.wav"}, "ml"},
		{[]string{"npm", "i"}, "install"}, {[]string{"pnpm", "ci"}, "install"},
		{[]string{"uv", "pip", "install"}, "install"}, {[]string{"uv", "sync"}, "install"},
		{[]string{"pip3", "install", "thing"}, "install"}, {[]string{"brew", "install", "thing"}, "install"},
		{[]string{"cargo", "install", "thing"}, "install"},
		{[]string{"go", "build", "./..."}, "build"}, {[]string{"xcodebuild", "test"}, "build"},
		{[]string{"cargo", "build"}, "build"}, {[]string{"gradle", "build"}, "build"},
		{[]string{"git", "status"}, ""}, {[]string{"npm", "info"}, ""},
		{[]string{"sh", "-c", "ffmpeg -i in"}, ""},
		// Arguments never select a lane; executables, wrappers, and known
		// dispatcher operations do, exactly as for the Claude hook.
		{[]string{"grep", "-r", "llama", "notes"}, ""},
		{[]string{"git", "checkout", "-b", "fix/playwright-flake"}, ""},
		{[]string{"git", "log", "--grep=chrome"}, ""},
		{[]string{"ls", "chromium-build"}, ""},
		{[]string{"./render-chrome.sh"}, ""},
		{[]string{"ffmpeg", "--version"}, ""},
		{[]string{"env", "FOO=1", "nice", "-n", "5", "ffmpeg", "-i", "in"}, "encode"},
		{[]string{"ollama", "run", "llama3"}, "ml"},
		// Launchers keep the launched tool's own operation.
		{[]string{"python", "-m", "pip", "install", "numpy"}, "install"},
		{[]string{"python3", "-m", "pip", "list"}, ""},
		{[]string{"pnpm", "exec", "cargo", "build"}, "build"},
	} {
		if got := settings.AutoLane(test.args); got != test.want {
			t.Errorf("%v = %q, want %q", test.args, got, test.want)
		}
	}
}

func TestMemoryBudget(t *testing.T) {
	for _, test := range []struct {
		value string
		want  uint64
	}{
		{"4G", 4 << 30}, {"1.5GiB", 3 << 29}, {"512M", 512 << 20}, {"2k", 2048}, {"123B", 123}, {"7", 7},
	} {
		got, err := ParseBytes(test.value)
		if err != nil || got != test.want {
			t.Errorf("%s = %d, %v; want %d", test.value, got, err, test.want)
		}
	}
	for _, text := range []string{"", "0", "-1G", "NaN", "Inf", "18446744073709551616", "4T", "0.1B"} {
		if _, err := ParseBytes(text); err == nil {
			t.Errorf("accepted invalid memory %q", text)
		}
	}
}

func TestDefaultSlots(t *testing.T) {
	for name, want := range map[string]int{"render": 2, "encode": 2, "build": 3, "ml": 1, "install": 1, "x": 1} {
		if got := slotsFor(name, 12, 36*gib); got != want {
			t.Errorf("%s slots %d, want %d", name, got, want)
		}
		if got := slotsFor(name, 1, gib); got != 1 {
			t.Errorf("small machine %s slots %d, want 1", name, got)
		}
	}
}

func TestPartialAndCustom(t *testing.T) {
	doc := defaults(12, 36*gib, Paths{CacheDir: t.TempDir()})
	config, err := decode(`match_order = ['video']
[lanes.render]
slots = 1
[lanes.video]
slots = 4
max_weight = 2
memory_gate = false
max_wait = '3s'
matchers = ['remotion']
[memory]
margin = '1G'
poll_interval = '12ms'
recovery_window = '35ms'
`, doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Lane("render"); got.Slots != 1 || got.MaxWeight != 1 || !got.MemoryGate || got.MaxWait != 30*time.Minute {
		t.Fatalf("partial override lost defaults: %+v", got)
	}
	if got := config.Lane("video"); got.Slots != 4 || got.MaxWeight != 2 || got.MemoryGate || got.MaxWait != 3*time.Second || got.MarginBytes != gib || got.PollInterval != 12*time.Millisecond || got.RecoveryWindow != 35*time.Millisecond {
		t.Fatalf("custom policy: %+v", got)
	}
	if got := config.AutoLane([]string{"npx", "remotion"}); got != "video" {
		t.Fatalf("custom precedence: %q", got)
	}
	if got := config.Lane("ad-hoc"); got.Slots != 1 || got.MaxWeight != 1 || !got.MemoryGate {
		t.Fatalf("ad-hoc lane: %+v", got)
	}
	var encoded bytes.Buffer
	if err := config.Write(&encoded); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := decode(encoded.String(), defaults(1, gib, Paths{CacheDir: t.TempDir()}))
	if err != nil || !reflect.DeepEqual(config.doc, roundTrip.doc) {
		t.Fatalf("effective configuration did not round trip: %v\n%s", err, &encoded)
	}
}

func TestUnsetAndEmptyMatchers(t *testing.T) {
	config, err := decode("[lanes.render]\nmatchers = []\n[lanes.zed]\n[lanes.aaa]\n", defaults(1, gib, Paths{CacheDir: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	if got := config.AutoLane([]string{"npx", "remotion"}); got != "" {
		t.Fatalf("empty matchers did not disable matching: %s", got)
	}
	want := []string{"encode", "render", "ml", "install", "build", "aaa", "zed"}
	if !reflect.DeepEqual(config.doc.MatchOrder, want) {
		t.Fatalf("order %v, want %v", config.doc.MatchOrder, want)
	}
	if got := config.Lane("aaa"); got.Slots != 1 || !got.MemoryGate || got.MaxWait != 30*time.Minute {
		t.Fatalf("new lane missing defaults: %+v", got)
	}
}

func TestRejectBadConfig(t *testing.T) {
	for _, text := range []string{
		"unknown = true", "[lanes.x]\nslots = 'two'", "[lanes.x]\nslots = 0",
		"[lanes.x]\nmax_weight = 0", "[lanes.x]\nmax_weight = 2",
		"[lanes.x]\nmax_wait = '-1s'", "[lanes.x]\nmatchers = ['[']",
		"[lanes.auto]", "[lanes.'']", "[lanes.' x']",
		"match_order = ['missing']", "match_order = ['ml', 'ml']",
		"[memory]\nmargin = 'bad'", "[memory]\npoll_interval = '0s'",
		"[memory]\nrecovery_window = '-1s'", "[memory]\nmargin = 'NaN'",
		"[reap]\nolder_than = '0s'", "[reap]\ngrace_period = 'bad'",
		"[reap]\nmatchers = ['[']", "[gc]\nroots = ['relative']",
		"[gc]\npatterns = ['../outside']", "[gc]\npatterns = ['/absolute']",
		"[gc]\npatterns = ['[']", "[gc]\nolder_than = '-1s'",
		"[cache]\ndir = 'relative'", "[cache]\nmax_size = '0G'",
		"[cache]\nenv = ['A=B']", "[shims]\ncommands = ['../ffmpeg']",
		"[shims.fast_paths]\nffmpeg = ['[']", "[shims.fast_paths]\n'../ffmpeg' = ['.*']",
		"[browser]\nidle_timeout = '0s'", "[browser]\nexecutable = 'relative'",
		"[lanes.x]\nslots = 1\nslots = 2", "[lanes.x]\nslotz = 2",
		"[lanes.x]\nSlots = 3", "[Lanes.x]\nslots = 3",
		"[lanes.x]\nMemory_Gate = false", "[Memory]\nmargin = '1G'",
	} {
		t.Run(text, func(t *testing.T) {
			if _, err := decode(text, defaults(12, 36*gib, Paths{CacheDir: t.TempDir()})); err == nil {
				t.Fatalf("accepted invalid TOML: %s", text)
			}
		})
	}
}

func TestExtraSettings(t *testing.T) {
	root := t.TempDir()
	text := fmt.Sprintf(`[shims]
commands = ['ffmpeg']
[shims.fast_paths]
ffmpeg = ['-version']
[reap]
older_than = '3h'
grace_period = '7s'
matchers = ['ffmpeg']
[gc]
roots = [%q]
patterns = ['**/frames/*.png']
older_than = '24h'
[cache]
dir = %q
max_size = '2G'
env = ['LANG']
[browser]
idle_timeout = '5m'
flags = ['--disable-gpu']
`, root, root)
	config, err := decode(text, defaults(1, gib, Paths{CacheDir: root}))
	if err != nil {
		t.Fatal(err)
	}
	if config.doc.GC.Roots[0] != root || config.doc.Cache.Dir != root || config.doc.Reap.OlderThan != "3h" || config.doc.Shims.FastPaths["ffmpeg"][0] != "-version" || config.doc.Browser.Flags[0] != "--disable-gpu" {
		t.Fatalf("settings lost: %+v", config.doc)
	}
}

func TestPaths(t *testing.T) {
	isolateHome(t)
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, "")
	}
	paths, err := ResolvePaths()
	home := os.Getenv("HOME")
	want := Paths{
		filepath.Join(home, ".config", "baton", "config.toml"),
		filepath.Join(home, ".local", "state", "baton"),
		filepath.Join(home, ".cache", "baton"),
		filepath.Join(home, ".local", "share", "baton"),
	}
	if err != nil || paths != want {
		t.Fatalf("defaults: %+v, %v; want %+v", paths, err, want)
	}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, "relative")
		if _, err := ResolvePaths(); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s: expected absolute path error, got %v", key, err)
		}
		t.Setenv(key, "")
	}
	base := t.TempDir()
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, base)
	}
	paths, err = ResolvePaths()
	if err != nil || paths.ConfigFile != filepath.Join(base, "baton", "config.toml") || paths.StateDir != filepath.Join(base, "baton") || paths.CacheDir != paths.StateDir || paths.DataDir != paths.StateDir {
		t.Fatalf("XDG override: %+v, %v", paths, err)
	}
}

func TestLoadErrorsAndMissing(t *testing.T) {
	isolateHome(t)
	config, err := Load()
	if err != nil || config.Lane("ml").Slots != 1 {
		t.Fatalf("missing file defaults: %v", err)
	}
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.ConfigFile, directoryMode); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), paths.ConfigFile) {
		t.Fatalf("unreadable config must fail explicitly: %v", err)
	}
}

func TestConcurrentInitialize(t *testing.T) {
	isolateHome(t)
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- initialize(paths)
		}()
	}
	workers.Wait()
	close(results)
	var success int
	for err := range results {
		if err == nil {
			success++
			continue
		}
		if !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("initialization winners %d, want one", success)
	}
	if _, err := Load(); err != nil {
		t.Fatalf("atomic initialization left invalid file: %v", err)
	}
	files, err := os.ReadDir(filepath.Dir(paths.ConfigFile))
	if err != nil || len(files) != 1 || files[0].Name() != "config.toml" {
		t.Fatalf("initialization left temporary files: %v, %v", files, err)
	}
}

func TestInitPreservesSymlink(t *testing.T) {
	isolateHome(t)
	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), directoryMode); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "user.toml")
	want := []byte("# user settings\n")
	if err := os.WriteFile(target, want, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.ConfigFile); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := initialize(paths); !os.IsExist(err) {
		t.Fatalf("init did not reject symlink: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("init modified symlink target: %q, %v", got, err)
	}
	link, err := os.Readlink(paths.ConfigFile)
	if err != nil || link != target {
		t.Fatalf("init replaced symlink: %q, %v", link, err)
	}
}

func isolateHome(t *testing.T) {
	t.Helper()
	keys := map[string]bool{"HOME": true, "USERPROFILE": true, "XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true, "XDG_RUNTIME_DIR": true, "XDG_CONFIG_DIRS": true, "XDG_DATA_DIRS": true}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = true
		}
	}
	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("BATON", "")
}

// Matchers a user writes see the full argv, as v0.1.0 documented, so a
// rule such as '^make render$' keeps reserving its lane. Built-in matchers
// see only the executable and operation.
func TestCustomMatchersSeeArgv(t *testing.T) {
	config, err := decode("match_order = ['video']\n[lanes.video]\nmatchers = ['^make render$']\n", defaults(1, gib, Paths{CacheDir: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"make", "render"}, "video"},
		{[]string{"env", "CI=1", "make", "render"}, "video"},
		{[]string{"make", "test"}, ""},
		{[]string{"grep", "-r", "llama", "notes"}, ""},
	} {
		if got := config.AutoLane(test.args); got != test.want {
			t.Errorf("%v = %q, want %q", test.args, got, test.want)
		}
	}
}

// baton itself never takes a lane, even under a catch-all custom matcher:
// an explicit baton run or hold manages its own reservation, and an outer
// one would let the inner run skip its lane's slots as a nested job.
func TestAutoLaneSkipsBaton(t *testing.T) {
	config, err := decode("[lanes.fallback]\nmatchers = ['.*']\n", defaults(1, gib, Paths{CacheDir: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"baton", "run", "--lane", "encode", "--", "ffmpeg", "-i", "in", "out"},
		{"sudo", "/opt/homebrew/bin/baton", "hold", "--lane", "encode", "--pid", "1"},
		{`C:\tools\baton.exe`, "run", "--", "x"},
	} {
		if got := config.AutoLane(args); got != "" {
			t.Errorf("%v = %q, want no lane", args, got)
		}
	}
	if got := config.AutoLane([]string{"ls"}); got != "fallback" {
		t.Fatalf("catch-all lane = %q", got)
	}
}
