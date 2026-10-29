// Package config resolves machine defaults, XDG paths, and validated TOML policy.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

// Policy is a lane's resolved capacity and memory-admission policy.
type Policy struct {
	// Slots is the lane's concurrent capacity.
	Slots int
	// MaxWeight limits any single job's reservation.
	MaxWeight int
	// MemoryGate enables pressure and budget checks before taking slots.
	MemoryGate bool
	// MaxWait bounds memory delays, never queue or slot contention.
	MaxWait time.Duration
	// MarginBytes is extra headroom required beyond the job's budget.
	MarginBytes uint64
	// PollInterval controls pressure and queue retry frequency.
	PollInterval time.Duration
	// RecoveryWindow requires sustained recovery after pressure.
	RecoveryWindow time.Duration
}

// Config holds validated policy; callers cannot mutate its source document.
type Config struct {
	doc   document
	lanes map[string]Policy
	rules []laneRule
}

type laneRule struct {
	lane  string
	match *regexp.Regexp
	scope matchScope
}

// matchScope selects the text a lane regex sees.
type matchScope int

const (
	// matchHead gives built-in matchers the executable and its operation.
	matchHead matchScope = iota
	// matchArgv gives matchers the user wrote the full space-joined argv.
	matchArgv
)

// Load overlays the optional user file on current machine defaults.
func Load() (*Config, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return nil, err
	}
	config, err := loadFile(paths.ConfigFile, systemDefaults(paths))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", paths.ConfigFile, err)
	}
	return config, nil
}

func loadFile(path string, doc document) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return decode(string(data), doc)
}

func decode(text string, doc document) (*Config, error) {
	// Decode overrides separately: TOML map entries replace structs, so
	// decoding directly into defaults would erase omitted lane fields.
	var override document
	meta, err := toml.Decode(text, &override)
	if err != nil {
		return nil, err
	}
	if keys := meta.Undecoded(); len(keys) != 0 {
		return nil, fmt.Errorf("unknown configuration key %s", keys[0])
	}
	// The decoder accepts case aliases, but presence checks are exact. Reject
	// aliases so e.g. Slots cannot be accepted and then silently ignored.
	for _, key := range meta.Keys() {
		for index, part := range key {
			if index == 1 && key[0] == "lanes" {
				continue
			}
			if index == 2 && key[0] == "shims" && key[1] == "fast_paths" {
				continue
			}
			if part != strings.ToLower(part) {
				return nil, fmt.Errorf("configuration keys are case-sensitive: %s", key)
			}
		}
	}
	for name, lane := range override.Lanes {
		base, exists := doc.Lanes[name]
		if !exists {
			base = laneConfig{defaultSlots, defaultSlots, true, defaultWait, []string{}}
		}
		if meta.IsDefined("lanes", name, "slots") {
			base.Slots = lane.Slots
		}
		if meta.IsDefined("lanes", name, "memory_gate") {
			base.MemoryGate = lane.MemoryGate
		}
		if meta.IsDefined("lanes", name, "max_wait") {
			base.MaxWait = lane.MaxWait
		}
		if meta.IsDefined("lanes", name, "matchers") {
			base.Matchers = lane.Matchers
		}
		// An omitted limit follows the resolved capacity, including when a
		// user lowers the slot count below a hardware-derived default.
		base.MaxWeight = base.Slots
		if meta.IsDefined("lanes", name, "max_weight") {
			base.MaxWeight = lane.MaxWeight
		}
		doc.Lanes[name] = base
	}
	// A second decode merges scalar/table settings; preserve the explicitly
	// merged lanes rather than letting map decoding replace their structs.
	lanes := doc.Lanes
	doc.Lanes = nil
	if _, err := toml.Decode(text, &doc); err != nil {
		return nil, err
	}
	doc.Lanes = lanes
	return compile(doc)
}

func compile(doc document) (*Config, error) {
	if err := validateExtras(doc); err != nil {
		return nil, err
	}
	margin, err := ParseBytes(doc.Memory.Margin)
	if err != nil {
		return nil, fmt.Errorf("memory.margin: %w", err)
	}
	poll, err := positiveDuration("memory.poll_interval", doc.Memory.PollInterval)
	if err != nil {
		return nil, err
	}
	recovery, err := positiveDuration("memory.recovery_window", doc.Memory.RecoveryWindow)
	if err != nil {
		return nil, err
	}
	config := &Config{doc: doc, lanes: make(map[string]Policy)}
	names := make([]string, 0, len(doc.Lanes))
	for name := range doc.Lanes {
		names = append(names, name)
	}
	sort.Strings(names)
	order, err := matchOrder(doc.MatchOrder, names)
	if err != nil {
		return nil, err
	}
	config.doc.MatchOrder = order
	for _, name := range order {
		lane := doc.Lanes[name]
		if name == "" || name == "auto" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00\r\n") {
			return nil, fmt.Errorf("invalid lane name %q", name)
		}
		if lane.Slots < defaultSlots || lane.MaxWeight < defaultSlots || lane.MaxWeight > lane.Slots {
			return nil, fmt.Errorf("lanes.%s: require slots >= max_weight >= 1", name)
		}
		wait, err := positiveDuration("lanes."+name+".max_wait", lane.MaxWait)
		if err != nil {
			return nil, err
		}
		config.lanes[name] = Policy{lane.Slots, lane.MaxWeight, lane.MemoryGate, wait, margin, poll, recovery}
		for _, pattern := range lane.Matchers {
			match, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("lanes.%s.matchers: %w", name, err)
			}
			scope := matchArgv
			if builtinPattern(pattern) {
				scope = matchHead
			}
			config.rules = append(config.rules, laneRule{name, match, scope})
		}
	}
	return config, nil
}

