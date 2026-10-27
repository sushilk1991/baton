package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	sequenceFile = "sequence"
	ticketDigits = 20
)

type record struct {
	Sequence uint64    `json:"sequence"`
	Created  time.Time `json:"created"`
	Priority Priority  `json:"priority"`
	Weight   int       `json:"weight"`
	PID      int       `json:"pid"`
}

func (q *Queue) addTicket(weight int, priority Priority) (*Ticket, error) {
	// Prune even on enqueue, so dead tickets do not need an active waiter.
	if _, err := q.liveTickets(); err != nil {
		return nil, err
	}
	sequence, err := q.nextSequence()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(q.dir, fmt.Sprintf("%020d", sequence))
	owner, err := lockfile.Try(path + ".lock")
	if err != nil {
		return nil, err
	}
	entry := record{Sequence: sequence, Created: q.now(), Priority: priority, Weight: weight, PID: os.Getpid()}
	if err := writeRecord(path+".json", entry); err != nil {
		return nil, errors.Join(err, owner.Close(), removeTicket(path))
	}
	return &Ticket{queue: q, path: path, entry: entry, owner: owner}, nil
}

func writeRecord(path string, entry record) error {
	// The guard hides incomplete writes; owner death makes them reclaimable.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	return errors.Join(json.NewEncoder(file).Encode(entry), file.Close())
}

func (q *Queue) liveTickets() ([]record, error) {
	files, err := os.ReadDir(q.dir)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var entries []record
	for _, file := range files {
		// A crash may leave either half of a ticket or a sequence temp file.
		if strings.HasPrefix(file.Name(), ".sequence-") {
			if err := os.Remove(filepath.Join(q.dir, file.Name())); err != nil {
				return nil, err
			}
			continue
		}
		base := ticketBase(file.Name())
		if base == "" || seen[base] {
			continue
		}
		seen[base] = true
		entry, live, err := readTicket(filepath.Join(q.dir, base))
		if err != nil {
			return nil, err
		}
		if live {
			entries = append(entries, entry)
		}
	}

	// A single snapshot time avoids inconsistent comparisons at a boundary.
	now := q.now()
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i].rank(now), entries[j].rank(now)
		if left != right {
			return left < right
		}
		return entries[i].Sequence < entries[j].Sequence
	})
	return entries, nil
}

func readTicket(path string) (record, bool, error) {
	owner, err := lockfile.Try(path + ".lock")
	if err == nil {
		// Kernel ownership, not PID existence, proves a waiter is still live.
		return record{}, false, errors.Join(owner.Close(), removeTicket(path))
	}
	if !errors.Is(err, lockfile.ErrBusy) {
		return record{}, false, err
	}
	data, err := os.ReadFile(path + ".json")
	if err != nil {
		return record{}, false, err
	}
	var entry record
	if err := json.Unmarshal(data, &entry); err != nil {
		return record{}, false, fmt.Errorf("ticket %s: %w", path, err)
	}
	sequence, err := strconv.ParseUint(filepath.Base(path), 10, 64)
	if err != nil || entry.Sequence != sequence || entry.Created.IsZero() || entry.Priority.rank() < 0 || entry.Weight < 1 || entry.PID < 1 {
		return record{}, false, fmt.Errorf("invalid ticket %s", path)
	}
	return entry, true, nil
}

func ticketBase(name string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".lock")
	if len(base) != ticketDigits || base == name {
		return ""
	}
	if _, err := strconv.ParseUint(base, 10, 64); err != nil {
		return ""
	}
	return base
}

func removeTicket(path string) error {
	var err error
	for _, suffix := range []string{".json", ".lock"} {
		if removeErr := os.Remove(path + suffix); !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}
	return err
}

func (q *Queue) nextSequence() (uint64, error) {
	path := filepath.Join(q.dir, sequenceFile)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	var sequence uint64
	if err == nil {
		sequence, err = strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if err != nil || sequence == math.MaxUint64 {
			return 0, fmt.Errorf("invalid queue sequence in %s", path)
		}
	}

	// Persist before publishing: crashes can skip a number but never reuse it.
	sequence++
	file, err := os.CreateTemp(q.dir, ".sequence-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(file.Name())
	_, writeErr := fmt.Fprintln(file, sequence)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return 0, err
	}
	return sequence, os.Rename(file.Name(), path)
}
