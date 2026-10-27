// Command baton coordinates heavy jobs on one machine.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/sushilk1991/baton/internal/cache"
	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/gc"
	"github.com/sushilk1991/baton/internal/reap"
	"github.com/sushilk1991/baton/internal/runner"
	"github.com/sushilk1991/baton/internal/shim"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2

	helpText = `baton queues heavy jobs on this machine.

Usage: baton <command> [arguments]

Commands:
  run     Queue and execute a command
  config  Initialize, show, locate, or validate configuration
  status  Show running jobs, queues, slots, and memory
  history Show completed jobs
  shim    Install, list, or uninstall command wrappers
  reap    List or terminate aged orphaned heavy processes
  gc      List or delete aged regenerable intermediates
  cache   List cached outputs or prune to the size cap
`
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	// A bare invocation or help flag shows usage without running any work.
	if len(args) == 0 {
		return printHelp(stdout)
	}

	if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		return printHelp(stdout)
	}

	// Keep command execution below argument dispatch.
	if args[0] == "run" {
		return runner.Run(args[1:], os.Stdin, stdout, stderr)
	}
	if args[0] == "config" {
		return config.Run(args[1:], stdout, stderr)
	}
	if args[0] == "status" {
		return runner.Status(args[1:], stdout, stderr)
	}
	if args[0] == "history" {
		return runner.History(args[1:], stdout, stderr)
	}

	if args[0] == "reap" {
		return reap.Run(args[1:], stdout, stderr)
	}
	if args[0] == "gc" {
		return gc.Run(args[1:], stdout, stderr)
	}

	if args[0] == "cache" {
		return cache.Run(args[1:], stdout, stderr)
	}

	if args[0] == "shim" {
		if len(args) > 1 && args[1] == "exec" {
			return shim.Invoke(args[2:])
		}
		return shim.Run(args[1:], stdout, stderr)
	}

	// Fail explicitly until each command exists, so agents cannot infer success.
	if _, err := fmt.Fprintf(stderr, "baton: unknown command %q\n\n%s", args[0], helpText); err != nil {
		return exitFailure
	}

	return exitUsage
}

func printHelp(output io.Writer) int {
	if _, err := io.WriteString(output, helpText); err != nil {
		return exitFailure
	}

	return exitOK
}
