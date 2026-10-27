package shim

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/runner"
)

// Invoke is the generated wrapper's entry point and uses inherited stdio.
func Invoke(args []string) int {
	if len(args) == 0 || !validName(args[0]) {
		fmt.Fprintln(os.Stderr, "baton: invalid shim invocation")
		return exitUsage
	}
	code, err := invoke(args[0], args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "baton:", err)
	}
	return code
}

func invoke(name string, args []string) (int, error) {
	if err := guardChain(name, args); err != nil {
		return exitNotExecutable, err
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return exitFailure, err
	}
	binary, err := os.Executable()
	if err != nil {
		return exitFailure, err
	}
	install := installation{paths.DataDir, filepath.Join(paths.DataDir, "shims"), binary}
	real, err := install.resolve(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return exitNotFound, err
		}
		return exitNotExecutable, err
	}
	if os.Getenv("BATON") == "off" || os.Getenv(jobKey) == admittedJob {
		return direct(real, args)
	}
	settings, err := config.Load()
	if err != nil {
		return exitFailure, err
	}
	if settings.ShimBypass(name, args) {
		return direct(real, args)
	}
	// wrapper -> resolver -> runner -> queue -> child
	//                         |
	//                 fast/off -> direct exec
	// Keep argument boundaries intact; classification uses the real basename.
	command := append([]string{real}, args...)

	// An admitted job owns its whole process tree. Descendant shims run in
	// that reservation so a child cannot wait on a slot held by its parent.
	if settings.AutoLane(command) != "" {
		if err := os.Setenv(jobKey, admittedJob); err != nil {
			return exitFailure, err
		}
	}
	runArgs := append([]string{"--lane", "auto", "--"}, command...)
	return runner.Run(runArgs, os.Stdin, os.Stdout, os.Stderr), nil
}

func guardChain(name string, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	invocation, err := json.Marshal(struct {
		Name string
		Args []string
		Cwd  string
	}{name, args, cwd})
	if err != nil {
		return err
	}
	signature := digest(invocation)
	var chain []string
	if text := os.Getenv(chainKey); text != "" {
		if err := json.Unmarshal([]byte(text), &chain); err != nil {
			return fmt.Errorf("%w: invalid %s", errRecursion, chainKey)
		}
	}
	for _, active := range chain {
		if active == signature {
			return fmt.Errorf("%w: %s re-entered its wrapper", errRecursion, name)
		}
	}
	// Only identical invocations count as recursion: npm install can call
	// npm run with different arguments. Hash argv and cwd rather than copying
	// potentially sensitive arguments into the inherited environment.
	data, err := json.Marshal(append(chain, signature))
	if err != nil {
		return err
	}
	return os.Setenv(chainKey, string(data))
}
