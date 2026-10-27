package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const kernelPredicate = `process == "kernel" AND (eventMessage CONTAINS[c] "memorystatus" OR eventMessage CONTAINS[c] "jetsam" OR eventMessage CONTAINS[c] "out of memory")`

func beginEvents(ctx context.Context) (endEvents, error) {
	// Check access first. An unreadable or malformed event source fails closed.
	data, err := queryLog(ctx, "--last", "1s")
	if err != nil {
		return nil, err
	}
	if _, err := readLog(data); err != nil {
		return nil, err
	}
	return func(ctx context.Context, start, end time.Time, dir string) (uint64, error) {
		// log timestamps have second granularity; widen the window rather than
		// omit boundary events. Matching kernel messages are counted conservatively.
		args := []string{"--start", start.Add(-time.Second).Format("2006-01-02 15:04:05"), "--end", end.Add(time.Second).Format("2006-01-02 15:04:05")}
		if err := writeJSON(filepath.Join(dir, "event-query.json"), args); err != nil {
			return 0, err
		}
		data, err := queryLog(ctx, args...)
		if writeErr := os.WriteFile(filepath.Join(dir, "events.ndjson"), data, fileMode); writeErr != nil {
			return 0, writeErr
		}
		if err != nil {
			return 0, err
		}
		return readLog(data)
	}, nil
}

func queryLog(ctx context.Context, window ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	args := append([]string{"show", "--style", "ndjson", "--info", "--predicate", kernelPredicate}, window...)
	command := exec.CommandContext(ctx, "/usr/bin/log", args...)
	command.Env = []string{"LC_ALL=C"}
	data, err := command.CombinedOutput()
	if err != nil {
		return data, fmt.Errorf("kernel log query: %w: %s", err, data)
	}
	return data, nil
}
