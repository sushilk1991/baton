package process

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	statParent     = 1
	statStart      = 19
	auxClockTicks  = 17
	auxWordBytes   = 8 // Both supported Linux architectures use 64-bit ELF words.
	sysPidfdOpen   = 434
	sysPidfdSignal = 424
)

// List snapshots visible processes, excluding processes that vanish mid-read.
func List(ctx context.Context) ([]Info, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	rows := []Info{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
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
	root := filepath.Join("/proc", strconv.Itoa(pid))
	stat, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return Info{}, goneError(err)
	}
	row, ticks, err := parseStat(stat, pid)
	if err != nil {
		return Info{}, err
	}
	status, err := os.ReadFile(filepath.Join(root, "status"))
	if err != nil {
		return Info{}, goneError(err)
	}
	row.UID, err = parseUID(status)
	if err != nil {
		return Info{}, err
	}
	hz, uptime, err := clockData()
	if err != nil {
		return Info{}, err
	}
	age := uptime - float64(ticks)/float64(hz)
	if age < 0 {
		age = 0
	}
	row.Started = time.Now().Add(-time.Duration(age * float64(time.Second)))
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return Info{}, err
	}
	token := row.Start
	row.Start = strings.TrimSpace(string(boot)) + ":" + token
	if row.UID != os.Geteuid() || row.Dead {
		return row, nil
	}
	args, err := os.ReadFile(filepath.Join(root, "cmdline"))
	if err != nil {
		return Info{}, goneError(err)
	}
	row.Args = parseCmdline(args)
	// Reading stat twice detects exit/reuse while inspecting argv and owner.
	after, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return Info{}, goneError(err)
	}
	check, _, err := parseStat(after, pid)
	if err != nil {
		return Info{}, err
	}
	if check.Start != token || check.Parent != row.Parent || check.Dead {
		return Info{}, ErrGone
	}
	return row, nil
}

func parseStat(data []byte, pid int) (Info, uint64, error) {
	end := bytes.LastIndexByte(data, ')')
	if end < 0 {
		return Info{}, 0, errors.New("invalid proc stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) <= statStart {
		return Info{}, 0, errors.New("incomplete proc stat")
	}
	parent, err := strconv.Atoi(fields[statParent])
	if err != nil || parent < 0 {
		return Info{}, 0, errors.New("invalid parent PID")
	}
	start, err := strconv.ParseUint(fields[statStart], 10, 64)
	if err != nil {
		return Info{}, 0, err
	}
	return Info{PID: pid, Parent: parent, Start: fields[statStart], Dead: fields[0] == "Z" || fields[0] == "X"}, start, nil
}

func parseUID(data []byte) (int, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "Uid:" {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil || uid < 0 {
			return 0, errors.New("invalid effective UID")
		}
		return uid, nil
	}
	return 0, errors.New("missing effective UID")
}

func parseCmdline(data []byte) []string {
	if len(data) == 0 || data[len(data)-1] != 0 {
		return nil
	}
	return strings.Split(string(data[:len(data)-1]), "\x00")
}

func clockData() (uint64, float64, error) {
	aux, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return 0, 0, err
	}
	var hz uint64
	for len(aux) >= 2*auxWordBytes {
		key := binary.NativeEndian.Uint64(aux)
		value := binary.NativeEndian.Uint64(aux[auxWordBytes:])
		aux = aux[2*auxWordBytes:]
		if key == auxClockTicks {
			hz = value
			break
		}
	}
	if hz == 0 {
		return 0, 0, errors.New("missing AT_CLKTCK")
	}
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, 0, errors.New("missing uptime")
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || uptime < 0 || math.IsNaN(uptime) || math.IsInf(uptime, 0) || uptime > float64(math.MaxInt64)/float64(time.Second) {
		return 0, 0, errors.New("invalid uptime")
	}
	return hz, uptime, nil
}

// Send binds a PID to a kernel handle before verifying and signalling it.
func Send(ctx context.Context, expected Info, signal Signal) error {
	sig, err := signalValue(signal)
	if err != nil {
		return err
	}
	fd, _, errno := syscall.Syscall(sysPidfdOpen, uintptr(expected.PID), 0, 0)
	if errno != 0 && errno != syscall.ENOSYS {
		return goneError(errno)
	}
	if errno == 0 {
		defer syscall.Close(int(fd))
	}
	if err := changed(ctx, expected); err != nil {
		return err
	}
	// Old kernels lack pidfds; retain identity revalidation as on macOS.
	if errno == syscall.ENOSYS {
		return goneError(syscall.Kill(expected.PID, sig))
	}
	_, _, errno = syscall.Syscall6(sysPidfdSignal, fd, uintptr(sig), 0, 0, 0, 0)
	if errno != 0 {
		return goneError(errno)
	}
	return nil
}
