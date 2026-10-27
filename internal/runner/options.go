package runner

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
	"github.com/sushilk1991/baton/internal/queue"
)

const (
	gib         = uint64(1 << 30)
	renderGiB   = 16
	encodeCores = 6
	buildCores  = 4
)

var errHelp = flag.ErrHelp

type laneRule struct {
	lane  string
	match *regexp.Regexp
}

// Default matcher order resolves commands that mention more than one tool.
var laneRules = []laneRule{
	{"encode", regexp.MustCompile(`(?i)(^|[/\\\s])ffmpeg(\.exe)?(\s|$)`)},
	{"render", regexp.MustCompile(`(?i)(^|[/\\\s])[^\s]*(remotion|hyperframes|playwright|puppeteer|chrome|chromium)[^\s]*(\s|$)`)},
	{"ml", regexp.MustCompile(`(?i)(^|[/\\\s])[^\s]*(whisper|mlx|ollama|llama)[^\s]*(\s|$)`)},
	{"install", regexp.MustCompile(`(?i)(^|[/\\\s])((npm|pnpm|yarn|bun)(\.exe)? (i|install|ci)|pip(3)?(\.exe)? install|uv(\.exe)? (pip|sync)|brew install|cargo install)(\s|$)`)},
	{"build", regexp.MustCompile(`(?i)(^|[/\\\s])(xcodebuild|gradle|cargo build|go build)(\s|$)`)},
}

func parse(args []string, out io.Writer) (options, error) {
	opts := options{provider: pressure.System()}
	flags := flag.NewFlagSet("baton run", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.StringVar(&opts.lane, "lane", "auto", "lane name or auto")
	flags.StringVar(&opts.label, "label", "", "job label")
	flags.StringVar(&opts.agent, "agent", "", "agent identity override")
	priority := flags.String("priority", string(queue.Normal), "high, normal, or low")
	flags.IntVar(&opts.weight, "weight", 1, "slots required")
	memory := flags.String("mem", "", "memory budget, e.g. 4G or 512M (binary units)")
	flags.DurationVar(&opts.maxWait, "max-wait", defaultMaxWait, "maximum memory delay")
	flags.BoolVar(&opts.quiet, "quiet", false, "silence ordinary waiting notices")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	opts.args = flags.Args()
	opts.priority = queue.Priority(*priority)
	if len(opts.args) == 0 {
		return opts, errors.New("usage: baton run [options] -- command [args...]")
	}
	if opts.weight < 1 || opts.maxWait <= 0 || (opts.priority != queue.High && opts.priority != queue.Normal && opts.priority != queue.Low) {
		return opts, errors.New("weight and max-wait must be positive; priority must be high, normal, or low")
	}
	if *memory != "" {
		var err error
		opts.memory, err = parseMemory(*memory)
		if err != nil {
			return opts, err
		}
	}
	if opts.lane == "auto" {
		opts.lane = autoLane(opts.args)
	}
	var ram uint64
	if opts.lane == "render" && os.Getenv("BATON") != "off" {
		ram = hardwareRAM(opts.provider)
	}
	opts.slots = laneSlots(opts.lane, runtime.NumCPU(), ram)
	if opts.lane != "" && opts.weight > opts.slots {
		return opts, fmt.Errorf("weight %d exceeds lane %s capacity %d", opts.weight, opts.lane, opts.slots)
	}
	return opts, nil
}

func hardwareRAM(provider pressure.Provider) uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sample, err := provider.Sample(ctx)
	if err != nil {
		return gib
	}
	return sample.TotalBytes
}

func laneSlots(name string, cores int, ram uint64) int {
	slots := 1
	switch name {
	case "render":
		slots = int(ram / gib / renderGiB)
	case "encode":
		slots = cores / encodeCores
	case "build":
		slots = cores / buildCores
	}
	if slots < 1 {
		return 1
	}
	return slots
}

func autoLane(args []string) string {
	if len(args) == 0 {
		return ""
	}
	// Shell text is opaque; argv matching recognizes direct tools and common
	// launchers (npx, pnpm exec), without interpreting or rewriting scripts.
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(args[0]), ".exe"))
	if base == "sh" || base == "bash" || base == "zsh" || base == "cmd" || base == "powershell" {
		return ""
	}
	joined := strings.Join(args, " ")
	for _, rule := range laneRules {
		if rule.match.MatchString(joined) {
			return rule.lane
		}
	}
	return ""
}

func parseMemory(text string) (uint64, error) {
	value := strings.ToUpper(strings.TrimSpace(text))
	multiplier := uint64(1)
	for _, unit := range []struct {
		suffix string
		bytes  uint64
	}{
		{"GIB", gib}, {"GB", gib}, {"G", gib},
		{"MIB", 1 << 20}, {"MB", 1 << 20}, {"M", 1 << 20},
		{"KIB", 1 << 10}, {"KB", 1 << 10}, {"K", 1 << 10}, {"B", 1},
	} {
		if strings.HasSuffix(value, unit.suffix) {
			multiplier, value = unit.bytes, strings.TrimSuffix(value, unit.suffix)
			break
		}
	}
	amount, err := strconv.ParseFloat(value, 64)
	bytes := amount * float64(multiplier)
	if err != nil || math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes <= 0 || bytes >= float64(math.MaxUint64) || math.Trunc(bytes) != bytes {
		return 0, fmt.Errorf("invalid memory budget %q", text)
	}
	return uint64(bytes), nil
}
