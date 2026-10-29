package runner

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sushilk1991/baton/internal/admission"
	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/pressure"
	"github.com/sushilk1991/baton/internal/queue"
)

var errHelp = flag.ErrHelp

func parse(args []string, out io.Writer) (options, error) {
	opts := options{provider: pressure.System()}
	flags := flag.NewFlagSet("baton run", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.StringVar(&opts.lane, "lane", "auto", "lane name or auto")
	flags.StringVar(&opts.label, "label", "", "job label")
	flags.StringVar(&opts.agent, "agent", "", "agent identity override")
	flags.StringVar(&opts.cacheKey, "cache-key", "", "comma-separated input globs for output caching")
	flags.StringVar(&opts.output, "output", "", "single regular output file to cache")
	priority := flags.String("priority", string(queue.Normal), "high, normal, or low")
	flags.IntVar(&opts.weight, "weight", 1, "slots required")
	memory := flags.String("mem", "", "memory budget, e.g. 4G or 512M (binary units)")
	flags.DurationVar(&opts.maxWait, "max-wait", 0, "maximum memory delay (lane default if omitted)")
	flags.BoolVar(&opts.quiet, "quiet", false, "silence ordinary waiting notices")
	setUsage(flags, runSynopsis, runExample)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, err
		}
		// The flag package already printed the error and the usage text.
		return opts, fmt.Errorf("%w: %w", errReported, err)
	}
	opts.args = flags.Args()
	opts.priority = queue.Priority(*priority)
	if len(opts.args) == 0 {
		return opts, errors.New("usage: " + runSynopsis)
	}
	if (opts.cacheKey == "") != (opts.output == "") {
		return opts, errors.New("cache-key and output must be provided together")
	}
	var waitSet bool
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "max-wait" {
			waitSet = true
		}
	})
	if opts.weight < 1 || (waitSet && opts.maxWait <= 0) || (opts.priority != queue.High && opts.priority != queue.Normal && opts.priority != queue.Low) {
		return opts, errors.New("weight and max-wait must be positive; priority must be high, normal, or low")
	}
	if *memory != "" {
		var err error
		opts.memory, err = config.ParseBytes(*memory)
		if err != nil {
			return opts, err
		}
	}
	// The bypass must remain usable even when configuration is damaged.
	if os.Getenv("BATON") == "off" {
		return opts, nil
	}
	settings, err := config.Load()
	if err != nil {
		return opts, err
	}
	if opts.lane == "auto" {
		opts.lane = settings.AutoLane(opts.args)
	}
	policy := settings.Lane(opts.lane)
	opts.slots = policy.Slots
	if !waitSet {
		opts.maxWait = policy.MaxWait
	}
	opts.gate = admission.Options{
		MarginBytes: policy.MarginBytes, PollInterval: policy.PollInterval,
		RecoveryWindow: policy.RecoveryWindow,
	}
	if !policy.MemoryGate {
		opts.gate.MemoryGate = admission.GateDisabled
	}
	if opts.lane != "" && opts.weight > policy.MaxWeight {
		return opts, fmt.Errorf("weight %d exceeds lane %s max_weight %d", opts.weight, opts.lane, policy.MaxWeight)
	}
	return opts, nil
}
