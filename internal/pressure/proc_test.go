package pressure

import (
	"context"
	"fmt"
	"os"
	"testing"
)

const meminfoFixture = "MemTotal: 8388608 kB\nMemAvailable: 6291456 kB\nSwapTotal: 2097152 kB\nSwapFree: 1572864 kB\nHugePages_Total: 0\n"

func TestLinuxProvider(t *testing.T) {
	isolateHome(t)
	for _, test := range []struct {
		name      string
		files     map[string]string
		total     uint64
		available uint64
		level     Level
	}{
		{"host", map[string]string{}, 8 << 30, 6 << 30, Normal},
		{"PSI warning", map[string]string{"/proc/pressure/memory": "some avg10=10.00 avg60=0 total=0\n"}, 8 << 30, 6 << 30, Warn},
		{"container", map[string]string{"/cg/team/job/memory.max": "2147483648", "/cg/team/job/memory.current": "1073741824"}, 2 << 30, 1 << 30, Normal},
		{"parent limit", map[string]string{"/cg/team/memory.max": "1073741824", "/cg/team/memory.current": "1006632960"}, 1 << 30, 64 << 20, Warn},
		{"over limit", map[string]string{"/cg/team/job/memory.max": "1024", "/cg/team/job/memory.current": "2048"}, 1024, 0, Critical},
		{"local PSI", map[string]string{"/cg/team/job/memory.pressure": "some avg10=30 avg60=0 total=0\n"}, 8 << 30, 6 << 30, Critical},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := linuxFiles()
			for name, value := range test.files {
				files[name] = value
			}
			sample, err := (linuxProvider{read: fixtureRead(files)}).Sample(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if sample.TotalBytes != test.total || sample.AvailableBytes != test.available || sample.Level != test.level || sample.SwapUsedBytes != 512<<20 {
				t.Fatalf("sample = %+v; want total %d, available %d, %s", sample, test.total, test.available, test.level)
			}
		})
	}
}

func TestLinuxOptionalFiles(t *testing.T) {
	isolateHome(t)
	files := linuxFiles()
	delete(files, "/proc/pressure/memory")
	files["/proc/self/cgroup"] = "2:memory:/old\n"
	delete(files, "/proc/self/mountinfo")
	if _, err := (linuxProvider{read: fixtureRead(files)}).Sample(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxInvalidSources(t *testing.T) {
	isolateHome(t)
	for _, test := range []struct{ path, value string }{
		{"/proc/meminfo", "MemTotal: 1 kB\n"},
		{"/proc/meminfo", "MemTotal: 18446744073709551615 kB\n"},
		{"/proc/pressure/memory", "some avg10=NaN\n"},
		{"/proc/pressure/memory", "some avg10=-1\n"},
		{"/proc/pressure/memory", "full avg10=0\n"},
		{"/cg/team/job/memory.max", "broken"},
		{"/cg/team/job/memory.current", "-1"},
		{"/proc/self/mountinfo", "malformed"},
		{"/proc/self/cgroup", "0::relative\n"},
	} {
		t.Run(test.path+test.value, func(t *testing.T) {
			files := linuxFiles()
			files[test.path] = test.value
			if sample, err := (linuxProvider{read: fixtureRead(files)}).Sample(context.Background()); err == nil {
				t.Fatalf("accepted invalid %s: %+v", test.path, sample)
			}
		})
	}
	files := linuxFiles()
	delete(files, "/cg/team/job/memory.current")
	if _, err := (linuxProvider{read: fixtureRead(files)}).Sample(context.Background()); err == nil {
		t.Fatal("missing current counter hidden")
	}
}

func TestCgroupMountRoots(t *testing.T) {
	isolateHome(t)
	files := linuxFiles()
	files["/proc/self/cgroup"] = "0::/host/team/job\n"
	files["/proc/self/mountinfo"] = "20 19 0:20 /host/team /cg\\040space rw - cgroup2 cgroup rw\n"
	files["/cg space/job/memory.max"] = "1073741824"
	files["/cg space/job/memory.current"] = "536870912"
	files["/cg space/memory.max"] = "max"
	files["/cg space/memory.current"] = "536870912"
	sample, err := (linuxProvider{read: fixtureRead(files)}).Sample(context.Background())
	if err != nil || sample.AvailableBytes != 512<<20 {
		t.Fatalf("rooted mount sample = %+v, %v", sample, err)
	}
}

func linuxFiles() map[string]string {
	return map[string]string{
		"/proc/meminfo":               meminfoFixture,
		"/proc/pressure/memory":       "some avg10=0.00 avg60=0 total=0\nfull avg10=0.00\n",
		"/proc/self/cgroup":           "0::/team/job\n",
		"/proc/self/mountinfo":        "20 19 0:20 / /cg rw - cgroup2 cgroup rw\n",
		"/cg/team/job/memory.max":     "max",
		"/cg/team/job/memory.current": "0",
		"/cg/team/memory.max":         "max",
		"/cg/team/memory.current":     "0",
	}
}

func fixtureRead(files map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		value, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("%s: %w", name, os.ErrNotExist)
		}
		return []byte(value), nil
	}
}
