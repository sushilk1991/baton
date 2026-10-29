package pressure

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSnapshotValidation(t *testing.T) {
	isolateHome(t)
	for _, sample := range []Snapshot{
		{}, {Level: "unknown", TotalBytes: 1},
		{Level: Normal, TotalBytes: 1, AvailableBytes: 2},
		{Level: Normal, TotalBytes: 1, SwapUsedBytes: 1},
		{Level: Normal, TotalBytes: 1, SomeAvg10: math.NaN()},
		{Level: Normal, TotalBytes: 1, SomeAvg10: 101},
	} {
		if err := sample.Validate(); err == nil {
			t.Fatalf("accepted invalid sample: %+v", sample)
		}
	}
}

func TestNativeSample(t *testing.T) {
	isolateHome(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sample, err := System().Sample(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := sample.Validate(); err != nil {
		t.Fatal(err)
	}
	t.Logf("native memory: %+v", sample)
}

func TestNativeCancellation(t *testing.T) {
	isolateHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := System().Sample(ctx); err != context.Canceled {
		t.Fatalf("canceled sample = %v", err)
	}
}

func TestPressureThresholds(t *testing.T) {
	isolateHome(t)
	for _, test := range []struct {
		available uint64
		psi       float64
		want      Level
	}{
		{500, 0, Normal}, {100, 0, Normal}, {99, 0, Warn},
		{50, 0, Warn}, {49, 0, Critical}, {500, 9.99, Normal},
		{500, 10, Warn}, {500, 25, Critical},
	} {
		if got := memoryLevel(1000, test.available, test.psi); got != test.want {
			t.Fatalf("available %d, PSI %g: %s, want %s", test.available, test.psi, got, test.want)
		}
	}
}

func isolateHome(t *testing.T) {
	t.Helper()
	keys := map[string]struct{}{
		"HOME": {}, "XDG_CONFIG_HOME": {}, "XDG_CACHE_HOME": {},
		"XDG_DATA_HOME": {}, "XDG_STATE_HOME": {}, "XDG_RUNTIME_DIR": {},
		"XDG_CONFIG_DIRS": {}, "XDG_DATA_DIRS": {},
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = struct{}{}
		}
	}
	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
}

// Every human-facing memory figure uses one binary-unit format.
func TestFormatBytes(t *testing.T) {
	for _, test := range []struct {
		value uint64
		want  string
	}{
		{0, "0 B"}, {512, "512 B"}, {1536, "1.5 KiB"}, {1153434, "1.1 MiB"},
		{10498293760, "9.8 GiB"}, {36 << 30, "36.0 GiB"}, {3 << 40, "3.0 TiB"},
	} {
		if got := FormatBytes(test.value); got != test.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", test.value, got, test.want)
		}
	}
}
