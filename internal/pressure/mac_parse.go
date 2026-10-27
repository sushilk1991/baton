package pressure

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	macNormal      = "1"
	macWarn        = "2"
	macCritical    = "4"
	macSysctlLines = 3
)

func parseMac(sysctl, stats []byte) (Snapshot, error) {
	lines := strings.Split(strings.TrimSpace(string(sysctl)), "\n")
	if len(lines) != macSysctlLines {
		return Snapshot{}, errors.New("incomplete macOS sysctl counters")
	}
	total, err := strconv.ParseUint(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return Snapshot{}, fmt.Errorf("hw.memsize: %w", err)
	}
	levels := map[string]Level{macNormal: Normal, macWarn: Warn, macCritical: Critical}
	level, ok := levels[strings.TrimSpace(lines[1])]
	if !ok {
		return Snapshot{}, errors.New("unknown macOS memory pressure level")
	}
	swap, used, err := parseSwap(lines[2])
	if err != nil {
		return Snapshot{}, err
	}
	available, err := parseVMStats(stats)
	if err != nil {
		return Snapshot{}, err
	}
	sample := Snapshot{Level: level, TotalBytes: total, AvailableBytes: available, SwapTotalBytes: swap, SwapUsedBytes: used}
	return sample, sample.Validate()
}

func parseSwap(line string) (uint64, uint64, error) {
	values := map[string]uint64{}
	fields := strings.Fields(line)
	for i := 0; i+2 < len(fields); i++ {
		if (fields[i] != "total" && fields[i] != "used") || fields[i+1] != "=" {
			continue
		}
		amount, err := swapBytes(fields[i+2])
		if err != nil {
			return 0, 0, err
		}
		values[fields[i]] = amount
	}
	if len(values) != 2 || values["used"] > values["total"] {
		return 0, 0, errors.New("invalid vm.swapusage counters")
	}
	return values["total"], values["used"], nil
}

func swapBytes(text string) (uint64, error) {
	if len(text) < 2 {
		return 0, errors.New("invalid swap size")
	}
	multipliers := map[byte]float64{'B': 1, 'K': 1 << 10, 'M': 1 << 20, 'G': 1 << 30, 'T': 1 << 40}
	multiplier, ok := multipliers[text[len(text)-1]]
	number, err := strconv.ParseFloat(text[:len(text)-1], 64)
	bytes := number * multiplier
	if !ok || err != nil || math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes < 0 || bytes >= float64(math.MaxUint64) {
		return 0, errors.New("invalid swap size")
	}
	return uint64(bytes), nil
}

func parseVMStats(data []byte) (uint64, error) {
	lines := strings.Split(string(data), "\n")
	_, header, ok := strings.Cut(lines[0], "page size of ")
	fields := strings.Fields(header)
	if !ok || len(fields) < 2 || fields[1] != "bytes)" {
		return 0, errors.New("missing vm_stat page size")
	}
	pageSize, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || pageSize == 0 {
		return 0, errors.New("invalid vm_stat page size")
	}
	counts := map[string]uint64{}
	for _, line := range lines[1:] {
		key, value, _ := strings.Cut(line, ":")
		if key != "Pages free" && key != "Pages inactive" {
			continue
		}
		number, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
		if err != nil || number > math.MaxUint64/pageSize {
			return 0, errors.New("invalid vm_stat page count")
		}
		counts[key] = number * pageSize
	}
	free, inactive := counts["Pages free"], counts["Pages inactive"]
	if len(counts) != 2 || free > math.MaxUint64-inactive {
		return 0, errors.New("incomplete or overflowing vm_stat counters")
	}
	return free + inactive, nil
}
