package config

import (
	"path/filepath"
	"strings"
)

// Option lists that take a value come from each tool's --help (uv 0.12,
// pipx 1.8, pip 25, pnpm 10, bun 1.3) so the next word is never mistaken
// for the launched tool: uv run -w numpy whisper runs whisper.
const (
	launcherOptions = "-p --package"
	launcherShell   = "-c --call"
	npmOptions      = launcherOptions + " -w --workspace --prefix"
	// uv global options, accepted before or after the operation.
	uvGlobals = "--cache-dir --color --allow-insecure-host --directory --project --config-file"
	// uv run, uv tool run, and uvx options before the launched tool.
	uvOptions     = uvGlobals + " --extra --no-extra --group --no-group --only-group --no-editable-package --env-file -w --with --with-editable --with-requirements --package --python-platform --index --default-index -i --index-url --extra-index-url -f --find-links --index-strategy --keyring-provider -P --upgrade-package --upgrade-group --resolution --prerelease --prerelease-package --fork-strategy --exclude-newer --exclude-newer-package --no-sources-package --reinstall-package --link-mode -C --config-setting --config-settings-package --no-build-isolation-package --no-build-package --no-binary-package --refresh-package -p --python --from -c --constraints -b --build-constraints --overrides --torch-backend"
	pipxOptions   = "--python-args --with --spec --python --fetch-python -i --index-url --pip-args --cooldown --backend"
	poetryOptions = "-C --directory -P --project"
	condaOptions  = "-n --name -p --prefix --cwd"
	pipGlobals    = "--python --log --keyring-provider --proxy --retries --timeout --exists-action --trusted-host --cert --client-cert --cache-dir --use-feature --use-deprecated --resume-retries"
	// Python short options that take a value, attached or not: -W ignore, -Xdev.
	pythonValueLetters = "WX"
	pythonHashOption   = "--check-hash-based-pycs"
	helpFlag           = "--help"
	versionFlag        = "--version"
	batonDisabled      = "off"
)

// dispatcherOptions are the options a dispatcher accepts before its
// operation, so uv --directory . run and pnpm --filter web exec still find
// run and exec. Opaque options run inline code: bun -e "...".
type dispatcherOptions struct {
	values string
	opaque string
}

var dispatchers = map[string]dispatcherOptions{
	"uv":     {values: uvGlobals},
	"npm":    {values: npmOptions + " --registry --cache --userconfig --loglevel"},
	"pnpm":   {values: "-C --dir -F --filter --filter-prod --loglevel --reporter --test-pattern --changed-files-ignore-pattern"},
	"yarn":   {values: "--cwd"},
	"bun":    {values: "-r --preload --require --import -F --filter --shell --env-file --cwd -c --config --install --port --conditions --title", opaque: "-e --eval -p --print"},
	"pip":    {values: pipGlobals},
	"pip3":   {values: pipGlobals},
	"pipx":   {},
	"poetry": {values: poetryOptions},
	"conda":  {},
	"cargo":  {values: "--color --config -Z -C"},
	"go":     {values: "-C"},
	"brew":   {},
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
		case "nohup":
			words = commandOptions(words[1:], "", "")
		case "caffeinate":
			words = commandOptions(words[1:], "-t -w", "")
		case "timeout":
			// The first operand is the duration: timeout -k 5 600 ffmpeg.
			words = commandOptions(words[1:], "-s -k --signal --kill-after", "")
			if len(words) > 0 {
				words = words[1:]
			}
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

	base := commandBase(words[0])
	if base == "python" || strings.HasPrefix(base, "python3") {
		// A module keeps its operation too: python -m pip install numpy.
		return append(head, pythonModule(words[1:])...)
	}
	switch base {
	case "npx":
		return launched(head, words[1:], npmOptions, launcherShell)
	case "bunx":
		return launched(head, words[1:], launcherOptions, "")
	case "uvx":
		return launched(head, words[1:], uvOptions, "")
	}

	// Global options precede a dispatcher's operation and never reach
	// matchers: uv --directory . run whisper reads as uv run whisper.
	words = append([]string{words[0]}, dispatcherArgs(base, words[1:])...)
	if len(words) < 2 {
		return head
	}
	if readOnlyCommand(words) {
		return nil
	}
	verb := words[1]
	// yarn workspace NAME runs the rest in one workspace.
	if base == "yarn" && verb == "workspace" && len(words) > 3 {
		words = append([]string{words[0]}, words[3:]...)
		verb = words[1]
	}

	// Known dispatchers expose only their executable/module or operation.
	// Payload arguments never reach built-in regexes, e.g. npm info ffmpeg.
	// Launchers run a named tool, which keeps its own operation:
	// npx cargo build, uv run whisper audio.wav, pnpm exec playwright test.
	packageManager := base == "npm" || base == "pnpm" || base == "yarn" || base == "bun"
	switch {
	case base == "npm" && verb == "exec":
		return launched(append(head, verb), words[2:], npmOptions, launcherShell)
	case packageManager && (verb == "exec" || verb == "dlx" || verb == "x"):
		return launched(append(head, verb), words[2:], launcherOptions, launcherShell)
	case base == "uv" && verb == "run":
		return launched(append(head, verb), words[2:], uvOptions, "")
	case base == "uv" && verb == "tool":
		tool := commandOptions(words[2:], uvGlobals, "")
		if len(tool) > 0 && tool[0] == "run" {
			return launched(append(head, verb, tool[0]), tool[1:], uvOptions, "")
		}
	case base == "pipx" && verb == "run":
		return launched(append(head, verb), words[2:], pipxOptions, "")
	case base == "poetry" && verb == "run":
		return launched(append(head, verb), words[2:], poetryOptions, "")
	case base == "conda" && verb == "run":
		return launched(append(head, verb), words[2:], condaOptions, "")
	}
	// pnpm, yarn, and bun also run a package binary named first, e.g.
	// yarn remotion render; their other verbs (add, test) match nothing.
	if (base == "pnpm" || base == "yarn" || base == "bun") && !strings.HasPrefix(verb, "-") && !scriptCommand(verb) {
		return append(head, filepath.Base(verb))
	}
	switch base {
	case "npm", "pnpm", "yarn", "bun", "pip", "pip3", "uv", "brew", "cargo", "go":
		if verb == "i" || verb == "install" || verb == "ci" || verb == "pip" || verb == "sync" || verb == "build" {
			return append(head, verb)
		}
	}
	return head
}

