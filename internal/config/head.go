package config

import (
	"path/filepath"
	"strings"
)

const (
	launcherOptions = "-p --package"
	launcherShell   = "-c --call"
	helpFlag        = "--help"
	versionFlag     = "--version"
	batonDisabled   = "off"
)

func commandBase(word string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(word)), ".exe")
}

func assignment(word string) bool {
	name, _, found := strings.Cut(word, "=")
	if !found || name == "" {
		return false
	}
	for index, char := range name {
		if char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || index > 0 && char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func unwrapCommand(words []string) []string {
	// Wrapper option values may name heavy tools too: sudo -u ffmpeg true.
	batonValue := ""
	for len(words) > 0 {
		if assignment(words[0]) {
			// The last literal assignment wins, including env BATON=off.
			name, value, _ := strings.Cut(words[0], "=")
			if name == "BATON" {
				batonValue = value
			}
			words = words[1:]
			continue
		}
		switch commandBase(words[0]) {
		case "sudo":
			words = commandOptions(words[1:], "-u -g -h -p -C -T -R -D -r -t --user --group --host --prompt --close-from --command-timeout --chroot --chdir --role --type", "-s -i --shell --login")
		case "time":
			words = commandOptions(words[1:], "-f -o --format --output", "")
		case "nice":
			words = commandOptions(words[1:], "-n --adjustment", "")
		case "env":
			words = commandOptions(words[1:], "-u -C --unset --chdir --argv0", "-S --split-string")
		default:
			if batonValue == batonDisabled {
				return nil
			}
			return words
		}
	}
	return nil
}

func commandOptions(words []string, values, opaque string) []string {
	// Consume only wrapper options; stop at -- or the actual executable.
	for len(words) > 0 {
		option := words[0]
		if option == "--" {
			return words[1:]
		}
		if !strings.HasPrefix(option, "-") {
			return words
		}
		name, _, attached := strings.Cut(option, "=")
		if strings.Contains(" "+opaque+" ", " "+name+" ") {
			return nil
		}
		words = words[1:]
		if !attached && strings.Contains(" "+values+" ", " "+name+" ") && len(words) > 0 {
			words = words[1:]
		}
	}
	return nil
}

func commandHead(words []string) []string {
	// Read-only operations and script filenames do not need reservations.
	if len(words) == 0 || readOnlyCommand(words) || scriptCommand(words[0]) {
		return nil
	}

	// Directory names are not tool names, e.g. /opt/chrome-tools/grep.
	head := []string{filepath.Base(words[0])}
	if len(words) < 2 {
		return head
	}

	// Known dispatchers expose only their executable/module or operation.
	// Payload arguments never enter config regexes, e.g. npm info ffmpeg.
	base, verb := commandBase(words[0]), words[1]
	if base == "npx" {
		target := commandOptions(words[1:], launcherOptions, launcherShell)
		if len(target) == 0 || readOnlyCommand(target) || scriptCommand(target[0]) {
			return head
		}
		return append(head, filepath.Base(target[0]))
	}
	if (base == "npm" || base == "pnpm" || base == "yarn" || base == "bun") && (verb == "exec" || verb == "dlx" || verb == "x") {
		target := commandOptions(words[2:], launcherOptions, launcherShell)
		if len(target) == 0 || readOnlyCommand(target) || scriptCommand(target[0]) {
			return head
		}
		return append(head, verb, filepath.Base(target[0]))
	}
	if (base == "python" || strings.HasPrefix(base, "python3")) && verb == "-m" && len(words) > 2 {
		return append(head, verb, words[2])
	}
	switch base {
	case "npm", "pnpm", "yarn", "bun", "pip", "pip3", "uv", "brew", "cargo", "go":
		if verb == "i" || verb == "install" || verb == "ci" || verb == "pip" || verb == "sync" || verb == "build" {
			return append(head, verb)
		}
	}
	return head
}

func readOnlyCommand(words []string) bool {
	// Inspect literal flags only, never text embedded in an argument.
	base := commandBase(words[0])
	for _, argument := range words[1:] {
		if argument == helpFlag || argument == versionFlag {
			return true
		}
		if base == "xcodebuild" {
			switch argument {
			case "-help", "-version", "-list", "-showsdks", "-showdestinations", "-showBuildSettings":
				return true
			}
		}
	}
	if len(words) < 2 {
		return false
	}

	// Only the tool's operation selects these paths; package/model names do not.
	if base == "ollama" {
		switch words[1] {
		case "list", "ls", "ps", "show", "help":
			return true
		}
	}
	if base == "uv" && words[1] == "pip" && len(words) > 2 {
		switch words[2] {
		case "list", "show", "freeze", "check", "tree":
			return true
		}
	}
	return false
}

func scriptCommand(word string) bool {
	// A script's name says nothing about its work; do not inspect its contents.
	switch strings.ToLower(filepath.Ext(word)) {
	case ".sh", ".bash", ".zsh", ".fish", ".ksh", ".csh", ".tcsh", ".py", ".js", ".mjs", ".cjs", ".ts", ".rb", ".pl", ".ps1", ".bat", ".cmd":
		return true
	}
	return false
}
