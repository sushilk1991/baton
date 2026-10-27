package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseBytes reads positive whole-byte quantities with binary K, M, G units.
func ParseBytes(text string) (uint64, error) {
	value := strings.ToUpper(strings.TrimSpace(text))
	multiplier := uint64(1)
	for _, unit := range []struct {
		suffix string
		bytes  uint64
	}{
		{"GIB", gib}, {"GB", gib}, {"G", gib},
		{"MIB", 1 << 20}, {"MB", 1 << 20}, {"M", 1 << 20},
		{"KIB", 1 << 10}, {"KB", 1 << 10}, {"K", 1 << 10}, {"B", 1},
	} {
		if strings.HasSuffix(value, unit.suffix) {
			multiplier, value = unit.bytes, strings.TrimSuffix(value, unit.suffix)
			break
		}
	}
	amount, err := strconv.ParseFloat(value, 64)
	bytes := amount * float64(multiplier)
	if err != nil || math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes <= 0 || bytes >= float64(math.MaxUint64) || math.Trunc(bytes) != bytes {
		return 0, fmt.Errorf("invalid memory budget %q", text)
	}
	return uint64(bytes), nil
}
