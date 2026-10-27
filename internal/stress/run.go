package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
)

type endEvents func(context.Context, time.Time, time.Time, string) (uint64, error)

type benchmark struct {
	opts     options
	worker   string
	prefix   []string
	probe    func(context.Context) (pressure.Snapshot, error)
	events   func(context.Context) (endEvents, error)
	progress io.Writer
}

type outcome struct {
	index int
	err   error
	ended time.Time
}

func (bench benchmark) runPhase(parent context.Context, mode string) (p phase) {
	p.Mode = mode
	dir := filepath.Join(bench.opts.output, mode)
	if err := os.Mkdir(dir, dirMode); err != nil {
		p.Error = err.Error()
		return p
	}
	ctx, cancel := context.WithTimeout(parent, bench.opts.timeout)
	defer cancel()
	end, err := bench.events(ctx)
	if err != nil {
		p.Error = "event observation: " + err.Error()
		return p
	}
	samples, err := os.Create(filepath.Join(dir, "samples.jsonl"))
	if err != nil {
		p.Error = err.Error()
		return p
	}
	defer samples.Close()
	log, err := os.Create(filepath.Join(dir, "jobs.log"))
	if err != nil {
		p.Error = err.Error()
		return p
	}
	defer log.Close()
	current, err := bench.observe(ctx, &p, samples)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	if err := safeBudget(bench.opts, current); err != nil {
		p.Error = err.Error()
		return p
	}
	home, err := os.MkdirTemp("", "baton-stress-state-")
	if err != nil {
		p.Error = err.Error()
		return p
	}
	// Artifact destinations may be VM bind mounts. Hold locks on the native
	// filesystem and archive state only after every owned process is reaped.
	defer func() {
		p.Error = joinError(p.Error, errors.Join(copyEvidence(home, filepath.Join(dir, "home")), os.RemoveAll(home)))
	}()
	env, err := isolatedEnv(home)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	if mode == "queued" {
		if err := writePolicy(env, bench.opts); err != nil {
			p.Error = err.Error()
			return p
		}
	}
	fmt.Fprintf(bench.progress, "stress: %s: %d jobs * %.4f GiB, hold %s\n", mode, bench.opts.jobs, float64(bench.opts.bytes)/float64(gib), bench.opts.hold)
	done := make(chan outcome, bench.opts.jobs)
	started, finished := 0, 0
	p.Started = time.Now()
	launch := func(index int) error {
		if err := bench.launch(ctx, mode, dir, env, index, log, done); err != nil {
			return err
		}
		started++
		return nil
	}
	initial := bench.opts.jobs
	if mode == "sequential" {
		initial = 1
	}
	// Both parallel phases launch all contenders before opening the barrier.
	// wrappers -> baton queue -> worker -> barrier -> allocation -> hold
	for i := 0; i < initial; i++ {
		if err := launch(i); err != nil {
			p.Error = err.Error()
			cancel()
			break
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go"), nil, fileMode); err != nil {
		p.Error = err.Error()
		cancel()
	}
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	ctxDone := ctx.Done()
	for finished < started {
		select {
		case result := <-done:
			finished++
			p.Ended = result.ended
			if result.err != nil {
				p.Failed++
				if killed(result.err) {
					p.Killed++
				}
				p.Error = joinError(p.Error, fmt.Errorf("job %d: %w", result.index, result.err))
				cancel()
			}
			if mode == "sequential" && p.Error == "" && ctx.Err() == nil && started < bench.opts.jobs {
				if err := launch(started); err != nil {
					p.Error = err.Error()
					cancel()
				}
			}
		case <-ticker.C:
			if ctx.Err() != nil {
				continue
			}
			if _, err := bench.observe(ctx, &p, samples); err != nil {
				p.Error = joinError(p.Error, err)
				cancel()
			}
		case <-ctxDone:
			p.Error = joinError(p.Error, ctx.Err())
			ctxDone = nil
		}
	}
	if !p.Ended.IsZero() {
		p.WallSeconds = p.Ended.Sub(p.Started).Seconds()
	}
	// Retrieve evidence after all owned processes are reaped, even on timeout.
	probeCtx, stop := context.WithTimeout(context.Background(), probeTimeout)
	defer stop()
	_, err = bench.observe(probeCtx, &p, samples)
	p.Error = joinError(p.Error, err)
	p.KillEvents, err = end(probeCtx, p.Started, time.Now(), dir)
	p.Error = joinError(p.Error, err)
	jobs := []jobRecord{}
	for i := 0; i < started; i++ {
		data, err := os.ReadFile(jobPath(dir, i))
		var job jobRecord
		if err == nil {
			err = json.Unmarshal(data, &job)
		}
		if err != nil || !job.Completed || job.Bytes != bench.opts.bytes || job.Touches == 0 {
			p.Error = joinError(p.Error, fmt.Errorf("job %d: incomplete allocation/touch evidence (%v)", i, err))
			continue
		}
		p.Completed++
		jobs = append(jobs, job)
	}
	p.MaxActive, err = overlap(jobs)
	p.Error = joinError(p.Error, err)
	data, err := os.ReadFile(filepath.Join(dir, "jobs.log"))
	p.Error = joinError(p.Error, err)
	p.Bypasses = strings.Count(string(data), "WARNING: max_wait")
	return p
}

func (bench benchmark) observe(ctx context.Context, p *phase, out io.Writer) (pressure.Snapshot, error) {
	sample, err := bench.probe(ctx)
	if err != nil {
		return sample, fmt.Errorf("pressure probe: %w", err)
	}
	record := struct {
		Time time.Time `json:"time"`
		pressure.Snapshot
	}{time.Now(), sample}
	if err := json.NewEncoder(out).Encode(record); err != nil {
		return sample, err
	}
	return sample, sampleRecord(p, sample)
}

func (bench benchmark) launch(ctx context.Context, mode, dir string, env []string, index int, log *os.File, done chan<- outcome) error {
	args := append([]string{}, bench.prefix...)
	args = append(args, "--worker", "--bytes", strconv.FormatUint(bench.opts.bytes, 10), "--hold", bench.opts.hold.String(), "--record", jobPath(dir, index), "--gate", filepath.Join(dir, "go"))
	program := bench.worker
	if mode == "queued" {
		args = append([]string{"run", "--lane", "stress", "--mem", strconv.FormatUint(bench.opts.bytes, 10), "--label", fmt.Sprintf("stress-%d", index), "--", program}, args...)
		program = bench.opts.baton
	}
	read, write, err := os.Pipe()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, program, args...)
	command.Env, command.Stdin, command.Stdout, command.Stderr = env, read, log, log
	command.WaitDelay = cleanupGrace
	command.Cancel = func() error {
		_ = write.Close()
		return command.Process.Signal(terminateSignal())
	}
	if err := command.Start(); err != nil {
		_ = read.Close()
		_ = write.Close()
		return err
	}
	_ = read.Close()
	go func() {
		err := command.Wait()
		_ = write.Close()
		done <- outcome{index, err, time.Now()}
	}()
	return nil
}

