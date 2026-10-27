// The stress helper is built by scripts/stress.sh, never installed with baton.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
)

const (
	gib             = uint64(1 << 30)
	safetyMargin    = uint64(512 << 20)
	defaultJobs     = 4
	defaultGiB      = 0.25
	defaultHold     = 45 * time.Second
	defaultMaxWait  = 30 * time.Second
	defaultTimeout  = 10 * time.Minute
	sampleInterval  = 200 * time.Millisecond
	probeTimeout    = 15 * time.Second
	cleanupGrace    = 3 * time.Second
	maxTimeRatio    = 1.2
	budgetRAMShare  = 4
	budgetFreeShare = 2
	maxJobs         = 1024
	dirMode         = 0700
	fileMode        = 0600
)

type options struct {
	jobs                   int
	bytes                  uint64
	hold, maxWait, timeout time.Duration
	baton, output          string
}

func main() {
	if err := entry(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "stress:", err)
		os.Exit(1)
	}
}

func entry(args []string) error {
	// Cancellation closes each owned child's stdin; workers also exit on EOF.
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals()...)
	defer stop()
	if len(args) > 0 && args[0] == "--worker" {
		return workerMain(ctx, args[1:])
	}
	opts, err := readOptions(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	return execute(ctx, opts)
}

func readOptions(args []string) (options, error) {
	var opts options
	var amount float64
	flags := flag.NewFlagSet("scripts/stress.sh", flag.ContinueOnError)
	flags.IntVar(&opts.jobs, "jobs", defaultJobs, "number of jobs in each phase")
	flags.Float64Var(&amount, "gib", defaultGiB, "GiB allocated and touched per job")
	flags.DurationVar(&opts.hold, "hold", defaultHold, "hold time after filling each allocation")
	flags.DurationVar(&opts.maxWait, "max-wait", defaultMaxWait, "queued memory-gate timeout (warnings counted)")
	flags.DurationVar(&opts.timeout, "timeout", defaultTimeout, "deadline for each phase")
	flags.StringVar(&opts.output, "output", "", "new artifact directory (default .build/stress-TIMESTAMP)")
	flags.StringVar(&opts.baton, "baton", "", "baton binary (set by the shell launcher)")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 || opts.jobs < 1 || opts.jobs > maxJobs || opts.hold <= 0 || opts.maxWait <= 0 || opts.timeout <= 0 {
		return options{}, errors.New("jobs must be 1..1024, durations positive, and no positional arguments")
	}
	bytes := amount * float64(gib)
	if math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes < 1 || bytes >= float64(int(^uint(0)>>1)) {
		return options{}, errors.New("gib must be a finite positive allocation that fits in an int")
	}
	opts.bytes = uint64(bytes)
	return opts, nil
}

func safeBudget(opts options, sample pressure.Snapshot) error {
	if err := sample.Validate(); err != nil {
		return err
	}
	if sample.Level == pressure.Critical {
		return errors.New("critical preflight pressure; no workers started")
	}
	// Bound the unqueued phase too; this is a repeatable probe, not an OOM test.
	// reserve + jobs*allocation <= min(RAM/4, available/2)
	cap := sample.TotalBytes / budgetRAMShare
	if available := sample.AvailableBytes / budgetFreeShare; available < cap {
		cap = available
	}
	if cap <= safetyMargin || opts.bytes > (cap-safetyMargin)/uint64(opts.jobs) {
		return fmt.Errorf("unsafe budget: %d jobs * %d bytes + %d reserve exceeds cap %d; reduce --jobs or --gib", opts.jobs, opts.bytes, safetyMargin, cap)
	}
	return nil
}

func execute(ctx context.Context, opts options) error {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return errors.New("stress measurements require macOS or Linux")
	}
	if opts.baton == "" {
		return errors.New("use scripts/stress.sh to build the helper and baton")
	}
	binary, err := filepath.Abs(opts.baton)
	if err != nil {
		return err
	}
	opts.baton = binary
	if opts.output == "" {
		opts.output = filepath.Join(".build", "stress-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	opts.output, err = filepath.Abs(opts.output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(opts.output), dirMode); err != nil {
		return err
	}
	// Never overwrite evidence or use the caller's scheduler/configuration state.
	if err := os.Mkdir(opts.output, dirMode); err != nil {
		return fmt.Errorf("new output directory: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	bench := benchmark{opts: opts, worker: executable, probe: pressure.System().Sample, events: beginEvents, progress: os.Stderr}
	initial, err := bench.probe(ctx)
	if err != nil {
		return err
	}
	if err := safeBudget(opts, initial); err != nil {
		return err
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	report := report{
		Time: time.Now().UTC(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(),
		RAMBytes: initial.TotalBytes, AvailableBytes: initial.AvailableBytes,
		Jobs: opts.jobs, BytesPerJob: opts.bytes, Hold: opts.hold.String(), MaxWait: opts.maxWait.String(),
		Timeout: opts.timeout.String(), BatonSHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
	}
	for _, mode := range []string{"sequential", "unqueued", "queued"} {
		p := bench.runPhase(ctx, mode)
		report.Phases = append(report.Phases, p)
		if p.Error != "" || ctx.Err() != nil {
			break
		}
	}
	return finishReport(opts.output, report, os.Stdout)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), fileMode)
}

func finishReport(dir string, result report, out io.Writer) error {
	passed := len(result.Phases) == 3
	var reference float64
	if len(result.Phases) > 0 {
		reference = result.Phases[0].WallSeconds
	}
	for i := range result.Phases {
		p := &result.Phases[i]
		p.Failures = evaluate(*p, reference, result.Jobs)
		p.Passed = len(p.Failures) == 0
		passed = passed && p.Passed
	}
	result.Passed = passed
	if err := writeJSON(filepath.Join(dir, "results.json"), result); err != nil {
		return err
	}
	text := formatReport(result)
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(text), fileMode); err != nil {
		return err
	}
	if _, err := io.WriteString(out, text); err != nil {
		return err
	}
	fmt.Fprintln(out, "Artifacts:", dir)
	if !passed {
		return errors.New("FAIL: stress criteria not met; see summary.md and raw artifacts")
	}
	return nil
}
