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

## Command execution

`cmd/baton` delegates `run` to `internal/runner`. The runner owns argument
validation, identity, job records, and execution. It calls admission and the
queue for scheduling; OS drivers own signals, terminals, and RSS counters.
Only `runner.Run` is exposed to the command package.

```text
argv -> matcher -> ticket -> memory gate -> slot lease -> child group
          |                       |                         |
       no match ---------------> run                       RSS samples
                                                            |
queued/running JSON <----------------------------------------+
                                                            |
                                     finish -> history + EWMA -> release
```

Arguments reach `exec.Command` without shell rewriting. A shell script is
opaque to automatic classification: use `--lane` for `sh -c` and similar
wrappers. Defaults match direct tools and launchers such as `npx remotion`
and `pnpm exec playwright`. Rule order is encode, render, ML, install, build.
Unmatched commands skip queue and pressure admission but retain history and
learning. `BATON=off` skips scheduling and telemetry.

Default capacity follows the spec's RAM/core formulas; unknown lane names
have one slot. Render capacity uses effective RAM from the pressure provider,
with one slot if probing fails. Configuration can override these defaults.
`--weight` must fit the lane. `--mem` accepts bytes and binary K/M/G units,
including decimal quantities that resolve to whole bytes, such as `1.5G`.
Explicit memory overrides a learned prediction. Cache flags belong to the
later cache item.

Unix children get a separate process group. SIGINT, SIGTERM, and SIGHUP go
to that group, including descendants that remain in it. Startup and signal
delivery share a mutex: a signal before startup cancels admission, while a
signal after startup reaches the child. A foreground terminal is handed to
the child's group before exec and restored after wait. Background callers
never take the terminal. Pipe/file descriptors pass through directly.
Windows creates a process group but console interruption currently kills
only the command, best effort. Native Windows signal behavior is unverified.

Exit codes pass through. Unix signal exits use `128 + signal`; lookup
failure returns 127, other launch failure 126, and invalid arguments 2.
Ordinary waiting notices appear only on blocked retries, at most once every
two seconds, on stderr. They report lane, pressure, and swap. `--quiet`
suppresses these notices; the gate's max_wait warning remains visible.
Admission's optional wait observer also runs during slot waits after
max_wait, so expiring memory policy does not suppress contention notices.
Observer I/O failure cancels and removes the ticket.

Each job has a random ID, owner lock, and atomic JSON record under `jobs/`.
Queued records include label, priority, weight, agent, user, cwd, repository,
and memory budget. Running records add child PID and current/peak RSS.
Normal completion removes the record and owner file. SIGKILL can leave
stale records; their released kernel owner locks permit later cleanup.
Agent detection prefers `--agent`, then Claude Code, Codex, Cursor, Gemini,
Aider, OpenCode, Goose, `TERM_PROGRAM`, and finally `terminal`. The nearest
`.git` directory or worktree pointer identifies the repository root.

The command signature is SHA-256 of JSON-encoded argv and cwd. This preserves
argument boundaries and separates project builds. Plaintext argv is not
persisted because arguments can contain credentials. RSS is sampled every
100 ms across the root and its current descendants: Linux `/proc/*/stat`,
macOS `ps -axo pid=,ppid=,rss=`, Windows Toolhelp plus working-set counters.
Unix wait rusage also captures the root peak for short-lived commands.
Sampling can miss short-lived or reparented descendants and inaccessible
processes; it is a prediction, not an enforced memory limit.

Completed records append to `history.jsonl` under a kernel guard shared
with learning updates. At 10 MiB it rotates to `history.jsonl.1`, retaining
one previous segment. `learned.json` stores an EWMA per signature with
alpha 1/4; the first nonzero observation initializes it. A difference-based
update avoids integer overflow. Temporary files and rename protect JSON
snapshots. Persistence errors are reported separately and preserve the
child's exit status. A crash between history append and learning may leave
a stale prediction without corrupting the previous JSON snapshot.

