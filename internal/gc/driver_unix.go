//go:build darwin || linux

package gc

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

type fileRoot struct {
	name string
	file *os.File
}

func openRoot(name string) (*fileRoot, error) {
	file, err := openAbsolute(name)
	if err != nil {
		return nil, err
	}
	return &fileRoot{filepath.Clean(name), file}, nil
}

func (root *fileRoot) close() {
	root.file.Close()
}

func openAbsolute(name string) (*os.File, error) {
	if !filepath.IsAbs(name) || strings.ContainsRune(name, '\x00') {
		return nil, fmt.Errorf("expected an absolute directory")
	}
	// Start at / and open each component without symlinks. Even a configured
	// root or its parent may be replaced; cleaning alone cannot protect it.
	file, err := os.Open("/")
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(filepath.Clean(name), "/") {
		if part == "" {
			continue
		}
		next, err := openDirAt(file, part)
		file.Close()
		if err != nil {
			return nil, fmt.Errorf("open directory component %q without symlinks: %w", part, err)
		}
		file = next
	}
	return file, nil
}

func openDirAt(parent *os.File, name string) (*os.File, error) {
	fd, err := openAt(int(parent.Fd()), name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_DIRECTORY|syscall.O_NOFOLLOW)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (root *fileRoot) directory(name string) (*os.File, error) {
	if name != "." && !fs.ValidPath(name) {
		return nil, fmt.Errorf("invalid root-relative directory %q", name)
	}
	file, err := openAbsolute(root.name)
	if err != nil {
		return nil, err
	}
	current, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	expected, oldErr := root.file.Stat()
	if oldErr != nil {
		file.Close()
		return nil, oldErr
	}
	if !os.SameFile(current, expected) {
		file.Close()
		return nil, errChanged
	}
	for _, part := range strings.Split(name, "/") {
		if part == "." {
			continue
		}
		next, err := openDirAt(file, part)
		file.Close()
		if err != nil {
			return nil, err
		}
		file = next
	}
	return file, nil
}

func (root *fileRoot) list(name string) ([]entry, error) {
	dir, err := root.directory(name)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	rows := []entry{}
	for _, leaf := range names {
		info, err := inspectAt(int(dir.Fd()), leaf)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect %q: %w", path.Join(name, leaf), err)
		}
		rows = append(rows, entry{path.Join(name, leaf), info})
	}
	return rows, nil
}

func (root *fileRoot) remove(expected entry) error {
	if !fs.ValidPath(expected.name) || !expected.info.mode.IsRegular() {
		return fmt.Errorf("invalid file candidate %q", expected.name)
	}
	dir, err := root.directory(path.Dir(expected.name))
	if err != nil {
		return err
	}
	defer dir.Close()
	leaf := path.Base(expected.name)
	current, err := inspectAt(int(dir.Fd()), leaf)
	if err != nil {
		return err
	}
	// Compare identity, type, size, mtime, and ctime before removing the leaf.
	// unlinkat never follows a replacement symlink, even after this check.
	if current != expected.info {
		return errChanged
	}
	return unlinkAt(int(dir.Fd()), leaf)
}

func fileMode(mode uint32) fs.FileMode {
	switch mode & syscall.S_IFMT {
	case syscall.S_IFREG:
		return 0
	case syscall.S_IFDIR:
		return fs.ModeDir
	case syscall.S_IFLNK:
		return fs.ModeSymlink
	default:
		return fs.ModeIrregular
	}
}
