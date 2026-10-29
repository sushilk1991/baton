# Changelog

## Unreleased

### Fixed

- `max_wait` counts only time blocked by memory. Waiting for a busy slot
  no longer spends it, and memory is checked again when the slot frees.
  A slow probe no longer stretches the wait past `max_wait`.
- Built-in lane matchers see executables and dispatcher operations,
  never arguments: `grep -r llama notes` no longer queues in the ml lane.
  Matchers you write still see the full argv. `baton run`, shims, MCP
  `explain_lane`, and the Claude hook share these rules.
- Tools started through `nohup`, `caffeinate`, `timeout`, `bunx`,
  `yarn`/`pnpm`/`bun` package binaries, `uv run`, `uvx`, `pipx run`,
  `poetry run`, `conda run`, or `python -m` get their tool's lane, also
  after global options (`uv --directory . run`, `pnpm --filter web exec`)
  and through nested wrappers (`uv run nice whisper`).
- A config that fails to load no longer breaks shimmed tools or
  hook-held commands; they warn and run unqueued.

### Changed

- `baton version` and `--version` print the installed release.
- Plain `status` and `history` use binary units, repo names, and local
  time. Wait notices say whether a job waits for a slot or for memory.
- Cache hits print the restored output instead of finishing silently.
- Shimmed commands that match no lane exec the real tool directly;
  a shimmed `go version` takes about 15 ms instead of 45-57 ms.
- The Claude hook entry follows `settings.json` indentation.
- `run`, `status`, `history`, and `hold` print baton usage text, with an
  example for `run`; a bad flag is reported once. `init` says update or
  restore for existing files. `cache` and `shim` accept `list` and `ls`.

## v0.1.0 (2026-10-29)

### Scheduling and observation

- Weighted, crash-released lane locks; priority/FIFO tickets with aging.
- Memory pressure admission on macOS/Linux and best-effort Windows,
  hysteresis, memory-wait timeout, and learned process-tree RSS.
- Foreground execution, Unix process-group signals, exit-code passthrough,
  live status, and rotated JSONL history.

### Integration and maintenance

- Hardware-derived TOML policy, XDG paths, command shims, and bypass rules.
- Orphan discovery/termination and rooted intermediate-file collection.
- Single-file output cache with restoration and size-based eviction.
- Shared headless-browser supervisor with CDP connection counting.
- Reversible agent setup, optional Claude Bash hooks, and read-only MCP.

### Documentation and validation

- Source installation, quick start, recipes, generated defaults, and a VHS
  demo tape; contribution, conduct, and security-reporting guidance.
- Hosted CI on Linux (amd64, arm64), macOS 26, and Windows core tests,
  plus five cgo-free cross-build targets. The Claude Code hook is
  macOS/Linux only. Stress benchmarks and the recorded demo remain
  pending.