Killing baton with SIGKILL releases its slots but cannot run descendant
cleanup. The child may survive until it exits or a later reap operation
finds it. Tests terminate their own orphan group after measuring admission.
A crash test must observe next-job admission before calling `Cmd.Wait` on
the killed wrapper: inherited stderr pipes can stay open in its orphan.

Real-process tests cover two-slot concurrency across 20 commands, SIGKILL
recovery below one second, exact signal forwarding through two generations,
queued cancellation, stdio and exit codes, terminal input and foreground
restoration, process-tree RSS, and reuse of the learned budget. Concurrent
history writers, rotation, corrupted predictions, matcher rules, identity,
and memory units have unit coverage. Native CLI smoke tests additionally
run the built binary with 20 render jobs and inspect its JSONL history.

## Configuration

`internal/config` owns XDG paths, machine defaults, strict TOML decoding,
and resolved lane policy. `run` and the config commands use this layer.
BurntSushi/toml v1.4.0 is the only dependency; its module requires Go 1.18,
below baton's Go 1.22 minimum. Baton performs no network access to load it.

```text
XDG environment -> paths -------+-> config path
                               |
hardware -> defaults -> TOML overlay -> validation -> effective policy
                                                       |         |
                                                  config show   runner
                                                                 |
                                                       queue -> memory gate
```

Path defaults are `.config/baton/config.toml`, `.local/state/baton`,
`.cache/baton`, and `.local/share/baton` beneath the OS home directory on
every platform. Windows uses `USERPROFILE` through `os.UserHomeDir`.
`XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `XDG_CACHE_HOME`, and `XDG_DATA_HOME`
override their respective bases. Relative overrides fail explicitly;
environment variables and `~` inside TOML paths are not expanded. Cache
directories and GC roots must be absolute. These choices avoid the
different macOS/Windows defaults in `os.UserConfigDir`.

An absent file uses current hardware defaults. Empty files are valid.
Overrides merge field by field; unmentioned lanes remain present. New
lanes default to one slot, memory gating enabled, and `max_wait = '30m'`.
An omitted `max_weight` follows the resolved slot count. Explicit unknown
lane names also retain the previous one-slot behavior. `auto` is reserved.
Config keys are case-sensitive: unknown keys and differently cased aliases
fail, including aliases accepted by the underlying TOML struct decoder.

The schema contains these tables:

- `[lanes.NAME]`: `slots`, `max_weight`, `memory_gate`, `max_wait`, and
  regex `matchers`. All counts and durations must be positive; weights
  cannot exceed capacity. Empty matchers disable automatic classification.
- `[memory]`: `margin = '512M'`, `poll_interval = '250ms'`, and
  `recovery_window = '2s'`. Byte units retain the runner's binary K/M/G
  interpretation; duration strings use Go's `time.ParseDuration` syntax.
- `[shims]`: `commands`; `[shims.fast_paths]`: command-to-regex-array
  entries. `[reap]`: `older_than = '2h'`, `grace_period = '5s'`, and regex
  `matchers`. Shims and reap consume their respective settings.
- `[gc]`: absolute `roots`, relative slash `patterns`, and
  `older_than = '168h'`. Roots default to empty, so no cleanup is enabled.
  Patterns cannot contain parent traversal. `[cache]`: absolute `dir`,
  `max_size = '10G'`, and allowlisted `env` names, empty by default.
- `[browser]`: `executable` (empty for discovery), extra `flags`, and
  `idle_timeout = '10m'`. Browser lifecycle behavior remains a later task.

Top-level `match_order` lists lanes to try first. The default order remains
encode, render, ML, install, build. Unlisted lanes follow in lexical order,
so custom precedence is deterministic across processes. Unknown/duplicate
entries and invalid regexes fail validation. Matching inspects joined argv;
shell script bodies remain opaque. CLI `--lane`, `--weight`, and
`--max-wait` take precedence, subject to the configured weight limit.
`memory_gate = false` skips pressure probes and memory timeout warnings,
while retaining FIFO, priorities, slot limits, cancellation, and notices.
`BATON=off` also bypasses configuration loading, allowing recovery from a
damaged file. Normal flag validation still applies.

`config show` emits complete effective TOML. `config path`, `show`, and
`validate` create no files or directories. Validation without a file checks
the effective defaults. `config init` snapshots current defaults into a
private file. It writes, syncs, and closes a same-directory temporary file,
then publishes it with a hard link. Publication cannot replace an existing
file or symlink, even under concurrent initialization. Filesystems without
hard links return an error; no overwrite fallback is attempted. A second
init fails while preserving all existing bytes and comments.

Unit tests cover hardware formulas, inheritance, custom lanes, ordering,
disabled matching, strict keys/types, regexes, paths, durations, byte units,
effective-TOML round trips, and concurrent initialization. Runner tests
verify configured weights and waits, CLI overrides, bypass recovery, and
disabled gates retaining capacity without probes or timeout warnings.

## Status and history

`cmd/baton` routes `status` and `history` through the runner's read and
render functions. Configuration exposes a detached list of lane names;
queue and lane packages expose read projections above the lock driver.
These remain internal APIs. The lock driver can probe existing files
without creating them.

```text
status -> config + live job records + pressure
                 |
                 v
           queue guard -> ordered tickets -> lane guard -> slot locks

