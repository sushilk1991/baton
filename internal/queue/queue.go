// Package queue orders crash-safe lane admission across processes.
package queue

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sushilk1991/baton/internal/lane"
	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	agingInterval = 5 * time.Minute
	retryInterval = 10 * time.Millisecond
	directoryMode = 0700
	fileMode      = 0600
	guardFile     = "queue.lock"
)

// Priority is a job's initial position among priority classes.
type Priority string

const (
	// High runs before fresh normal and low priority jobs.
	High Priority = "high"
	// Normal runs before fresh low priority jobs.
	Normal Priority = "normal"
	// Low waits behind fresh high and normal priority jobs.
	Low Priority = "low"
)

// ErrBusy means an earlier ticket or an occupied lane prevents admission.
var ErrBusy = errors.New("queue busy")

// ErrClosed means a ticket was canceled or already admitted.
var ErrClosed = errors.New("ticket closed")

// Queue orders tickets for one lane under a shared state root.
type Queue struct {
	dir   string
	slots int
	lane  *lane.Lane
	now   func() time.Time
}

// Ticket holds a place until admission, cancellation, or process exit.
type Ticket struct {
	mu     sync.Mutex
	queue  *Queue
	path   string
	entry  record
	owner  *lockfile.Lock
	closed bool
}

// Sequence identifies this ticket within its lane for live job correlation.
func (ticket *Ticket) Sequence() uint64 {
	return ticket.entry.Sequence
}

// New prepares the queue and weighted lane beneath root.
func New(root, name string, slots int) (*Queue, error) {
	if root == "" || name == "" || slots < 1 {
		return nil, errors.New("queue needs a state root, name, and positive capacity")
	}

	// Keep slot mechanics below the queue, with both rooted in shared state.
	slotsLane, err := lane.New(filepath.Join(root, "locks"), name, slots)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "queue", fmt.Sprintf("%x", sha256.Sum256([]byte(name))))
	if err := os.MkdirAll(dir, directoryMode); err != nil {
		return nil, err
	}
	return &Queue{dir: dir, slots: slots, lane: slotsLane, now: time.Now}, nil
}

// Enqueue registers a live ticket without waiting for slots.
func (q *Queue) Enqueue(ctx context.Context, weight int, priority Priority) (*Ticket, error) {
	if weight < 1 || weight > q.slots || priority.rank() < 0 {
		return nil, fmt.Errorf("invalid ticket weight %d or priority %q", weight, priority)
	}
	guard, err := q.lockGuard(ctx)
	if err != nil {
		return nil, err
	}
	ticket, err := q.addTicket(weight, priority)
	if closeErr := guard.Close(); closeErr != nil {
		if ticket != nil {
			closeErr = errors.Join(closeErr, ticket.Close())
		}
		return nil, errors.Join(err, closeErr)
	}
	return ticket, err
}

// TryAcquire reserves slots only when this ticket leads the live queue.
func (ticket *Ticket) TryAcquire() (*lane.Lease, error) {
	ticket.mu.Lock()
	defer ticket.mu.Unlock()
	if ticket.closed {
		return nil, ErrClosed
	}
	guard, err := ticket.queue.tryGuard()
	if err != nil {
		return nil, err
	}

	// Keep the turn check and slot reservation inside the same queue guard.
	// queue.lock -> prune/order -> lane.TryAcquire -> remove ticket -> job
	lease, err := ticket.admit()
	if closeErr := guard.Close(); closeErr != nil {
		if lease != nil {
			closeErr = errors.Join(closeErr, lease.Release())
		}
		return nil, errors.Join(err, closeErr)
	}
	return lease, err
}

// Acquire waits for ordered admission and removes the ticket on error.
func (ticket *Ticket) Acquire(ctx context.Context) (lease *lane.Lease, err error) {
	// Any canceled or failed wait relinquishes its place for later callers.
	defer func() { err = errors.Join(err, ticket.Close()) }()
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lease, err := ticket.TryAcquire()
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

// Close cancels a ticket; concurrent and repeated calls are safe.
func (ticket *Ticket) Close() error {
	ticket.mu.Lock()
	defer ticket.mu.Unlock()
	if ticket.closed {
		return nil
	}
	guard, err := ticket.queue.lockGuard(context.Background())
	if err != nil {
		// Releasing ownership lets the next healthy caller prune this ticket.
		ticket.closed = true
		return errors.Join(err, ticket.owner.Close())
	}
	return errors.Join(ticket.finish(), guard.Close())
}

func (ticket *Ticket) admit() (*lane.Lease, error) {
	entries, err := ticket.queue.liveTickets()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 || entries[0].Sequence != ticket.entry.Sequence {
		return nil, ErrBusy
	}
	lease, err := ticket.queue.lane.TryAcquire(ticket.entry.Weight)
	if errors.Is(err, lane.ErrBusy) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, err
	}
	if err := ticket.finish(); err != nil {
		return nil, errors.Join(err, lease.Release())
	}
	return lease, nil
}

func (ticket *Ticket) finish() error {
	// Owner paths are unique and removed only under the queue guard. No
	// scanner can recreate a removed inode while its owner is still live.
	ticket.closed = true
	return errors.Join(ticket.owner.Close(), removeTicket(ticket.path))
}

func (q *Queue) tryGuard() (*lockfile.Lock, error) {
	guard, err := lockfile.Try(filepath.Join(q.dir, guardFile))
	if errors.Is(err, lockfile.ErrBusy) {
		return nil, ErrBusy
	}
	return guard, err
}

func (q *Queue) lockGuard(ctx context.Context) (*lockfile.Lock, error) {
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		guard, err := q.tryGuard()
		if !errors.Is(err, ErrBusy) {
			return guard, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (priority Priority) rank() int {
	switch priority {
	case High:
		return 0
	case Normal:
		return 1
	case Low:
		return 2
	default:
		return -1
	}
}

func (entry record) rank(now time.Time) int {
	// Capped promotion eventually puts every waiter in the FIFO high class.
	rank := entry.Priority.rank()
	wait := now.Sub(entry.Created)
	if wait <= 0 {
		return rank
	}
	promotions := wait / agingInterval
	if promotions >= time.Duration(rank) {
		return 0
	}
	return rank - int(promotions)
}
