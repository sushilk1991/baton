package shim

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type selection int

const (
	explicit selection = iota
	defaults
)

type plannedShim struct {
	name string
	body []byte
}

func (install installation) install(names []string, owned map[string]string, out io.Writer, mode selection) error {
	if err := checkDirectory(install.dir); err != nil {
		return err
	}
	seen := make(map[string]bool)
	planned := make([]plannedShim, 0, len(names))
	// Preflight all commands before publishing any. Explicit missing binaries
	// fail; default installation skips tools absent from this machine.
	for _, name := range names {
		if !validName(name) {
			return fmt.Errorf("invalid shim command %q", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if _, err := install.resolve(name); err != nil {
			if mode == defaults && errors.Is(err, os.ErrNotExist) {
				if _, err := fmt.Fprintf(out, "skipped %s: not found in PATH\n", name); err != nil {
					return err
				}
				continue
			}
			return err
		}
		body, err := script(install.binary, name)
		if err != nil {
			return err
		}
		path := filepath.Join(install.dir, shimName(name))
		if err := checkInstall(path, body, owned[name]); err != nil {
			return err
		}
		planned = append(planned, plannedShim{name, body})
	}
	if len(planned) == 0 {
		return guidance(install.dir, out)
	}
	if err := os.MkdirAll(install.dir, directoryMode); err != nil {
		return err
	}
	for _, entry := range planned {
		path := filepath.Join(install.dir, shimName(entry.name))
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			if err := publishFile(path, entry.body, executableMode); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		owned[entry.name] = digest(entry.body)
		// Persist after each publication. An exact unrecorded script from a
		// crash can be adopted on reinstall, without trusting a marker alone.
		if err := install.saveManifest(owned); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "installed %s\n", path); err != nil {
			return err
		}
	}
	return guidance(install.dir, out)
}

func (install installation) readManifest() (map[string]string, error) {
	owned := make(map[string]string)
	if err := checkDirectory(install.dir); err != nil {
		return nil, err
	}
	path := filepath.Join(install.root, manifestName)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return owned, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: expected a regular manifest", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &owned); err != nil || owned == nil {
		return nil, fmt.Errorf("%s: invalid shim manifest", path)
	}
	for name, hash := range owned {
		decoded, err := hex.DecodeString(hash)
		if !validName(name) || err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("%s: invalid shim manifest entry %q", path, name)
		}
	}
	return owned, nil
}

func (install installation) saveManifest(owned map[string]string) error {
	data, err := json.Marshal(owned)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(install.root, ".shim-manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(fileMode); err != nil {
		return errors.Join(err, file.Close())
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(install.root, manifestName))
}

func checkDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s: expected a directory, refusing symlink or file", path)
	}
	return nil
}

func checkOwned(path, hash string) error {
	data, err := readRegular(path)
	if err != nil {
		return err
	}
	if digest(data) != hash {
		return fmt.Errorf("%s: modified shim; preserved", path)
	}
	return nil
}

func checkInstall(path string, body []byte, hash string) error {
	data, err := readRegular(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(data, body) || (hash != "" && digest(data) != hash) {
		return fmt.Errorf("%s: conflicting or modified file; preserved", path)
	}
	return nil
}

func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: expected a regular shim; preserved", path)
	}
	return os.ReadFile(path)
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func publishFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".shim-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	// Publish complete executable bytes without replacing any existing path:
	// temp -> chmod/write/sync/close -> hardlink -> remove temp.
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
	return os.Link(file.Name(), path)
}
