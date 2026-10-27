// Package pressure exposes memory observations without platform mechanics.
package pressure

import (
	"context"
	"errors"
	"math"
)

const (
	warnPSI                  = 10.0
	criticalPSI              = 25.0
	warnAvailablePercent     = 10.0
	criticalAvailablePercent = 5.0
	percentScale             = 100.0
)

// Level is the machine's current memory pressure classification.
type Level string

const (
	// Normal permits admission when the job's memory budget fits.
	Normal Level = "normal"
	// Warn delays heavy work until recovery or max_wait.
	Warn Level = "warn"
	// Critical indicates severe pressure and delays heavy work.
	Critical Level = "critical"
)

// Snapshot contains byte counts and pressure from one observation.
type Snapshot struct {
	// Level is the worst pressure signal available to the provider.
	Level Level
	// TotalBytes is physical RAM, capped by visible container limits.
	TotalBytes uint64
	// AvailableBytes is reusable RAM, capped by container headroom.
	AvailableBytes uint64
	// SwapTotalBytes is host swap capacity; zero when unavailable.
	SwapTotalBytes uint64
	// SwapUsedBytes is host swap use; zero when unavailable.
	SwapUsedBytes uint64
	// SomeAvg10 is Linux's ten-second partial memory stall percentage.
	SomeAvg10 float64
}

// Provider reads memory state; implementations must respect cancellation.
type Provider interface {
	Sample(context.Context) (Snapshot, error)
}

// Validate rejects incomplete or inconsistent observations before admission.
func (snapshot Snapshot) Validate() error {
	if snapshot.Level != Normal && snapshot.Level != Warn && snapshot.Level != Critical {
		return errors.New("unknown memory pressure level")
	}
	if snapshot.TotalBytes == 0 || snapshot.AvailableBytes > snapshot.TotalBytes || snapshot.SwapUsedBytes > snapshot.SwapTotalBytes {
		return errors.New("invalid memory counters")
	}
	if math.IsNaN(snapshot.SomeAvg10) || math.IsInf(snapshot.SomeAvg10, 0) || snapshot.SomeAvg10 < 0 || snapshot.SomeAvg10 > percentScale {
		return errors.New("invalid memory PSI")
	}
	return nil
}

func memoryLevel(total, available uint64, psi float64) Level {
	// Floating-point ratios avoid overflowing large byte counters.
	percent := float64(available) / float64(total) * percentScale
	if psi >= criticalPSI || percent < criticalAvailablePercent {
		return Critical
	}
	if psi >= warnPSI || percent < warnAvailablePercent {
		return Warn
	}
	return Normal
}
