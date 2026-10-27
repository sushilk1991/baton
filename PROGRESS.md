# Progress

Shipped locally: the first eight TASKS.md items. Shim implementation:
9b3f841. SPEC.md is unchanged. No push, tag, release, or remote change.
The overall goal remains incomplete.

Verified: final native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and all five cgo-free cross-builds.
golangci-lint was unavailable and skipped. Windows compiled; its runtime
remains unverified.

Shims now install, list, and uninstall owned wrappers. They resolve real
executables on every call, exclude shim directories and symlink aliases,
guard recursive invocations, and preserve argv and exit status. Configured
fast paths and BATON=off bypass scheduling and telemetry. Descendant shims
share an admitted parent job's reservation, avoiding nested-slot deadlock.
An atomic hash manifest protects conflicting or changed wrapper files.
Shell guidance prints a PATH line without changing shell configuration.
Decisions and platform limits are in docs/architecture.md.

The first red CLI test failed with `unknown command "shim"`. A later
regression timed out behind a held slot when a nested shim queued again.
Both now pass. Real subprocess tests cover queue admission and release,
exit 37, literal arguments, quoted paths, replacement tools, recursion,
fast paths, invalid-config bypass, nested jobs, and uninstall. Unit tests
cover config overrides, ownership, interrupted installation, invalid names,
management contention, and failed output. All use temporary HOME/XDG paths.

A separate native `bin/baton` smoke printed:

```text
shim list: 1 installed command
PATH invocation: real:inner; history: 1 encode job, exit 0
fast path: ran; history unchanged
BATON=off: ran with broken TOML; history unchanged
uninstall: 0 owned wrappers remain
```

Pending: complete only `reap` and test g next. Read SPEC.md section 3.4,
TASKS.md, docs/architecture.md, the reap config schema, and existing process
enumeration before editing. Tests must spawn and signal only their own fake
processes. Do not start gc, cache, browser, init, docs, or distribution work.

Existing limits remain: native Windows signals, batch targets, shell
escaping, and hosted CI are unverified. The required macos-13 hosted runner
is retired. RSS sampling can miss short-lived or reparented descendants.
SIGKILL releases slots but can leave descendants. Status is sampled across
multiple observations. Full stress and README verification remain pending.