func isolatedEnv(home string) ([]string, error) {
	if err := os.MkdirAll(home, dirMode); err != nil {
		return nil, err
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "LC_ALL=C", "BATON=on"}
	// Strip all inherited XDG/agent/config knobs, preserving only executable
	// lookup and platform requirements. No shell or global settings are read.
	for _, key := range []string{"PATH", "SystemRoot", "SYSTEMROOT", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "XDG_CONFIG_DIRS", "XDG_DATA_DIRS"} {
		dir := filepath.Join(home, key)
		if err := os.Mkdir(dir, dirMode); err != nil {
			return nil, err
		}
		env = append(env, key+"="+dir)
	}
	return env, nil
}

func writePolicy(env []string, opts options) error {
	for _, entry := range env {
		value, ok := strings.CutPrefix(entry, "XDG_CONFIG_HOME=")
		if !ok {
			continue
		}
		dir := filepath.Join(value, "baton")
		if err := os.Mkdir(dir, dirMode); err != nil {
			return err
		}
		// Keep the production margin, polling and hysteresis defaults. Only
		// capacity and the explicit benchmark memory timeout are customized.
		policy := fmt.Sprintf("[lanes.stress]\nslots = 1\nmax_weight = 1\nmemory_gate = true\nmax_wait = %q\n", opts.maxWait.String())
		return os.WriteFile(filepath.Join(dir, "config.toml"), []byte(policy), fileMode)
	}
	return errors.New("missing isolated config directory")
}

func jobPath(dir string, index int) string {
	return filepath.Join(dir, fmt.Sprintf("job-%04d.json", index))
}

func joinError(previous string, err error) string {
	if err == nil {
		return previous
	}
	if previous == "" {
		return err.Error()
	}
	return previous + "; " + err.Error()
}

func copyEvidence(source, target string) error {
	// Only regular files and directories are created by our private workers.
	// Reject links rather than following a child-controlled path while copying.
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, dirMode)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("nonregular evidence file: %s", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, fileMode)
	})
}
