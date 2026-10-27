# Progress

Done: scaffold, lanes/slots, fair queues, pressure admission, and `baton run`.
Commit da3e5a3 admits through queue and memory policy, forwards Unix signals to
the child group, preserves stdio and exit status, restores terminal control,
and records identity, JSONL history, process-tree RSS, and memory predictions.
Policy and failure boundaries are recorded in docs/architecture.md.
Exactly the first five TASKS.md items are implemented; overall work continues.

Verified: native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0
on the final implementation. Vet, race tests, and all five cgo-free builds
passed. golangci-lint is absent and was explicitly skipped. New subprocess
tests measured 20 commands with a two-slot peak, SIGKILL-to-admission in
254.4055 ms, exact SIGINT/SIGTERM/SIGHUP delivery through two generations,
queued cancellation, exit codes, stdio, terminal input/foreground restoration,
process-tree RSS, and learned-budget reuse. One captured tree peak was
46891008 bytes, reused as the next run's memory budget. Unit tests cover
concurrent history writers, rotation, invalid predictions, matching, identity,
memory units, and wait-observer failures.

Windows runner and CLI test binaries also cross-compiled.

The built native binary also passed a terminal smoke test and 20 parallel
`run --lane render` commands: job-measured active=0, peak=2 after completion.
Its isolated state contained 21 valid JSONL records, including a separate
stdio command with exit=37, stdout=out:unchanged, stderr=err. All CLI and
process tests used temporary HOME/XDG roots. Test-created orphans from failed
fixtures were terminated; no unrelated processes were signaled.

Next: complete only the config item. Read SPEC.md, TASKS.md, this file,
internal/runner/options.go, internal/runner/run.go, and docs/architecture.md.
Introduce TOML and shared hardware defaults/XDG paths, wire runner policy to
config, and add `config init|show|path|validate`. Keep existing run/process
coverage. Do not start status/history commands in that iteration.

Pending: native Windows runtime and hosted CI remain unverified. Windows
interruption currently kills only the owned root command. RSS sampling can
miss short-lived or reparented descendants. Killing baton itself with SIGKILL
releases slots but can leave its child alive; later reap work handles those
orphans. Config, remaining commands, docs/distribution, stress targets, and
the final gate are still queued. SPEC.md is unchanged. No push, tag, release,
or remote change occurred.
