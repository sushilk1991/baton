# Tasks

One item per iteration, in order. Check it off only when its tests pass.

- [x] Scaffold: go.mod (`github.com/sushilk1991/baton`, go 1.22), `cmd/baton`, `internal/` layout, Makefile with `check`, `.golangci.yml`, CI workflow, LICENSE (Apache-2.0), .gitignore
- [x] Lanes and slots: lock-file semaphores (flock on Unix, LockFileEx on Windows), weights, crash safety; tests a and b
- [x] Queue: tickets, FIFO within priority, aging, dead-ticket cleanup; test c
- [x] Pressure providers: darwin, linux (PSI, meminfo, cgroup v2), windows; gate with hysteresis and max_wait; fake provider; test e
- [x] `baton run`: exec, process group, signal forwarding, exit codes, wait notices, auto lane, identity detection, history JSONL, peak-RSS sampling, learned memory; test d
- [x] Config: TOML, hardware-derived defaults, `config init|show|path|validate`, XDG paths on every OS
- [x] `status` (`--json`, `--watch`, `--line`) and `history`
- [x] `shim install|uninstall|list` with fast paths and recursion guard; test f
- [x] `reap`; test g
- [x] `gc`; test h
- [x] Cache (`--cache-key`, `--output`, `cache ls|prune`); test i
- [x] `browser start|stop|status|endpoint` with idle shutdown and client counting
- [x] `init` (skill, AGENTS.md/CLAUDE.md snippet, optional Claude Code hook, manifest, `--uninstall`) and `mcp`; test j
- [x] Docs: README, docs/*, demo.tape, comparison table, community files
- [x] Distribution: .goreleaser.yaml, install.sh, Homebrew formula template, release workflow (do not publish)
- [x] `scripts/stress.sh` and `docs/benchmarks.md` with real results from this machine

## Review fixes (2026-10-28)

From an independent safety review. Each item starts with a test that fails
on the current code (record the red run), then the fix, then green.

- [x] Reap safety (SPEC 3.4 as amended 2026-10-28): red tests for a headed Chrome with `--remote-debugging-port` and a custom `--user-data-dir` whose parent is PID 1 (never eligible), an ffmpeg with parent PID 1 that baton did not start (never eligible), and a container where PID 1 is not the system init (only baton-started jobs eligible). Fix `internal/reap/reap.go` `eligible()` and `internal/reap/match.go`.
- [x] Nested jobs: a `baton run` or shim inside a running baton job must never queue behind its own lane. `baton run` marks its child's environment; a nested run in a lane the parent already holds runs at once under the parent's slot and is recorded as nested in history. Red test: shims installed, `baton run --lane install -- <fake npm that runs npm again>` finishes; same for the exact command lines in the installed skill and AGENTS snippet (`internal/integration/plan.go`).
- [x] Shim fallback: when the baton binary is missing or not executable, a shim runs the real command (PATH without the shim dir) with its exit code, and `BATON=off` works without baton. Record the path the user installed from, not the symlink-resolved one, so Homebrew/Linuxbrew upgrades keep shims working. Red tests: move the binary after `shim install`; the shimmed tool still runs and passes through exit 0 and exit 3.
- [x] Uninstall after user edits: `init --uninstall` removes exactly baton's own block or entries from the current file when other parts changed, and preserves with a warning only when baton's own block was edited; re-running `init` is idempotent under the same rule. Red tests: install `--claude-hook`, change an unrelated key in settings.json, uninstall succeeds and keeps that key; AGENTS.md snippet with user text added above and below.
- [x] Hook classification: classify each command segment by its executable (after `VAR=x` assignments and `sudo`/`time`/`nice`/`env` wrappers), never by a keyword anywhere in the string. Red tests: `grep -rn ffmpeg src/`, `cat docs/chrome-setup.md`, `rg -l whisper .`, `git commit -m 'Bump llama.cpp pin'` stay unqueued; `ffmpeg -i a.mp4 b.mp4`, `npx remotion render`, `FOO=1 ffmpeg -i a b` are queued. Rewritten commands run under the user's `$SHELL` (else /bin/sh), not bash.
- [x] Job control: Ctrl-Z on an interactive `baton run` stops the child and returns the terminal to the shell, `fg` resumes it, Ctrl-C reaches a child that traps SIGINT, and the lane slot is released when the job ends. Red test through a pty on macOS and Linux.
- [x] Agent label: detect the calling agent from agent-specific variables, not any `GEMINI_*` (an API key is not an agent). Red test: `GEMINI_API_KEY=x baton run -- true` records no gemini agent.

- [x] Final gate: `make check` on macOS, `make check` in `golang:1.27` Docker, golangci-lint v2.15.0 with 0 issues, every README command re-run. The three public-install commands (Homebrew, install.sh, `go install @latest`) need a published release that only the owner can make: record them as pending owner publication, not as a blocker, and check this item off when everything else passes.

## Re-review fixes (2026-10-28, second pass)

From a second independent review of 10e90c6..ea71f25 (reproductions in
the owner's notes). Same rule: red test first (record the run), then the
fix, then green. Order is severity.

- [x] Hook keeps Claude's shell semantics: a rewritten Bash command must behave exactly like the original in Claude Code's own shell (bash or zsh; Claude Code never runs fish, tcsh or dash): shell-snapshot functions still resolve (`nvm use 20 && npm ci`), `cd` carries over to the next command, `&>` keeps its bash meaning, and the original stays a top-level command that Claude's permission checker sees, never a quoted string inside baton's argv (so an allow rule for baton can never approve the wrapped command). A prefix that holds the lane slot for the life of the calling shell fits, e.g. `baton hold --lane install --pid $$ && <original>`; the design is yours, record it in docs/architecture.md. Red tests: with `SHELL=/bin/tcsh` and with `SHELL` unset, `for d in web; do (cd $d && npm ci); done` and `npm ci &> log.txt` give the original's output and exit code; a function defined before the command is callable; the slot is released when the calling shell exits, including after `kill -9` of that shell.
- [x] Background tty jobs: a `baton run` started with `&` whose child reads or configures the terminal behaves as without baton: the shell reports it stopped, `fg` resumes it with the terminal, input reaches it, and `baton status` shows it stopped. Red test through a pty on macOS and Linux: `baton run --lane jc -- sh -c 'stty -echo; read x; echo GOT=$x' &`, `fg`, type `hello` → `GOT=hello`, exit 0, slot released.
- [x] Nested runs across lanes: a run nested inside a baton job never waits on a lane slot; it runs at once under its ancestor's reservation (memory admission still applies, bounded by MaxWait) and is recorded as nested (SPEC 2.1: never blocks a job permanently). Red test: with 1-slot lanes x and y, `baton run --lane x -- sh -c 'sleep 1; baton run --lane y -- echo A'` alongside the mirror (y then x) both finish within 10 s.
- [ ] Job control under any parent: one Ctrl-Z returns the prompt when baton runs under a non-interactive parent (`sh -c "baton run …; echo AFTER"`, as make and npm run do); an outside `kill -STOP` then `kill -CONT` of the child leaves baton waiting, never stopped; Ctrl-Z → `bg` → `fg` → Ctrl-Z stops the child again. Red tests through a pty on macOS and Linux, each compared against the same commands without baton.
- [ ] Hand-removed integration: `init --uninstall` and re-`init` succeed when the user deleted baton's own hook entry or instruction block by hand (treat what is gone as removed, remove the rest; re-`init` adds it back). Red tests: delete the PreToolUse entry from settings.json; delete the CLAUDE.md block lines leaving blank lines.
- [ ] Stable hook path: the hook records the path the user invoked baton by, as shims do, never the versioned target of `/proc/self/exe` or a resolved symlink. Red test on Linux: a symlinked install records the symlink path.
- [ ] Agent labels from runtime markers only: config and credential variables never label a job (`CODEX_HOME=/x`, `CURSOR_API_KEY=x`, `AIDER_MODEL=x` → `terminal`). Red tests for those three.
- [ ] Hook classification gaps: `BATON=off <cmd>` passes through unwrapped; read-only subcommands of heavy tools stay unqueued (`ollama list`, `uv pip list`, `xcodebuild -showBuildSettings`, any tool with `--help` or `--version`); a script is never queued for a keyword in its file name (`./scripts/chrome-debug.sh`). `if …; then npm ci; fi` may stay a documented limit. Red tests for each.
- [ ] Settings bytes: install and uninstall keep the user's key order and formatting outside baton's own entry, and never write `&&` or `>` as `&` or `>`. Red test: a settings.json with custom key order and `&&` in an existing hook command round-trips byte for byte except baton's entry.
- [ ] Reap listing says why: each candidate in `baton reap`'s list names the rule that made it eligible (started by baton, parent dead / adopted orphan of the system init), so the owner sees it before `--yes`. Red test on the listing text.

- [ ] Final gate (second pass): `make check` on macOS; the Linux copy-in Docker command from AGENTS.md; golangci-lint v2.15.0 with 0 issues; every README command re-run. The three public-install commands stay recorded as pending owner publication, not a blocker.
