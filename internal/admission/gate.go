// Package admission combines memory policy with fair, crash-safe slots.
package admission

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sushilk1991/baton/internal/lane"
	"github.com/sushilk1991/baton/internal/pressure"
	"github.com/sushilk1991/baton/internal/queue"
)

const (
	defaultMaxWait  = 30 * time.Minute
	defaultPoll     = 250 * time.Millisecond
	defaultRecovery = 2 * time.Second
	defaultMargin   = 512 << 20
	// lateProbe bounds a probe once max_wait is spent; a provider may honor
	// only cancellation, and the probe then decides just the warning.
	lateProbe = time.Second
)

// MemoryGate selects whether admission enforces memory policy.
type MemoryGate int

const (
	// GateEnabled checks pressure and memory headroom before taking slots.
	GateEnabled MemoryGate = iota
	// GateDisabled retains queue and slot limits without probing memory.
	GateDisabled
)

// WaitReason says what a blocked admission attempt is waiting for.
type WaitReason int

const (
	// WaitMemory means pressure or memory headroom delays the job.
	WaitMemory WaitReason = iota
	// WaitSlots means memory allows the job but its lane has no free slots.
	WaitSlots
)

// Options controls a memory wait; zero fields select defaults.
type Options struct {
	// MemoryGate can disable memory checks for a configured lightweight lane.
	MemoryGate MemoryGate
	// MaxWait bounds memory delays, never slot or queue contention.
	MaxWait time.Duration
	// PollInterval controls how often pressure and slots are retried.
	PollInterval time.Duration
	// RecoveryWindow requires uninterrupted healthy observations after blocking.
	RecoveryWindow time.Duration
	// OnWait observes a blocked retry and its reason, including waits after
	// max_wait. A returned error cancels the wait and relinquishes the ticket.
	OnWait func(WaitReason, pressure.Snapshot, error) error
	// MarginBytes reserves memory beyond the declared or predicted budget.
	MarginBytes uint64
}

// Gate checks pressure before attempting a ticket's weighted reservation.
type Gate struct {
	provider pressure.Provider
	options  Options
}

type waitState struct {
	blocked      bool
	healthySince time.Time
}

// memoryBudget spends max_wait only while memory blocks admission. Time a
// job waits for a busy slot with healthy memory is never counted:
//
//	probe: blocked -- pause -- blocked -- pause -- ready -- slot busy -- ...
//	       |<---------- spent ---------->|          not spent
type memoryBudget struct {
	spent   time.Duration
	last    time.Time
	blocked bool
}

// New prepares a gate without reading or changing machine state.
func New(provider pressure.Provider, options Options) (*Gate, error) {
	if provider == nil || options.MaxWait < 0 || options.PollInterval < 0 || options.RecoveryWindow < 0 {
		return nil, errors.New("gate needs a provider and nonnegative durations")
	}
	if options.MemoryGate != GateEnabled && options.MemoryGate != GateDisabled {
		return nil, errors.New("unknown memory gate policy")
	}
	if options.MaxWait == 0 {
		options.MaxWait = defaultMaxWait
	}
	if options.PollInterval == 0 {
		options.PollInterval = defaultPoll
	}
	if options.RecoveryWindow == 0 {
		options.RecoveryWindow = defaultRecovery
	}
	if options.MarginBytes == 0 {
		options.MarginBytes = defaultMargin
	}
	return &Gate{provider: provider, options: options}, nil
}

