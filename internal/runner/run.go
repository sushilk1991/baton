// Package runner admits commands and records their execution on this machine.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/sushilk1991/baton/internal/admission"
	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/lane"
	"github.com/sushilk1991/baton/internal/lockfile"
	"github.com/sushilk1991/baton/internal/pressure"
	"github.com/sushilk1991/baton/internal/queue"
)

const (
	gib               = uint64(1 << 30)
	exitFailure       = 1
	exitUsage         = 2
	exitNotExecutable = 126
	exitNotFound      = 127
	directoryMode     = 0700
	fileMode          = 0600
	sampleInterval    = 100 * time.Millisecond
	noticeInterval    = 2 * time.Second
)

type streams struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

type options struct {
	args     []string
	lane     string
	label    string
	agent    string
	priority queue.Priority
	weight   int
	memory   uint64
	maxWait  time.Duration
	quiet    bool
	slots    int
	provider pressure.Provider
	gate     admission.Options
}

type supervisor struct {
	mu     sync.Mutex
	child  *exec.Cmd
	caught os.Signal
	cancel context.CancelFunc
}

// Run parses run arguments, forwards signals, and returns the child's status.
func Run(args []string, in io.Reader, out, diagnostics io.Writer) int {
	ioSet := streams{in: in, out: out, err: diagnostics}
	opts, err := parse(args, diagnostics)
	if errors.Is(err, errHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitUsage
	}
	code, err := runJob(context.Background(), opts, ioSet)
	if err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
	}
	return code
}

func runJob(parent context.Context, opts options, ioSet streams) (int, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	control := &supervisor{cancel: cancel}
	stop := control.watch(ctx)
	defer stop()
	if os.Getenv("BATON") == "off" {
		return execute(ctx, control, opts.args, ioSet, nil)
	}

	root, err := stateRoot()
	if err != nil {
		return control.waitCode(), err
	}
	store, err := newStore(root)
	if err != nil {
		return control.waitCode(), err
	}
	job, err := newJob(opts)
	if err != nil {
		return control.waitCode(), err
	}
	if opts.memory == 0 {
		opts.memory, err = store.predict(ctx, job.Signature)
		if err != nil {
			return control.waitCode(), err
		}
	}
	job.MemoryBytes = opts.memory
	owner, err := lockfile.Try(filepath.Join(root, "jobs", job.ID+".lock"))
	if err != nil {
		return control.waitCode(), err
	}
	defer func() {
		_ = owner.Close()
		store.remove(job.ID)
	}()
	if err := store.publish(job); err != nil {
		return control.waitCode(), err
	}

	lease, err := admit(ctx, opts, store, &job, ioSet.err)
	if err != nil {
		return control.waitCode(), err
	}
	if lease != nil {
		defer lease.Release()
	}
	job.Start = time.Now().UTC()
	job.WaitNanos = job.Start.Sub(job.Queued).Nanoseconds()
	code, runErr := execute(ctx, control, opts.args, ioSet, func(command *exec.Cmd, done <-chan struct{}) {
		job.PID = command.Process.Pid
		job.Phase = "running"
		monitor(store, &job, command, done, ioSet.err)
	})
	job.End = time.Now().UTC()
	job.ExitCode = code
	job.Phase = "finished"
	// Persistence must never replace the command's exit code. Report failures
	// separately so callers can distinguish execution from missing telemetry.
	persistErr := store.finish(context.Background(), job)
	return code, errors.Join(runErr, persistErr)
}

func admit(ctx context.Context, opts options, store *store, job *jobRecord, out io.Writer) (*lane.Lease, error) {
	if opts.lane == "" {
		return nil, nil
	}
	provider := opts.provider
	if provider == nil {
		provider = pressure.System()
	}
	notices := &noticeProvider{out: out, lane: opts.lane, quiet: opts.quiet}
	q, err := queue.New(store.root, opts.lane, opts.slots)
	if err != nil {
		return nil, err
	}
	ticket, err := q.Enqueue(ctx, opts.weight, opts.priority)
	if err != nil {
		return nil, err
	}
	defer ticket.Close()
	// Publish the queue's sequence so readers use actual priority/FIFO order,
	// even when multiple jobs share a parent PID or cross an aging boundary.
	job.QueueSequence = ticket.Sequence()
	if err := store.publish(*job); err != nil {
		return nil, err
	}
	policy := opts.gate
	policy.MaxWait, policy.OnWait = opts.maxWait, notices.wait
	gate, err := admission.New(provider, policy)
	if err != nil {
		return nil, err
	}
	return gate.Acquire(ctx, ticket, opts.memory, out)
}

func execute(ctx context.Context, control *supervisor, args []string, ioSet streams, sample func(*exec.Cmd, <-chan struct{})) (int, error) {
	command := exec.Command(args[0], args[1:]...)
	command.Stdin, command.Stdout, command.Stderr = ioSet.in, ioSet.out, ioSet.err
	restore := prepareGroup(command, ioSet.in)
	defer func() {
		if err := restore(); err != nil {
			fmt.Fprintln(ioSet.err, "baton: restore terminal:", err)
		}
	}()
	// Serialize startup with signal handling: a signal either cancels the
	// queued job or reaches its fully started process group, never a gap.
	control.mu.Lock()
	if ctx.Err() != nil {
		control.mu.Unlock()
		return control.waitCode(), ctx.Err()
	}
	err := command.Start()
	if err == nil {
		control.child = command
	}
	control.mu.Unlock()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return exitNotFound, err
		}
		return exitNotExecutable, err
	}

	done := make(chan struct{})
	monitored := make(chan struct{})
	if sample != nil {
		go func() {
			sample(command, done)
			close(monitored)
		}()
	} else {
		close(monitored)
	}
	err = command.Wait()
	control.mu.Lock()
	control.child = nil
	control.mu.Unlock()
	close(done)
	<-monitored
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return childCode(exit.ProcessState), nil
	}
	return exitFailure, err
}

func (control *supervisor) watch(ctx context.Context) func() {
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, forwardSignals()...)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case sig := <-signals:
				control.mu.Lock()
				control.caught = sig
				if control.child == nil {
					control.cancel()
				} else {
					_ = signalGroup(control.child, sig)
				}
				control.mu.Unlock()
			case <-ctx.Done():
				control.mu.Lock()
				if control.child != nil {
					_ = signalGroup(control.child, terminateSignal())
				}
				control.mu.Unlock()
				return
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(signals); close(done); <-stopped }
}

func (control *supervisor) waitCode() int {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.caught != nil {
		return signalCode(control.caught)
	}
	return exitFailure
}

func stateRoot() (string, error) {
	paths, err := config.ResolvePaths()
	return paths.StateDir, err
}

func monitor(store *store, job *jobRecord, command *exec.Cmd, done <-chan struct{}, out io.Writer) {
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	var reported bool
	for {
		rss, err := treeRSS(job.PID)
		if err == nil {
			job.RSSBytes = rss
			if rss > job.PeakRSSBytes {
				job.PeakRSSBytes = rss
			}
		}
		if err != nil && !reported {
			fmt.Fprintln(out, "baton: RSS sampling unavailable:", err)
			reported = true
		}
		if err := store.publish(*job); err != nil && !reported {
			fmt.Fprintln(out, "baton: running record:", err)
			reported = true
		}
		select {
		case <-done:
			// Wait has completed before done closes; include kernel root peak
			// so short-lived commands can still contribute a memory estimate.
			if peak := rootPeak(command.ProcessState); peak > job.PeakRSSBytes {
				job.PeakRSSBytes = peak
			}
			return
		case <-ticker.C:
		}
	}
}