history -> history guard -> copy previous + current -> release -> parse/filter
```

Status lists configured lanes, including idle lanes, and ad-hoc lanes named
by live jobs. Occupancy comes from kernel slot locks, so a crashed runner's
stale job record cannot inflate it. The persisted capacity is `total`;
`configured_total` shows a pending configuration change. Before the first
reservation, total is the configured capacity. Idle ad-hoc lanes disappear
when no live job names them.

Job owner locks distinguish live records from crash residue without PID
liveness guesses. Readers leave stale records and owner files in place.
They never prune tickets, resize lanes, or create state directories or
lock files. Queue scans hold the scheduler's queue guard and use the same
priority, aging, and sequence comparison. A live ticket with missing or
invalid metadata fails explicitly. New runners publish the ticket sequence
in their job record; older records can be correlated by owner PID. A ticket
published before job metadata remains visible with its scheduling fields.

Snapshots span multiple observations rather than a global transaction.
Jobs can finish or start between reading job records, queue state, and
memory counters. Slot counts are sampled under the lane allocation guard;
slot release can still occur during observation. Current RSS is the
runner's latest sample. Runtime and current wait durations clamp at zero
if the wall clock moves backward. JSON uses byte counts, nanosecond
durations, UTC timestamps, and empty arrays for empty lists.

`status --watch` emits immediately and then once per second. Text appends
frames with timestamps; `--line` appends compact lines; `--json` emits one
complete JSON object per line. No terminal clearing escapes are emitted.
`--json` and `--line` are mutually exclusive. SIGINT, SIGTERM, and SIGHUP
stop watching with exit 0. Configuration is loaded once per invocation;
restart a watch after editing it. One observation has a five-second deadline;
probe, metadata, lock, and output errors fail the command. A failed pressure
probe is never presented as normal pressure. Text output replaces control
characters in labels and paths to prevent terminal or row injection.

History defaults to every retained record. `--since` accepts a nonnegative
Go duration and filters by completion time, inclusive of the cutoff;
`--lane` matches the exact lane name. JSON output is JSONL. Human output
includes start, end, wait, exit, peak RSS, lane, label, agent, repo, and cache
hit. Both rotated segments are copied oldest segment first under the same
guard as append and rotation. Parsing and filtering happen after releasing
the guard, so JSON decoding cannot stall writers. Missing history is an
empty result. Invalid,
blank, or incomplete lines fail with file and line, including records a
filter would exclude. No partial result is printed on a read failure.

Tests cover empty state without filesystem changes, real running and queued
CLI processes, weighted occupancy, priority positions, identity, RSS,
completion history, filtering, valid rotation under concurrent writes,
stale owners, damaged live metadata, cancellation, watch frames and signals,
output errors, and terminal control characters. Test jobs use temporary
HOME/XDG paths and release only their own processes.

## Shims

`internal/shim` installs wrappers in `XDG_DATA_HOME/baton/shims`. Unix
wrappers use `/bin/sh` and `exec` the absolute baton binary; Windows gets
`.cmd` wrappers. The private `shim exec` entry point resolves the real tool
on each call, then passes its absolute path and unchanged argv to
`runner.Run --lane auto`. Upgrading a tool does not require reinstalling
its shim. Moving baton itself requires uninstalling from its original path
and reinstalling at the new path.

```text
PATH -> shim -> baton shim exec -> resolve PATH without shim directories
                                      |
                          +-----------+------------+
                          |                        |
                    configured fast path      runner -> queue -> child
                    or BATON=off                   |
                          |                  history + learned RSS
                     direct execution
