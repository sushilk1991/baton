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
)

// Options controls a memory wait; zero fields select defaults.
type Options struct {
	// MaxWait bounds memory delays, never slot or queue contention.
	MaxWait time.Duration
	// PollInterval controls how often pressure and slots are retried.
	PollInterval time.Duration
	// RecoveryWindow requires uninterrupted healthy observations after blocking.
	RecoveryWindow time.Duration
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

// New prepares a gate without reading or changing machine state.
func New(provider pressure.Provider, options Options) (*Gate, error) {
	if provider == nil || options.MaxWait < 0 || options.PollInterval < 0 || options.RecoveryWindow < 0 {
		return nil, errors.New("gate needs a provider and nonnegative durations")
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
func (gate *Gate) Acquire(ctx context.Context, ticket *queue.Ticket, memory uint64, warnings io.Writer) (lease *lane.Lease, err error) {
	defer func() { err = errors.Join(err, ticket.Close()) }()
	if warnings == nil {
		warnings = os.Stderr
	}
	deadline := time.Now().Add(gate.options.MaxWait)
	var state waitState
	var sample pressure.Snapshot
	var sampleErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// Never retain slots while probing. Expiry bypasses only memory policy.
		// sample -> healthy/timeout -> ticket.TryAcquire -> lease -> child
		expired := !time.Now().Before(deadline)
		if !expired {
			sample, sampleErr = gate.probe(ctx, deadline)
		}
		ready := state.ready(sample, sampleErr, memory, gate.options.MarginBytes, gate.options.RecoveryWindow, time.Now())
		expired = !time.Now().Before(deadline)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !ready && !expired {
			if err := gate.pause(ctx, deadline); err != nil {
				return nil, err
			}
			continue
		}

		lease, err := ticket.TryAcquire()
		if errors.Is(err, queue.ErrBusy) {
			if err := gate.pause(ctx, deadline); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if !expired {
			return lease, nil
		}
		if err := gate.warn(warnings, sample, sampleErr); err != nil {
			return nil, errors.Join(err, lease.Release())
		}
		return lease, nil
	}
}

func (gate *Gate) probe(ctx context.Context, deadline time.Time) (pressure.Snapshot, error) {
	probeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	sample, err := gate.provider.Sample(probeCtx)
	if err != nil {
		return sample, err
	}
	return sample, sample.Validate()
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

func (gate *Gate) pause(ctx context.Context, deadline time.Time) error {
	delay := gate.options.PollInterval
	if remaining := time.Until(deadline); remaining > 0 && remaining < delay {
		delay = remaining
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

func (gate *Gate) warn(out io.Writer, sample pressure.Snapshot, probeErr error) error {
	reason := fmt.Sprintf("pressure %s, available %d bytes", sample.Level, sample.AvailableBytes)
	if probeErr != nil {
		reason = probeErr.Error()
	}
	_, err := fmt.Fprintf(out, "baton: WARNING: max_wait %s reached; bypassing memory gate (%s)\n", gate.options.MaxWait, reason)
	return err
}