// Acquire retains queue position while waiting and closes tickets on errors.
// A nil ticket checks only memory for a job sharing an ancestor's reservation.
func (gate *Gate) Acquire(ctx context.Context, ticket *queue.Ticket, memory uint64, warnings io.Writer) (lease *lane.Lease, err error) {
	if ticket != nil {
		defer func() { err = errors.Join(err, ticket.Close()) }()
	}
	if warnings == nil {
		warnings = os.Stderr
	}
	var state waitState
	var budget memoryBudget
	var sample pressure.Snapshot
	var sampleErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// Never retain slots while probing. Every attempt probes, even after
		// max_wait, so a slot freed after a long wait meets current pressure.
		// sample -> healthy/budget spent -> ticket.TryAcquire -> lease -> child
		gated := gate.options.MemoryGate == GateEnabled
		ready := true
		if gated {
			probed := time.Now()
			sample, sampleErr = gate.probe(ctx, budget.remaining(gate.options.MaxWait))
			now := time.Now()
			ready = state.ready(sample, sampleErr, memory, gate.options.MarginBytes, gate.options.RecoveryWindow, now)
			budget.observe(ready, probed, now)
		}
		expired := gated && budget.remaining(gate.options.MaxWait) <= 0
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !ready && !expired {
			if err := gate.wait(ctx, budget.remaining(gate.options.MaxWait), WaitMemory, sample, sampleErr); err != nil {
				return nil, err
			}
			continue
		}

		// A nested job has no ticket and must never take another lane's slots.
		if ticket != nil {
			lease, err = ticket.TryAcquire()
		}
		if errors.Is(err, queue.ErrBusy) {
			if err := gate.wait(ctx, 0, WaitSlots, sample, sampleErr); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if ready {
			return lease, nil
		}
		err = gate.warn(warnings, sample, sampleErr, state.healthySince)
		if err == nil {
			return lease, nil
		}
		if lease == nil {
			return nil, err
		}
		return nil, errors.Join(err, lease.Release())
	}
}

func (gate *Gate) probe(ctx context.Context, remaining time.Duration) (pressure.Snapshot, error) {
	// A probe never outlasts the unspent budget, or lateProbe once spent.
	if remaining <= 0 {
		remaining = lateProbe
	}
	probeCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	sample, err := gate.provider.Sample(probeCtx)
	if err != nil {
		return sample, err
	}
	return sample, sample.Validate()
}

// observe spends the time since the last blocked probe, or the duration of
// a probe that blocked on its own, such as one that failed slowly.
func (budget *memoryBudget) observe(ready bool, probed, now time.Time) {
	switch {
	case budget.blocked:
		budget.spent += now.Sub(budget.last)
	case !ready:
		budget.spent += now.Sub(probed)
	}
	budget.last, budget.blocked = now, !ready
}

func (budget *memoryBudget) remaining(maxWait time.Duration) time.Duration {
	return maxWait - budget.spent
}

func (state *waitState) ready(snapshot pressure.Snapshot, err error, memory, margin uint64, recovery time.Duration, now time.Time) bool {
	// Subtraction avoids wrapping an oversized --mem plus the safety margin.
	if err != nil || snapshot.Validate() != nil || snapshot.Level != pressure.Normal || snapshot.AvailableBytes <= margin || snapshot.AvailableBytes-margin <= memory {
		state.blocked = true
		state.healthySince = time.Time{}
		return false
	}
	if !state.blocked {
		return true
	}
	if state.healthySince.IsZero() {
		state.healthySince = now
	}
	return now.Sub(state.healthySince) >= recovery
}

// wait pauses one poll interval, or less when an unspent memory budget
// ends sooner; a zero budget is a slot wait and always pauses a full poll.
func (gate *Gate) wait(ctx context.Context, budget time.Duration, reason WaitReason, sample pressure.Snapshot, sampleErr error) error {
	if gate.options.OnWait != nil {
		if err := gate.options.OnWait(reason, sample, sampleErr); err != nil {
			return err
		}
	}
	return gate.pause(ctx, budget)
}

func (gate *Gate) pause(ctx context.Context, budget time.Duration) error {
	delay := gate.options.PollInterval
	if budget > 0 && budget < delay {
		delay = budget
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// warn names why memory still blocks: the probe error, the sample, or a
// healthy sample that has not yet lasted the recovery window, e.g.
// "pressure normal, 9.8 GiB available; healthy 1.2s of the 2s recovery window".
func (gate *Gate) warn(out io.Writer, sample pressure.Snapshot, probeErr error, healthySince time.Time) error {
	reason := fmt.Sprintf("pressure %s, %s available", sample.Level, pressure.FormatBytes(sample.AvailableBytes))
	if !healthySince.IsZero() {
		healthy := time.Since(healthySince).Truncate(time.Millisecond)
		reason += fmt.Sprintf("; healthy %s of the %s recovery window", healthy, gate.options.RecoveryWindow)
	}
	if probeErr != nil {
		reason = probeErr.Error()
	}
	_, err := fmt.Fprintf(out, "baton: WARNING: max_wait %s reached; bypassing memory gate (%s)\n", gate.options.MaxWait, reason)
	return err
}
