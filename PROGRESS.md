# Progress

Shipped locally: the first seven TASKS.md items. Status and history code:
5ad0ed5. SPEC.md is unchanged. No push, tag, release, or remote change.
The overall goal remains incomplete.

Verified: native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and five cgo-free cross-builds. golangci-lint
was unavailable and explicitly skipped. The Windows CLI test binary also
cross-compiled; Windows runtime remains unverified.

`status` now reports configured and active ad-hoc lanes, kernel-held slot
counts, running jobs, ordered waiters, identity, runtime, RSS, RAM, swap,
and pressure. It supports human tables, JSON, compact lines, and watch.
Readers leave stale state untouched and create no directories or locks.
`history` reads both retained segments, filters by completion time and lane,
and prints tables or JSONL. Invalid data fails with file and line before
printing results. Architecture choices are in docs/architecture.md.

A built native CLI smoke used temporary HOME/XDG paths: one weighted job
occupied two slots, high-priority and normal waiters appeared in order,
and RSS was nonzero. Human, JSON, and compact status output passed.
Watch emitted two valid JSON frames and exited 0 on SIGTERM. After release,
status was idle and filtered history contained three successful jobs.
Appending an incomplete fourth history line produced exit 1, empty stdout,
and history.jsonl:4. All processes belonged to the test.

Red-first checks caught the missing commands and a live ticket hidden when
its JSON was missing. A concurrent rotation test exposed history readers
holding the writer guard through JSON parsing. The fix copies both segments
under the guard and parses after release. Restoring the old guard behavior
in a temporary source copy failed the writer deadline at five seconds; the
corrected race test passed in about two seconds. An earlier Docker run with
the old test binary was stopped; the final corrected Docker gate passed.

Pending: complete only `shim install|uninstall|list`, fast paths, recursion
protection, BATON=off, and test f. First read SPEC.md, TASKS.md, this file,
docs/architecture.md, the config schema/paths, and command dispatch. Reuse
validated config through narrow read projections. Do not start reap, gc,
cache, browser, or init work.

Existing limits remain: native Windows signal/runtime and hosted CI are
unverified; the required macos-13 hosted runner is retired. RSS sampling
can miss short-lived or reparented descendants. SIGKILL releases slots but
can leave descendants. Status spans multiple observations, not a global
transaction. Watch loads configuration once; restart it after config edits.
