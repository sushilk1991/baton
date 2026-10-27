//go:build darwin || linux

package shim

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const scriptPrefix = "#!/bin/sh\n# baton shim v1\n"

func script(binary, name string) ([]byte, error) {
	return []byte(scriptPrefix + "exec " + shellQuote(binary) + " shim exec " + shellQuote(name) + " \"$@\"\n"), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func shimName(name string) string {
	return name
}

func candidates(path string) []string {
	return []string{path}
}

func isExecutable(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func direct(real string, args []string) (int, error) {
	// Replace the wrapper, preserving descriptors, signals, PID, and status.
	err := syscall.Exec(real, append([]string{real}, args...), os.Environ())
	return exitNotExecutable, err
}

func guidance(dir string, out io.Writer) error {
	shell := filepath.Base(os.Getenv("SHELL"))
	if shell == "." || shell == "" {
		shell = "sh"
	}
	line := "export PATH=" + shellQuote(dir) + ":\"$PATH\""
	if shell == "fish" {
		// Fish single quotes escape backslashes and quotes with a backslash.
		escaped := strings.ReplaceAll(strings.ReplaceAll(dir, `\`, `\\`), "'", `\'`)
		line = "fish_add_path -- '" + escaped + "'"
	}
	_, err := fmt.Fprintf(out, "shell: %s\nAdd to your shell config:\n%s\n", shell, line)
	return err
}
