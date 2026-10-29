//go:build darwin || linux

package runner

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/lockfile"
	"github.com/sushilk1991/baton/internal/process"
)

const holdReady = "baton: hold ready"

// Hold reserves capacity for the exact calling shell without executing its text.
func Hold(args []string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("baton hold", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	name := flags.String("lane", "", "lane to reserve")
	pid := flags.Int("pid", 0, "calling shell PID")
	watch := flags.String("watch", "", "internal shell birth token")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return exitUsage
	}
	if *name == "" || *pid <= 1 || len(flags.Args()) != 0 {
		fmt.Fprintln(diagnostics, "usage: baton hold --lane L --pid $$")
		return exitUsage
	}
	if os.Getenv("BATON") == "off" {
		return 0
	}
	ctx := context.Background()
	shell, err := process.Read(ctx, *pid)
	if err != nil || shell.Dead || shell.UID != os.Geteuid() || shell.Start == "" {
		fmt.Fprintln(diagnostics, "baton hold: calling shell unavailable:", err)
		return exitFailure
	}
	if *watch != "" {
		if shell.Start != *watch {
			return exitFailure
		}
		err = holdShell(ctx, *name, shell, out, diagnostics)
	} else {
		// Only an actual ancestor can lend its shell lifetime to this caller.
		if !shellAncestor(ctx, shell) {
			fmt.Fprintln(diagnostics, "baton hold: pid is not a calling ancestor")
			return exitUsage
		}
		// A config typo must not block the command after hold &&; it runs
		// unqueued, as with BATON=off, until the config is fixed.
		if _, err := config.Load(); err != nil {
			fmt.Fprintf(diagnostics, "baton hold: %v\nbaton hold: running unqueued; fix the config or set BATON=off\n", err)
			return 0
		}
		err = startHold(ctx, *name, shell, diagnostics)
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "baton hold:", err)
		return exitFailure
	}
	return 0
}

func startHold(ctx context.Context, name string, shell process.Info, diagnostics io.Writer) error {
	root, err := stateRoot()
	if err != nil {
		return err
	}
	store, err := newStore(root)
	if err != nil {
		return err
	}
	lanes, err := inheritedLanes(ctx, store)
	if err != nil {
		return err
	}
	// Any live ancestor reservation covers this shell across lanes, as for
	// run: x -> hold y alongside y -> hold x must not wait in a circle.
	if len(lanes) != 0 {
		return nil
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	// The keeper has no terminal or user pipes after readiness. Its own
	// session survives Ctrl-C; only the shell's birth identity ends the hold.
	// shell -> hold -> keeper --ready--> hold exits; keeper watches shell
	command := exec.Command(binary, "hold", "--lane", name, "--pid", strconv.Itoa(shell.PID), "--watch", shell.Start)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	command.Stdout, command.Stderr = writer, writer
	err = command.Start()
	_ = writer.Close()
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if scanner.Text() == holdReady {
			return command.Process.Release()
		}
		fmt.Fprintln(diagnostics, scanner.Text())
	}
	return errors.Join(errors.New("keeper exited before admission"), scanner.Err(), command.Wait())
}

func holdShell(parent context.Context, name string, shell process.Info, out, diagnostics io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stopped := make(chan struct{})
	// Cancel queued admission too: killing the shell must remove its ticket.
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()
		for {
			if !shellAlive(ctx, shell) {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { cancel(); <-stopped }()
	opts, err := parse([]string{"--lane", name, "--label", "shell hold", "--", "baton hold"}, diagnostics)
	if err != nil {
		return err
	}
	root, err := stateRoot()
	if err != nil {
		return err
	}
	store, err := newStore(root)
	if err != nil {
		return err
	}
	job, err := newJob(opts)
	if err != nil {
		return err
	}
	owner, err := lockfile.Try(filepath.Join(root, "jobs", job.ID+".lock"))
	if err != nil {
		return err
	}
	defer func() { _ = owner.Close(); store.remove(job.ID) }()
	// The keeper owns the lock. A separate shell identity prevents reap from
	// mistaking an adopted shell or exec'd program for a baton-started child.
	job.ShellPID, job.ShellStart = shell.PID, shell.Start
	if err := store.publish(job); err != nil {
		return err
	}
	lease, err := admit(ctx, opts, store, &job, diagnostics)
	if err != nil {
		return err
	}
	if lease != nil {
		defer func() { _ = lease.Release() }()
	}
	job.Start = time.Now().UTC()
	job.WaitNanos = job.Start.Sub(job.Queued).Nanoseconds()
	job.PID, job.Phase = os.Getpid(), "running"
	if err := store.publish(job); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, holdReady); err != nil {
		return err
	}
	// Close both inherited write descriptors before letting the shell proceed;
	// otherwise Claude could wait on the keeper's pipes until the shell exits.
	_ = os.Stdout.Close()
	_ = os.Stderr.Close()
	<-ctx.Done()
	job.End, job.Phase = time.Now().UTC(), "finished"
	return store.finish(context.Background(), job)
}

func shellAlive(ctx context.Context, expected process.Info) bool {
	current, err := process.Read(ctx, expected.PID)
	if errors.Is(err, process.ErrGone) {
		return false
	}
	if err != nil {
		return true
	} // Unreadable identity alone is not an exit.
	return !current.Dead && current.Start == expected.Start && current.UID == expected.UID
}

func shellAncestor(ctx context.Context, expected process.Info) bool {
	// Follow kernel parent links, never names or environment-provided PIDs.
	for pid := os.Getpid(); pid > 1; {
		current, err := process.Read(ctx, pid)
		if err != nil {
			return false
		}
		if current.PID == expected.PID {
			return current.Start == expected.Start
		}
		if current.Parent == current.PID {
			return false
		}
		pid = current.Parent
	}
	return false
}

func addHeldLanes(ctx context.Context, jobs []jobRecord, lanes map[string]string) {
	// A prefix cannot export into its parent shell. Discover same-lane holds
	// through verified ancestry instead, so installed shims do not self-queue.
	for _, job := range jobs {
		if job.ShellPID <= 1 || job.ShellStart == "" || job.Phase != "running" {
			continue
		}
		if shellAncestor(ctx, process.Info{PID: job.ShellPID, Start: job.ShellStart}) {
			lanes[job.Lane] = job.ID
		}
	}
}
