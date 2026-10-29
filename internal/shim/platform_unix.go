//go:build darwin || linux

package shim

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const scriptPrefix = "#!/bin/sh\n# baton shim v1\n"

func script(binary, name string) ([]byte, error) {
	// shim -> available baton -> scheduler
	//      -> unavailable baton -> remove self from PATH -> real tool
	// Filter every alias before exec so copied wrappers also make progress.
	const fallback = `
_baton_path=
_baton_sep=
_baton_rest=${PATH-}
while :; do
    case $_baton_rest in
        *:*) _baton_dir=${_baton_rest%%:*}; _baton_rest=${_baton_rest#*:}; _baton_more=yes ;;
        *) _baton_dir=$_baton_rest; _baton_more= ;;
    esac
    if [ ! "${_baton_dir:-.}/$_baton_name" -ef "$0" ]; then
        _baton_path=$_baton_path$_baton_sep$_baton_dir
        _baton_sep=:
    fi
    if [ -z "$_baton_more" ]; then
        break
    fi
done
if [ -z "$_baton_sep" ]; then
    printf '%s\n' "baton: $_baton_name: executable not found in PATH" >&2
    exit `
	body := scriptPrefix + "if [ -f " + shellQuote(binary) + " ] && [ -x " + shellQuote(binary) + " ]; then\n" +
		"    exec " + shellQuote(binary) + " shim exec " + shellQuote(name) + " \"$@\"\nfi\n" +
		"_baton_name=" + shellQuote(name) + "\n" + fallback + fmt.Sprint(exitNotFound) + "\nfi\n" +
		"PATH=$_baton_path\nexport PATH\nexec \"$_baton_name\" \"$@\"\n"
	return []byte(body), nil
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
	// A missing file, such as a removed #! interpreter, exits 127 as a
	// queued run does; other exec failures exit 126.
	err := syscall.Exec(real, append([]string{real}, args...), os.Environ())
	if errors.Is(err, os.ErrNotExist) {
		return exitNotFound, err
	}
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
