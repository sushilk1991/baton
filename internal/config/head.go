package config

import (
	"path/filepath"
	"strings"
)

// Option lists that take a value come from each tool's --help (uv 0.12,
// pipx 1.8, pip 25, pnpm 10.28, bun 1.3, micromamba's run.cpp) so the next
// word is never mistaken for the launched tool: uv run -w numpy whisper.
const (
	launcherOptions = "-p --package"
	launcherShell   = "-c --call"
	// bunx and bun x options that take a value (bun.com/docs/pm/bunx).
	bunxOptions = launcherOptions + " --cwd --env-file --config"
	// npx's -p is --package; npm exec's -p is the --parseable flag.
	npxOptions = launcherOptions + " " + npmConfigValues
	npmOptions = "--package " + npmConfigValues
	// pnpm options that take a value, before or after exec, dlx, and run,
	// including npm's long config options (--prefix) and pnpm's directory
	// settings; -w is pnpm's --workspace-root flag, not npm's --workspace.
	pnpmOptions = "-C --dir -F --filter --filter-prod --loglevel --reporter --resume-from --workspace-concurrency --test-pattern --changed-files-ignore-pattern --allow-build --package --store-dir --virtual-store-dir --modules-dir --lockfile-dir --global-dir --global-bin-dir --cache-dir --state-dir --node-linker --package-import-method --child-concurrency --network-concurrency --resolution-mode --use-node-version " + npmLongValues
	pnpmShell   = "-c --shell-mode"
	// npm, pnpm, and yarn print their version or help for these shorthands.
	npmQueries = "-v -h -?"
	// Yarn Classic global options with a required value (src/cli/index.js).
	yarnGlobals = "--cwd --use-yarnrc --link-folder --global-folder --modules-folder --preferred-cache-folder --cache-folder --mutex --proxy --https-proxy --registry --network-concurrency --network-timeout --otp"
	// bun options that take a value; --inspect's value is only attached.
	bunOptions = "--elide-lines -F --filter --shell -r --preload --require --import --cpu-prof-name --cpu-prof-dir --cpu-prof-interval --heap-prof-name --heap-prof-dir --install --port --conditions --fetch-preconnect --max-http-header-size --dns-result-order --title --unhandled-rejections --console-depth --user-agent --cron-title --cron-period --main-fields --extension-order --tsconfig-override -d --define --drop --feature -l --loader --jsx-factory --jsx-fragment --jsx-import-source --jsx-runtime --env-file --cwd -c --config"
	// Inline code, or - for a script read from stdin: bun run - < x.ts.
	bunInline = "-e --eval -p --print -"
	// Characters that make one word a shell body: yarn exec 'printf chrome'.
	shellSyntax = " \t\n;|&$`<>()"
	// yarn workspaces foreach options that take a value (yarn 4 docs).
	yarnForeachOptions = "--from -j --jobs --include --exclude"
	// uv global options, accepted before or after the operation.
	uvGlobals = "--cache-dir --color --allow-insecure-host --directory --project --config-file"
	// uv run, uv tool run, and uvx options before the launched tool.
	uvOptions     = uvGlobals + " --extra --no-extra --group --no-group --only-group --no-editable-package --env-file -w --with --with-editable --with-requirements --package --python-platform --index --default-index -i --index-url --extra-index-url -f --find-links --index-strategy --keyring-provider -P --upgrade-package --upgrade-group --resolution --prerelease --prerelease-package --fork-strategy --exclude-newer --exclude-newer-package --no-sources-package --reinstall-package --link-mode -C --config-setting --config-settings-package --no-build-isolation-package --no-build-package --no-binary-package --refresh-package -p --python --from -c --constraints -b --build-constraints --overrides --torch-backend"
	pipxOptions   = "--python-args --with --spec --python --fetch-python -i --index-url --pip-args --cooldown --backend"
	poetryOptions = "-C --directory -P --project"
	condaOptions  = "-n --name -p --prefix --cwd"
	// mamba 2 and micromamba root options (umamba.cpp, common_options.cpp),
	// accepted before run, plus run's own (run.cpp).
	mambaGlobals = "-r --root-prefix --relocate-prefix -n --name -p --prefix --log-level --rc-file"
	mambaOptions = condaOptions + " " + mambaGlobals + " -a --attach -e --env --label"
	// uv run -s and --script read the target as a script, whatever its
	// name, and - reads one from stdin.
	uvScript = "-s --script --gui-script - " + uvQueries
	// uv and uvx print their version or help for these.
	uvQueries = "-V -h"
	// maxNesting bounds launchers inside launchers, so a request holding
	// npx 120,000 times stays linear: nohup uv run npx playwright is 3.
	maxNesting = 8
	pipGlobals = "--python --log --keyring-provider --proxy --retries --timeout --exists-action --trusted-host --cert --client-cert --cache-dir --use-feature --use-deprecated --resume-retries"
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
	"uv":         {values: uvGlobals, opaque: uvQueries},
	"npm":        {values: npmOptions},
	"pnpm":       {values: pnpmOptions, opaque: pnpmShell},
	"yarn":       {values: yarnGlobals},
	"bun":        {values: bunOptions, opaque: bunInline},
	"pip":        {values: pipGlobals},
	"pip3":       {values: pipGlobals},
	"pipx":       {},
	"poetry":     {values: poetryOptions},
	"conda":      {},
	"mamba":      {values: mambaGlobals},
	"micromamba": {values: mambaGlobals},
	"cargo":      {values: "--color --config -Z -C"},
	"go":         {values: "-C"},
	"brew":       {},
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
			// macOS env adds -P utilpath and -L/-U user.
			words = commandOptions(words[1:], "-u -C -P -L -U --unset --chdir --argv0", "-S --split-string")
		case "nohup":
			words = commandOptions(words[1:], "", "")
		case "cross-env":
			// The loop skips its assignments: cross-env A=1 remotion.
			words = words[1:]
		case "caffeinate":
			words = commandOptions(words[1:], "-t -w", "")
		case "timeout", "gtimeout":
			// The first operand is the duration: timeout -k 5 600 ffmpeg.
			words = commandOptions(words[1:], "-s -k --signal --kill-after", "")
			if len(words) > 0 {
				words = words[1:]
			}
		case "xvfb-run":
			words = commandOptions(words[1:], "-n --server-num -s --server-args -f --auth-file -e --error-file -p --xauth-protocol -w --wait", "")
		case "stdbuf":
			words = commandOptions(words[1:], "-i -o -e --input --output --error", "")
		case "ionice":
			words = commandOptions(words[1:], "-c -n -p -P -u --class --classdata --pid --pgid --uid", "")
		case "arch":
			// arch options are whole words: -arm64e, -arch x86_64, -d NAME.
			words = skipOptions(words[1:], "-arch -d -e", "", wordOptions)
		case "exec":
			words = commandOptions(words[1:], "-a", "")
		case "command":
			// command -v ffmpeg only looks the tool up.
			words = commandOptions(words[1:], "", "-v -V")
		default:
			if batonValue == batonDisabled {
				return nil
			}
			return words
		}
	}
	return nil
}

