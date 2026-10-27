// Package cache stores content-addressed command outputs behind file locks.
package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/lockfile"
)

// Session owns one cache key until Close, including the command on a miss.
type Session struct {
	driver *driver
	recipe recipe
	key    string
	owner  *lockfile.Lock
}

// Prepare hashes inputs and serializes identical jobs without reserving a lane.
func Prepare(ctx context.Context, globs, output string, args []string) (*Session, error) {
	settings, err := config.Load()
	if err != nil {
		return nil, err
	}
	root, limit, env := settings.CachePolicy()
	return prepare(ctx, root, limit, env, globs, output, args)
}

func prepare(ctx context.Context, root string, limit uint64, env []string, globs, output string, args []string) (*Session, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	patterns, err := parsePatterns(globs)
	if err != nil {
		return nil, err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, output)
	if err != nil {
		return nil, err
	}
	if filepath.IsLocal(relative) || relative == "." {
		return nil, errors.New("output cannot be inside the cache directory")
	}
	sort.Strings(env)
	r := recipe{cwd, output, filepath.Clean(root), patterns, append([]string{}, args...), env}
	key, err := r.key(ctx)
	if err != nil {
		return nil, err
	}
	d, err := openDriver(root, limit)
	if err != nil {
		return nil, err
	}
	owner, err := acquire(ctx, filepath.Join(root, "locks", key+".lock"))
	if err != nil {
		return nil, err
	}
	session := &Session{d, r, key, owner}
	if err := session.unchanged(ctx); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

// Close releases the per-key lock; a crash also releases it through the kernel.
func (s *Session) Close() error { return s.owner.Close() }

// Restore verifies an entry and atomically restores its single regular output.
func (s *Session) Restore(ctx context.Context) (bool, error) {
	if err := s.unchanged(ctx); err != nil {
		return false, err
	}
	return s.driver.restore(ctx, s.key, s.recipe.output)
}

// Store publishes only a successful job's stable output and enforces the cap.
func (s *Session) Store(ctx context.Context) error {
	if err := s.unchanged(ctx); err != nil {
		return err
	}
	return s.driver.store(ctx, s.key, s.recipe.output)
}

func (s *Session) unchanged(ctx context.Context) error {
	key, err := s.recipe.key(ctx)
	if err != nil {
		return err
	}
	if key != s.key {
		return fmt.Errorf("cache inputs changed; result will not be cached")
	}
	return nil
}