```

Resolution excludes the shim directory, symlink aliases, links to baton,
and other generated wrappers. An inherited chain of hashed command/argv/cwd
signatures detects repeated invocations, including an external wrapper
that calls itself through PATH. Different arguments or working directories
have different signatures; raw arguments are not added to the environment.
Missing executables return 127; recursion and non-executable targets return
126. The guard remains active under `BATON=off` to prevent bypass loops.

Fast-path regexes match space-joined arguments, excluding the command name.
Defaults bypass all `ffprobe` calls and `ffmpeg -version`, `-h`, `-help`,
and `-f null`. Overrides use the validated config projection; no defaults
are duplicated in the shim layer. Fast paths skip slots, pressure checks,
history, and learning. `BATON=off` also skips TOML loading. Unix bypasses
replace baton with the real process, preserving its PID and descriptors.
Windows waits for the child and returns its exit code; native Windows
runtime, batch-tool targets, and shell argument escaping remain unverified.

`install` without names considers configured commands and reports missing
tools as skipped. Explicit missing names fail before installing any files.
Names are validated, deduplicated, and preflighted before publication.
Shell quoting preserves literal Unix paths, including single quotes.
Shell guidance supports sh/zsh/bash, fish, and best-effort Windows cmd and
PowerShell. No command edits shell configuration.

An admitted shim job marks its descendants through an inherited environment
value. Descendant shims resolve their tools and execute directly within the
parent reservation; they add no tickets or history. Otherwise a tool such
as `ffmpeg outer` invoking `ffmpeg inner` could wait forever for its own
slot. Unmatched root commands carry no reservation marker. Descendants can
still outlive a crashed wrapper, as described under command execution.

A SHA-256 manifest records owned wrapper bytes. Install, uninstall, and
manifest writes share a kernel management lock; a competing management
command fails explicitly rather than mixing changes. List is read-only.
Wrappers publish through a complete temporary file and a non-replacing
hard link; the manifest updates atomically after each file. Reinstall can
adopt an exact wrapper left by a crash before manifest publication. An
interrupted uninstall tolerates missing files. Conflicts, changed bytes,
symlinked wrappers, and a symlinked shim directory fail without replacement
or deletion. Unrelated directory entries are retained. The shared lock is
never unlinked.

Real CLI tests invoke installed wrappers through PATH with temporary home
and XDG directories. They cover automatic encode admission behind an
occupied slot, release, exit 37, literal argv, a quoted baton path, changed
real executables, symlink aliases, direct fast paths, bypass with broken
TOML, recursive external wrappers, nested jobs without another reservation
or history record, and owned-file uninstall. Unit tests cover policy
overrides, missing tools, manifest recovery, user-file
preservation, invalid names, management contention, and closed output.


## Reaping orphan processes

`cmd/baton` dispatches `reap` to `internal/reap`. The policy layer calls
`internal/process` for OS inspection and single-process signals. Default
minimum age is two hours; `[reap].older_than` and `--older-than` override
it. The CLI accepts zero for explicit immediate cleanup. Configuration
requires positive durations. Grace defaults to five seconds and comes from
`[reap].grace_period`.

```text
OS snapshot + stale job birth/owner records
                   |
     own UID + age + orphan + heavy command + config matchers
                   |
              print candidates
                   |
        --yes -> revalidate -> TERM all -> one grace period
                                            |
                                  revalidate survivors -> KILL
                                            |
                                      confirm exit
