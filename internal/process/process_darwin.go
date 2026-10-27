package process

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	procInExit     = 4
	procPIDInfo    = 2
	procPIDBSDInfo = 3
	procZombie     = 5
	ctlKern        = 1
	kernProcArgs2  = 49
	maxArgBytes    = 8 << 20
	maxComm        = 16
)

// bsdInfo follows XNU proc_bsdinfo's fixed-width ABI on amd64 and arm64.
type bsdInfo struct {
	flags, status, exit, pid, parent, uid, gid, ruid, rgid, svuid, svgid, reserved uint32
	comm                                                                           [maxComm]byte
	name                                                                           [2 * maxComm]byte
	files, group, job, device, ttyGroup                                            uint32
	nice                                                                           int32
	seconds, micros                                                                uint64
}

// List enumerates PIDs; kernel calls supply owner, birth, and exact argv.
func List(ctx context.Context) ([]Info, error) {
	command := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=")
	command.Env = append(os.Environ(), "LC_ALL=C")
	data, err := command.Output()
	if err != nil {
		return nil, err
	}
	rows := []Info{}
	for _, field := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("invalid ps PID %q", field)
		}
		row, err := Read(ctx, pid)
		if errors.Is(err, ErrGone) || errors.Is(err, os.ErrPermission) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("process %d: %w", pid, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func read(_ context.Context, pid int) (Info, error) {
	info, err := readBSD(pid)
	if err != nil {
		return Info{}, err
	}
	row := Info{PID: pid, Parent: int(info.parent), UID: int(info.uid), Start: fmt.Sprintf("%d:%d", info.seconds, info.micros), Started: time.Unix(int64(info.seconds), int64(info.micros)*int64(time.Microsecond)), Dead: info.status == procZombie || info.flags&procInExit != 0}
	if row.UID != os.Geteuid() || row.Dead {
		return row, nil
	}
	row.Args, err = readArgs(pid)
	if err != nil {
		// XNU procargs returns EINVAL when the process or its task disappears.
		// Confirm exit through kernel metadata; unreadability alone is not death.
		after, inspectErr := readBSD(pid)
		if errors.Is(inspectErr, ErrGone) || (inspectErr == nil && (after.status == procZombie || after.flags&procInExit != 0)) {
			return Info{}, ErrGone
		}
		return Info{}, err
	}
	after, err := readBSD(pid)
	if err != nil {
		return Info{}, err
	}
	if info.seconds != after.seconds || info.micros != after.micros || info.parent != after.parent || info.uid != after.uid || (after.status == procZombie || after.flags&procInExit != 0) {
		return Info{}, ErrGone
	}
	return row, nil
}

func readBSD(pid int) (bsdInfo, error) {
	var info bsdInfo
	size := unsafe.Sizeof(info)
	count, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, procPIDInfo, uintptr(pid), procPIDBSDInfo, 0, uintptr(unsafe.Pointer(&info)), size)
	if errno != 0 {
		return bsdInfo{}, fmt.Errorf("proc_pidinfo %d: %w", pid, goneError(errno))
	}
	if count != size || info.pid != uint32(pid) {
		return bsdInfo{}, ErrGone
	}
	return info, nil
}

func readArgs(pid int) ([]string, error) {
	limit, err := syscall.SysctlUint32("kern.argmax")
	if err != nil {
		return nil, err
	}
	if limit == 0 || limit > maxArgBytes {
		return nil, errors.New("invalid kern.argmax")
	}
	data := make([]byte, limit)
	size := uintptr(len(data))
	mib := [3]int32{ctlKern, kernProcArgs2, int32(pid)}
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	if errno != 0 {
		return nil, fmt.Errorf("procargs %d: %w", pid, goneError(errno))
	}
	if size > uintptr(len(data)) {
		return nil, errors.New("invalid procargs size")
	}
	return parseArgs(data[:size])
}

func parseArgs(data []byte) ([]string, error) {
	const argcBytes = 4
	if len(data) < argcBytes {
		return nil, errors.New("missing process argc")
	}
	count := binary.NativeEndian.Uint32(data)
	data = data[argcBytes:]
	end := bytes.IndexByte(data, 0)
	if count == 0 || count > uint32(len(data)) || end < 0 {
		return nil, errors.New("invalid process argv")
	}
	// KERN_PROCARGS2: argc | executable NUL | padding | argv | environment.
	// Read only argc arguments; environment bytes never leave this driver.
	data = bytes.TrimLeft(data[end+1:], "\x00")
	args := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		end := bytes.IndexByte(data, 0)
		if end < 0 {
			return nil, errors.New("truncated process argv")
		}
		args = append(args, string(data[:end]))
		data = data[end+1:]
	}
	return args, nil
}

// Send rechecks birth and argv immediately before signalling a single PID.
func Send(ctx context.Context, expected Info, signal Signal) error {
	sig, err := signalValue(signal)
	if err != nil {
		return err
	}
	if err := changed(ctx, expected); err != nil {
		return err
	}
	return goneError(syscall.Kill(expected.PID, sig))
}
