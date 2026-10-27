# Progress

Done: scaffold, lanes/slots, fair queues, pressure admission, `baton run`,
and configuration. Config implementation: 85068e2. Exactly the first six
TASKS.md items are implemented; the overall goal remains incomplete.
SPEC.md is unchanged. No push, tag, release, or remote change occurred.

Configuration now supplies shared hardware defaults, XDG paths on every OS,
strict TOML overlays, custom lanes and regex precedence, weight limits,
memory policy, and `config init|show|path|validate`. Initialization preserves
existing files and symlinks under contention. Config schemas for shims,
reap, gc, cache, and browser are validated; their commands remain queued.
Decisions and the schema are recorded in docs/architecture.md.

Verified: final native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and five cgo-free cross-builds. golangci-lint
was absent and explicitly skipped. Config's Windows test binary also
cross-compiled. The final native binary's path/show/init/validate commands
exited 0 in temporary HOME/XDG paths; initialized TOML matched shown defaults.
This machine resolved build=3, encode=2, install=1, ml=1, render=2.

A built-CLI smoke launched 20 jobs auto-matched to a configured two-slot
lane. The jobs measured active=0, peak=2; all 20 history records named that
lane and exit=0. Weight 3 was rejected with exit=2; differently cased keys
failed validation with exit=1. BATON=off bypassed damaged config and
preserved exit=37. Unit coverage includes inheritance, custom precedence,
round trips, invalid keys/types/regexes/durations/paths, concurrent init,
symlink preservation, overrides, and disabled gates retaining slot limits.
Existing concurrency, crash, signal, terminal, and RSS tests remain green.

Next: complete only `status` (--json, --watch, --line) and `history`.
Read SPEC.md, TASKS.md, this file, docs/architecture.md, internal/config,
internal/runner/store.go, and queue/lane storage before writing tests.
Reuse config policy and XDG paths. Add read interfaces where needed; do
not duplicate configuration or start shim/reap/gc work.

Pending: native Windows runtime and hosted CI remain unverified. Windows
interruption kills only the owned root command. RSS sampling can miss
short-lived or reparented descendants. Killing baton with SIGKILL releases
slots but can leave a child alive. Config initialization requires hard-link
support and fails safely without it. Later commands, docs/distribution,
stress targets, and the final gate remain queued.