func matchOrder(preferred, names []string) ([]string, error) {
	known, seen := make(map[string]bool), make(map[string]bool)
	for _, name := range names {
		known[name] = true
	}
	order := make([]string, 0, len(names))
	for _, name := range preferred {
		if !known[name] || seen[name] {
			return nil, fmt.Errorf("match_order: unknown or duplicate lane %q", name)
		}
		seen[name] = true
		order = append(order, name)
	}
	for _, name := range names {
		if !seen[name] {
			order = append(order, name)
		}
	}
	return order, nil
}

// Lane returns configured policy or a single-slot policy for explicit names.
func (config *Config) Lane(name string) Policy {
	if lane, exists := config.lanes[name]; exists {
		return lane
	}
	// Explicit ad-hoc lanes remain usable without editing configuration.
	lane := config.lanes["install"]
	lane.Slots, lane.MaxWeight, lane.MemoryGate = defaultSlots, defaultSlots, true
	lane.MaxWait, _ = time.ParseDuration(defaultWait)
	return lane
}

// LaneNames returns a detached, sorted list for status, including idle lanes.
func (config *Config) LaneNames() []string {
	names := make([]string, 0, len(config.lanes))
	for name := range config.lanes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// AutoLane matches argv in configured order while leaving shell text opaque.
// Built-in matchers see only the executable after wrappers (env, sudo,
// timeout, ...) and, for known dispatchers, its operation or launched tool,
// so grep -r llama notes stays unqueued while ollama run uses ml. Matchers
// the user wrote see the full unwrapped argv, e.g. '^make render$'.
//
//	argv -> unwrap -> built-in: executable [+ operation] -> first lane
//	                  user:     whole argv              -/
func (config *Config) AutoLane(args []string) string {
	words := unwrapCommand(args)
	if len(words) == 0 {
		return ""
	}
	base := programName(words[0])
	if base == "sh" || base == "bash" || base == "zsh" || base == "cmd" || base == "powershell" || base == "pwsh" {
		return ""
	}
	// baton never queues behind itself under any name, wherever it appears:
	// uv run baton run, a renamed binary, or uv run baton --version. An
	// explicit baton run or hold takes its own lane, and an outer
	// reservation would let it skip that lane's slots as a nested job. A
	// payload word naming baton (grep baton notes) is skipped too.
	for _, word := range words {
		if name := programName(word); name == "baton" || name == selfName() {
			return ""
		}
	}
	head := strings.Join(commandHead(words), " ")
	argv := strings.Join(words, " ")
	for _, rule := range config.rules {
		text := head
		if rule.scope == matchArgv {
			text = argv
		}
		if text != "" && rule.match.MatchString(text) {
			return rule.lane
		}
	}
	return ""
}

// selfName is the running baton binary's program name, which differs from
// baton when it was renamed: /opt/tools/baton-dev -> baton-dev.
var selfName = sync.OnceValue(func() string {
	path, err := os.Executable()
	if err != nil {
		return "baton"
	}
	return programName(path)
})

// programName is a program's lowercase base name on any OS, without .exe:
// C:\tools\baton.exe -> baton.
func programName(word string) string {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(word, `\`, "/")))
	return strings.TrimSuffix(base, ".exe")
}

// Executable returns the program argv runs after wrappers such as env,
// sudo, and timeout, or "" when there is none or BATON=off disables it.
func Executable(args []string) string {
	words := unwrapCommand(args)
	if len(words) == 0 {
		return ""
	}
	return words[0]
}

func builtinPattern(pattern string) bool {
	for _, builtin := range builtinMatchers {
		if pattern == builtin {
			return true
		}
	}
	return false
}

// Write emits effective TOML, including inherited defaults and matcher order.
func (config *Config) Write(out io.Writer) error {
	return toml.NewEncoder(out).Encode(config.doc)
}
