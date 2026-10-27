# Progress

Shipped locally: nine TASKS.md items, through reap. Reap implementation:
038c6d1. SPEC.md is unchanged. No push, tag, release, or remote change.
The overall goal remains incomplete.

Verified: final native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and all five cgo-free cross-builds.
golangci-lint was unavailable and skipped. Windows compiled; its runtime
remains unverified.

Reap lists aged, owned heavy processes adopted by init/launchd or linked
by a kernel birth identity to a dead recorded owner. Default age is two
hours; config controls age, grace, and narrowing matchers. `--yes` checks
identities before TERM and KILL, shares one grace period, and confirms
exit. Browser guards preserve interactive and known default profiles.
Runner records now include optional process birth tokens. OS inspection
and signals stay in internal/process. Decisions are in docs/architecture.md.

The first CLI regression failed with `unknown command "reap"`. The runner
record regression then failed because a running record lacked its birth
token. Real CLI tests selected three owned orphan fakes. Dry-run left all
six fakes alive. `--yes` delivered TERM and removed the three selected
identities, including a TERM-ignoring fake; the active child, interactive
browser, and default-profile browser survived. Every signalling test used
an anchored temporary-directory matcher, never existing machine jobs.

Further regressions cover PID reuse, stale records, live recorded owners,
lost orphan status, changed argv, denied signals, closed output, malformed
flags, empty arguments, profile aliases/case/duplicates, Node eval, project
name false positives, and npm .bin drivers. A real macOS TERM test exposed
procargs EINVAL during exit; kernel metadata now confirms disappearance.

Pending: complete only `gc` and test h next. Read SPEC.md section 3.5,
TASKS.md, docs/architecture.md, and the gc config schema before editing.
Use temporary roots; test dry-run, deletion, age, containment, and symlink
protection. Do not start cache, browser, init, docs, or distribution work.

Limits: Windows reap fails explicitly without signalling. macOS and Linux
kernels without pidfds retain a small inspection-to-signal race. Opaque
wrappers are excluded. Old records without birth tokens rely on direct
init/launchd adoption. Hosted CI, Windows runtime, stress, and README
verification remain pending. Existing runner/shim limits remain documented.