// optionStyle says how a tool's single-dash options combine.
type optionStyle int

const (
	// posixOptions cluster short letters: timeout -vk 5 is -v -k 5.
	posixOptions optionStyle = iota
	// wordOptions are whole words: arch -arm64e is one option.
	wordOptions
)

func commandOptions(words []string, values, opaque string) []string {
	return skipOptions(words, values, opaque, posixOptions)
}

// skipOptions consumes a tool's own options and returns what follows: the
// first other word on, or the words after --. Values listed in values are
// consumed with their option; an opaque option runs a shell body or inline
// code, and help or version runs nothing, so both return nil.
func skipOptions(words []string, values, opaque string, style optionStyle) []string {
	for len(words) > 0 {
		option := words[0]
		if option == "--" {
			return words[1:]
		}
		if !strings.HasPrefix(option, "-") {
			return words
		}
		if option == helpFlag || option == versionFlag {
			return nil
		}
		name, _, attached := strings.Cut(option, "=")
		if listed(opaque, name) {
			return nil
		}
		words = words[1:]
		if listed(values, name) {
			if !attached && len(words) > 0 {
				words = words[1:]
			}
			continue
		}
		if style == wordOptions || strings.HasPrefix(option, "--") {
			continue
		}

		// A short cluster ends at its first letter that takes a value, which
		// is the rest of the word or the next one: -vk 5, -la name, -Sbody,
		// -e'x=1' (an = there is part of the body, not an attachment).
		for at := 1; at < len(option); at++ {
			letter := "-" + option[at:at+1]
			if listed(opaque, letter) {
				return nil
			}
			if listed(values, letter) {
				if at+1 == len(option) && len(words) > 0 {
					words = words[1:]
				}
				break
			}
		}
	}
	return nil
}

