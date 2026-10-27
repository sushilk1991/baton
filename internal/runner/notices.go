package runner

import (
	"fmt"
	"io"
	"time"

	"github.com/sushilk1991/baton/internal/pressure"
)

type noticeProvider struct {
	out   io.Writer
	lane  string
	quiet bool
	last  time.Time
}

func (notice *noticeProvider) wait(sample pressure.Snapshot, err error) error {
	// Only blocked retries report; immediate admission preserves child stderr.
	if notice.quiet || (!notice.last.IsZero() && time.Since(notice.last) < noticeInterval) {
		return nil
	}
	notice.last = time.Now()
	level := string(sample.Level)
	if err != nil || level == "" {
		level = "unavailable"
	}
	_, writeErr := fmt.Fprintf(notice.out, "baton: waiting for %s (pressure %s · swap %.1f/%.1f GB)\n", notice.lane, level, float64(sample.SwapUsedBytes)/float64(gib), float64(sample.SwapTotalBytes)/float64(gib))
	return writeErr
}
