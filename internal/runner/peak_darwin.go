package runner

import (
	"os"
	"syscall"
)

func rootPeak(state *os.ProcessState) uint64 {
	if state == nil {
		return 0
	}
	if usage, ok := state.SysUsage().(*syscall.Rusage); ok && usage.Maxrss > 0 {
		return uint64(usage.Maxrss)
	}
	return 0
}