// npmCommand skips npm's options as its parser, nopt, does, and pnpm's and
// Yarn Classic's, which read options the same way: an option
// listed in values takes the next word, a Boolean one takes an explicit
// value such as false (npm exec --yes false -- playwright), and a cluster
// of shorthands ends at one that takes a value (-yw web).
func npmCommand(words []string, values, opaque string) []string {
	for len(words) > 0 {
		option := words[0]
		if option == "--" {
			return words[1:]
		}
		if !strings.HasPrefix(option, "-") {
			return words
		}
		if option == helpFlag || option == versionFlag || listed(npmQueries, option) {
			return nil
		}
		name, _, attached := strings.Cut(option, "=")
		if listed(opaque, name) {
			return nil
		}
		words = words[1:]
		if attached || len(words) == 0 {
			continue
		}
		next := words[0]
		if listed(values, name) || listed(npmBooleanValues, next) || listed(npmBooleanLiterals, name+":"+next) {
			words = words[1:]
			continue
		}
		if strings.HasPrefix(option, "--") {
			continue
		}
		for at := 1; at < len(option); at++ {
			letter := "-" + option[at:at+1]
			if listed(opaque, letter) {
				return nil
			}
			if listed(values, letter) {
				if at+1 == len(option) {
					words = words[1:]
				}
				break
			}
		}
	}
	return nil
}

func listed(list, word string) bool {
	return strings.Contains(" "+list+" ", " "+word+" ")
}

func commandHead(words []string) []string {
	return nestedHead(words, 0)
}

