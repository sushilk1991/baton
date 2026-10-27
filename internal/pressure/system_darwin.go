package pressure

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const probeTimeout = 2 * time.Second

type darwinProvider struct{}

// System returns the native memory provider without sampling it.
func System() Provider {
	return darwinProvider{}
}

// Sample reads kernel pressure and free/inactive pages without cgo.
func (darwinProvider) Sample(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	sysctl, err := macCommand(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize", "kern.memorystatus_vm_pressure_level", "vm.swapusage")
	if err != nil {
		return Snapshot{}, err
	}
	stats, err := macCommand(ctx, "/usr/bin/vm_stat")
	if err != nil {
		return Snapshot{}, err
	}
	return parseMac(sysctl, stats)
}

func macCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	// Fixed system paths and the C locale keep executable and text formats stable.
	command := exec.CommandContext(ctx, name, args...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	data, err := command.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("memory probe %s: %w", name, err)
	}
	return data, nil
}
