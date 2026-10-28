package runner

import (
	"context"
	"fmt"
	"io"
)

// Hold reports that shell-lifetime reservations require macOS or Linux.
func Hold(_ []string, _, diagnostics io.Writer) int {
	fmt.Fprintln(diagnostics, "baton hold: requires macOS or Linux")
	return exitFailure
}

func addHeldLanes(context.Context, []jobRecord, map[string]string) {}
