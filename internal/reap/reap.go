// Package reap lists and terminates aged, orphaned heavy processes.
package reap

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/process"
)

const (
	initPID      = 1
	exitFailure  = 1
	exitUsage    = 2
	pollInterval = 25 * time.Millisecond
	killWait     = 2 * time.Second
	scanLimit    = 10 * time.Second
	usage        = "usage: baton reap [--older-than 2h] [--yes]"
)

type options struct {
	older, grace time.Duration
	uid, self    int
	home, config string
	match        func([]string) bool
}

type driver interface {
	Read(context.Context, int) (process.Info, error)
	Send(context.Context, process.Info, process.Signal) error
}

type systemDriver struct{}

func (systemDriver) Read(ctx context.Context, pid int) (process.Info, error) {
	return process.Read(ctx, pid)
}

func (systemDriver) Send(ctx context.Context, row process.Info, signal process.Signal) error {
	return process.Send(ctx, row, signal)
}

// Run lists by default; --yes revalidates and terminates only listed candidates.
func Run(args []string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("reap", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	older := flags.String("older-than", "", "minimum process age (default: reap.older_than, 2h)")
	yes := flags.Bool("yes", false, "send TERM, then KILL after reap.grace_period")
	flags.Usage = func() { fmt.Fprintln(diagnostics, usage) }
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		if _, err := fmt.Fprintln(out, usage); err != nil {
			return exitFailure
		}
		return 0
	}
	if err != nil || len(flags.Args()) != 0 {
		fmt.Fprintln(diagnostics, usage)
		return exitUsage
	}
	var age time.Duration
	override := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "older-than" {
			override = true
		}
	})
	if override {
		age, err = time.ParseDuration(*older)
		if err != nil || age < 0 {
			fmt.Fprintln(diagnostics, "baton: older-than must be a nonnegative duration")
			return exitUsage
		}
	}
	if err := runReap(out, *yes, override, age); err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitFailure
	}
	return 0
}

func runReap(out io.Writer, yes, override bool, age time.Duration) error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	older, grace := settings.ReapTiming()
	if override {
		older = age
	}
	opts := options{older: older, grace: grace, uid: currentUID(), self: os.Getpid(), home: home, config: paths.ConfigFile, match: settings.ReapMatch}
	ctx, cancel := context.WithTimeout(context.Background(), scanLimit)
	defer cancel()
	rows, err := process.List(ctx)
	if err != nil {
		return fmt.Errorf("process discovery: %w", err)
	}
	origins, err := readOrigins(ctx, systemDriver{}, paths.StateDir)
	if err != nil {
		return err
	}
	now := time.Now()
	candidates := []process.Info{}
	for _, row := range rows {
		if eligible(row, opts, now, origins) {
			candidates = append(candidates, row)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].PID < candidates[j].PID })
	// Print the entire selection before any signal. A closed output aborts
	// action; argv values are omitted because they can carry credentials.
	if err := printCandidates(out, candidates, now); err != nil {
		return err
	}
	if !yes || len(candidates) == 0 {
		return nil
	}
	return terminate(context.Background(), systemDriver{}, candidates, opts, origins, out)
}

func printCandidates(out io.Writer, rows []process.Info, now time.Time) error {
	var buffer bytes.Buffer
	fmt.Fprintln(&buffer, "PID\tPPID\tAGE\tCOMMAND")
	for _, row := range rows {
		fmt.Fprintf(&buffer, "%d\t%d\t%s\t%s\n", row.PID, row.Parent, now.Sub(row.Started).Truncate(time.Second), clean(row.Args[0]))
	}
	fmt.Fprintf(&buffer, "%d reap candidate(s); use --yes to terminate\n", len(rows))
	_, err := out.Write(buffer.Bytes())
	return err
}

func clean(value string) string {
	return strings.Map(func(char rune) rune {
		if char < ' ' || char == 127 {
			return '?'
		}
		return char
	}, value)
}

func eligible(row process.Info, opts options, now time.Time, origins map[int]string) bool {
	if row.PID <= initPID || row.PID == opts.self || row.UID != opts.uid || row.Dead || row.Start == "" || row.Started.IsZero() || now.Sub(row.Started) < opts.older || len(row.Args) == 0 {
		return false
	}
	if row.Parent != initPID && origins[row.PID] != row.Start {
		return false
	}
	return heavy(row.Args, opts) && opts.match(row.Args)
}

func revalidate(ctx context.Context, d driver, expected process.Info, opts options, origins map[int]string) (bool, error) {
	current, err := d.Read(ctx, expected.PID)
	if errors.Is(err, process.ErrGone) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return process.Same(expected, current) && eligible(current, opts, time.Now(), origins), nil
}

func terminate(ctx context.Context, d driver, rows []process.Info, opts options, origins map[int]string, out io.Writer) error {
	pending := []process.Info{}
	for _, row := range rows {
		valid, err := revalidate(ctx, d, row, opts, origins)
		if err != nil {
			return err
		}
		if !valid {
			continue
		}
		if err := d.Send(ctx, row, process.Terminate); errors.Is(err, process.ErrGone) {
			continue
		} else if err != nil {
			return fmt.Errorf("TERM %d: %w", row.PID, err)
		}
		pending = append(pending, row)
	}
	// All TERM requests share one grace period, rather than N serial delays.
	// candidates -> TERM -> wait grace -> revalidate survivors -> KILL -> wait
	pending, err := waitGone(ctx, d, pending, opts.grace)
	if err != nil {
		return err
	}
	survivors := []process.Info{}
	for _, row := range pending {
		valid, err := revalidate(ctx, d, row, opts, origins)
		if err != nil {
			return err
		}
		if !valid {
			continue
		}
		if err := d.Send(ctx, row, process.Kill); errors.Is(err, process.ErrGone) {
			continue
		} else if err != nil {
			return fmt.Errorf("KILL %d: %w", row.PID, err)
		}
		survivors = append(survivors, row)
	}
	survivors, err = waitGone(ctx, d, survivors, killWait)
	if err != nil {
		return err
	}
	if len(survivors) != 0 {
		return fmt.Errorf("process %d remains alive after KILL", survivors[0].PID)
	}
	_, err = fmt.Fprintln(out, "Reap complete; selected process identities are gone or no longer eligible.")
	return err
}

func waitGone(ctx context.Context, d driver, rows []process.Info, timeout time.Duration) ([]process.Info, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		pending := []process.Info{}
		for _, row := range rows {
			current, err := d.Read(ctx, row.PID)
			if errors.Is(err, process.ErrGone) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if process.Same(row, current) {
				pending = append(pending, row)
			}
		}
		rows = pending
		if len(rows) == 0 {
			return rows, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return rows, nil
		case <-ticker.C:
		}
	}
}
