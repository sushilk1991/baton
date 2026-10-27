# baton: product and engineering spec

> One machine, many agents, no swap storms.
> `baton` queues heavy jobs (headless-browser renders, video encodes, local ML,
> package installs, builds) from every AI coding agent and terminal on a
> machine, and starts each one only when there is room.

This file is the immutable goal. `TASKS.md` is the work queue. `AGENTS.md`
holds the working rules.

## 1. The problem (why this exists)

Developers now run several AI coding agents at once (Claude Code, Codex,
Cursor, Gemini CLI, Aider, OpenCode, Goose…), each in its own terminal and
repo. Each agent launches heavy work without knowing about the others:

- every renderer starts its own headless Chrome (Remotion, HyperFrames,
  Playwright, Puppeteer, chrome-devtools-mcp bridges);
- `ffmpeg -preset slow` encodes take every core, two at once halve each other;
- local ML (Whisper, mlx, Ollama, llama.cpp) loads multi-GB models per run;
- two agents run `npm install` / `pip install` in parallel and duplicate
  gigabytes;
- crashed agents leave orphaned headless browsers and bridges behind;
- intermediates (frame folders, render slices, superseded renders) pile up.

Result: swap storms, macOS jetsam kills, the Linux OOM killer, a laptop that
stops responding. The author's 36 GB Mac had 15.8 GB of its 16 GB swap in
use. Nothing coordinates these processes, because they are launched by
different programs that never talk to each other.

## 2. Principles

1. **Queues, never refuses.** baton delays heavy work; it never blocks a
   job permanently and never silently drops one.
2. **Crash-safe without a daemon.** Slots are kernel-held advisory locks on
   files; a process that dies releases its slot automatically. No background
   service is required for the core (`run`, `status`).
3. **Zero config to start, everything configurable.** Defaults are derived
   from the machine (RAM, cores).
4. **Transparent.** Shims make existing commands go through baton without
   any agent or human changing habits. `BATON=off` bypasses everything.
5. **Agent-native.** One command wires baton into every installed agent
   (skill, instructions, hook, MCP). Reversible.
6. **Observable.** Anyone (human or agent) can see what runs, what waits,
   who started it and the machine's pressure.
7. **Portable.** macOS and Linux are first-class (amd64 + arm64, inside
   containers too). Windows builds and its core works (best effort).
8. **One static binary.** Go, `CGO_ENABLED=0`, minimal dependencies, no
   telemetry, no network access except `baton browser` talking to a local
   Chrome.

## 3. Features

### 3.1 `baton run` (the core)

```
baton run [--lane L|auto] [--label S] [--priority high|normal|low]
          [--weight N] [--mem 4G] [--max-wait 30m] [--quiet]
          [--cache-key GLOBS --output PATH] -- <command> [args...]
```

- Blocks until admitted, then execs the command with stdio passed through
  untouched. Exit code passes through. SIGINT/SIGTERM/SIGHUP forwarded to the
  child's process group; the slot is released on any exit path.
- While waiting it prints one-line notices to stderr only, e.g.
  `baton: waiting for render (2 ahead · pressure warn · swap 14.9/16 GB)`;
  `--quiet` silences them.
- **Lanes** are counting semaphores (default lanes: `render`, `encode`,
  `ml`, `install`, `build`, plus user-defined). A lane has N slots; a job may
  take `--weight` slots.
- **Auto lane**: when `--lane` is omitted (or `auto`), argv is matched against
  config regex rules (e.g. `ffmpeg` → encode; `remotion|hyperframes|
  playwright|puppeteer|chrome|chromium` → render; `whisper|mlx|ollama|
  llama` → ml; `(npm|pnpm|yarn|bun) (i|install|ci)|pip install|uv (pip|sync)|
  brew install|cargo install` → install; `xcodebuild|gradle|cargo build|go
  build` → build). No match → run immediately (no lane).
- **Fair queue**: FIFO within priority, high before normal before low,
  starvation-free (aging). Tickets of dead processes are cleaned up.
- **Admission control (memory gate)**: heavy lanes additionally wait while
  the machine is under memory pressure, with hysteresis:
  - macOS: `kern.memorystatus_vm_pressure_level` (sysctl), `vm.swapusage`,
    free/inactive pages (`vm_stat` or Mach host statistics without cgo).
  - Linux: PSI `/proc/pressure/memory` (`some avg10`), `/proc/meminfo`
    (`MemAvailable`, `SwapFree`), cgroup v2 `memory.max`/`memory.current`
    when running in a container.
  - Windows: `GlobalMemoryStatusEx` (best effort).
  - A job declaring (or predicted to need) `--mem X` waits until available
    memory exceeds X plus a safety margin.
  - `max_wait` per lane: after it, the job is admitted with a loud warning,
    never starved.
