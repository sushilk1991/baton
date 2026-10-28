// Package shim installs command wrappers and resolves their real executables.
package shim

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	exitFailure       = 1
	exitUsage         = 2
	exitNotExecutable = 126
	exitNotFound      = 127
	directoryMode     = 0700
	fileMode          = 0600
	executableMode    = 0700
	usage             = "usage: baton shim install [cmd ...] | uninstall | list"
	manifestName      = "shim-manifest.json"
	lockName          = "shims.lock"
	chainKey          = "_BATON_SHIM_CHAIN"
)

type installation struct {
	root   string
	dir    string
	binary string
}

// Run installs, lists, or uninstalls only owned wrappers.
func Run(args []string, out, diagnostics io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		if _, err := fmt.Fprintln(out, usage); err != nil {
			return exitFailure
		}
		return 0
	}
	if len(args) == 0 || (args[0] != "install" && args[0] != "uninstall" && args[0] != "list") || (args[0] != "install" && len(args) != 1) {
		fmt.Fprintln(diagnostics, usage)
		return exitUsage
	}
	if err := manage(args, out); err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitFailure
	}
	return 0
}

func manage(args []string, out io.Writer) error {
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	// Preserve the invoked link, such as /opt/homebrew/bin/baton. Resolving
	// its versioned target would strand wrappers after a package upgrade.
	// ErrDot is allowed because this records a path rather than executing it.
	binary, err := exec.LookPath(os.Args[0])
	if err != nil && !errors.Is(err, exec.ErrDot) {
		return err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return err
	}
	install := installation{paths.DataDir, filepath.Join(paths.DataDir, "shims"), binary}
	if args[0] == "list" {
		owned, err := install.readManifest()
		if err != nil {
			return err
		}
		return install.list(owned, out)
	}
	// Serialize file and manifest changes. Busy management fails explicitly;
	// job scheduling is independent and never waits for this management lock.
	if err := os.MkdirAll(install.root, directoryMode); err != nil {
		return err
	}
	guard, err := lockfile.Try(filepath.Join(install.root, lockName))
	if err != nil {
		return err
	}
	defer guard.Close()
	owned, err := install.readManifest()
	if err != nil {
		return err
	}
	if args[0] == "uninstall" {
		return install.uninstall(owned, out)
	}
	settings, err := config.Load()
	if err != nil {
		return err
	}
	commands := args[1:]
	if len(commands) == 0 {
		commands = settings.ShimCommands()
	}
	mode := explicit
	if len(args) == 1 {
		mode = defaults
	}
	return install.install(commands, owned, out, mode)
}

func validName(name string) bool {
	// Reject paths and control characters before they can become filenames or
	// terminal output. Shell metacharacters are quoted by the platform writer.
	if name == "" || name == "." || name == ".." || strings.EqualFold(name, "baton") || strings.EqualFold(name, "baton.exe") {
		return false
	}
	for _, char := range name {
		if char <= ' ' || char == 127 || strings.ContainsRune(`/\`, char) {
			return false
		}
	}
	return true
}

func sortedNames(owned map[string]string) []string {
	names := make([]string, 0, len(owned))
	for name := range owned {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (install installation) list(owned map[string]string, out io.Writer) error {
	for _, name := range sortedNames(owned) {
		path := filepath.Join(install.dir, shimName(name))
		if err := checkOwned(path, owned[name]); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\n", name, path); err != nil {
			return err
		}
	}
	return nil
}

func (install installation) uninstall(owned map[string]string, out io.Writer) error {
	// Preflight every entry before deleting anything. User edits and symlinks
	// are preserved; a prior interrupted uninstall can leave missing files.
	for _, name := range sortedNames(owned) {
		path := filepath.Join(install.dir, shimName(name))
		if err := checkOwned(path, owned[name]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, name := range sortedNames(owned) {
		path := filepath.Join(install.dir, shimName(name))
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, err := fmt.Fprintf(out, "removed %s\n", path); err != nil {
			return err
		}
	}
	// Retain the shared lock inode; removing it could split concurrent owners.
	return install.saveManifest(map[string]string{})
}
