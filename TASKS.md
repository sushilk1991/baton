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
- [ ] Job control: Ctrl-Z on an interactive `baton run` stops the child and returns the terminal to the shell, `fg` resumes it, Ctrl-C reaches a child that traps SIGINT, and the lane slot is released when the job ends. Red test through a pty on macOS and Linux.
- [ ] Agent label: detect the calling agent from agent-specific variables, not any `GEMINI_*` (an API key is not an agent). Red test: `GEMINI_API_KEY=x baton run -- true` records no gemini agent.

- [ ] Final gate: `make check` on macOS, `make check` in `golang:1.27` Docker, golangci-lint v2.15.0 with 0 issues, every README command re-run. The three public-install commands (Homebrew, install.sh, `go install @latest`) need a published release that only the owner can make: record them as pending owner publication, not as a blocker, and check this item off when everything else passes.
