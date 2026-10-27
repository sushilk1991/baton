package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	exitFailure   = 1
	exitUsage     = 2
	directoryMode = 0700
	fileMode      = 0600
	commandUsage  = "usage: baton config init|show|path|validate"
)

// Run executes one config action without modifying files for read commands.
func Run(args []string, out, diagnostics io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		if _, err := fmt.Fprintln(out, commandUsage); err != nil {
			return exitFailure
		}
		return 0
	}
	if len(args) != 1 || (args[0] != "init" && args[0] != "show" && args[0] != "path" && args[0] != "validate") {
		fmt.Fprintln(diagnostics, commandUsage)
		return exitUsage
	}
	if err := command(args[0], out); err != nil {
		fmt.Fprintln(diagnostics, "baton:", err)
		return exitFailure
	}
	return 0
}

func command(action string, out io.Writer) error {
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	if action == "path" {
		_, err := fmt.Fprintln(out, paths.ConfigFile)
		return err
	}
	if action == "init" {
		if err := initialize(paths); err != nil {
			return fmt.Errorf("%s: %w", paths.ConfigFile, err)
		}
		_, err := fmt.Fprintln(out, paths.ConfigFile)
		return err
	}
	config, err := Load()
	if err != nil {
		return err
	}
	if action == "show" {
		return config.Write(out)
	}
	_, err = fmt.Fprintf(out, "%s: valid\n", paths.ConfigFile)
	return err
}

func initialize(paths Paths) error {
	_, statErr := os.Lstat(paths.ConfigFile)
	if statErr == nil {
		return os.ErrExist
	}
	if !os.IsNotExist(statErr) {
		return statErr
	}
	config, err := compile(systemDefaults(paths))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), directoryMode); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(paths.ConfigFile), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	// Publish only a complete private file. Link is atomic and never replaces
	// an existing path: temp -> fsync/close -> link config.toml -> unlink temp.
	if err := file.Chmod(fileMode); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := config.Write(file); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Link(file.Name(), paths.ConfigFile)
}
