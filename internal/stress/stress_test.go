package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
)

// The boundaries come from SPEC.md; equal swap growth fails, equal time passes.
func TestPassBoundaries(t *testing.T) {
	good := phase{Mode: "queued", Samples: 2, Completed: 4, MaxActive: 1, Worst: pressure.Warn, WallSeconds: 12}
	for _, test := range []struct {
		name string
		edit func(*phase)
		want string
	}{
		{"pass", func(*phase) {}, ""},
		{"time", func(p *phase) { p.WallSeconds = 12.001 }, "wall"},
		{"swap", func(p *phase) { p.SwapGrowthBytes = 1 << 30 }, "swap"},
		{"critical", func(p *phase) { p.Worst = pressure.Critical }, "critical"},
		{"missing", func(p *phase) { p.Completed-- }, "completed"},
		{"kill", func(p *phase) { p.Killed++ }, "kill"},
		{"event", func(p *phase) { p.KillEvents++ }, "event"},
		{"probe", func(p *phase) { p.Error = "probe failed" }, "probe failed"},
		{"no samples", func(p *phase) { p.Samples = 0 }, "sample"},
		{"overlap", func(p *phase) { p.MaxActive++ }, "overlap"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := good
			test.edit(&p)
			got := strings.Join(evaluate(p, 10, 4), "; ")
			if test.want == "" && got != "" || test.want != "" && !strings.Contains(got, test.want) {
				t.Fatalf("want %q, got %q", test.want, got)
			}
		})
	}
}

// Unknown or incomplete event output must never become a zero-event success.
func TestEventParsing(t *testing.T) {
	for _, test := range []struct {
		data string
		want uint64
	}{
		{"{\"count\":0,\"finished\":1}\n", 0},
		{"{\"eventMessage\":\"memorystatus kill\"}\n{\"count\":1,\"finished\":1}\n", 1},
	} {
		got, err := readLog([]byte(test.data))
		if err != nil || got != test.want {
			t.Fatalf("readLog: %d %v", got, err)
		}
	}
	for _, data := range []string{"", "not JSON", "{}\n", "{\"count\":1,\"finished\":1}\n", "{\"count\":0,\"finished\":0}\n"} {
		if _, err := readLog([]byte(data)); err == nil {
			t.Fatalf("accepted incomplete log %q", data)
		}
	}
	got, err := readCounter([]byte("pgfault 90\noom_kill 7\n"))
	if err != nil || got != 7 {
		t.Fatalf("counter %d %v", got, err)
	}
	for _, data := range []string{"", "oom_kill nope", "oom_kill -1", "oom_kill 1\noom_kill 2"} {
		if _, err := readCounter([]byte(data)); err == nil {
			t.Fatalf("accepted counter %q", data)
		}
	}
}

// Retain the high-water mark even if swap falls before the phase finishes.
func TestSampleHighWater(t *testing.T) {
	p := phase{}
	for _, sample := range []pressure.Snapshot{
		{TotalBytes: 16 << 30, AvailableBytes: 8 << 30, SwapTotalBytes: 4 << 30, SwapUsedBytes: 2 << 30, Level: pressure.Normal},
		{TotalBytes: 16 << 30, AvailableBytes: 8 << 30, SwapTotalBytes: 4 << 30, SwapUsedBytes: 3 << 30, Level: pressure.Warn},
		{TotalBytes: 16 << 30, AvailableBytes: 8 << 30, SwapTotalBytes: 4 << 30, SwapUsedBytes: 1 << 30, Level: pressure.Normal},
	} {
		_ = sampleRecord(&p, sample)
	}
	if p.BaseSwapBytes != 2<<30 || p.PeakSwapBytes != 3<<30 || p.SwapGrowthBytes != 1<<30 || p.Worst != pressure.Warn || p.Samples != 3 {
		t.Fatalf("lost observation: %+v", p)
	}
	if err := sampleRecord(&p, pressure.Snapshot{}); err == nil {
		t.Fatal("accepted invalid pressure")
	}
}

// Reject unsafe budgets before allocating and malformed CLI values before work.
func TestOptionsAndBudget(t *testing.T) {
	opts, err := readOptions([]string{"--jobs", "3", "--gib", "0.125", "--hold", "1s", "--max-wait", "2s", "--baton", "/tmp/baton"})
	if err != nil || opts.bytes != 128<<20 || opts.jobs != 3 || opts.hold != time.Second {
		t.Fatalf("options %+v %v", opts, err)
	}
	for _, args := range [][]string{{"--jobs", "0"}, {"--gib", "NaN"}, {"--gib", "Inf"}, {"--gib", "-1"}, {"--gib", "1e30"}, {"--hold", "0s"}, {"--timeout", "-1s"}, {"--max-wait", "0s"}, {"extra"}} {
		if _, err := readOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	sample := pressure.Snapshot{TotalBytes: 16 << 30, AvailableBytes: 8 << 30, Level: pressure.Warn}
	if err := safeBudget(opts, sample); err != nil {
		t.Fatal(err)
	}
	opts.bytes = 2 << 30
	if err := safeBudget(opts, sample); err == nil {
		t.Fatal("allowed simultaneous allocation above safety cap")
	}
	opts.bytes = 1 << 20
	sample.Level = pressure.Critical
	if err := safeBudget(opts, sample); err == nil {
		t.Fatal("allowed critical preflight")
	}
}

// Worker timestamps measure overlap independently of baton status or history.
func TestWorkerOverlap(t *testing.T) {
	start := time.Now()
	jobs := []jobRecord{{Started: start, Ended: start.Add(time.Second)}, {Started: start.Add(time.Millisecond), Ended: start.Add(2 * time.Second)}}
	if got, err := overlap(jobs); err != nil || got != 2 {
		t.Fatalf("overlap %d %v", got, err)
	}
	jobs[1].Started = jobs[0].Ended
	if got, err := overlap(jobs); err != nil || got != 1 {
		t.Fatalf("touching intervals %d %v", got, err)
	}
}
