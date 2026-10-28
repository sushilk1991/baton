package integration

import (
	"path/filepath"
	"strings"
)

const (
	launcherOptions = "-p --package"
	launcherShell   = "-c --call"
)

func shellSegments(command string) [][]string {
	// Read literal words and control operators only; never expand shell text.
	var segments [][]string
	var words []string
	var word strings.Builder
	var quote rune
	started, escaped, comment, redirect := false, false, false, false
	finish := func() {
		if !started {
			return
		}
		if !redirect {
			words = append(words, word.String())
		}
		word.Reset()
		started, redirect = false, false
	}
	chars := []rune(command)
	for index := 0; index < len(chars); index++ {
		char := chars[index]
		if comment && char != '\n' {
			continue
		}
		comment = false
		if escaped {
			if char != '\n' {
				word.WriteRune(char)
				started = true
			}
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
				continue
			}
			word.WriteRune(char)
			continue
		}
		if char == '\'' || char == '"' {
			quote, started = char, true
			continue
		}
		if char == '#' && !started {
			comment = true
			continue
		}
		// Redirection targets (including fd duplication) are never executables.
		if char == '&' && index+1 < len(chars) && chars[index+1] == '>' {
			finish()
			index++
			char = '>'
		}
		if char == '<' || char == '>' {
			if char == '<' && index+1 < len(chars) && chars[index+1] == '<' {
				return nil // Here-doc bodies need a shell parser; leave them opaque.
			}
			if strings.Trim(word.String(), "0123456789") == "" {
				word.Reset()
				started = false
			}
			finish()
			if index+1 < len(chars) && strings.ContainsRune(">&|", chars[index+1]) {
				index++
			}
			redirect = true
			continue
		}
		if strings.ContainsRune(";|&\n()", char) {
			finish()
			segments = append(segments, words)
			words = nil
			continue
		}
		if strings.ContainsRune(" \t\r", char) {
			finish()
			continue
		}
		word.WriteRune(char)
		started = true
	}
	if quote != 0 || escaped {
		return nil
	}
	finish()
	return append(segments, words)
}

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
	for len(words) > 0 {
		if assignment(words[0]) {
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
	if len(words) == 0 {
		return words
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
		if len(target) == 0 {
			return head
		}
		return append(head, filepath.Base(target[0]))
	}
	if (base == "npm" || base == "pnpm" || base == "yarn" || base == "bun") && (verb == "exec" || verb == "dlx" || verb == "x") {
		target := commandOptions(words[2:], launcherOptions, launcherShell)
		if len(target) == 0 {
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
