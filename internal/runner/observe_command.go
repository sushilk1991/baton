package runner

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/pressure"
)

const (
	watchInterval      = time.Second
	observationTimeout = 5 * time.Second
)

type outputMode int

const (
	plainOutput outputMode = iota
	jsonOutput
	lineOutput
)

// Status reports live scheduler state, optionally repeating until interrupted.
func Status(args []string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	asJSON := flags.Bool("json", false, "print a JSON snapshot (JSONL with --watch)")
	line := flags.Bool("line", false, "print one compact status line")
	watch := flags.Bool("watch", false, "refresh every second until interrupted")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return exitUsage
	}
	if flags.NArg() != 0 || (*asJSON && *line) {
		return reportError(diagnostics, exitUsage, errors.New("usage: baton status [--json | --line] [--watch]"))
	}
	cfg, err := config.Load()
	if err != nil {
		return reportError(diagnostics, exitFailure, err)
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return reportError(diagnostics, exitFailure, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), forwardSignals()...)
	defer stop()
	sample := func() (statusView, error) {
		bounded, cancel := context.WithTimeout(ctx, observationTimeout)
		defer cancel()
		return readStatus(bounded, paths.StateDir, cfg, pressure.System(), time.Now().UTC())
	}
	mode := plainOutput
	if *asJSON {
		mode = jsonOutput
	}
	if *line {
		mode = lineOutput
	}
	if *watch {
		err = watchStatus(ctx, out, sample, mode, watchInterval)
	} else {
		var view statusView
		view, err = sample()
		if err == nil {
			err = printStatus(out, view, mode)
		}
	}
	if err != nil {
		return reportError(diagnostics, exitFailure, err)
	}
	return 0
}

func watchStatus(ctx context.Context, out io.Writer, sample func() (statusView, error), mode outputMode, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Emit immediately, then append complete frames. No terminal escapes are
	// sent to redirected logs; JSON and compact modes remain one line/frame.
	for {
		if ctx.Err() != nil {
			return nil
		}
		view, err := sample()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := printStatus(out, view, mode); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// History reads retained completed jobs, with completion-time and lane filters.
func History(args []string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	asJSON := flags.Bool("json", false, "print one JSON record per line")
	since := flags.String("since", "", "include jobs completed within this duration, e.g. 24h")
	lane := flags.String("lane", "", "include only this lane")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return exitUsage
	}
	if flags.NArg() != 0 {
		return reportError(diagnostics, exitUsage, errors.New("usage: baton history [--json] [--since 24h] [--lane L]"))
	}
	cutoff := time.Time{}
	if *since != "" {
		age, err := time.ParseDuration(*since)
		if err != nil || age < 0 {
			return reportError(diagnostics, exitUsage, errors.New("--since requires a nonnegative duration, e.g. 24h"))
		}
		cutoff = time.Now().UTC().Add(-age)
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return reportError(diagnostics, exitFailure, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), forwardSignals()...)
	defer stop()
	bounded, cancel := context.WithTimeout(ctx, observationTimeout)
	defer cancel()
	jobs, err := readHistory(bounded, paths.StateDir, cutoff, *lane)
	if err != nil {
		return reportError(diagnostics, exitFailure, err)
	}
	if err := printHistory(out, jobs, *asJSON); err != nil {
		return reportError(diagnostics, exitFailure, err)
	}
	return 0
}

func reportError(out io.Writer, code int, err error) int {
	if _, err := fmt.Fprintln(out, "baton:", err); err != nil {
		return exitFailure
	}
	return code
}

func printStatus(out io.Writer, view statusView, mode outputMode) error {
	if mode == jsonOutput {
		return json.NewEncoder(out).Encode(view)
	}
	if mode == lineOutput {
		parts := []string{}
		for _, lane := range view.Lanes {
			parts = append(parts, fmt.Sprintf("%s %d/%d", cleanText(lane.Name), lane.Used, lane.Total))
		}
		level := string(view.Memory.Pressure)
		if view.Memory.Pressure == pressure.Normal {
			level = "ok"
		}
		parts = append(parts, fmt.Sprintf("%d waiting", len(view.Queue)), "mem "+level)
		_, err := fmt.Fprintln(out, "⏵ "+strings.Join(parts, " · "))
		return err
	}
	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintf(table, "%s\nLANE\tUSED/TOTAL\tCONFIGURED\n", view.Time.Format(time.RFC3339)); err != nil {
		return err
	}
	for _, lane := range view.Lanes {
		if _, err := fmt.Fprintf(table, "%s\t%d/%d\t%d\n", cleanText(lane.Name), lane.Used, lane.Total, lane.ConfiguredTotal); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(table, "\nRUNNING\tAGENT\tREPO\tLANE\tRUNTIME\tRSS\tPID"); err != nil {
		return err
	}
	for _, job := range view.Running {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n", cleanText(job.Label), cleanText(job.Agent), cleanText(job.Repo), cleanText(job.Lane), durationText(job.RuntimeNanos), byteText(job.RSSBytes), job.PID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(table, "\nQUEUE\tAGENT\tREPO\tLANE\tPOSITION\tWAIT\tPRIORITY\tWEIGHT"); err != nil {
		return err
	}
	for _, job := range view.Queue {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%d\n", cleanText(job.Label), cleanText(job.Agent), cleanText(job.Repo), cleanText(job.Lane), job.Position, durationText(job.WaitNanos), cleanText(job.Priority), job.Weight); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(table, "\nRAM available/total\t%s/%s\nSwap used/total\t%s/%s\nPressure\t%s\n", byteText(view.Memory.AvailableBytes), byteText(view.Memory.TotalBytes), byteText(view.Memory.SwapUsedBytes), byteText(view.Memory.SwapTotalBytes), view.Memory.Pressure); err != nil {
		return err
	}
	return table.Flush()
}

func printHistory(out io.Writer, jobs []jobRecord, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(out)
		for _, job := range jobs {
			if err := encoder.Encode(job); err != nil {
				return err
			}
		}
		return nil
	}
	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "START\tEND\tWAIT\tEXIT\tPEAK RSS\tLANE\tLABEL\tAGENT\tREPO\tCACHE HIT"); err != nil {
		return err
	}
	for _, job := range jobs {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%t\n", job.Start.Format(time.RFC3339), job.End.Format(time.RFC3339), durationText(job.WaitNanos), job.ExitCode, byteText(job.PeakRSSBytes), cleanText(job.Lane), cleanText(job.Label), cleanText(job.Agent), cleanText(job.Repo), job.CacheHit); err != nil {
			return err
		}
	}
	return table.Flush()
}

func cleanText(value string) string {
	// Labels and paths are user input; replace control characters so a single
	// job cannot inject terminal escapes, additional rows, or fake columns.
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return '�'
		}
		return char
	}, value)
}

func durationText(nanos int64) string {
	return time.Duration(nanos).Truncate(time.Millisecond).String()
}

func byteText(value uint64) string {
	return fmt.Sprintf("%.1f MiB", float64(value)/float64(1<<20))
}