- **Learned memory**: every finished job records peak RSS of its process
  tree (sampled) against a command signature; the next run of the same
  signature uses an EWMA of it as its `--mem` prediction.
- **Cache**: `--cache-key 'src/**,audio/*.wav' --output out/master.mp4`
  hashes inputs + argv + allowlisted env; on a hit, restores the output
  (hardlink, else copy) and skips running; on a miss, runs and stores.
- Identity: each job records the agent (detected from env such as
  `CLAUDECODE`, `CODEX_*`, `CURSOR_*`, `TERM_PROGRAM`, or `--agent`), the cwd
  and its git repo root, the label and the user.

### 3.2 `baton status`

`baton status [--json] [--watch] [--line]`: lanes with used/total slots,
running jobs (label, agent, repo, runtime, RSS), queue with positions and
wait times, RAM, swap, pressure level. `--line` prints a short string for
status lines (Claude Code statusline, tmux, starship), e.g.
`⏵ render 1/1 · encode 2/2 · 3 waiting · mem ok`.

### 3.3 `baton history`

JSONL log (`baton history [--json] [--since 24h] [--lane L]`): start, end,
wait time, exit code, peak RSS, lane, label, agent, repo, cache hit.
Rotated by size.

### 3.4 `baton reap`

Finds orphaned heavy processes: headless Chrome/Chromium/chrome-headless-
shell (must carry `--headless` or `--remote-debugging-*`), chrome-devtools-
mcp, Playwright/Puppeteer drivers, Remotion/HyperFrames renderers, ffmpeg,
whose launching agent is gone (reparented to init/launchd, or the recorded
parent PID is dead) and older than `--older-than` (default 2h). Lists by
default; `--yes` sends SIGTERM, then SIGKILL after a grace period. Never
touches the user's interactive browser (no `--headless`, default profile).

### 3.5 `baton gc`

Configurable regenerable-intermediate patterns under configured roots
(e.g. `**/out/slice-*.mp4`, `**/.remotion/**`, frame dumps) older than N
days. Dry-run by default with sizes; `--yes` deletes. Never follows
symlinks, never leaves the configured roots.

### 3.6 `baton shim`

`baton shim install [cmd ...] | uninstall | list`: tiny shims in
`$XDG_DATA_HOME/baton/shims` that exec `baton run --lane auto -- <real
binary> args`. The real binary is resolved by searching PATH minus the shim
dir; recursion is detected; `BATON=off` makes a shim exec the real binary
directly. Per-command fast-path rules (e.g. `ffmpeg` with `-f null` or
`-version`, `ffprobe`) bypass the queue. Prints the PATH line to add and
detects the user's shell.

### 3.7 `baton browser`

One shared warm headless browser: `start | stop | status | endpoint`.
Finds Chrome, Chromium, Edge or chrome-headless-shell on each OS (and the
Playwright cache), launches it with configurable flags (GPU flags on by
default where supported), exposes the CDP endpoint (`baton browser
endpoint` prints the `ws://` URL), counts clients, shuts down after an idle
period. Docs show Playwright `connectOverCDP`, Puppeteer `connect`,
chrome-devtools-mcp `--browserUrl`, and env vars for tools that accept them.

### 3.8 Agent integration

- `baton init [--yes] [--dry-run] [--uninstall]` detects installed agents
  (Claude Code `~/.claude`, Codex `~/.codex`, Cursor, Gemini CLI, Aider,
  OpenCode, Goose, any `~/.agents/skills` user) and installs:
  - a skill in Agent Skills format (`SKILL.md` with a sharp description) at
    `~/.agents/skills/baton/`, linked into each agent's skills dir;
  - an instructions snippet between `<!-- baton:start -->` /
    `<!-- baton:end -->` markers in the global `AGENTS.md` / `CLAUDE.md`
    (idempotent, removable);
  - optionally a Claude Code `PreToolUse` hook that wraps heavy Bash
    commands in `baton run` (or, where rewriting is unsupported, blocks with
    the exact `baton run` command to use).
  Every change is listed; `--uninstall` reverses exactly what was written
  (a manifest records it).
