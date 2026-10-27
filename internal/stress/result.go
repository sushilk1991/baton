package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
)

// Exported DTO fields serve JSON only; the helper has no public Go API.
type report struct {
	Time           time.Time `json:"time"`
	OS             string    `json:"os"`
	Arch           string    `json:"arch"`
	CPUs           int       `json:"cpus"`
	RAMBytes       uint64    `json:"ram_bytes"`
	AvailableBytes uint64    `json:"available_bytes"`
	Jobs           int       `json:"jobs"`
	BytesPerJob    uint64    `json:"bytes_per_job"`
	Hold           string    `json:"hold"`
	MaxWait        string    `json:"max_wait"`
	Timeout        string    `json:"timeout"`
	BatonSHA256    string    `json:"baton_sha256"`
	Phases         []phase   `json:"phases"`
	Passed         bool      `json:"passed"`
}

type phase struct {
	Mode            string         `json:"mode"`
	Started         time.Time      `json:"started"`
	Ended           time.Time      `json:"ended"`
	WallSeconds     float64        `json:"wall_seconds"`
	BaseSwapBytes   uint64         `json:"base_swap_bytes"`
	PeakSwapBytes   uint64         `json:"peak_swap_bytes"`
	SwapGrowthBytes uint64         `json:"swap_growth_bytes"`
	Worst           pressure.Level `json:"worst_pressure"`
	Samples         int            `json:"samples"`
	Completed       int            `json:"completed"`
	Failed          int            `json:"failed"`
	Killed          int            `json:"killed"`
	KillEvents      uint64         `json:"observed_kill_events"`
	MaxActive       int            `json:"max_active"`
	Bypasses        int            `json:"max_wait_bypasses"`
	Error           string         `json:"error,omitempty"`
	Failures        []string       `json:"failures"`
	Passed          bool           `json:"passed"`
}

func sampleRecord(p *phase, sample pressure.Snapshot) error {
	if err := sample.Validate(); err != nil {
		return err
	}
	if p.Samples == 0 {
		p.BaseSwapBytes = sample.SwapUsedBytes
		p.Worst = sample.Level
	}
	p.Samples++
	if sample.SwapUsedBytes > p.PeakSwapBytes {
		p.PeakSwapBytes = sample.SwapUsedBytes
	}
	p.SwapGrowthBytes = p.PeakSwapBytes - p.BaseSwapBytes
	if sample.Level == pressure.Critical || sample.Level == pressure.Warn && p.Worst == pressure.Normal {
		p.Worst = sample.Level
	}
	if p.Worst == pressure.Critical {
		return errors.New("critical pressure; stopping owned workers")
	}
	if p.SwapGrowthBytes >= gib {
		return errors.New("swap growth reached 1 GiB; stopping owned workers")
	}
	return nil
}

func evaluate(p phase, reference float64, jobs int) []string {
	failures := []string{}
	if p.Error != "" {
		failures = append(failures, p.Error)
	}
	if p.Samples < 2 {
		failures = append(failures, "missing pressure samples")
	}
	if p.Worst == pressure.Critical || p.Worst == "" {
		failures = append(failures, "critical or unknown pressure")
	}
	if p.SwapGrowthBytes >= gib {
		failures = append(failures, "swap growth >= 1 GiB")
	}
	if p.Completed != jobs || p.Failed != 0 {
		failures = append(failures, fmt.Sprintf("completed %d/%d; failed %d", p.Completed, jobs, p.Failed))
	}
	if p.Killed != 0 || p.KillEvents != 0 {
		failures = append(failures, "kill or OOM/jetsam event observed")
	}
	if reference <= 0 || p.WallSeconds <= 0 || p.WallSeconds > maxTimeRatio*reference {
		failures = append(failures, "wall time > 1.2x sequential or missing")
	}
	if p.MaxActive < 1 || p.Mode != "unqueued" && p.MaxActive != 1 || p.Mode == "unqueued" && jobs > 1 && p.MaxActive < 2 {
		failures = append(failures, "worker overlap does not match the phase")
	}
	return failures
}

func formatReport(result report) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Time: %s\n\n", result.Time.Format(time.RFC3339))
	fmt.Fprintf(&text, "Host: %s/%s, %d CPUs, %.2f GiB RAM, %.2f GiB available at preflight.\n\n", result.OS, result.Arch, result.CPUs, float64(result.RAMBytes)/float64(gib), float64(result.AvailableBytes)/float64(gib))
	fmt.Fprintf(&text, "Work: %d jobs, %.4f GiB each, hold %s; one queued slot, memory gate on, max_wait %s, phase timeout %s.\n\n", result.Jobs, float64(result.BytesPerJob)/float64(gib), result.Hold, result.MaxWait, result.Timeout)
	fmt.Fprintln(&text, "| Mode | Wall s | Peak swap GiB | Growth GiB | Worst pressure | Kill events / killed jobs | Completed | Max active | Bypasses | Result |")
	fmt.Fprintln(&text, "| --- | ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: | --- |")
	for _, p := range result.Phases {
		status := "FAIL"
		if p.Passed {
			status = "PASS"
		}
		fmt.Fprintf(&text, "| %s | %.3f | %.3f | %.3f | %s | %d / %d | %d/%d | %d | %d | %s |\n", p.Mode, p.WallSeconds, float64(p.PeakSwapBytes)/float64(gib), float64(p.SwapGrowthBytes)/float64(gib), p.Worst, p.KillEvents, p.Killed, p.Completed, result.Jobs, p.MaxActive, p.Bypasses, status)
	}
	for _, p := range result.Phases {
		if len(p.Failures) > 0 {
			fmt.Fprintf(&text, "\n%s: %s\n", p.Mode, strings.Join(p.Failures, "; "))
		}
	}
	status := "FAIL"
	if result.Passed {
		status = "PASS"
	}
	if result.Passed {
		fmt.Fprintf(&text, "\n%s: pressure below critical; swap growth < 1 GiB; no observed kills/events; wall <= 1.2x sequential.\n", status)
		return text.String()
	}
	fmt.Fprintln(&text, "\nFAIL: one or more stress criteria were not met.")
	return text.String()
}

func overlap(jobs []jobRecord) (int, error) {
	type point struct {
		time  time.Time
		delta int
	}
	var points []point
	for _, job := range jobs {
		if job.Started.IsZero() || !job.Ended.After(job.Started) {
			return 0, errors.New("missing or reversed worker timestamps")
		}
		points = append(points, point{job.Started, 1}, point{job.Ended, -1})
	}
	// End before start on ties: [start,end) intervals do not overlap at a boundary.
	sort.Slice(points, func(i, j int) bool {
		if points[i].time.Equal(points[j].time) {
			return points[i].delta < points[j].delta
		}
		return points[i].time.Before(points[j].time)
	})
	active, peak := 0, 0
	for _, point := range points {
		active += point.delta
		if active > peak {
			peak = active
		}
	}
	return peak, nil
}
