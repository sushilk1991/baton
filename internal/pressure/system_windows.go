package pressure

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"
)

var globalMemoryStatus = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// memoryStatus matches the Win32 MEMORYSTATUSEX ABI, including unused fields.
type memoryStatus struct {
	length               uint32
	load                 uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

type windowsProvider struct{}

// System returns the native memory provider without sampling it.
func System() Provider {
	return windowsProvider{}
}

// Sample reads physical memory; commit allowance is not swap usage.
func (windowsProvider) Sample(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	var status memoryStatus
	status.length = uint32(unsafe.Sizeof(status))
	result, _, err := globalMemoryStatus.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return Snapshot{}, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	// PageFile fields describe commit limits, not swap use; leave swap unknown.
	sample := Snapshot{TotalBytes: status.totalPhys, AvailableBytes: status.availPhys}
	sample.Level = memoryLevel(sample.TotalBytes, sample.AvailableBytes, 0)
	return sample, sample.Validate()
}
