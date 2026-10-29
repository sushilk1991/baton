package shim

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const scriptPrefix = "@echo off\r\nrem baton shim v1\r\n"

func script(binary, name string) ([]byte, error) {
	// Batch expansion cannot preserve these characters in a literal command.
	// Fail rather than generating a wrapper with different command semantics.
	if strings.ContainsAny(binary+name, "%!\"\r\n") {
		return nil, fmt.Errorf("unsupported batch shim path or command %q", name)
	}
	body := scriptPrefix + "setlocal DisableDelayedExpansion\r\n\"" + binary + "\" shim exec \"" + name + "\" %*\r\nexit /b %errorlevel%\r\n"
	return []byte(body), nil
}

func shimName(name string) string {
	return name + ".cmd"
}

func candidates(path string) []string {
	if filepath.Ext(path) != "" {
		return []string{path}
	}
	extensions := strings.Split(os.Getenv("PATHEXT"), ";")
	if len(extensions) == 0 || extensions[0] == "" {
		extensions = []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	paths := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		paths = append(paths, path+extension)
	}
	return paths
}

func isExecutable(info os.FileInfo) bool {
	return info.Mode().IsRegular()
}

func direct(real string, args []string) (int, error) {
	command := exec.Command(real, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := command.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return exitNotFound, err
	}
	if err != nil {
		return exitNotExecutable, err
	}
	return 0, nil
}

func guidance(dir string, out io.Writer) error {
	shell := strings.ToLower(filepath.Base(os.Getenv("SHELL")))
	if shell == "pwsh.exe" || shell == "powershell.exe" || shell == "pwsh" || shell == "powershell" {
		_, err := fmt.Fprintf(out, "shell: %s\nAdd to your shell config:\n$env:Path = '%s;' + $env:Path\n", shell, strings.ReplaceAll(dir, "'", "''"))
		return err
	}
	_, err := fmt.Fprintf(out, "shell: cmd\nAdd to your shell config:\nset \"PATH=%s;%%PATH%%\"\n", dir)
	return err
}
