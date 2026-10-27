package runner

import (
	"os"
	"syscall"
)

const maxRSSKiB = 1024

func rootPeak(state *os.ProcessState) uint64 {
	if state == nil {
		return 0
	}
	if usage, ok := state.SysUsage().(*syscall.Rusage); ok && usage.Maxrss > 0 {
		return uint64(usage.Maxrss) * maxRSSKiB
	}
	return 0
}
