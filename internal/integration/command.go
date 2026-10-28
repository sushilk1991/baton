// Package integration installs reversible agent instructions and skills.
package integration

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	exitFailure     = 1
	exitUsage       = 2
	directoryMode   = 0700
	fileMode        = 0600
	manifestVersion = 1
	manifestName    = "integration-manifest.json"
	usage           = "usage: baton init [--yes] [--dry-run] [--uninstall] [--claude-hook | --claude-hook-block]"
)

type installer struct{ home, config, root, binary string }

type operation uint8
type application uint8
type hookPolicy uint8

const (
	installAgents operation = iota
	removeAgents
)
const (
	previewChanges application = iota
	applyChanges
)
const (
	withoutHook hookPolicy = iota
	withClaudeHook
	withClaudeBlock
)

// Run previews changes unless --yes applies them; dry-run always wins.
func Run(args []string, in io.Reader, out, diagnostics io.Writer) int {
	if len(args) == 1 && args[0] == "_hook" {
		return hook(in, out, diagnostics, withClaudeHook)
	}
	if len(args) == 1 && args[0] == "_hook-block" {
		return hook(in, out, diagnostics, withClaudeBlock)
	}
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	yes := flags.Bool("yes", false, "apply the listed changes")
	dry := flags.Bool("dry-run", false, "list changes without writing files")
	remove := flags.Bool("uninstall", false, "restore files recorded in the manifest")
	claude := flags.Bool("claude-hook", false, "also install the Claude Code Bash hook")
	block := flags.Bool("claude-hook-block", false, "block heavy Bash calls with an exact queued command")
	flags.Usage = func() { fmt.Fprintln(diagnostics, usage) }
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(out, usage)
		return 0
	}
	if err != nil || flags.NArg() != 0 || (*remove && (*claude || *block)) || (*claude && *block) {
		fmt.Fprintln(diagnostics, usage)
		return exitUsage
	}
	if (*claude || *block) && !holdSupported {
		fmt.Fprintln(diagnostics, "baton init: the Claude Code hook requires macOS or Linux")
		return exitUsage
	}
	install, err := newInstaller()
	if err == nil {
		action, mode, hook := installAgents, previewChanges, withoutHook
		if *yes && !*dry {
			mode = applyChanges
		}
		if *remove {
			action = removeAgents
		}
		if *claude {
			hook = withClaudeHook
		}
		if *block {
			hook = withClaudeBlock
		}
		err = install.manage(action, mode, hook, out)
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitFailure
	}
	return 0
}

func newInstaller() (installer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return installer{}, err
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return installer{}, err
	}
	binary, err := invokedBinary()
	if err != nil {
		return installer{}, err
	}
	// Resolve existing aliases before recording paths, preserving symlink entries.
	home, err = resolvePath(home)
	if err != nil {
		return installer{}, err
	}
	base, err := resolvePath(filepath.Dir(filepath.Dir(paths.ConfigFile)))
	if err != nil {
		return installer{}, err
	}
	root, err := resolvePath(paths.DataDir)
	return installer{home, base, root, binary}, err
}

func invokedBinary() (string, error) {
	// Match shim installation: retain the invoked link across package upgrades.
	// ErrDot is safe here because this records an absolute path, not an exec.
	binary, err := exec.LookPath(os.Args[0])
	if err != nil && !errors.Is(err, exec.ErrDot) {
		return "", err
	}
	return filepath.Abs(binary)
}

func (i installer) manage(action operation, mode application, claude hookPolicy, out io.Writer) error {
	// Preview is observational: no directory, manifest, or lock is created.
	if mode == previewChanges {
		return i.preview(action, claude, out)
	}
	if err := os.MkdirAll(i.root, directoryMode); err != nil {
		return err
	}
	guard, err := lockfile.Try(filepath.Join(i.root, "integration.lock"))
	if err != nil {
		return err
	}
	defer guard.Close()
	journal, err := i.load()
	if err != nil {
		return err
	}
	if err := i.reconcile(&journal); err != nil {
		return err
	}
	if action == removeAgents {
		return i.uninstall(journal, out)
	}
	if journal.Removing {
		return errors.New("interrupted uninstall: run baton init --yes --uninstall first")
	}
	if err := i.validate(journal); err != nil {
		return err
	}
	// Persist rebased snapshots before replaying a hand-removed edit, so a
	// crash cannot leave a write whose current surroundings were unrecorded.
	if len(journal.Entries) > 0 {
		if err := i.save(journal); err != nil {
			return err
		}
	}
	// Recover interrupted publication before extending an existing installation.
	if err := i.apply(journal); err != nil {
		return err
	}
	planned, err := i.plan(journal, claude, out)
	if err != nil {
		return err
	}
	if err := printPlan(planned, "install", out); err != nil {
		return err
	}
	// Journal precedes all agent writes. A crash leaves either before or after
	// bytes for each entry; reinstall replays, uninstall restores in reverse.
	// plan -> private journal -> directories -> files/links -> installed
	if err := i.save(planned); err != nil {
		return err
	}
	return i.apply(planned)
}

func (i installer) preview(action operation, claude hookPolicy, out io.Writer) error {
	journal, err := i.load()
	if err != nil {
		return err
	}
	if err := i.reconcile(&journal); err != nil {
		return err
	}
	if err := i.validate(journal); err != nil {
		return err
	}
	label := "would uninstall"
	if action == installAgents {
		if journal.Removing {
			return errors.New("interrupted uninstall: run baton init --yes --uninstall first")
		}
		journal, err = i.plan(journal, claude, out)
		if err != nil {
			return err
		}
		label = "would install"
	}
	if err := printPlan(journal, label, out); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Preview only; add --yes to apply.")
	return err
}

func printPlan(j journal, action string, out io.Writer) error {
	for _, entry := range j.Entries {
		if _, err := fmt.Fprintf(out, "%s %s %s\n", action, entry.Kind, entry.Path); err != nil {
			return err
		}
	}
	if len(j.Entries) == 0 {
		_, err := fmt.Fprintln(out, "No recorded integration changes.")
		return err
	}
	return nil
}
