// Package lane coordinates weighted slots with crash-safe kernel locks.
package lane

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	directoryMode = 0700
	retryInterval = 10 * time.Millisecond
	capacityFile  = "capacity"
	guardFile     = "allocation.lock"
)

// ErrBusy means the requested slots cannot currently be reserved together.
var ErrBusy = errors.New("lane busy")

// Lane is a named counting semaphore shared by processes using one state root.
type Lane struct {
	dir   string
	slots int
}

// Lease owns a complete weighted reservation until Release or process exit.
type Lease struct {
	locks []*lockfile.Lock
	once  sync.Once
	err   error
}

// New prepares a lane under root; names cannot escape the state directory.
func New(root, name string, slots int) (*Lane, error) {
	if root == "" || name == "" || slots < 1 {
		return nil, errors.New("lane needs a state root, name, and positive capacity")
	}

	// Hash the whole identifier, so names such as ../build remain local.
	dir := filepath.Join(root, fmt.Sprintf("%x", sha256.Sum256([]byte(name))))
	if err := os.MkdirAll(dir, directoryMode); err != nil {
		return nil, err
	}
	return &Lane{dir: dir, slots: slots}, nil
}

// TryAcquire reserves all weight slots or releases its partial reservation.
func (lane *Lane) TryAcquire(weight int) (*Lease, error) {
	if weight < 1 || weight > lane.slots {
		return nil, fmt.Errorf("weight %d outside lane capacity 1..%d", weight, lane.slots)
	}

	// Serialize short allocation attempts, never the lifetime of a job.
	// guard -> check capacity -> lock W slots -> release guard -> run job
	guard, err := lockfile.Try(filepath.Join(lane.dir, guardFile))
	if err != nil {
		return nil, laneError(err)
	}
	lease, err := lane.reserve(weight)
	if closeErr := guard.Close(); closeErr != nil {
		if lease != nil {
			closeErr = errors.Join(closeErr, lease.Release())
		}
		return nil, errors.Join(err, closeErr)
	}
	return lease, err
}

// Acquire retries until the entire weight is available or ctx is canceled.
func (lane *Lane) Acquire(ctx context.Context, weight int) (*Lease, error) {
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	// Waiting holds no files or slots; crashes therefore leave no reservations.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lease, err := lane.TryAcquire(weight)
		if !errors.Is(err, ErrBusy) {
			return lease, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Release frees every reserved slot; concurrent or repeated calls are safe.
func (lease *Lease) Release() error {
	lease.once.Do(func() {
		// Close every handle even if an earlier unlock reports an I/O error.
		for _, lock := range lease.locks {
			lease.err = errors.Join(lease.err, lock.Close())
		}
	})
	return lease.err
}

func (lane *Lane) reserve(weight int) (*Lease, error) {
	if err := lane.checkCapacity(); err != nil {
		return nil, err
	}
	return lane.takeSlots(lane.slots, weight)
}

func (lane *Lane) takeSlots(capacity, weight int) (*Lease, error) {
	lease := &Lease{}

	// Busy slots are skipped, but no partial weight survives a failed attempt.
	for slot := 0; slot < capacity; slot++ {
		path := filepath.Join(lane.dir, fmt.Sprintf("slot-%d.lock", slot))
		lock, err := lockfile.Try(path)
		if errors.Is(err, lockfile.ErrBusy) {
			continue
		}
		if err != nil {
			return nil, errors.Join(err, lease.Release())
		}
		lease.locks = append(lease.locks, lock)
		if len(lease.locks) == weight {
			return lease, nil
		}
	}
	return nil, errors.Join(ErrBusy, lease.Release())
}

func (lane *Lane) checkCapacity() error {
	path := filepath.Join(lane.dir, capacityFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return lane.writeCapacity(path)
	}
	if err != nil {
		return err
	}
	previous, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || previous < 1 {
		return fmt.Errorf("invalid lane capacity in %s", path)
	}
	if previous == lane.slots {
		return nil
	}

	// Resize only while idle: old and new capacities must never coexist.
	// guard -> lock every old slot -> replace capacity -> unlock old slots
	idle, err := lane.takeSlots(previous, previous)
	if err != nil {
		return err
	}
	return errors.Join(lane.writeCapacity(path), idle.Release())
}

func (lane *Lane) writeCapacity(path string) error {
	// Rename a complete file, so a crash cannot expose half-written metadata.
	file, err := os.CreateTemp(lane.dir, ".capacity-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := fmt.Fprintln(file, lane.slots)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func laneError(err error) error {
	if errors.Is(err, lockfile.ErrBusy) {
		return ErrBusy
	}
	return err
}