```

Selection requires the current effective UID, a readable kernel birth
identity, and either parent PID 1 or a matching job record whose recorded
owner is dead. New runner records store `process_start`; old records
without it qualify only through direct init/launchd adoption. A stale PID
alone is insufficient. Linux identities include the boot ID and start
ticks; macOS uses kernel start seconds and microseconds. Unreadable owner
metadata is an error, not evidence of death. Reap leaves all state files
unchanged.

Known executable names identify ffmpeg, browser automation drivers, and
Remotion/HyperFrames render commands. Node drivers require a recognized
package entry path or npm `.bin` entry. Arbitrary arguments, shell source,
Node eval text, and projects merely named `playwright` do not establish a
heavy process. Config regexes match joined argv and narrow this selection;
an empty matcher list disables it. They cannot override the browser guards.
Opaque wrappers and unnamed custom drivers require a later driver rule;
reap does not infer their identity from incidental argument substrings.

Browsers require an actual `--headless` or `--remote-debugging-*` argument
before `--`. Debugging without headless mode also requires an explicit
private absolute profile path. Known normal Chrome, Chromium, and Edge
profile directories are protected, including aliases and subdirectories.
Known profile paths are compared without case to protect case-insensitive
filesystems; this also conservatively excludes case variants on Linux.
Every explicit profile argument is checked, so a later duplicate cannot
hide a normal profile. Headless mode without an explicit profile remains
eligible. A browser with no automation flags is always excluded.

macOS enumerates PIDs with `ps`, then reads
[XNU proc_bsdinfo](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/proc_info.h)
and `KERN_PROCARGS2` through raw system calls without cgo. The
[XNU procargs implementation](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_sysctl.c)
can return `EINVAL` during process exit; a second metadata read confirms
exit before treating that error as disappearance. The argument decoder
preserves spaces and empty arguments and discards environment strings.
Linux reads `/proc/PID/stat`, `status`, and NUL-delimited `cmdline`; age
uses `/proc/uptime` and the ELF `AT_CLKTCK` value rather than assuming a
clock frequency. See the kernel's
[proc interface](https://docs.kernel.org/filesystems/proc.html).

Before TERM and again before KILL, reap verifies birth, UID, argv, age,
and orphan status. Signals target individual PIDs, never process groups.
All selected processes share one grace period. Linux binds signals to
pidfds when supported; kernels without `pidfd_open` fall back to immediate
identity revalidation. macOS also revalidates immediately before `kill`.
Those fallback paths retain a small unavoidable inspection-to-signal race;
there is no atomic compare-and-signal syscall there. PID 1 and baton itself
are always protected. Zombies count as exited. Reap polls every 25 ms and
waits up to two seconds after KILL; surviving identities fail the command.
Windows reap fails explicitly without signalling; Windows core commands
still build. Windows reap runtime support is deferred.

Output lists PID, parent, age, and executable path, omitting arguments that
could contain credentials. Control characters are replaced. Discovery has
a ten-second deadline. Discovery, record, signal, and output errors return
failure. Listing finishes before any signal, so a closed stdout prevents
cleanup. A successful empty list means no inspectable eligible process;
it does not establish that inaccessible processes are absent.

Native CLI integration tests spawn their own named process fakes and let
owned launchers exit. A temporary-directory matcher excludes every existing
machine process. Dry-run lists three orphans while leaving all six fakes
alive. `--yes` delivers TERM and removes all three selected identities,
including a fake that ignores TERM; active children and interactive/default
profile browsers survive. Separate tests cover recorded dead owners,
reused PIDs, live owners, changed argv, lost orphan status, closed output,
parser errors, profile aliases, and config overrides.

## Collecting intermediates

`cmd/baton` dispatches `gc [--yes]` to `internal/gc`. Configuration supplies
absolute roots, relative slash globs, and `older_than` (default seven days,
written as `168h`). Default roots are empty, so installation never opts a
directory into deletion. `Config.GCPolicy` returns copies of validated
policy. The filesystem driver owns directory traversal, metadata, and
removal; the collector owns matching, age selection, and reporting.

```text
configured roots -> open without symlinks -> discover regular files
                                                   |
                                    glob + mtime strictly before cutoff
                                                   |
                     scan all roots -> print sorted paths and byte sizes
                                                   |
                          --yes -> reopen parents -> compare -> unlink leaf
