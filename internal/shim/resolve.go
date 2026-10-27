package shim

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var errRecursion = errors.New("shim recursion detected")

func (install installation) resolve(name string) (string, error) {
	recursive := false
	var denied error
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		for _, candidate := range candidates(filepath.Join(dir, name)) {
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				continue
			}
			if samePath(dir, install.dir) || samePath(filepath.Dir(canonical(candidate)), install.dir) || samePath(candidate, install.binary) || isWrapper(candidate) {
				recursive = true
				continue
			}
			if !isExecutable(info) {
				denied = fmt.Errorf("%s: %w", candidate, os.ErrPermission)
				continue
			}
			// Always return an absolute path. The child cannot repeat PATH lookup
			// and accidentally enter the shim again after resolution.
			return filepath.Abs(candidate)
		}
	}
	if recursive {
		return "", fmt.Errorf("%s: %w (no real executable outside shims)", name, errRecursion)
	}
	if denied != nil {
		return "", denied
	}
	return "", fmt.Errorf("%s: executable not found in PATH: %w", name, os.ErrNotExist)
}

func canonical(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return path
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil && leftAbs == rightAbs {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func isWrapper(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	// Read only the marker, never the contents of an arbitrary large binary.
	prefix := make([]byte, len(scriptPrefix))
	_, err = io.ReadFull(file, prefix)
	return err == nil && bytes.Equal(prefix, []byte(scriptPrefix))
}
