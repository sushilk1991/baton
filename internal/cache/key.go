package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const keyVersion = 1

type recipe struct {
	cwd, output, root   string
	patterns, args, env []string
}

type input struct {
	Name   string
	Mode   uint32
	Size   int64
	Digest string
}

type variable struct {
	Name, Value string
	Present     bool
}

func (r recipe) key(ctx context.Context) (string, error) {
	files := map[string]input{}
	for _, pattern := range r.patterns {
		if err := r.expand(ctx, pattern, files); err != nil {
			return "", err
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	inputs := make([]input, 0, len(names))
	for _, name := range names {
		inputs = append(inputs, files[name])
	}
	env := make([]variable, 0, len(r.env))
	for _, name := range r.env {
		value, present := os.LookupEnv(name)
		env = append(env, variable{name, value, present})
	}
	// JSON framing preserves argv, filename, and unset/empty env boundaries.
	// inputs + argv + cwd/output + allowlisted env -> SHA256 -> opaque key
	payload, err := json.Marshal(struct {
		Version        int
		Cwd, Output    string
		Patterns, Args []string
		Inputs         []input
		Env            []variable
	}{keyVersion, r.cwd, r.output, r.patterns, r.args, inputs, env})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func parsePatterns(globs string) ([]string, error) {
	patterns := strings.Split(globs, ",")
	for i, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || path.IsAbs(pattern) || strings.Contains(pattern, `\`) || strings.Contains(pattern, ":") {
			return nil, fmt.Errorf("cache-key requires relative slash globs: %q", pattern)
		}
		for _, part := range strings.Split(pattern, "/") {
			if part == ".." {
				return nil, errors.New("cache-key cannot leave the working directory")
			}
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, err
		}
		patterns[i] = path.Clean(pattern)
	}
	sort.Strings(patterns)
	return patterns, nil
}

func (r recipe) expand(ctx context.Context, pattern string, files map[string]input) error {
	// Start at the literal prefix instead of scanning unrelated repositories.
	parts := strings.Split(pattern, "/")
	prefix := []string{}
	for _, part := range parts {
		if strings.ContainsAny(part, "*?[") {
			break
		}
		prefix = append(prefix, part)
	}
	base := filepath.Join(r.cwd, filepath.FromSlash(strings.Join(prefix, "/")))
	matched := false
	err := filepath.WalkDir(base, func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if name == r.root || name == r.output {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(r.cwd, name)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("cache input symlink: %q", name)
		}
		if entry.IsDir() || !matchParts(strings.Split(pattern, "/"), strings.Split(filepath.ToSlash(relative), "/")) {
			return nil
		}
		matched = true
		if _, exists := files[relative]; exists {
			return nil
		}
		digest, info, err := digestFile(ctx, name)
		if err != nil {
			return err
		}
		files[relative] = input{filepath.ToSlash(relative), uint32(info.Mode().Perm()), info.Size(), digest}
		return nil
	})
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("cache-key pattern %q matched no input files", pattern)
	}
	return nil
}

func matchParts(pattern, name []string) bool {
	// ** matches zero or more full components; memoization bounds backtracking.
	type position struct{ p, n int }
	seen := map[position]bool{}
	var match func(int, int) bool
	match = func(p, n int) bool {
		at := position{p, n}
		if seen[at] {
			return false
		}
		seen[at] = true
		if p == len(pattern) {
			return n == len(name)
		}
		if pattern[p] == "**" {
			return match(p+1, n) || (n < len(name) && match(p, n+1))
		}
		if n == len(name) {
			return false
		}
		ok, err := path.Match(pattern[p], name[n])
		return err == nil && ok && match(p+1, n+1)
	}
	return match(0, 0)
}
