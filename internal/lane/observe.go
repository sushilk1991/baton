package lane

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

// Observe counts kernel-held slots without resizing or creating lane state.
func Observe(ctx context.Context, root, name string, fallback int) (int, int, error) {
	dir := filepath.Join(root, fmt.Sprintf("%x", sha256.Sum256([]byte(name))))
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	// Allocation and observation share a guard; partial weighted reservations
	// cannot appear as complete jobs. Releases can lower the count afterward.
	for {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		guard, err := lockfile.TryExisting(filepath.Join(dir, guardFile))
		if errors.Is(err, os.ErrNotExist) {
			return 0, fallback, nil
		}
		if errors.Is(err, lockfile.ErrBusy) {
			select {
			case <-ctx.Done():
				return 0, 0, ctx.Err()
			case <-ticker.C:
				continue
			}
		}
		if err != nil {
			return 0, 0, err
		}
		used, total, err := countSlots(ctx, dir, fallback)
		return used, total, errors.Join(err, guard.Close())
	}
}

func countSlots(ctx context.Context, dir string, fallback int) (int, int, error) {
	data, err := os.ReadFile(filepath.Join(dir, capacityFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, fallback, nil
	}
	if err != nil {
		return 0, 0, err
	}
	total, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || total < 1 {
		return 0, 0, fmt.Errorf("invalid lane capacity in %s", dir)
	}
	used := 0
	for slot := 0; slot < total; slot++ {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		lock, err := lockfile.TryExisting(filepath.Join(dir, fmt.Sprintf("slot-%d.lock", slot)))
		if errors.Is(err, lockfile.ErrBusy) {
			used++
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		if err := lock.Close(); err != nil {
			return 0, 0, err
		}
	}
	return used, total, nil
}