- `baton mcp`: an MCP server over stdio exposing read tools (`status`,
  `history`, `reap_candidates`, `gc_candidates`, `explain_lane`) so agents
  can check the machine before starting heavy work.

### 3.9 Config

`$XDG_CONFIG_HOME/baton/config.toml` (default `~/.config/baton/config.toml`
on every OS): lanes (slots, weight limits, memory gate, max_wait, matchers),
shim rules, reap rules, gc roots and patterns, cache dir and size cap.
`baton config init | show | path | validate`. Defaults are computed from
the hardware, e.g. render slots = max(1, RAM_GB / 16), encode = max(1,
cores / 6), ml = 1, install = 1, build = max(1, cores / 4). State lives in
`$XDG_STATE_HOME/baton` (locks, queue, history), cache in
`$XDG_CACHE_HOME/baton`.

### 3.10 Distribution

GoReleaser config (darwin/linux amd64+arm64, windows amd64; archives,
checksums, SBOM optional), a Homebrew formula for a
`sushilk1991/homebrew-tap`, `install.sh` (curl | sh, verifies checksums,
installs to `~/.local/bin`), `go install github.com/sushilk1991/baton/cmd/
baton@latest`. A tag-triggered release workflow. Prepare all of it; do not
publish a release.

## 4. Quality bar

- Go module `github.com/sushilk1991/baton`, `go 1.22` in go.mod for wide
  compatibility, `CGO_ENABLED=0` builds for every target.
- Dependencies: stdlib first. Allowed: a TOML parser, an MCP or JSON-RPC
  helper if it saves real code. No CLI framework needed.
- Every package has unit tests. Integration tests spawn real processes and
  must cover:
  a. 20 parallel `baton run --lane x -- sleep` with 2 slots never exceed 2
     concurrent (measured by the jobs themselves);
  b. `kill -9` of a slot holder admits the next waiter within 1 s;
  c. FIFO order within a priority, and high before normal;
  d. signal forwarding and exit-code passthrough;
  e. memory gate with an injected fake pressure provider (warn → waits,
     normal → admits, hysteresis, max_wait admits with warning);
  f. shims: resolution, recursion guard, `BATON=off`, fast paths;
  g. reap lists only orphaned headless processes (spawn fakes), `--yes`
     kills them;
  h. gc dry-run vs `--yes` inside a temp root, symlinks not followed;
  i. cache hit/miss and restore;
  j. `init` against a fake `$HOME` (never the real one): install, idempotent
     re-run, uninstall restores byte-identical files.
- `go test -race ./...` clean; `go vet` clean; golangci-lint config included
  and clean when the tool is available.
- `make check` runs vet, lint (if installed), tests with -race, and
  cross-builds every target.
- CI (GitHub Actions): ubuntu-latest, ubuntu-24.04-arm, macos-latest,
  macos-13, windows-latest (build + core tests).
- `scripts/stress.sh`: N synthetic memory-hog jobs (configurable GB each)
  launched at once, with and without baton; records peak swap, worst
  pressure level, OOM/jetsam events, wall time; prints a pass/fail table.
  Pass: pressure never critical, swap growth < 1 GB, no kills, wall time
  ≤ 1.2× sequential. Results go in `docs/benchmarks.md`.

## 5. Docs (make people star it)

- README: hero line, a 20-second problem story, an asciinema/vhs demo
  (commit `docs/demo.tape` for charmbracelet/vhs), install (brew, curl,
  go install), 60-second quick start, recipes for AI agents (Claude Code,
  Codex, Cursor), video pipelines (Remotion, HyperFrames, ffmpeg), local ML
  (Whisper, Ollama), monorepos (parallel installs and builds), laptops used
  as CI runners; an ASCII "how it works" diagram; an honest comparison table
  vs flock, GNU parallel/sem, task-spooler (ts), pueue, systemd-run/cgroups,
  saying when each is the better tool; FAQ; uninstall.
- `docs/`: configuration reference (generated from the defaults), agents,
  shims, browser, cache, architecture, benchmarks.
- LICENSE (Apache-2.0), CONTRIBUTING, CODE_OF_CONDUCT, SECURITY,
  CHANGELOG, issue and PR templates.

## 6. Non-goals

A cluster scheduler, a service manager, cgroup enforcement (maybe later),
telemetry, a GUI.

## 7. Done means

1. `make check` passes on macOS (this machine).
2. `docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` passes
   (Linux).
3. `scripts/stress.sh` run on this machine prints PASS, with results in
   `docs/benchmarks.md`.
4. Every README command was run and works as shown.
