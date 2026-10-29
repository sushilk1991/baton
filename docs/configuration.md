# Configuration

```sh
baton config show
baton config path
baton config validate
```

No file is required. `show` prints effective defaults plus overrides;
`validate` checks that same configuration. Neither writes files. To snapshot
your current machine defaults, run `baton config init` once. It refuses to
replace any existing file or symlink.

A configuration that fails to load never blocks other work: shims and the
Claude hook's `baton hold` print the error and run their command unqueued.
`baton run` and the other commands report it and exit nonzero.

The [generated defaults reference](defaults.toml) comes from the binary's
`config show`, not a second hand-maintained schema. Its slot counts reflect
the generation host; its cache-directory assignment is replaced by a
comment because that path depends on HOME and XDG. Regenerate from the repo:

```sh
make build
./scripts/config-reference.sh "$PWD/bin/baton" > docs/defaults.toml
```

## Paths on every OS

| Content | Override base | Default |
| --- | --- | --- |
| TOML | `XDG_CONFIG_HOME` | `~/.config/baton/config.toml` |
| Locks, queues, jobs, history, predictions | `XDG_STATE_HOME` | `~/.local/state/baton` |
| Cached outputs | `XDG_CACHE_HOME` | `~/.cache/baton` |
| Shims and integration manifest | `XDG_DATA_HOME` | `~/.local/share/baton` |

Each override is an absolute base directory; baton appends `baton`.
Windows uses `USERPROFILE` for the home fallback and the same relative
layout. TOML paths must be absolute where specified: `~`, `$HOME`, and other
environment references inside strings are not expanded.

## Lanes and matching

| Lane | Default slots | Typical matches |
| --- | --- | --- |
| `render` | `max(1, floor(RAM_GiB / 16))` | Chrome, Playwright, Puppeteer, Remotion, HyperFrames |
| `encode` | `max(1, floor(CPU_count / 6))` | ffmpeg |
| `ml` | 1 | whisper, mlx, ollama, llama |
| `install` | 1 | npm/pnpm/yarn/bun install, pip install, uv pip/sync, brew/cargo install |
| `build` | `max(1, floor(CPU_count / 4))` | xcodebuild, gradle, cargo build, go build |

RAM uses the effective pressure-provider total, including visible cgroup v2
limits on Linux. A failed hardware probe gives render one slot. CPU count
uses Go's runtime count. These are initial estimates; tune for your workload.

```toml
match_order = ["custom", "encode", "render", "ml", "install", "build"]

[lanes.custom]
slots = 2
max_weight = 2
memory_gate = true
max_wait = "30m"
matchers = ['(^|/)my-renderer(\s|$)']
```

Fields overlay defaults individually. All counts are positive and
`max_weight <= slots`; an omitted `max_weight` follows the resolved slots.
New lanes default to one slot, a memory gate, and a 30-minute memory wait.
An explicit unconfigured lane also gets one slot. `auto` is reserved.

Regexes use Go's regexp syntax. They match the executable's name after
`env`, `sudo`, `time`, and `nice` wrappers, plus the operation for known
dispatchers: `npm install`, `go build`, `npx remotion`, `python -m whisper`.
Other arguments never select a lane, so `grep -r llama notes` runs
immediately; `baton run`, shims, the Claude hook, and MCP `explain_lane`
share this rule. Default order is
encode, render, ml, install, build. Unlisted configured lanes follow in
lexical order. A custom lane needs an earlier `match_order` entry to beat an
overlapping built-in. Empty `matchers` disables auto matching for that lane.
Shell command bodies remain opaque; use an explicit lane for them.

## Memory admission

| Setting | Default | Meaning |
| --- | --- | --- |
| `lanes.NAME.memory_gate` | `true` | Require normal pressure and headroom before slots. |
| `lanes.NAME.max_wait` | `"30m"` | Maximum memory delay; never bypasses slot or queue order. |
| `memory.margin` | `"512M"` | Available RAM must exceed budget plus this amount. |
| `memory.poll_interval` | `"250ms"` | Retry sampling and admission. |
| `memory.recovery_window` | `"2s"` | Require continuous recovery after any failed check. |

`--mem` overrides the learned prediction for that invocation. All byte
quantities are positive whole-byte values; K, M, G, KiB, MiB, GiB, KB, MB,
and GB use powers of 1024. `1.5G` is valid. Durations use Go syntax such as
`250ms`, `30m`, or `168h`; `7d` is not valid.

Failed probes delay admission. Only time blocked by memory counts toward
`max_wait`; waiting for a busy slot does not, and memory is checked again
when the slot frees. At `max_wait`, a warning accompanies
admission once the job has its queue turn and enough slots. `--quiet` hides
ordinary notices but never this warning. Disabling the memory gate retains
queue, priority, weight, and slot controls. No policy enforces memory usage.

## Other tables

| Table | Fields and defaults | Details |
| --- | --- | --- |
| `shims` | `commands`; `fast_paths.COMMAND = [regex, ...]` | [Default command list and bypass rules](shims.md) |
| `reap` | `older_than = "2h"`, `grace_period = "5s"`, `matchers` | Regexes restrict built-in process eligibility; they cannot broaden it. |
| `gc` | `roots = []`, `patterns`, `older_than = "168h"` | Empty roots disable cleanup. Absolute roots; relative slash globs. |
| `cache` | `dir` from XDG, `max_size = "10G"`, `env = []` | [Single-file cache and environment dependencies](cache.md) |
| `browser` | `executable = ""`, `flags = []`, `idle_timeout = "10m"` | [Discovery, reserved flags, and lifetime](browser.md) |

Unknown keys, wrong types, case aliases, invalid regexes, duplicate match
order entries, and invalid paths fail validation. Config is loaded per
invocation; restart `status --watch` after editing it. Changing capacity
waits for existing reservations to drain. `status` shows both current and
configured capacity. Keep shared state on a local locking filesystem.
