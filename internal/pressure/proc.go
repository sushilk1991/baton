package pressure

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"strconv"
	"strings"
)

const kibibyte = 1024

type linuxProvider struct {
	read func(string) ([]byte, error)
}

// Sample combines host counters with every visible cgroup memory limit.
func (provider linuxProvider) Sample(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	data, err := provider.read("/proc/meminfo")
	if err != nil {
		return Snapshot{}, err
	}
	sample, err := parseMeminfo(data)
	if err != nil {
		return Snapshot{}, err
	}
	if err := provider.addPSI("/proc/pressure/memory", &sample); err != nil {
		return Snapshot{}, err
	}
	if err := provider.addCgroup(ctx, &sample); err != nil {
		return Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	sample.Level = memoryLevel(sample.TotalBytes, sample.AvailableBytes, sample.SomeAvg10)
	return sample, sample.Validate()
}

func parseMeminfo(data []byte) (Snapshot, error) {
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, _ := strings.Cut(line, ":")
		if key != "MemTotal" && key != "MemAvailable" && key != "SwapTotal" && key != "SwapFree" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) != 2 || fields[1] != "kB" {
			return Snapshot{}, fmt.Errorf("invalid meminfo %s", key)
		}
		number, err := strconv.ParseUint(fields[0], 10, 64)
		if _, duplicate := values[key]; err != nil || duplicate || number > math.MaxUint64/kibibyte {
			return Snapshot{}, fmt.Errorf("invalid meminfo %s counter", key)
		}
		values[key] = number * kibibyte
	}
	if len(values) != 4 || values["MemTotal"] == 0 || values["MemAvailable"] > values["MemTotal"] || values["SwapFree"] > values["SwapTotal"] {
		return Snapshot{}, errors.New("incomplete or inconsistent meminfo")
	}
	return Snapshot{TotalBytes: values["MemTotal"], AvailableBytes: values["MemAvailable"], SwapTotalBytes: values["SwapTotal"], SwapUsedBytes: values["SwapTotal"] - values["SwapFree"]}, nil
}

func parsePSI(data []byte) (float64, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}
		for _, field := range fields[1:] {
			key, value, _ := strings.Cut(field, "=")
			if key != "avg10" {
				continue
			}
			number, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > percentScale {
				return 0, errors.New("invalid PSI some avg10")
			}
			return number, nil
		}
	}
	return 0, errors.New("missing PSI some avg10")
}

func (provider linuxProvider) addPSI(name string, sample *Snapshot) error {
	data, err := provider.read(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil // Older kernels may omit PSI; counters still gate memory.
	}
	if err != nil {
		return err
	}
	value, err := parsePSI(data)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	sample.SomeAvg10 = math.Max(sample.SomeAvg10, value)
	return nil
}

func (provider linuxProvider) addCgroup(ctx context.Context, sample *Snapshot) error {
	data, err := provider.read("/proc/self/cgroup")
	if err != nil {
		return err
	}
	group, err := unifiedGroup(data)
	if err != nil || group == "" {
		return err
	}
	data, err = provider.read("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	dir, root, err := cgroupMount(data, group)
	if err != nil {
		return err
	}
	// A parent's limit includes siblings: cap by every visible ancestor.
	// host -> mount root -> parent -> leaf; choose minimum free headroom.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := provider.capMemory(dir, sample); err != nil {
			return err
		}
		if err := provider.addPSI(path.Join(dir, "memory.pressure"), sample); err != nil {
			return err
		}
		if dir == root {
			return nil
		}
		dir = path.Dir(dir)
	}
}

func unifiedGroup(data []byte) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		group, found := strings.CutPrefix(line, "0::")
		if !found {
			continue
		}
		if !path.IsAbs(group) || path.Clean(group) != group {
			return "", errors.New("invalid cgroup v2 membership")
		}
		return group, nil
	}
	return "", nil // A cgroup v1-only host has no unified memory limit.
}

func cgroupMount(data []byte, group string) (string, string, error) {
	bestRoot, bestMount := "", ""
	for _, line := range strings.Split(string(data), "\n") {
		before, after, ok := strings.Cut(line, " - ")
		if !ok || !strings.HasPrefix(after, "cgroup2 ") {
			continue
		}
		fields := strings.Fields(before)
		if len(fields) < 6 {
			return "", "", errors.New("invalid cgroup mountinfo")
		}
		root, err := mountPath(fields[3])
		if err != nil {
			return "", "", err
		}
		mount, err := mountPath(fields[4])
		if err != nil {
			return "", "", err
		}
		if group != root && !strings.HasPrefix(group, strings.TrimSuffix(root, "/")+"/") {
			continue
		}
		if len(root) > len(bestRoot) {
			bestRoot, bestMount = root, mount
		}
	}
	if bestRoot == "" {
		return "", "", errors.New("cgroup v2 mount is not visible")
	}
	relative := strings.TrimPrefix(group, bestRoot)
	return path.Join(bestMount, relative), bestMount, nil
}

func mountPath(field string) (string, error) {
	value, err := strconv.Unquote(`"` + field + `"`)
	if err != nil || !path.IsAbs(value) || path.Clean(value) != value {
		return "", errors.New("invalid cgroup mount path")
	}
	return value, nil
}

func (provider linuxProvider) capMemory(dir string, sample *Snapshot) error {
	data, err := provider.read(path.Join(dir, "memory.max"))
	if errors.Is(err, os.ErrNotExist) {
		return nil // The root or a disabled memory controller has no limit.
	}
	if err != nil {
		return err
	}
	limit := strings.TrimSpace(string(data))
	data, err = provider.read(path.Join(dir, "memory.current"))
	if err != nil {
		return err
	}
	current, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return fmt.Errorf("%s/memory.current: %w", dir, err)
	}
	if limit == "max" {
		return nil
	}
	maximum, err := strconv.ParseUint(limit, 10, 64)
	if err != nil {
		return fmt.Errorf("%s/memory.max: %w", dir, err)
	}
	headroom := uint64(0)
	if current < maximum {
		headroom = maximum - current
	}
	sample.TotalBytes = min(sample.TotalBytes, maximum)
	sample.AvailableBytes = min(sample.AvailableBytes, headroom)
	return nil
}
