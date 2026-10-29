package runner

import (
	"fmt"
	"io"
	"time"

	"github.com/sushilk1991/baton/internal/admission"
	"github.com/sushilk1991/baton/internal/pressure"
)

type noticeProvider struct {
	out    io.Writer
	lane   string
	quiet  bool
	last   time.Time
	reason admission.WaitReason
}

func (notice *noticeProvider) wait(reason admission.WaitReason, sample pressure.Snapshot, err error) error {
	// Only blocked retries report; immediate admission preserves child stderr.
	// A new reason reports at once: slot wait -> memory wait, or the reverse.
	repeated := !notice.last.IsZero() && reason == notice.reason
	if notice.quiet || (repeated && time.Since(notice.last) < noticeInterval) {
		return nil
	}
	notice.last, notice.reason = time.Now(), reason
	if reason == admission.WaitSlots {
		_, writeErr := fmt.Fprintf(notice.out, "baton: waiting for a free %s slot\n", notice.lane)
		return writeErr
	}
	level := string(sample.Level)
	if err != nil || level == "" {
		level = "unavailable"
	}
	_, writeErr := fmt.Fprintf(notice.out, "baton: waiting for memory before %s (pressure %s · %s available · swap %s/%s)\n",
		notice.lane, level, pressure.FormatBytes(sample.AvailableBytes), pressure.FormatBytes(sample.SwapUsedBytes), pressure.FormatBytes(sample.SwapTotalBytes))
	return writeErr
}
