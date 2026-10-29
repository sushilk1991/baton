package pressure

import (
	"context"
	"encoding/binary"
	"errors"
	"syscall"
)

const memsizeBytes = 8

// TotalBytes reads physical memory with one sysctl call. Configuration
// loads on every baton start, shims included, and a full Sample would
// spawn sysctl and vm_stat for figures that defaults never use.
func TotalBytes(context.Context) (uint64, error) {
	raw, err := syscall.Sysctl("hw.memsize")
	if err != nil {
		return 0, err
	}

	// syscall.Sysctl drops one trailing NUL, which can be the top byte of
	// the little-endian value: 36 GiB is 00 00 00 00 09 00 00 00.
	data := []byte(raw)
	if len(data) == memsizeBytes-1 {
		data = append(data, 0)
	}
	if len(data) != memsizeBytes {
		return 0, errors.New("unexpected hw.memsize size")
	}
	total := binary.LittleEndian.Uint64(data)
	if total == 0 {
		return 0, errors.New("hw.memsize is zero")
	}
	return total, nil
}
