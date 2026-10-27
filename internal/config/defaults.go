package config

import (
	"context"
	"runtime"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
)

const (
	gib           = uint64(1 << 30)
	renderGiB     = 16
	encodeCores   = 6
	buildCores    = 4
	defaultSlots  = 1
	defaultWait   = "30m"
	hardwareProbe = 2 * time.Second
	reapAge       = "2h"
	reapGrace     = "5s"
	gcAge         = "168h"
	cacheSize     = "10G"
	browserIdle   = "10m"
	memoryMargin  = "512M"
	pollInterval  = "250ms"
	recoveryTime  = "2s"
)

// These private DTOs expose fields only for TOML reflection. Other packages
// receive resolved policy rather than mutable decoder state.
type document struct {
	MatchOrder []string              `toml:"match_order"`
	Lanes      map[string]laneConfig `toml:"lanes"`
	Memory     memoryConfig          `toml:"memory"`
	Shims      shimConfig            `toml:"shims"`
	Reap       reapConfig            `toml:"reap"`
	GC         gcConfig              `toml:"gc"`
	Cache      cacheConfig           `toml:"cache"`
	Browser    browserConfig         `toml:"browser"`
}

type laneConfig struct {
	Slots      int      `toml:"slots"`
	MaxWeight  int      `toml:"max_weight"`
	MemoryGate bool     `toml:"memory_gate"`
	MaxWait    string   `toml:"max_wait"`
	Matchers   []string `toml:"matchers"`
}

type memoryConfig struct {
	Margin         string `toml:"margin"`
	PollInterval   string `toml:"poll_interval"`
	RecoveryWindow string `toml:"recovery_window"`
}

type shimConfig struct {
	Commands  []string            `toml:"commands"`
	FastPaths map[string][]string `toml:"fast_paths"`
}

type reapConfig struct {
	OlderThan   string   `toml:"older_than"`
	GracePeriod string   `toml:"grace_period"`
	Matchers    []string `toml:"matchers"`
}

type gcConfig struct {
	Roots     []string `toml:"roots"`
	Patterns  []string `toml:"patterns"`
	OlderThan string   `toml:"older_than"`
}

type cacheConfig struct {
	Dir     string   `toml:"dir"`
	MaxSize string   `toml:"max_size"`
	Env     []string `toml:"env"`
}

type browserConfig struct {
	Executable  string   `toml:"executable"`
	Flags       []string `toml:"flags"`
	IdleTimeout string   `toml:"idle_timeout"`
}

func defaults(cores int, ram uint64, paths Paths) document {
	doc := document{
		MatchOrder: []string{"encode", "render", "ml", "install", "build"},
		Lanes:      make(map[string]laneConfig),
		Memory:     memoryConfig{memoryMargin, pollInterval, recoveryTime},
		Shims: shimConfig{
			Commands: []string{"ffmpeg", "ffprobe", "npm", "pnpm", "yarn", "bun", "pip", "pip3", "uv", "cargo", "go", "xcodebuild"},
			FastPaths: map[string][]string{
				"ffprobe": {`.*`},
				"ffmpeg":  {`(^|\s)-(version|h|help)(\s|$)`, `(^|\s)-f\s+null(\s|$)`},
			},
		},
		Reap:    reapConfig{reapAge, reapGrace, []string{`(?i)(chrome|chromium|chrome-headless-shell|chrome-devtools-mcp|playwright|puppeteer|remotion|hyperframes|ffmpeg)`}},
		GC:      gcConfig{[]string{}, []string{"**/out/slice-*.mp4", "**/.remotion/**"}, gcAge},
		Cache:   cacheConfig{paths.CacheDir, cacheSize, []string{}},
		Browser: browserConfig{Flags: []string{}, IdleTimeout: browserIdle},
	}
	// Preserve the existing default order; explicit match_order can put a
	// custom lane first when its regex overlaps a built-in rule.
	patterns := map[string]string{
		"encode":  `(?i)(^|[/\\\s])ffmpeg(\.exe)?(\s|$)`,
		"render":  `(?i)(^|[/\\\s])[^\s]*(remotion|hyperframes|playwright|puppeteer|chrome|chromium)[^\s]*(\s|$)`,
		"ml":      `(?i)(^|[/\\\s])[^\s]*(whisper|mlx|ollama|llama)[^\s]*(\s|$)`,
		"install": `(?i)(^|[/\\\s])((npm|pnpm|yarn|bun)(\.exe)? (i|install|ci)|pip(3)?(\.exe)? install|uv(\.exe)? (pip|sync)|brew install|cargo install)(\s|$)`,
		"build":   `(?i)(^|[/\\\s])(xcodebuild|gradle|cargo build|go build)(\s|$)`,
	}
	for name, pattern := range patterns {
		slots := slotsFor(name, cores, ram)
		doc.Lanes[name] = laneConfig{slots, slots, true, defaultWait, []string{pattern}}
	}
	return doc
}

func slotsFor(name string, cores int, ram uint64) int {
	slots := defaultSlots
	switch name {
	case "render":
		slots = int(ram / gib / renderGiB)
	case "encode":
		slots = cores / encodeCores
	case "build":
		slots = cores / buildCores
	}
	if slots < defaultSlots {
		return defaultSlots
	}
	return slots
}

func systemDefaults(paths Paths) document {
	ctx, cancel := context.WithTimeout(context.Background(), hardwareProbe)
	defer cancel()
	sample, err := pressure.System().Sample(ctx)
	if err != nil {
		return defaults(runtime.NumCPU(), gib, paths)
	}
	return defaults(runtime.NumCPU(), sample.TotalBytes, paths)
}
