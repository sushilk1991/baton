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

## Queue

`internal/queue` orders admission above `internal/lane`. The queue takes a
state root, lane name, and capacity. It stores tickets under
`queue/<lane hash>/` and passes `locks/` to the lane package. Callers must
use the queue for fairness; direct lane acquisitions remain unordered.

```text
enqueue: queue.lock -> sequence -> owner.lock + ticket.json -> unlock
                                      |
wait:                         retain owner.lock only
                                      |
admit:   queue.lock -> prune/order -> lane.TryAcquire -> delete ticket
                                            |
job:                                  retain slot locks
```

Each ticket has a persisted sequence, creation time, priority, weight, and
owner PID. The sequence increments under the queue guard before publication;
a crash can skip a number but cannot reuse an owner path. Atomic rename
protects the sequence file from partial writes. FIFO means registration
order under that guard, including simultaneous arrivals.

Initial order is high, normal, then low. Every five minutes of waiting
promotes a ticket one level, capped at high. Equal effective priorities use
sequence order. A low-priority ticket reaches high after ten minutes, where
new arrivals cannot overtake it. Promotion uses wall-clock elapsed time;
moving the clock backward delays promotion until the interval elapses.
Tests inject time to check both sides of the promotion boundary.

Only the head ticket attempts a complete weighted reservation. Smaller jobs
do not fill slots ahead of a waiting heavier job, so repeated small arrivals
cannot starve it. Admission order does not guarantee child startup order
when several slots become available together.

A ticket retains a kernel owner lock until admission or cancellation.
Scanners try that lock under the queue guard: success proves the owner is
gone and permits cleanup, including incomplete ticket writes. PID reuse
does not affect liveness. Every ticket path is unique, and removal happens
under the same guard used for creation and scanning; no live owner lock is
unlinked. The shared guard file stays in place. Damaged live metadata fails
explicitly instead of silently discarding a waiting job.

`Ticket.TryAcquire` supports a later memory gate without surrendering queue
position. `Ticket.Acquire` polls every 10 ms and closes its ticket on errors
or context cancellation. `Close` is safe to repeat or call concurrently.
The APIs remain internal. The CLI and memory gate belong to later tasks.

Real subprocesses verify FIFO within each priority, high before normal
before low, and dead-ticket cleanup after SIGKILL. Jobs record their own
admission order while holding a slot. Unit tests cover aging, weighted head
blocking, cancellation, concurrent registration, sequence recovery, and
corrupt metadata.

## Pressure and admission

`internal/pressure` hides OS counters behind `Provider.Sample(ctx)`. A
snapshot reports effective RAM, available RAM, host swap, and a normalized
pressure level. Invalid counters and failed probes return errors.

```text
admission.Gate -> pressure.Provider -> OS counters
      |
      +-------> queue.Ticket -> lane -> lockfile

enqueue -> sample -> memory fits and pressure normal -> try queue turn
              ^                |                            |
              +-- wait --------+                       slot lease
              |
              +-- max_wait -> warn and try queue turn
```

The gate takes an existing ticket and keeps its place throughout a memory
wait. It takes no slots before the memory check. Every retry checks queue
order and reserves the full weight through `Ticket.TryAcquire`. Cancellation
removes the ticket. Gate state belongs to each `Acquire` call, so concurrent
waiters can share one gate without sharing recovery timers.

Policy choices left open by the spec:

- Default safety margin: 512 MiB beyond declared or predicted memory.
  Available RAM must strictly exceed their sum; subtraction avoids overflow.
- After any pressure, headroom, or probe failure, require two seconds of
  continuously healthy observations. A bad sample resets this recovery
  window. Initially healthy jobs can enter immediately.
- Poll every 250 ms. Default `max_wait` is 30 minutes, measured from the
  start of `Gate.Acquire`, including time waiting for slots. Callers should
  enqueue and start the gate together. At expiry only the memory check is
  bypassed; queue priority, order, weight, and slot capacity still apply.
- Failed probes delay admission until recovery or timeout. A timeout emits
  one `WARNING` on admission, including the last probe error or counters.
  A failed warning write releases the lease and returns an error. Ordinary
  quiet-mode notices belong to the later CLI task; timeout warnings must
  remain visible.
- Linux and Windows classify available RAM below 10% as warning and below
  5% as critical. Linux also warns at PSI `some avg10 >= 10` and marks
  `>= 25` critical. These are baton policy thresholds, not OS guarantees.

macOS uses fixed system paths for `sysctl -n hw.memsize
kern.memorystatus_vm_pressure_level vm.swapusage` and `vm_stat`, with a
two-second probe timeout and C locale. Available RAM is free plus inactive
pages using the reported page size. The kernel pressure value is
authoritative; inactive pages alone do not imply normal pressure. Swap is
read separately from availability. The implementation needs no cgo. Apple's
[pressure sysctl implementation](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_memorystatus_notify.c)
maps the internal level to dispatch pressure values.

Linux reads `/proc/meminfo` and `/proc/pressure/memory`. Missing PSI is
allowed for kernels without it; malformed or unreadable PSI is an error.
For cgroup v2, `/proc/self/cgroup` and `/proc/self/mountinfo` locate the
current group's visible mount, including non-root mounts and escaped path
names. Effective RAM and available RAM are capped by `memory.max` and
`max(0, memory.max - memory.current)` at every visible ancestor. Parent
usage includes siblings. The worst host or cgroup `memory.pressure` PSI is
used. Missing controller limit files and unlimited `max` values add no cap;
unreadable or invalid counters fail the probe. A v2 membership without a
visible matching mount also fails. Swap counters describe the host, not a
container's swap allowance. See the kernel's
[PSI](https://docs.kernel.org/accounting/psi.html),
[meminfo](https://docs.kernel.org/filesystems/proc.html), and
[cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html) interfaces.

Windows calls
[GlobalMemoryStatusEx](https://learn.microsoft.com/en-us/windows/win32/api/sysinfoapi/nf-sysinfoapi-globalmemorystatusex)
through the Win32 ABI without cgo. It reports physical RAM availability.
`MEMORYSTATUSEX` page-file fields are commit limits, so they are not reported
as swap usage. Swap remains unavailable on Windows. Native Windows runtime
verification remains pending; all release targets cross-build.

These observations do not reserve memory across lanes or enforce budgets.
Another process can allocate after a sample; cgroup ancestors hidden by a
mount namespace cannot be inspected. Slot limits still bound concurrency.
The later stress task measures whether the default policy meets the
machine-level pressure and swap targets.

Tests inject snapshots into real queue waiters and real subprocesses.
They cover warn-to-normal recovery, interrupted recovery, strict headroom,
oversized requests, cancellation, timeout warnings, occupied-slot safety,
warning-write failures, cgroup parent limits, and malformed OS data. Native
provider tests sample the host on macOS and Linux without creating pressure.
