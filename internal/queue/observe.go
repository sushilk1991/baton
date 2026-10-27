package queue

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/sushilk1991/baton/internal/lane"
	"github.com/sushilk1991/baton/internal/lockfile"
)

// Waiting is a detached live ticket in current admission order.
type Waiting struct {
	// Sequence uniquely identifies the ticket within this lane.
	Sequence uint64
	// Created is the registration time used by queue aging.
	Created time.Time
	// Priority is the original priority before aging.
	Priority Priority
	// Weight is the requested slot count.
	Weight int
	// PID is the owner process, for correlation with job metadata.
	PID int
}

// Snapshot combines kernel occupancy with a lane's ordered live tickets.
type Snapshot struct {
	// Used counts occupied slots, including unpublished running jobs.
	Used int
	// Total is persisted capacity, or configured capacity before first use.
	Total int
	// Waiting excludes released owner locks without deleting their files.
	Waiting []Waiting
}

// Observe reads existing state without pruning tickets or changing capacity.
func Observe(ctx context.Context, root, name string, slots int) (Snapshot, error) {
	dir := filepath.Join(root, "queue", fmt.Sprintf("%x", sha256.Sum256([]byte(name))))
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	// Match scheduler lock order to avoid deadlock:
	// queue guard -> ordered tickets -> lane allocation guard -> slot probes.
	for {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		guard, err := lockfile.TryExisting(filepath.Join(dir, guardFile))
		if errors.Is(err, os.ErrNotExist) {
			return observeLane(ctx, root, name, slots, nil)
		}
		if errors.Is(err, lockfile.ErrBusy) {
			select {
			case <-ctx.Done():
				return Snapshot{}, ctx.Err()
			case <-ticker.C:
				continue
			}
		}
		if err != nil {
			return Snapshot{}, err
		}
		waiting, err := peekTickets(ctx, dir, time.Now())
		if err != nil {
			return Snapshot{}, errors.Join(err, guard.Close())
		}
		view, err := observeLane(ctx, root, name, slots, waiting)
		return view, errors.Join(err, guard.Close())
	}
}

func observeLane(ctx context.Context, root, name string, slots int, waiting []Waiting) (Snapshot, error) {
	used, total, err := lane.Observe(ctx, filepath.Join(root, "locks"), name, slots)
	return Snapshot{Used: used, Total: total, Waiting: waiting}, err
}

func peekTickets(ctx context.Context, dir string, now time.Time) ([]Waiting, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := []record{}
	seen := make(map[string]bool)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Inspect both halves so a live owner with missing JSON fails visibly.
		base := ticketBase(file.Name())
		if base == "" || seen[base] {
			continue
		}
		seen[base] = true
		path := filepath.Join(dir, base)
		lock, err := lockfile.TryExisting(path + ".lock")
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
		data, err := os.ReadFile(path + ".json")
		if err != nil {
			return nil, err
		}
		var entry record
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, fmt.Errorf("ticket %s: %w", path, err)
		}
		sequence, _ := strconv.ParseUint(filepath.Base(path), 10, 64)
		if entry.Sequence != sequence || entry.Created.IsZero() || entry.Priority.rank() < 0 || entry.Weight < 1 || entry.PID < 1 {
			return nil, fmt.Errorf("invalid ticket %s", path)
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i].rank(now), entries[j].rank(now)
		if left != right {
			return left < right
		}
		return entries[i].Sequence < entries[j].Sequence
	})
	waiting := make([]Waiting, 0, len(entries))
	for _, entry := range entries {
		waiting = append(waiting, Waiting{entry.Sequence, entry.Created, entry.Priority, entry.Weight, entry.PID})
	}
	return waiting, nil
}
