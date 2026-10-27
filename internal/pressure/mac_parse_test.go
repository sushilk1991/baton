package pressure

import (
	"strings"
	"testing"
)

const macStatsFixture = "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 100.\nPages inactive: 200.\nPages active: 500.\n"
const macSysctlFixture = "38654705664\n1\ntotal = 4096.00M used = 1536.50M free = 2559.50M (encrypted)\n"

func TestMacCounters(t *testing.T) {
	isolateHome(t)
	for _, test := range []struct {
		raw  string
		want Level
	}{
		{"1", Normal}, {"2", Warn}, {"4", Critical},
	} {
		sysctl := strings.Replace(macSysctlFixture, "\n1\n", "\n"+test.raw+"\n", 1)
		sample, err := parseMac([]byte(sysctl), []byte(macStatsFixture))
		if err != nil {
			t.Fatal(err)
		}
		if sample.Level != test.want || sample.TotalBytes != 36<<30 || sample.AvailableBytes != 300*16384 || sample.SwapTotalBytes != 4<<30 || sample.SwapUsedBytes != 1536<<20+1<<19 {
			t.Fatalf("incorrect macOS counters: %+v", sample)
		}
	}
}

func TestMacInvalidCounters(t *testing.T) {
	isolateHome(t)
	for _, sysctl := range []string{
		"", strings.Replace(macSysctlFixture, "\n1\n", "\n0\n", 1),
		strings.Replace(macSysctlFixture, "1536.50M", "NaNM", 1),
		strings.Replace(macSysctlFixture, "1536.50M", "9999M", 1),
	} {
		if _, err := parseMac([]byte(sysctl), []byte(macStatsFixture)); err == nil {
			t.Fatalf("accepted invalid sysctl: %q", sysctl)
		}
	}
	for _, stats := range []string{"", "page size of 0 bytes\n", strings.Replace(macStatsFixture, "200.", "-1.", 1), strings.Replace(macStatsFixture, "200.", "18446744073709551615.", 1)} {
		if _, err := parseMac([]byte(macSysctlFixture), []byte(stats)); err == nil {
			t.Fatalf("accepted invalid vm_stat: %q", stats)
		}
	}
}
