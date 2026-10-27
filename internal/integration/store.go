package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type journal struct {
	Version  int      `json:"version"`
	Home     string   `json:"home"`
	Config   string   `json:"config"`
	Removing bool     `json:"removing,omitempty"`
	Entries  []change `json:"entries"`
}

type change struct {
	Path       string      `json:"path"`
	Kind       string      `json:"kind"`
	Existed    bool        `json:"existed,omitempty"`
	Before     []byte      `json:"before,omitempty"`
	BeforeMode os.FileMode `json:"before_mode,omitempty"`
	After      []byte      `json:"after,omitempty"`
	Mode       os.FileMode `json:"mode,omitempty"`
	Target     string      `json:"target,omitempty"`
}

func resolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("expected absolute path: %s", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = resolvePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (i installer) allowed(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && (within(i.home, path) || within(i.config, path)) && !within(i.root, path) && path != i.root
}

func (i installer) load() (journal, error) {
	j := journal{Version: manifestVersion, Home: i.home, Config: i.config}
	path := filepath.Join(i.root, manifestName)
	data, _, err := readFile(path)
	if os.IsNotExist(err) {
		return j, nil
	}
	if err != nil {
		return j, err
	}
	j = journal{}
	if err := json.Unmarshal(data, &j); err != nil {
		return j, fmt.Errorf("%s: invalid manifest: %w", path, err)
	}
	if j.Version != manifestVersion || j.Home != i.home || j.Config != i.config {
		return j, errors.New("integration manifest belongs to another home or version")
	}
	seen := map[string]bool{}
	for _, e := range j.Entries {
		if !i.allowed(e.Path) || seen[e.Path] || (e.Kind != "file" && e.Kind != "link" && e.Kind != "directory") {
			return j, fmt.Errorf("invalid integration manifest entry: %s", e.Path)
		}
		if e.Kind == "link" && e.Target != filepath.Join(i.home, ".agents", "skills", "baton") {
			return j, errors.New("invalid skill link target")
		}
		seen[e.Path] = true
	}
	return j, nil
}

func (i installer) save(j journal) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(i.root, manifestName), append(data, '\n'), fileMode)
}

func readFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s: expected regular file; preserved", path)
	}
	data, err := os.ReadFile(path)
	return data, info.Mode().Perm(), err
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".baton-integration-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		return errors.Join(err, file.Close())
	}
	if _, err := file.Write(data); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func sameMode(a, b os.FileMode) bool {
	// Windows does not preserve Unix permission bits on ordinary files.
	return os.PathSeparator == '\\' || a.Perm() == b.Perm()
}

func state(e change) (bool, bool, error) {
	info, err := os.Lstat(e.Path)
	if os.IsNotExist(err) {
		return !e.Existed, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if e.Kind == "directory" {
		return false, info.IsDir(), nil
	}
	if e.Kind == "link" {
		target, err := os.Readlink(e.Path)
		return false, err == nil && target == e.Target, nil
	}
	if !info.Mode().IsRegular() {
		return false, false, nil
	}
	data, mode, err := readFile(e.Path)
	if err != nil {
		return false, false, err
	}
	before := e.Existed && bytes.Equal(data, e.Before) && sameMode(mode, e.BeforeMode)
	after := bytes.Equal(data, e.After) && sameMode(mode, e.Mode)
	return before, after, nil
}

func (i installer) validate(j journal) error {
	for _, e := range j.Entries {
		// Refuse redirected ancestors, including a replaced agent directory.
		resolved, err := resolvePath(filepath.Dir(e.Path))
		if err != nil {
			return err
		}
		if resolved != filepath.Dir(e.Path) {
			return fmt.Errorf("%s: parent changed; preserved", e.Path)
		}
		before, after, err := state(e)
		if err != nil {
			return err
		}
		if !before && !after {
			return fmt.Errorf("%s: modified integration; preserved", e.Path)
		}
	}
	return nil
}

func (i installer) apply(j journal) error {
	for _, e := range j.Entries {
		if err := i.validate(journal{Entries: []change{e}}); err != nil {
			return err
		}
		_, after, err := state(e)
		if err != nil {
			return err
		}
		if after {
			continue
		}
		switch e.Kind {
		case "directory":
			err = os.Mkdir(e.Path, directoryMode)
		case "link":
			err = os.Symlink(e.Target, e.Path)
		case "file":
			err = writeFile(e.Path, e.After, e.Mode)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (i installer) uninstall(j journal, out io.Writer) error {
	if err := i.validate(j); err != nil {
		return err
	}
	if err := printPlan(j, "restore", out); err != nil {
		return err
	}
	j.Removing = true
	if err := i.save(j); err != nil {
		return err
	}
	// Restore leaves before parents. Remove only empty owned directories;
	// unrelated new files remain and prevent directory removal.
	for n := len(j.Entries) - 1; n >= 0; n-- {
		e := j.Entries[n]
		if err := i.validate(journal{Entries: []change{e}}); err != nil {
			return err
		}
		before, _, err := state(e)
		if err != nil {
			return err
		}
		if before {
			continue
		}
		if e.Existed {
			err = writeFile(e.Path, e.Before, e.BeforeMode)
		} else {
			err = os.Remove(e.Path)
		}
		if err != nil && e.Kind == "directory" {
			entries, listErr := os.ReadDir(e.Path)
			if listErr == nil && len(entries) > 0 {
				continue
			}
		}
		if err != nil {
			return err
		}
	}
	return os.Remove(filepath.Join(i.root, manifestName))
}
