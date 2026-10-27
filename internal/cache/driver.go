package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	directoryMode = 0700
	fileMode      = 0600
	lockPoll      = 25 * time.Millisecond
	metadataLimit = 64 << 10
)

type driver struct {
	root  string
	limit uint64
}

type record struct {
	Key    string    `json:"key"`
	Digest string    `json:"digest"`
	Size   int64     `json:"size"`
	Mode   uint32    `json:"mode"`
	Used   time.Time `json:"used"`
}

func openDriver(root string, limit uint64) (*driver, error) {
	for _, dir := range []string{root, filepath.Join(root, "entries"), filepath.Join(root, "locks")} {
		if err := os.MkdirAll(dir, directoryMode); err != nil {
			return nil, err
		}
	}
	return &driver{root, limit}, nil
}

func acquire(ctx context.Context, name string) (*lockfile.Lock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		guard, err := lockfile.Try(name)
		if !errors.Is(err, lockfile.ErrBusy) {
			return guard, err
		}
		timer := time.NewTimer(lockPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (d *driver) guard(ctx context.Context) (*lockfile.Lock, error) {
	return acquire(ctx, filepath.Join(d.root, "cache.lock"))
}

func validKey(key string) bool {
	decoded, err := hex.DecodeString(key)
	return err == nil && len(decoded) == sha256.Size && key == strings.ToLower(key)
}

func (d *driver) entry(key string) string { return filepath.Join(d.root, "entries", key) }

func readRecord(dir string) (record, error) {
	var r record
	info, err := os.Lstat(dir)
	if err != nil {
		return r, err
	}
	if !info.IsDir() {
		return r, errors.New("cache entry is not a directory")
	}
	file, err := openRegular(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return r, err
	}
	err = json.NewDecoder(io.LimitReader(file, metadataLimit)).Decode(&r)
	err = errors.Join(err, file.Close())
	if err != nil {
		return r, err
	}
	if !validKey(r.Key) || !validKey(r.Digest) || r.Key != filepath.Base(dir) || r.Size < 0 || r.Mode > 0777 || r.Used.IsZero() {
		return r, errors.New("invalid cache metadata")
	}
	output, err := os.Lstat(filepath.Join(dir, "output"))
	if err != nil {
		return r, err
	}
	if !output.Mode().IsRegular() || output.Size() != r.Size || uint32(output.Mode().Perm()) != r.Mode {
		return r, errors.New("invalid cache payload metadata")
	}
	return r, nil
}

func openRegular(name string) (*os.File, error) {
	before, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("cache requires a regular file: %q", name)
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, errors.Join(err, errors.New("file changed while opening"))
	}
	return file, nil
}

type contextReader struct {
	ctx context.Context
	in  io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.in.Read(p)
}

func digestFile(ctx context.Context, name string) (string, os.FileInfo, error) {
	file, err := openRegular(name)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return "", nil, err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, contextReader{ctx, file})
	if err != nil {
		return "", nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return "", nil, err
	}
	if before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() || n != before.Size() {
		return "", nil, errors.New("file changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), after, nil
}

func (d *driver) restore(ctx context.Context, key, output string) (bool, error) {
	guard, err := d.guard(ctx)
	if err != nil {
		return false, err
	}
	defer guard.Close()
	dir := d.entry(key)
	r, err := readRecord(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, d.discard(dir, err)
	}
	digest, info, err := digestFile(ctx, filepath.Join(dir, "output"))
	if err != nil || digest != r.Digest || info.Size() != r.Size || uint32(info.Mode().Perm()) != r.Mode {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, d.discard(dir, errors.New("corrupt cache output"))
	}
	if err := restoreFile(ctx, filepath.Join(dir, "output"), output, os.FileMode(r.Mode)); err != nil {
		return false, err
	}
	r.Used = time.Now().UTC()
	if err := writeRecord(dir, r); err != nil {
		return false, err
	}
	return true, nil
}

func (d *driver) discard(dir string, cause error) error {
	// Corruption is an explicit miss: remove only this generated key directory.
	if err := os.RemoveAll(dir); err != nil {
		return errors.Join(cause, err)
	}
	return nil
}

func restoreFile(ctx context.Context, source, output string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(output), directoryMode); err != nil {
		return err
	}
	if info, err := os.Lstat(output); err == nil && !info.Mode().IsRegular() {
		return errors.New("cache output destination is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(output), ".baton-restore-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	defer os.Remove(name)
	if err := os.Remove(name); err != nil {
		return err
	}
	// Same-filesystem hits link the immutable payload. Cross-device restores
	// copy instead. A later in-place edit is detected by the next digest check.
	if err := os.Link(source, name); err != nil {
		if err := copyFile(ctx, source, name, mode); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(name, output)
}

func copyFile(ctx context.Context, source, target string, mode os.FileMode) error {
	in, err := openRegular(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, contextReader{ctx, in})
	return errors.Join(copyErr, out.Chmod(mode), out.Sync(), out.Close())
}

func writeRecord(dir string, r record) error {
	file, err := os.CreateTemp(dir, ".metadata-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = errors.Join(json.NewEncoder(file).Encode(r), file.Sync(), file.Close())
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, "metadata.json"))
}

func (d *driver) store(ctx context.Context, key, output string) error {
	digest, info, err := digestFile(ctx, output)
	if err != nil {
		return err
	}
	if uint64(info.Size()) > d.limit {
		return errors.New("output exceeds cache.max_size; not stored")
	}
	guard, err := d.guard(ctx)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err := d.cleanTemps(); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(filepath.Join(d.root, "entries"), ".pending-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err := copyFile(ctx, output, filepath.Join(temp, "output"), info.Mode().Perm()); err != nil {
		return err
	}
	copied, copyInfo, err := digestFile(ctx, filepath.Join(temp, "output"))
	if err != nil {
		return err
	}
	if copied != digest || copyInfo.Size() != info.Size() {
		return errors.New("output changed while caching")
	}
	r := record{key, digest, info.Size(), uint32(info.Mode().Perm()), time.Now().UTC()}
	if err := writeRecord(temp, r); err != nil {
		return err
	}
	// Per-key -> global lock; publish a whole directory only after both files
	// are synced. Readers/prune hold global; abandoned staging is never a hit.
	// command -> staging {output, metadata} -> rename key -> LRU cap
	if err := os.RemoveAll(d.entry(key)); err != nil {
		return err
	}
	if err := os.Rename(temp, d.entry(key)); err != nil {
		return err
	}
	_, _, err = d.prune()
	return err
}

func (d *driver) cleanTemps() error {
	entries, err := os.ReadDir(filepath.Join(d.root, "entries"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(d.root, "entries", entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) records() ([]record, error) {
	entries, err := os.ReadDir(filepath.Join(d.root, "entries"))
	if err != nil {
		return nil, err
	}
	rows := []record{}
	for _, entry := range entries {
		if !validKey(entry.Name()) {
			continue
		}
		r, err := readRecord(d.entry(entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("cache entry %s: %w", entry.Name(), err)
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Used.Equal(rows[j].Used) {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].Used.Before(rows[j].Used)
	})
	return rows, nil
}

func (d *driver) prune() (int, uint64, error) {
	if err := d.cleanTemps(); err != nil {
		return 0, 0, err
	}
	count, removed, err := d.cleanCorrupt()
	if err != nil {
		return count, removed, err
	}
	rows, err := d.records()
	if err != nil {
		return count, removed, err
	}
	var total uint64
	for _, r := range rows {
		total += uint64(r.Size)
	}
	for _, r := range rows {
		if total <= d.limit {
			break
		}
		if err := os.RemoveAll(d.entry(r.Key)); err != nil {
			return count, removed, err
		}
		count++
		total -= uint64(r.Size)
		removed += uint64(r.Size)
	}
	return count, removed, nil
}

func (d *driver) cleanCorrupt() (int, uint64, error) {
	entries, err := os.ReadDir(filepath.Join(d.root, "entries"))
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var removed uint64
	for _, entry := range entries {
		if !validKey(entry.Name()) {
			continue
		}
		dir := d.entry(entry.Name())
		if _, err := readRecord(dir); err == nil {
			continue
		}
		// Bad generated entries cannot block recovery. Count only payload bytes
		// observed without following links; unknown metadata contributes zero.
		var size uint64
		if entry.IsDir() {
			if info, err := os.Lstat(filepath.Join(dir, "output")); err == nil && info.Mode().IsRegular() {
				size = uint64(info.Size())
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			return count, removed, err
		}
		count++
		removed += size
	}
	return count, removed, nil
}
