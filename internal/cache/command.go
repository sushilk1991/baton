package cache

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/sushilk1991/baton/internal/config"
)

const (
	exitFailure = 1
	exitUsage   = 2
	usage       = "usage: baton cache ls|list|prune (prune evicts oldest entries to cache.max_size)"
)

// Run lists cache keys and sizes or enforces the configured least-recently-used cap.
func Run(args []string, out, diagnostics io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		if _, err := fmt.Fprintln(out, usage); err != nil {
			return exitFailure
		}
		return 0
	}
	// list matches baton shim list; both spellings list entries.
	if len(args) == 1 && args[0] == "list" {
		args = []string{"ls"}
	}
	if len(args) != 1 || (args[0] != "ls" && args[0] != "prune") {
		fmt.Fprintln(diagnostics, usage)
		return exitUsage
	}
	if err := manage(context.Background(), args[0], out); err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitFailure
	}
	return 0
}

func manage(ctx context.Context, command string, out io.Writer) error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	root, limit, _ := settings.CachePolicy()
	d, err := openDriver(root, limit)
	if err != nil {
		return err
	}
	guard, err := d.guard(ctx)
	if err != nil {
		return err
	}
	defer guard.Close()
	var buffer bytes.Buffer
	if command == "prune" {
		count, size, err := d.prune()
		if err != nil {
			return err
		}
		fmt.Fprintf(&buffer, "Pruned %d cache entry(s); %d bytes\n", count, size)
	} else {
		rows, err := d.records()
		if err != nil {
			return err
		}
		var total uint64
		fmt.Fprintln(&buffer, "KEY\tBYTES\tLAST_USED")
		for _, r := range rows {
			fmt.Fprintf(&buffer, "%s\t%d\t%s\n", r.Key, r.Size, r.Used.Format("2006-01-02T15:04:05.999999999Z07:00"))
			total += uint64(r.Size)
		}
		fmt.Fprintf(&buffer, "%d cache entry(s); %d bytes; limit %d bytes\n", len(rows), total, limit)
	}
	n, err := out.Write(buffer.Bytes())
	if err == nil && n != buffer.Len() {
		return io.ErrShortWrite
	}
	return err
}
