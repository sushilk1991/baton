package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const vmstatPath = "/proc/vmstat"

func beginEvents(ctx context.Context) (endEvents, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := os.ReadFile(vmstatPath)
	if err != nil {
		return nil, err
	}
	initial, err := readCounter(before)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, _, _ time.Time, dir string) (uint64, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		after, err := os.ReadFile(vmstatPath)
		if err != nil {
			return 0, err
		}
		for name, data := range map[string][]byte{"vmstat-before.txt": before, "vmstat-after.txt": after} {
			if err := os.WriteFile(filepath.Join(dir, name), data, fileMode); err != nil {
				return 0, err
			}
		}
		final, err := readCounter(after)
		if err != nil {
			return 0, err
		}
		if final < initial {
			return 0, errors.New("oom_kill counter moved backward")
		}
		return final - initial, nil
	}, nil
}