```

An entire `**` component matches zero or more directories; ordinary `*`,
`?`, and character classes use slash-glob semantics. Patterns match paths
relative to each configured root. For example, `**/out/slice-*.mp4` covers
both `out/slice-1.mp4` and `project/out/slice-1.mp4`; `frames/**` covers
files throughout a frame dump. Duplicate and overlapping roots produce
one row per absolute path. Modification time determines age; files at the
cutoff, future files, and fresh files are retained.

Only regular files are candidates. Directories, symlinks (including
cycles and dangling links), sockets, devices, and FIFOs remain. Directory
shells are retained: deleting a matching directory recursively could erase
fresh, unmatched, or newly created files. Sizes report logical file lengths;
physical space reclaimed depends on sparse extents and remaining hard links.

The Unix driver opens every root component and every descendant directory
with `O_DIRECTORY | O_NOFOLLOW`, relative to an already open parent. Roots
with symlinked ancestors are rejected; on macOS, use `/private/tmp` rather
than the `/tmp` alias. Before each operation, the configured root must
still identify the original directory. Linux uses `O_PATH | O_NOFOLLOW`
for metadata without device I/O or FIFO waits. macOS uses `fstatat64` with
`AT_SYMLINK_NOFOLLOW`; its cgo-free syscall constants follow
[XNU's syscall table](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/syscalls.master)
and [fcntl flags](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/fcntl.h).

Before unlinking, the driver compares device, inode, type, size, mtime,
and ctime with discovery. A missing or changed file, root, or parent aborts
with a nonzero exit status. The complete selection must reach stdout before
deletion starts; short or closed output aborts. A later failure reports how
many files were already deleted. Removal uses only parent-relative leaf
unlinks.

The final metadata check and unlink are separate syscalls. A concurrent
replacement in that interval can remove a replacement directory entry;
`unlinkat` still never follows a symlink. Directory handles identify opened
objects: a process able to rename an opened directory elsewhere can move
that object during an operation. Hostile directory renames and privileged
mount changes fall outside the driver's isolation guarantees. Windows with
configured roots fails explicitly until a handle-relative driver is
available; unconfigured gc and all core commands still build.

Native and Linux CLI tests use temporary HOME/XDG directories and owned
temporary roots. Dry-run lists three files totaling 16 bytes and changes
nothing. `--yes` removes those files while preserving fresh and unmatched
files, symlinks, and an outside sentinel. Further tests cover globstar,
overlapping roots, strict age boundaries, special files, root/parent/leaf
replacement, same-size rewrites with restored mtime, traversal rejection,
closed/short output, config errors, and partial-deletion reporting.

The Linux gate exposed an existing status publication race: a live ticket
could join job metadata whose `queue_sequence` was still zero. Status now
copies the authoritative sequence from that ticket. A regression fails
without this assignment; no scheduling or status assertions were relaxed.
