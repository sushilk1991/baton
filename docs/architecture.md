# Architecture decisions

## Scaffold

`cmd/baton` starts with private command dispatch and injected output writers.
No CLI framework or exported Go API is needed. Packages under `internal/`
will own scheduler and platform code as those queue items are implemented.

```text
cmd/baton (arguments, stdio, exit status)
    |
    v
internal domain packages (later tasks)
    |
    v
internal platform drivers (later tasks)
```

`make check` runs vet, optional golangci-lint, race tests, and serial builds
for darwin/linux amd64+arm64 and windows amd64. Race tests enable cgo because
the Go race runtime requires it; release builds set `CGO_ENABLED=0`.

Tests replace `HOME`, all inherited `XDG_*` keys, and the standard XDG paths
with temporary directories before exercising commands. Tests that need
parallelism must pass an isolated environment to child processes instead
of combining `t.Setenv` with `t.Parallel`.

CI checks Go 1.22 compatibility and Go 1.27, matching the required Docker
image. Dependency caching is disabled until there are module dependencies.
Windows runs vet, core tests, and a build without cgo.

The required CI matrix retains `macos-13`. GitHub [retired that hosted
runner](https://github.blog/changelog/2025-09-19-github-actions-macos-13-runner-image-is-closing-down/)
in December 2025, so that job cannot run on a GitHub-hosted runner. A macOS
13 runner or a spec change is required before the full CI matrix can pass.
The immutable spec has not been changed.

## Lanes and slots

`internal/lane` owns weighted admission. `internal/lockfile` hides Unix
`flock` and Windows `LockFileEx`; neither needs cgo or a daemon. These are
internal Go APIs, not a supported public library.

```text
lane.TryAcquire(weight)
    |
    v
allocation.lock -> capacity check -> slot-0.lock ... slot-N.lock
    |                                  |
    +-- release before returning       +-- lease owns weight handles
                                           |
                                      release or process death
```

Each lane name maps to a SHA-256 directory beneath the supplied lock root.
Names can contain path separators without escaping that root. Lock files
stay in place: unlinking one while another process holds it could create
two independent locks at the same path.

A short allocation lock prevents competing weighted requests from retaining
partial reservations. Each attempt takes all requested slots or releases
everything it took. Waiting holds no handles. `TryAcquire` lets the later
queue layer decide admission order; `Acquire` currently retries every 10 ms
with context cancellation. This primitive does not promise FIFO or aging.

Capacity is stored atomically under the allocation lock. Changing capacity
waits until every slot in the previous capacity is idle; callers using
different capacities cannot run together. Corrupt capacity metadata fails
explicitly. No lock file is removed during resizing.

The drivers use nonblocking exclusive locks and explicit unlock on normal
release. Kernel cleanup releases handles after process death. See the
primary references for [flock](https://man7.org/linux/man-pages/man2/flock.2.html)
and [LockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex).
Use local storage for lock state; network filesystem lock semantics vary.

Real-process tests cover 20 jobs measuring their own overlap with two
slots, weighted contention, and kill-to-admission latency below one second.
They exercise the lane API through test subprocesses. The literal
`baton run --lane x -- sleep` integration belongs to the later CLI item.
Tests replace HOME and every XDG variable before spawning children and
terminate only their own subprocesses. Cross-builds compile every package,
including drivers not yet referenced by the CLI.
