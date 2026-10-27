// Package gc collects aged regenerable files through a rooted filesystem driver.
package gc

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/config"
)

const (
	exitFailure = 1
	exitUsage   = 2
	usage       = "usage: baton gc [--yes] (roots, patterns, older_than from [gc] config)"
)

var errChanged = errors.New("candidate changed since discovery")

type fileInfo struct {
	device, inode     uint64
	size              int64
	mode              fs.FileMode
	modified, changed time.Time
}

type entry struct {
	name string
	info fileInfo
}

type candidate struct {
	root *fileRoot
	path string
	entry
}

// Run lists sizes; --yes revalidates regular files before deleting them.
func Run(args []string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("gc", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	yes := flags.Bool("yes", false, "delete the listed aged files")
	flags.Usage = func() { fmt.Fprintln(diagnostics, usage) }
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		if _, err := fmt.Fprintln(out, usage); err != nil {
			return exitFailure
		}
		return 0
	}
	if err != nil || len(flags.Args()) != 0 {
		fmt.Fprintln(diagnostics, usage)
		return exitUsage
	}
	if err := collect(out, *yes); err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitFailure
	}
	return 0
}

func collect(out io.Writer, yes bool) error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	roots, patterns, age := settings.GCPolicy()
	cutoff := time.Now().Add(-age)
	rows := []candidate{}
	seen := map[string]bool{}
	// Keep root handles until deletion completes. No file is removed until
	// every root was scanned and the full selection reached the caller.
	// roots -> no-follow scan -> print selection -> revalidate -> unlinkat
	for _, name := range roots {
		root, err := openRoot(name)
		if err != nil {
			return fmt.Errorf("gc root %q: %w", name, err)
		}
		defer root.close()
		found, err := discover(root, name, patterns, cutoff)
		if err != nil {
			return fmt.Errorf("gc root %q: %w", name, err)
		}
		for _, row := range found {
			if seen[row.path] {
				continue
			}
			seen[row.path] = true
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].path < rows[j].path })
	if err := printRows(out, rows); err != nil {
		return err
	}
	if !yes {
		return nil
	}
	return deleteRows(out, rows)
}

func discover(root *fileRoot, base string, patterns []string, cutoff time.Time) ([]candidate, error) {
	rows := []candidate{}
	pending := []string{"."}
	for len(pending) != 0 {
		dir := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		entries, err := root.list(dir)
		if err != nil {
			return nil, err
		}
		for _, item := range entries {
			if item.info.mode.IsDir() {
				pending = append(pending, item.name)
				continue
			}
			if !item.info.mode.IsRegular() || !item.info.modified.Before(cutoff) || !matches(patterns, item.name) {
				continue
			}
			rows = append(rows, candidate{root, filepath.Join(base, filepath.FromSlash(item.name)), item})
		}
	}
	return rows, nil
}

func matches(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matchParts(strings.Split(pattern, "/"), strings.Split(name, "/")) {
			return true
		}
	}
	return false
}

func matchParts(pattern, name []string) bool {
	// Globstar consumes zero or more complete components; ordinary * never
	// crosses a slash. Memoization bounds repeated ** patterns to O(P*N).
	type position struct{ pattern, name int }
	seen := map[position]bool{}
	var match func(int, int) bool
	match = func(p, n int) bool {
		key := position{p, n}
		if seen[key] {
			return false
		}
		seen[key] = true
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

func printRows(out io.Writer, rows []candidate) error {
	var buffer bytes.Buffer
	var total int64
	fmt.Fprintln(&buffer, "BYTES\tPATH")
	for _, row := range rows {
		fmt.Fprintf(&buffer, "%d\t%q\n", row.info.size, row.path)
		total += row.info.size
	}
	fmt.Fprintf(&buffer, "%d gc candidate(s); %d bytes; dry-run unless --yes\n", len(rows), total)
	written, err := out.Write(buffer.Bytes())
	if err == nil && written != buffer.Len() {
		return io.ErrShortWrite
	}
	return err
}

func deleteRows(out io.Writer, rows []candidate) error {
	count := 0
	var total int64
	for _, row := range rows {
		if err := row.root.remove(row.entry); err != nil {
			return fmt.Errorf("gc delete %q (deleted %d file(s), %d bytes): %w", row.path, count, total, err)
		}
		count++
		total += row.info.size
	}
	_, err := fmt.Fprintf(out, "Deleted %d file(s); %d bytes\n", count, total)
	return err
}