// dispatcherArgs drops a known dispatcher's global options and returns
// the operation and its arguments: cargo +nightly -Z x build -> build.
func dispatcherArgs(base string, args []string) []string {
	options, known := dispatchers[base]
	if !known {
		return args
	}

	// rustup picks a toolchain before cargo's own options.
	if base == "cargo" && len(args) > 0 && strings.HasPrefix(args[0], "+") {
		args = args[1:]
	}
	return commandOptions(args, options.values, options.opaque)
}

// launched appends the head of the tool a launcher runs, after the
// launcher's own options: npx -y remotion render -> npx remotion. The
// tool may be wrapped too: uv run nice -n 10 whisper -> uv run whisper.
func launched(head, rest []string, values, opaque string) []string {
	target := unwrapCommand(commandOptions(rest, values, opaque))
	if len(target) == 0 || readOnlyCommand(target) || scriptCommand(target[0]) {
		return head
	}
	return append(head, commandHead(target)...)
}

// pythonModule reads interpreter options as Python's getopt does and
// returns -m plus the module's own head: python3 -u -m mlx_whisper a.wav,
// -um mlx_whisper, and -mmlx_whisper all give -m mlx_whisper.
// A -c command, a script, stdin, or no -m yields nothing.
func pythonModule(args []string) []string {
	for index := 0; index < len(args); index++ {
		option := args[index]
		switch {
		case option == pythonHashOption:
			index++
			continue
		case option == "-" || option == "--" || !strings.HasPrefix(option, "-"):
			return nil
		case strings.HasPrefix(option, "--"):
			continue
		}

		// A cluster ends at the first letter that takes a value, which is
		// the rest of the word or the next one: -Wignore, -um mlx_whisper.
	cluster:
		for at := 1; at < len(option); at++ {
			letter, value := option[at], option[at+1:]
			switch {
			case letter == 'c':
				return nil
			case letter == 'm':
				rest := args[index+1:]
				if value == "" {
					if len(rest) == 0 {
						return nil
					}
					value, rest = rest[0], rest[1:]
				}
				return append([]string{"-m"}, commandHead(append([]string{value}, rest...))...)
			case strings.IndexByte(pythonValueLetters, letter) >= 0:
				if value == "" {
					index++
				}
				break cluster
			}
		}
	}
	return nil
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