// nestedHead is commandHead for a tool that depth launchers started.
func nestedHead(words []string, depth int) []string {
	// Read-only operations and script filenames do not need reservations.
	if len(words) == 0 || depth > maxNesting || readOnlyCommand(words) || scriptCommand(words[0]) {
		return nil
	}

	// Directory names are not tool names, e.g. /opt/chrome-tools/grep, but
	// a scoped npm package keeps its scope: npx @playwright/test.
	name := filepath.Base(words[0])
	if strings.HasPrefix(words[0], "@") {
		name = words[0]
	}
	head := []string{name}
	if len(words) < 2 {
		return head
	}

	base := commandBase(words[0])
	if base == "python" || strings.HasPrefix(base, "python3") {
		// A module keeps its operation too: python -m pip install numpy.
		return append(head, pythonModule(words[1:], depth)...)
	}
	switch base {
	case "npx":
		return launched(head, npmCommand(words[1:], npxOptions, launcherShell), depth)
	case "bunx":
		return launched(head, commandOptions(words[1:], bunxOptions, ""), depth)
	case "uvx":
		return launched(head, commandOptions(words[1:], uvOptions, uvQueries), depth)
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
	// yarn workspace NAME runs the rest in one workspace, and yarn
	// workspaces foreach [options] in each: foreach -A run playwright.
	if base == "yarn" && verb == "workspace" && len(words) > 3 {
		words = append([]string{words[0]}, words[3:]...)
		verb = words[1]
	}
	if base == "yarn" && verb == "workspaces" && len(words) > 2 && words[2] == "foreach" {
		words = append([]string{words[0]}, commandOptions(words[3:], yarnForeachOptions, "")...)
		if len(words) < 2 {
			return head
		}
		verb = words[1]
	}

	// Known dispatchers expose only their executable/module or operation.
	// Payload arguments never reach built-in regexes, e.g. npm info ffmpeg.
	// Launchers run a named tool, which keeps its own operation:
	// npx cargo build, uv run whisper audio.wav, pnpm exec playwright test.
	// The script or package binary after run is the operation:
	// yarn run --binaries-only playwright test -> yarn run playwright.
	// yarn exec joins its words into a shell script: a single word holding
	// shell syntax stays opaque, yarn exec remotion render is a command.
	launch := append(head, verb)
	rest := words[2:]
	run := verb == "run" || verb == "run-script"
	switch {
	case base == "npm" && (verb == "exec" || verb == "x"):
		return launched(launch, npmCommand(rest, npmOptions, launcherShell), depth)
	case base == "npm" && run:
		return launched(launch, npmCommand(rest, npmOptions, ""), depth)
	case base == "pnpm" && (verb == "exec" || verb == "dlx"):
		return launched(launch, npmCommand(rest, pnpmOptions, pnpmShell), depth)
	case base == "pnpm" && run:
		return launched(launch, npmCommand(rest, pnpmOptions, ""), depth)
	case base == "yarn" && verb == "exec":
		// Yarn Classic reads its global options after exec too:
		// yarn exec --cwd web playwright test.
		rest = npmCommand(rest, yarnGlobals, "")
		if len(rest) > 0 && strings.ContainsAny(rest[0], shellSyntax) {
			return launch
		}
		return launched(launch, rest, depth)
	case base == "yarn" && verb == "dlx":
		return launched(launch, commandOptions(rest, launcherOptions, ""), depth)
	case base == "yarn" && run:
		return launched(launch, commandOptions(rest, "--require", ""), depth)
	case base == "bun" && verb == "x":
		return launched(launch, commandOptions(rest, bunxOptions, ""), depth)
	case base == "bun" && run:
		return launched(launch, commandOptions(rest, bunOptions, bunInline), depth)
	case base == "uv" && verb == "run":
		return launched(launch, commandOptions(rest, uvOptions, uvScript), depth)
	case base == "uv" && verb == "tool":
		tool := commandOptions(rest, uvGlobals, "")
		if len(tool) > 0 && tool[0] == "run" {
			return launched(append(launch, tool[0]), commandOptions(tool[1:], uvOptions, uvQueries), depth)
		}
	case base == "pipx" && verb == "run":
		return launched(launch, commandOptions(rest, pipxOptions, ""), depth)
	case base == "poetry" && verb == "run":
		return launched(launch, commandOptions(rest, poetryOptions, ""), depth)
	case base == "conda" && verb == "run":
		return launched(launch, commandOptions(rest, condaOptions, ""), depth)
	case (base == "mamba" || base == "micromamba") && verb == "run":
		return launched(launch, commandOptions(rest, mambaOptions, ""), depth)
	}
	// pnpm, yarn, and bun also run a package binary named first, which is
	// classified like any command: yarn remotion render, pnpm cargo build.
	// Their own verbs keep only the verb: yarn add playwright -> yarn add.
	if (base == "pnpm" || base == "yarn" || base == "bun") && !strings.HasPrefix(verb, "-") && !scriptCommand(verb) {
		return launched(head, words[1:], depth)
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
	if base == "npm" || base == "pnpm" || base == "yarn" {
		return npmCommand(args, options.values, options.opaque)
	}
	if !known {
		return args
	}

	// rustup picks a toolchain before cargo's own options.
	if base == "cargo" && len(args) > 0 && strings.HasPrefix(args[0], "+") {
		args = args[1:]
	}
	return commandOptions(args, options.values, options.opaque)
}

// launched appends the head of the tool a launcher runs, given the words
// after the launcher's own options: npx -y remotion render -> npx remotion.
// The tool may be wrapped too: uv run nice -n 10 whisper -> uv run whisper.
func launched(head, target []string, depth int) []string {
	target = unwrapCommand(target)
	if len(target) == 0 || readOnlyCommand(target) || scriptCommand(target[0]) {
		return head
	}
	return append(head, nestedHead(target, depth+1)...)
}

// pythonModule reads interpreter options as Python's getopt does and
// returns -m plus the module's own head: python3 -u -m mlx_whisper a.wav,
// -um mlx_whisper, and -mmlx_whisper all give -m mlx_whisper.
// A -c command, a script, stdin, or no -m yields nothing.
func pythonModule(args []string, depth int) []string {
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
			// -c runs a command; -V and -h only print.
			case letter == 'c' || letter == 'V' || letter == 'h':
				return nil
			case letter == 'm':
				rest := args[index+1:]
				if value == "" {
					if len(rest) == 0 {
						return nil
					}
					value, rest = rest[0], rest[1:]
				}
				return append([]string{"-m"}, nestedHead(append([]string{value}, rest...), depth+1)...)
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
	if base == "uv" && words[1] == "pip" {
		// Options may precede the query: uv pip -q list.
		query := commandOptions(words[2:], uvGlobals, "")
		if len(query) == 0 {
			return false
		}
		switch query[0] {
		case "list", "show", "freeze", "check", "tree":
			return true
		}
	}
	return false
}

func scriptCommand(word string) bool {
	// A script's name says nothing about its work; do not inspect its contents.
	// - reads one from stdin, and a URL names one: uv run https://x/a.py?raw=1.
	if word == "-" || strings.Contains(word, "://") {
		return true
	}
	switch strings.ToLower(filepath.Ext(word)) {
	case ".sh", ".bash", ".zsh", ".fish", ".ksh", ".csh", ".tcsh", ".py", ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts", ".jsx", ".tsx", ".rb", ".pl", ".ps1", ".bat", ".cmd":
		return true
	}
	return false
}
