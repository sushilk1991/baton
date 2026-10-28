package runner

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	ctlKern        = 1
	kernProc       = 14
	kernProcPID    = 1
	kinfoProcBytes = 648
	// XNU extern_proc's p_sigcatch on amd64 and arm64, after p_sigmask and
	// p_sigignore. TestCatchesSignal checks the offset against this process.
	sigCatchOffset = 236
)

// catchesSignal reports whether pid has a handler installed for sig, read
// from the kernel's kinfo_proc: bit n-1 of p_sigcatch is set for signal n.
func catchesSignal(pid int, sig syscall.Signal) (bool, error) {
	mib := [4]int32{ctlKern, kernProc, kernProcPID, int32(pid)}
	var info [kinfoProcBytes]byte
	size := uintptr(len(info))
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&info[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	if errno != 0 {
		return false, fmt.Errorf("kinfo_proc %d: %w", pid, errno)
	}
	// A vanished PID returns success with no data.
	if size < sigCatchOffset+4 {
		return false, fmt.Errorf("kinfo_proc %d: %w", pid, syscall.ESRCH)
	}
	mask := binary.LittleEndian.Uint32(info[sigCatchOffset:])
	return mask&(1<<(uint(sig)-1)) != 0, nil
}
