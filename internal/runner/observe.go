package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/lockfile"
	"github.com/sushilk1991/baton/internal/pressure"
	"github.com/sushilk1991/baton/internal/queue"
)

// Read projections stay private; exported fields support JSON encoding only.
type laneView struct {
	Name            string `json:"name"`
	Used            int    `json:"used"`
	Total           int    `json:"total"`
	ConfiguredTotal int    `json:"configured_total"`
}

type jobView struct {
	jobRecord
	RuntimeNanos int64 `json:"runtime_ns"`
	Position     int   `json:"position,omitempty"`
}

type memoryView struct {
	Pressure       pressure.Level `json:"pressure"`
	TotalBytes     uint64         `json:"total_bytes"`
	AvailableBytes uint64         `json:"available_bytes"`
	SwapTotalBytes uint64         `json:"swap_total_bytes"`
	SwapUsedBytes  uint64         `json:"swap_used_bytes"`
	SomeAvg10      float64        `json:"some_avg10"`
}

type statusView struct {
	Time    time.Time  `json:"time"`
	Lanes   []laneView `json:"lanes"`
	Running []jobView  `json:"running"`
	Queue   []jobView  `json:"queue"`
	Memory  memoryView `json:"memory"`
}

func readStatus(ctx context.Context, root string, cfg *config.Config, provider pressure.Provider, now time.Time) (statusView, error) {
	sample, err := provider.Sample(ctx)
	if err != nil {
		return statusView{}, fmt.Errorf("memory pressure: %w", err)
	}
	if err := sample.Validate(); err != nil {
		return statusView{}, fmt.Errorf("memory pressure: %w", err)
	}
	jobs, err := activeJobs(ctx, root)
	if err != nil {
		return statusView{}, err
	}
	view := statusView{Time: now, Lanes: []laneView{}, Running: []jobView{}, Queue: []jobView{}, Memory: memoryView{sample.Level, sample.TotalBytes, sample.AvailableBytes, sample.SwapTotalBytes, sample.SwapUsedBytes, sample.SomeAvg10}}
	names := make(map[string]bool)
	for _, name := range cfg.LaneNames() {
		names[name] = true
	}
	for _, job := range jobs {
		if job.Lane != "" {
			names[job.Lane] = true
		}
		if job.Phase == phaseRunning || job.Phase == phaseStopped {
			view.Running = append(view.Running, jobView{jobRecord: job, RuntimeNanos: elapsed(now, job.Start)})
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		capacity := cfg.Lane(name).Slots
		lane, err := queue.Observe(ctx, root, name, capacity)
		if err != nil {
			return statusView{}, fmt.Errorf("lane %q: %w", name, err)
		}
		view.Lanes = append(view.Lanes, laneView{name, lane.Used, lane.Total, capacity})
		for index, ticket := range lane.Waiting {
			job, err := queuedJob(ctx, root, jobs, name, ticket)
			if err != nil {
				return statusView{}, err
			}
			job.WaitNanos = elapsed(now, job.Queued)
			view.Queue = append(view.Queue, jobView{jobRecord: job, Position: index + 1})
		}
	}
	sort.Slice(view.Running, func(i, j int) bool {
		left, right := view.Running[i], view.Running[j]
		if !left.Start.Equal(right.Start) {
			return left.Start.Before(right.Start)
		}
		return left.ID < right.ID
	})
	return view, nil
}

func queuedJob(ctx context.Context, root string, jobs []jobRecord, name string, ticket queue.Waiting) (jobRecord, error) {
	job := queueJob(jobs, name, ticket)
	if job.ID != "" {
		return job, nil
	}

	// A ticket may appear after the initial job scan. Its writer publishes
	// identity before enqueueing, so refresh once before using a placeholder.
	fresh, err := activeJobs(ctx, root)
	if err != nil {
		return jobRecord{}, err
	}
	return queueJob(fresh, name, ticket), nil
}

func queueJob(jobs []jobRecord, name string, ticket queue.Waiting) jobRecord {
	for _, job := range jobs {
		if job.Lane != name || job.OwnerPID != ticket.PID || job.Phase != "queued" {
			continue
		}
		// Older runners have no sequence field; their CLI owns one job per PID.
		if job.QueueSequence != 0 && job.QueueSequence != ticket.Sequence {
			continue
		}
		// The live ticket supplies the sequence while job publication catches up.
		job.QueueSequence = ticket.Sequence
		return job
	}
	// A ticket can precede publication of its job metadata. Keep it visible
	// with its real scheduling fields rather than silently dropping a waiter.
	return jobRecord{Lane: name, OwnerPID: ticket.PID, QueueSequence: ticket.Sequence, Queued: ticket.Created, Priority: string(ticket.Priority), Weight: ticket.Weight, Phase: "queued"}
}

func elapsed(now, start time.Time) int64 {
	if start.IsZero() || now.Before(start) {
		return 0
	}
	return now.Sub(start).Nanoseconds()
}

func activeJobs(ctx context.Context, root string) ([]jobRecord, error) {
	dir := filepath.Join(root, "jobs")
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	jobs := []jobRecord{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".json")
		lock, err := lockfile.TryExisting(filepath.Join(dir, id+".lock"))
		if err == nil {
			if err := lock.Close(); err != nil {
				return nil, err
			}
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if !errors.Is(err, lockfile.ErrBusy) {
			return nil, err
		}
		path := filepath.Join(dir, file.Name())
		data, err := os.ReadFile(path)
		// Normal completion may remove the record after its owner probe.
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var job jobRecord
		if err := json.Unmarshal(data, &job); err != nil {
			return nil, fmt.Errorf("job %s: %w", path, err)
		}
		if job.ID != id || job.OwnerPID < 1 || (job.Phase != "queued" && job.Phase != phaseRunning && job.Phase != phaseStopped) {
			return nil, fmt.Errorf("invalid job %s", path)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func readHistory(ctx context.Context, root string, since time.Time, lane string) ([]jobRecord, error) {
	guard, err := observeGuard(ctx, filepath.Join(root, "history.lock"))
	if err != nil {
		return nil, err
	}
	// Copy both segments under the guard, then release it before parsing.
	// Readers cannot delay completed jobs with JSON decoding or filtering.
	segments, err := copyHistory(root)
	if guard != nil {
		err = errors.Join(err, guard.Close())
	}
	if err != nil {
		return nil, err
	}
	entries := []jobRecord{}
	for _, segment := range segments {
		jobs, err := parseHistory(ctx, segment.path, segment.data)
		if err != nil {
			return nil, err
		}
		for _, job := range jobs {
			if !since.IsZero() && job.End.Before(since) {
				continue
			}
			if lane != "" && job.Lane != lane {
				continue
			}
			entries = append(entries, job)
		}
	}
	return entries, nil
}

type historyData struct {
	path string
	data []byte
}

func copyHistory(root string) ([]historyData, error) {
	segments := []historyData{}
	for _, name := range []string{"history.jsonl.1", "history.jsonl"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		segments = append(segments, historyData{path, data})
	}
	return segments, nil
}

func observeGuard(ctx context.Context, path string) (*lockfile.Lock, error) {
	ticker := time.NewTicker(storePoll)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		guard, err := lockfile.TryExisting(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if !errors.Is(err, lockfile.ErrBusy) {
			return guard, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func parseHistory(ctx context.Context, path string, data []byte) ([]jobRecord, error) {
	// Validate every retained line before filtering, so damage outside the
	// selected lane or time window still reports its source location.
	reader := bufio.NewReader(bytes.NewReader(data))
	entries := []jobRecord{}
	for line := 1; ; line++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) && len(data) == 0 {
			return entries, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: incomplete history: %w", path, line, err)
		}
		var job jobRecord
		if err := json.Unmarshal(data, &job); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if job.ID == "" || job.Start.IsZero() || job.End.Before(job.Start) || job.WaitNanos < 0 {
			return nil, fmt.Errorf("%s:%d: invalid history record", path, line)
		}
		entries = append(entries, job)
	}
}
