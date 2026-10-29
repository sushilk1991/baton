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
Windows runs vet, core tests, and a build without cgo. macos-latest (macOS
26) runs Go 1.27 only: its dyld aborts Go 1.22 binaries with "missing
LC_UUID load command", so a macOS 26 source build needs a newer Go.

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
- Poll every 250 ms. Default `max_wait` is 30 minutes of memory-blocked
  time: only attempts that memory blocks spend it, never waits for a busy
  slot. Every attempt probes, so a slot freed after a long wait meets
  current pressure. At expiry only the memory check is bypassed; queue
  priority, order, weight, and slot capacity still apply.
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

For a controlling terminal, `waitid` observes stops, continuations, and exits
without consuming exit status (`WNOWAIT`). Stop/continue notifications are
consumed separately; `Cmd.Wait` retains exit status, rusage, and pipe cleanup.
SIGCHLD wakes the waiter, which checks with `WNOHANG` to avoid blocking tty
handoffs. Signals are subscribed before the first check to avoid lost wakes.

A terminal stop (SIGTSTP, SIGTTIN, SIGTTOU) stops the whole child group,
returns the tty to baton's group if the child owns it, and sends SIGSTOP to
baton's entire process group. This also stops a non-interactive sh, make,
or npm parent waiting in that shell job. One Ctrl-Z then returns the prompt.
A SIGSTOP also counts as a terminal stop when the child's group owns the
tty and the child catches SIGTSTP: nano and mutt answer Ctrl-Z by
stopping their group with SIGSTOP. Linux reads the caught-signal mask
from /proc/<pid>/status; macOS reads p_sigcatch from kinfo_proc.
baton cannot tell that stop from an outside `kill -STOP` of the same
program, so an outside STOP of a foreground program that catches SIGTSTP
(vim, nano, less) also suspends the job, and `fg` resumes it. Without
this rule, Ctrl-Z in nano or mutt hung the terminal.
Any other outside SIGSTOP of the child updates stopped status but leaves
baton waiting and runnable; an outside SIGCONT updates running status. Stopped
jobs retain their slots because their processes and allocations still exist.

```text
Ctrl-Z -> child group stops -> baton + parent group stop -> shell owns tty
Ctrl-Z -> TSTP handler -> kill(0, SIGSTOP) -> treated as Ctrl-Z above
fg -> caller group resumes -> child owns tty -> child group resumes
outside STOP/CONT -> child state changes -> baton keeps waiting
exit -> Cmd.Wait -> history -> slot released
```

Each suspension uses a fresh SIGCONT subscription. On Linux, the stop
syscall can return before every Go runtime thread stops; baton awaits that
subscription before continuing the child. A prior continuation cannot
satisfy the next suspension. On fg, baton transfers the tty before resuming
the child. On bg, it leaves the tty with the interactive shell.

A shell can foreground an already-running background job without SIGCONT.
The waiter also checks tty ownership every 20 ms; if baton's group now owns
the tty, it hands it to the child before continuing that group. A poll with
the tty still owned by the child or interactive shell sends no signal, so
an externally stopped child remains stopped. SIGCONT stays subscribed for
the job's lifetime. See Bash's [start_job implementation](https://github.com/apple-oss-distributions/bash/blob/main/bash-3.2/jobs.c):
it gives a foreground job the terminal, then sends SIGCONT only if the job
was stopped (checked 2026-10-28).

A supervisor SIGCONT sends no signal when the child already owns the tty.
The suspension path has already resumed that child; a delayed notification
must not undo its next Ctrl-Z or an independent SIGSTOP. Paired bare/queued
PTY tests stop the foreground child, continue only its waiting parent, and
verify that the child stays stopped until explicitly continued.

Completion restores only a tty still owned by the child; failed exec also
restores it. A failed tty handoff resumes the child before waiting, so
pending HUP/TERM or ordinary completion can finish it.

The private siginfo_t buffer is 128 bytes. Its common three-int header is
followed by child PID, UID, and stop status, with four bytes of alignment
padding on Linux amd64/arm64 and none on Darwin. No cgo or dependency is
added. References: [Linux waitid](https://www.man7.org/linux/man-pages/man2/waitpid.2.html),
[Linux siginfo_t](https://github.com/torvalds/linux/blob/master/include/uapi/asm-generic/siginfo.h),
[Darwin wait options](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/wait.h),
and [Darwin siginfo_t](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/signal.h).
PTY tests run the built CLI through Bash on macOS/Linux and zsh on macOS
with line editing disabled. They repeat stop/resume on one child, deliver
Ctrl-C to its trap, check exit 23 and history, admit the next one-slot job,
and verify failed exec. Paired bare/queued commands also cover a waiting
non-interactive parent, outside child STOP/CONT, and Ctrl-Z -> bg -> fg ->
Ctrl-Z, checking kernel child state, tty ownership, public stopped status,
exit 23, and next-job admission. A driver test covers terminal loss after a
terminal stop; SIGSTOP is deliberately excluded from shell suspension.
These tests isolate HOME/XDG paths and signal only their own processes.

Background tty callers retain the terminal descriptor without setting the
fork driver's `Foreground` flag. A child's terminal read or `stty` can then
stop its group without taking the terminal from the interactive shell.
The same stop/continue protocol suspends baton, so `jobs` reports the stop
and `fg` transfers the terminal before the child continues. Pipe/file input
uses a sentinel descriptor and ordinary `Cmd.Wait`.

```text
background child -> tty access -> child stops -> metadata ack -> baton stops
fg -> baton owns tty -> child owns tty -> child continues -> exit -> release
```

The monitor remains the only writer of job metadata. The waiter sends phase
changes over a channel and waits for acknowledgement after publication,
before suspending Go's threads. This prevents RSS sampling from overwriting
the stopped phase or racing a second writer. `status` keeps stopped jobs in
the JSON `running` array with `phase: stopped`; text output adds a STATE
column. Slots stay reserved. Paired PTY tests compare the bare and wrapped
`sh -c 'stty -echo; read x; echo GOT=$x' &` through stop, `fg`, input, exit 0,
and the next job acquiring the released slot.

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
OpenCode, Goose, `TERM_PROGRAM`, and finally `terminal`. The nearest
`.git` directory or worktree pointer identifies the repository root.

Only exact, nonempty runtime markers identify agents: `CLAUDECODE`,
`CODEX_THREAD_ID`, `CURSOR_AGENT`, `GEMINI_CLI`, `OPENCODE`, and
`GOOSE_TERMINAL`. Prefix matching also accepted configuration and credentials
from human shells, so `CODEX_HOME`, `CURSOR_API_KEY`, `AIDER_MODEL`,
`OPENCODE_CONFIG`, and `GOOSE_MODEL` now fall through to the terminal label.
Aider has no accepted automatic marker; use `--agent aider`. Empty markers
fall through too. Inherited or manually set runtime markers can still label
a descendant; detection is cooperative metadata.

Sources checked on 2026-10-28: Codex's [shell environment source](https://github.com/openai/codex/blob/main/codex-rs/protocol/src/shell_environment.rs)
injects `CODEX_THREAD_ID`; Cursor's [terminal documentation](https://cursor.com/docs/agent/tools/terminal)
names `CURSOR_AGENT`; Gemini's [shell execution source](https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/services/shellExecutionService.ts)
sets `GEMINI_CLI=1`. OpenCode's [v1.2.0 entry point](https://github.com/anomalyco/opencode/blob/v1.2.0/packages/opencode/src/index.ts)
sets `OPENCODE=1`; Goose's [retry command source](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/agents/retry.rs)
sets `GOOSE_TERMINAL=1` on that path. These sources do not prove every
version or execution path exports a marker. Aider's [command runner](https://github.com/Aider-AI/aider/blob/main/aider/run_cmd.py)
sets none at its subprocess launch sites, so configuration prefixes provide
no reliable replacement for an explicit label.

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
wrappers use `/bin/sh` and `exec` the absolute invoked baton path; Windows gets
`.cmd` wrappers. The private `shim exec` entry point resolves the real tool
on each call, then passes its absolute path and unchanged argv to
`runner.Run --lane auto`. Upgrading a tool does not require reinstalling
its shim. Installation resolves argv[0] through PATH when needed, makes it
absolute, and preserves symlinks. Package manager links can therefore point
to a new version without reinstalling shims.

Unix wrappers check that baton is a regular executable before invoking it.
If unavailable, they remove every PATH entry whose command is the current
wrapper, comparing file identity to cover symlink and hardlink aliases, then
exec the command through the remaining PATH. Copied wrappers remove their
own entries in turn; a PATH containing only wrappers terminates with exit
127. Empty PATH entries retain their current-directory meaning. With no
entries left, fallback exits before command lookup. It uses shell builtins,
preserves argv and exit status, and works with `BATON=off`.
It skips configuration, scheduling, and history. It does not rerun a command
after a working baton returns nonzero, or promise recovery if baton vanishes
between the executable check and exec. Windows batch wrappers remain best
effort and do not provide this Unix fallback.

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

Every admitted run passes `_BATON_LANES`, a JSON map of lane names to owning
job IDs, to its child. Shims use the same runner path as explicit calls.
A descendant in any lane reuses the ancestor's reservation without another
ticket or slot; history records `nested: true`. Taking another lane could
deadlock mirrored jobs such as x -> y and y -> x. Each heavy descendant
still checks its own declared or learned memory budget, with the selected
lane's hysteresis and `max_wait`. A nil admission ticket selects this
memory-only path, retaining timeout warnings and cancellation:

```text
run install -> run encode -> run install
holds slot     memory gate     memory gate
    +---------- shared reservation --------+
```

Inheritance requires a non-nested job record in the same state directory
whose owner lock is still held. Expired, malformed, or foreign-state markers
grant no bypass. The runner passes its marker only after admission. Its
first child can arrive while metadata still says queued; the monitor
publishes running metadata after capturing the child's kernel birth token.
The environment changes only for the child; unmatched commands add no
reservation. These markers are cooperative scheduling metadata. Descendants
share the parent's slot weight; memory checks do not reserve additional RAM
or expand that weight. Parallel children within that reservation need their
tool's own limits.
Descendants can still outlive a crashed wrapper, as described under command
execution; inherited markers do not retain its slot after it exits.

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
TOML, recursive external wrappers, nested jobs sharing a reservation with
their own history records, and owned-file uninstall. Unit tests cover policy
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
identity, and either a matching job record whose recorded owner is dead
or verified host-init adoption of a headless browser or automation driver.
ffmpeg and Remotion/HyperFrames renderers always require a matching record.
Records without `process_start` cannot prove ownership. A stale PID alone
is insufficient. Linux identities include the boot ID and start ticks;
macOS uses kernel start seconds and microseconds. Unreadable owner metadata
is an error, not evidence of death. Reap leaves all state files unchanged.

PID 1 alone proves nothing. macOS checks `/bin/ps` for `/sbin/launchd`.
Linux requires `/proc/1/comm` to name `init` or `systemd`, then requires
`/usr/bin/systemd-detect-virt --container` to print `none` with exit code 1.
The detector distinguishes systemd running on a host from systemd running
inside a container. Its
[output and exit behavior](https://github.com/systemd/systemd/blob/main/src/detect-virt/detect-virt.c)
are checked together: a missing utility, unreadable init, or ambiguous
result disables adoption-based selection. Recorded jobs still qualify.
This conservative fallback avoids treating an incomplete list of container
marker files as proof of a host.

Known executable names identify ffmpeg, browser automation drivers, and
Remotion/HyperFrames render commands. Node drivers require a recognized
package entry path or npm `.bin` entry. Arbitrary arguments, shell source,
Node eval text, and projects merely named `playwright` do not establish a
heavy process. Config regexes match joined argv and narrow this selection;
an empty matcher list disables it. They cannot override the browser guards.
Opaque wrappers and unnamed custom drivers require a later driver rule;
reap does not infer their identity from incidental argument substrings.

Browsers require an actual `--headless` argument before `--`, including
forms such as `--headless=new`. The `chrome-headless-shell` executable is
inherently headless and needs no flag. Remote debugging and a private
profile never authorize reaping a headed browser, even with a job record.
Known normal Chrome, Chromium, and Edge
profile directories are protected, including aliases and subdirectories.
Known profile paths are compared without case to protect case-insensitive
filesystems; this also conservatively excludes case variants on Linux.
Every explicit profile argument is checked, so a later duplicate cannot
hide a normal profile. Headless mode without an explicit profile remains
eligible.

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

The `REASON` column names each candidate's eligibility rule: `started by
baton, parent dead` or `adopted orphan of the system init`. It uses the
scan's validated owner records and matching birth token; a stale token
cannot label an adopted process as baton-started.

Output lists PID, parent, age, and executable path, omitting arguments that
could contain credentials. Control characters are replaced. Discovery has
a ten-second deadline. Discovery, record, signal, and output errors return
failure. Listing finishes before any signal, so a closed stdout prevents
cleanup. A successful empty list means no inspectable eligible process;
it does not establish that inaccessible processes are absent.

Native CLI integration tests spawn their own named process fakes and let
owned launchers exit. A temporary-directory matcher excludes every existing
machine process. Dry-run lists two orphans while leaving all seven fakes
alive. `--yes` delivers TERM and removes both selected identities,
including a fake that ignores TERM; unrecorded encoders, active children,
and headed/default-profile browsers survive. A Linux container test keeps
unrecorded browsers, drivers, and encoders alive despite PID 1 adoption.
Separate tests cover recorded dead owners,
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

## Command output cache

`run --cache-key 'src/**,audio/*.wav' --output out/master.mp4 -- command`
uses `internal/cache` before taking a heavy lane slot. Only a successful
command with a readable regular output file is stored. A hit restores that
file, skips execution, and records `cache_hit=true` in history without
updating learned RSS. Missing output, changed inputs, or cache I/O failure
prints a diagnostic and preserves the command's exit code. `BATON=off`
skips cache access with scheduling and telemetry.

Keys hash versioned JSON containing cwd, absolute output path, sorted input
patterns, argv boundaries, and matched file names, permissions, lengths,
and SHA-256 contents. Configured `cache.env` names include both their value
and whether they were set. The allowlist defaults empty. No plaintext argv,
environment, input names, or cwd is persisted in cache metadata. Callers
must include every dependency that affects the artifact, including tool or
configuration files; executable versions and unlisted environment are not
automatically tracked.

Globs are comma-separated relative slash patterns under cwd. `**` matches
zero or more complete components; `*`, `?`, and character classes stay
within one component. Each pattern must select at least one regular file.
Absolute paths, parent traversal, and backslashes are rejected. Traversal
starts at each pattern's literal prefix and rejects encountered symlinks.
The output file and cache subtree are excluded so generated artifacts do
not invalidate their own key. Only one regular output file is supported;
directories and other side effects are not restored.

```text
inputs + argv + allowed env -> SHA-256 key -> kernel key lock
                                                |
                                  verify inputs and cached payload
                                      /                     \
                              atomic restore            lane admission
                                   |                         |
                              record hit                execute command
                                                             |
                                               success + unchanged inputs
                                                             |
                                                 copy to private staging
                                                             |
                              global cache lock -> publish -> enforce cap
```

A persistent lock file per key serializes identical jobs through execution
and storage. Different keys can execute concurrently. The global cache
lock covers entry reads, restores, publication, listing, and eviction;
lock order is key then global. Prune takes only global. Process death
releases locks; lock files are never unlinked. Input hashes are checked
after lock acquisition, before restore, and after execution. Callers must
keep inputs stable during the command and give concurrent distinct jobs
distinct output paths; this is not a filesystem sandbox.

Entries contain an output copy and metadata with key, output digest, size,
mode, and last-use timestamp. Files are synced before staging-directory
rename. Incomplete staging cannot become a hit and is removed by the next
store or prune. Payload digests are verified on each hit. Damaged entries
become misses; prune removes malformed entries so corruption cannot prevent
cap enforcement. No power-loss durability is promised: a lost cache entry
requires regeneration.

Restore creates a temporary hardlink in the output's parent and renames it
over a regular destination. Cross-filesystem link failure falls back to a
synced copy before rename. Directory and symlink destinations are rejected.
A consumer that edits a restored hardlink in place also edits the cached
inode; the next verification detects the change and regenerates the entry.
Cached files are user-owned regenerable data, not protected against hostile
same-user filesystem races. Atomic output replacement avoids this sharing.

`cache ls` lists opaque keys, logical payload bytes, and last-use times.
`cache prune` removes least recently used entries until `cache.max_size`
is met; stores enforce the same cap. The default remains 10 GiB. Oversized
individual outputs run normally but are not stored. Metadata, persistent
lock files, filesystem overhead, and retained hardlinks outside the cache
do not count toward the payload cap. Unrecognized cache-root files remain.

## Shared headless browser

`internal/browser` owns lifecycle and CDP proxying. Only `browser start`
launches a detached copy of the same baton binary as a supervisor. Core
scheduling still needs no daemon. A control lock serializes start/stop;
a lifetime kernel lock enforces one supervisor per state directory.
Observation never creates state or trusts a persisted PID for liveness.

Observers briefly acquire the lifetime lock to validate stale ownership.
A new supervisor retries that contention within the existing 15-second
startup budget. Otherwise a status probe can make startup or crash recovery
fail with `file lock busy` before a browser launches. The supervisor keeps
the acquired lock through shutdown; no second supervisor can launch early.

```text
start/stop -> control.lock -> supervisor -> browser.lock (held for lifetime)
                                  |
                                  +-> owned Chrome, unique private profile
                                  |
clients -> loopback CDP proxy -----+-> Chrome loopback CDP
             |
             +-> successful WebSocket upgrades increment clients
             +-> disconnect decrements clients, starts idle interval
             +-> zero clients for idle_timeout -> stop owned browser
```

Chrome writes an ephemeral debugging port and browser path to its private
`DevToolsActivePort` file. The supervisor probes HTTP discovery before
publishing an atomic, owner-readable session file. A loopback reverse proxy
forwards WebSocket frames unchanged and rewrites discovery WebSocket URLs
back to itself. Counts measure open browser/page WebSockets, not tabs,
processes, or unique tools. Failed upgrades do not count. Active connections
prevent idle shutdown; observation and HTTP discovery do not reset it.

Control endpoints require a random 256-bit session token. The token is
stored in private state and sent in an HTTP header, never command arguments.
HTTP clients ignore environment proxies and redirects. The proxy rejects
nonlocal Host headers and all Origin headers. Local CDP remains available
to other processes on the machine; this is not a same-user sandbox.

Discovery checks PATH, OS application locations, and known Playwright cache
layouts, including its cache override. A configured absolute executable
wins. Extra flags are separate argv entries with no shell expansion.
Headless mode, profile, and debugging switches remain managed. GPU is
requested with `--enable-gpu`; users can add `--disable-gpu`. GPU availability
is not inferred from these flags. The browser keeps its normal sandbox.

Normal stop, idle expiry, or supervisor INT/TERM/HUP terminates the owned
Unix process group, waits three seconds, then forces termination if needed.
The supervisor waits for the browser before dropping its lifetime lock.
Startup errors return nonzero and name the private diagnostic log. Only
normal shutdown removes the newly created private profile.

On macOS/Linux, ownership metadata records child PID, birth token, and
private-profile path before readiness. A replacement supervisor holding the
lifetime lock first retires a surviving recorded child. Recovery checks
birth identity, current user, and exact profile argv; process.Send
revalidates identity immediately before signalling. It waits for observed
death before launching the replacement. A changed process or corrupt record
fails closed. A crash before ownership publication can leave an unrecorded
orphan; reap remains the recovery path. Crash profiles remain on disk.
Windows starts/stops owned children on a best-effort basis and deliberately
lacks persisted-PID recovery until a birth-identity driver exists.

Primary references checked during implementation: Chrome's
[nondefault-profile requirement](https://developer.chrome.com/blog/remote-debugging-port),
Playwright's [executable registry](https://github.com/microsoft/playwright/blob/main/packages/playwright-core/src/server/registry/index.ts),
and the connection APIs linked in [browser recipes](browser.md).

The native browser gate exposed another status snapshot race: a ticket can
appear after the initial job scan, even though its writer publishes identity
before enqueueing. Status now refreshes live job metadata once when a ticket
has no matching record in that scan. A deterministic regression reproduces
the empty label and agent; the original real-process assertion is unchanged.
If a job finishes during observation, its ticket can still appear with only
scheduling fields. Status remains an observation, not an atomic transaction
across every lane and job.

## Agent integration and MCP

`internal/integration` separates detection/planning, file publication, and
hook rewriting. A plain `init` previews; `--yes` applies. `--dry-run` takes
precedence. `--claude-hook` opts into a Bash PreToolUse hook; existing
sessions need a restart. Discovery uses existing standard agent directories,
not PATH executables or third-party services. The default shared skill is
installed even when no specific agent directory exists.

```text
agent discovery -> preflight plan -> private write-ahead manifest
                                          |
                                directories -> files/links
                                          |
                     reinstall replays / uninstall reverses

MCP stdin -> validate JSON-RPC -> fixed read adapters -> domain packages
                                                        |
MCP stdout <- text content / isError <- snapshot or candidate listing
```

The manifest records original and installed bytes, modes, links, and created
directories. Publication uses synced temporary files and atomic rename.
The management kernel lock serializes baton writers; its inode is retained.
Before every operation, originals or installed bytes must still match, or
the recorded owned edit must remain unchanged within the current file.
Instruction blocks match between their markers, independent of surrounding
text. Recorded separators are removed when still adjacent; otherwise current
surroundings remain. Supported YAML edits use their unique recorded text span;
JSON settings remove the single recorded array entry by value. Other
current text, settings, hooks, and file modes survive. Duplicate or changed
owned blocks/entries fail with the path before restoration starts. Aider YAML
must still reproduce the current document when its supported edit is replayed;
ambiguous read-field changes require manual resolution.

Reconciliation updates both manifest snapshots in memory: current contents
become the installed side, and removing only the owned edit produces the
original side. Reinstall leaves current files byte-identical and saves these
snapshots. Uninstall journals them and its direction before restoring leaves
and removing empty parents. A crash between publication and a target write
leaves a recoverable before/after state. Existing version-1 manifests need no
migration; ownership is inferred from recorded contents, including resolved
symlink targets. Claude settings installation appends only the owned entry
and any missing parent keys. Decoder offsets locate raw JSON values, so
existing key order, whitespace, string escapes, and literal `&&` or `>` stay
unchanged. Removal validates ownership by value with integer precision,
then splices out that entry and its comma. Moving or reformatting the owned
entry is supported; user hooks and nonempty parent objects survive. Duplicate
keys on an edited path fail before publication. Unchanged files restore exact
original bytes. Originals may include sensitive settings, so the manifest is
private (0600). No secrets are printed.

With both instruction markers absent, or the recorded hook entry removed
leaving the original array, uninstall leaves current bytes alone and removes
the rest.
Missing `hooks` or `PreToolUse` keys also establish removal. Re-init rebuilds
the missing edit against current contents, including leftover blank lines,
and retains the recorded hook opt-in and mode without another flag. It saves
the rebased snapshots before replay. JSON settings take precedence over
instruction markers inside string values. Partial markers, changed or duplicated
owned entries, and ambiguous remaining hook arrays still fail with a preserved
path; re-init does not guess whether a different entry replaced baton's hook.

Existing instruction symlinks resolve within home/config roots and retain
their links. Skill-parent aliases are resolved before checking for self-links.
External targets fail. This is cooperative filesystem management, not an
isolation boundary against a hostile same-user process racing validation
and rename. Power-loss directory durability is not promised. Windows needs
symlink permission for agent links; Unix permission restoration is best
effort there.

Aider uses its documented read-only `read` convention. The installer narrowly
edits common scalar/list YAML and JSON forms. Ambiguous YAML fails before publication; no YAML dependency is
added. Goose also receives a global `.goosehints` snippet. Cursor receives
the discoverable skill; no undocumented global instructions file is created.
The shared `~/.agents/AGENTS.md` snippet is available to other integrations.

The Claude hook returns only `updatedInput`, preserving other tool fields.
It prefixes the original command with
`baton hold --lane install --pid $$ && ` (using the installed binary's
quoted absolute path). Claude's existing Bash or zsh parses the original
text at top level. Snapshot functions such as `nvm`, working-directory
changes, and `&>` retain that shell's semantics. `$SHELL` is ignored: it can
name tcsh even when Claude executes Bash. The original never enters baton's
argv or a new interpreter's `-c` argument. The hook emits no permission
approval; the original remains visible as shell syntax to Claude's normal
permission checks. Tests inspect that structure and execute it in real
shells; a live Claude permission-policy session remains unverified.
The blocking alternative (`--claude-hook-block`) exits 2 with the same
prefixed command for clients without input-rewrite support.

`hold` starts a detached keeper, waits for lane and memory admission, then
returns without launching the payload. The keeper retains the locks while
the calling shell continues:

```text
Claude shell -> hold -> keeper -> queue + memory gate -> slot locks
                 ^        |
                 +--ready-+   (close both readiness pipe descriptors)
Claude shell -> original command, functions and cwd unchanged
keeper -> poll shell PID + birth token every 100 ms -> exit -> unlock
```

The keeper closes inherited pipes before the shell proceeds and owns no
terminal. It watches the shell's kernel birth token, UID, and exit state;
normal exit, SIGKILL, and PID reuse release the reservation. Shell death
also cancels queued admission. An inspection error alone does not prove an
exit. The keeper never signals the shell or its descendants. Its own death
releases the kernel locks. This is one temporary keeper per shell,
with no installed service. Windows reports that `hold` is unsupported,
so the hook passes commands through there and `init` refuses its flags.

A reservation lasts until the calling shell exits, including commands after
the original. A hold under any live ancestor reservation (a run, a shim,
or an earlier hold, in any lane) returns at once, as a nested run does,
so x -> hold y alongside y -> hold x cannot wait in a circle. Shims in
any lane share the shell's reservation through verified ancestry.
The holder's PID and the shell's PID/birth token are separate record fields:
baton did not start that shell, so the reservation cannot establish reap
provenance. Status and history label it `shell hold`. Its history describes
the reservation lifetime with exit 0, not the workload's exit status or RSS;
explicit runs and shims still record their actual child results.

Hook test fixtures wait for the keeper's job record to disappear before
removing private state. Shell exit alone does not finish the keeper's
history writes; record removal follows those writes and slot release.
TTY-loss fixtures observe the child stopped before invalidating its tty,
so an early continuation cannot arrive before the fixture's self-stop.

Hook classification reads literal command segments separated by unquoted
shell operators. It skips assignments, `sudo`, `time`, `nice`, and `env`,
including their recognized option values. Only executable basenames and
known dispatch operations reach built-in matchers: install/build verbs,
npm-style, `uv run`, and `uvx` launchers, and Python `-m` modules after
interpreter options. Payload arguments and executable directory names
never select a built-in lane. For example, `grep ffmpeg` stays unqueued;
`npx remotion render` selects render. Matchers the user configures see
the segment's full unwrapped words, and segments running baton itself
never take a lane (`baton run` and the hook's own binary). Each
segment uses configured matcher order; the first matching segment selects
the whole command's lane. This keeps compound commands intact without
reserving several lanes. A segment already executing `baton run` is skipped
without hiding later bare heavy commands.

Literal `BATON=off` assignments bypass their segment, including through
`env`; the last assignment wins. Later segments are still classified.
The hook skips exact `--help` and `--version` arguments, Ollama's
`list`/`ls`/`ps`/`show`/`help`, `uv pip list`/`show`/`freeze`/`check`/`tree`,
and xcodebuild's `-help`, `-version`, `-list`, `-showsdks`,
`-showdestinations`, and `-showBuildSettings`, including launcher targets.
Other arguments do not select these paths: `ollama run list` and
`uv pip install list` still queue.
Known shell, Python, JavaScript/TypeScript, Ruby, Perl, PowerShell, and batch
script extensions stay opaque, including launcher targets such as
`npx ./scripts/chrome-debug.sh`. The hook does not inspect script files;
an extensionless executable still follows configured basename matchers.

No shell command executes during classification. Quotes, escapes, comments,
and redirection targets are recognized; shell expansions, functions, script
contents, here-documents, and `env -S` remain opaque. Shell grammar such as
`if true; then npm ci; fi` is not parsed and can remain unqueued.
Use explicit commands and shims for those forms and custom commands whose
operation is not recognized. The runner passes explicit-run reservations
to its children; shell holds use the ancestry check above. Installed hook commands and their
`hold` prefixes use the absolute invoked path, resolved through PATH from
`os.Args[0]`, with symlinks retained, matching shim installation. A package
upgrade can retarget that link and remove the old version without breaking
the hook. Repeated-prefix detection uses the same path, including renamed
binaries. No live agent session was modified or used to prove hook loading.

The MCP stdio server uses a small stdlib JSON-RPC implementation. Its current
protocol is 2025-11-25, with the compatible basic tool exchange accepted for
2025-06-18, 2025-03-26, and 2024-11-05. Initialization and the initialized
notification precede tools. Only status, history, reap_candidates,
gc_candidates, and explain_lane are advertised, all annotated read-only.
Read-only reap/gc adapters expose no mutation option. History accepts only
validated filter strings; explain_lane applies the same config matcher as
run. No argument can select a CLI subcommand, signal, deletion, or executable.

Messages are newline-delimited with a 1 MiB input cap. Calls run serially;
notifications produce no response. Unknown methods/tools, invalid IDs, and
invalid parameters return JSON-RPC errors. Domain failures are MCP tool
errors. Results use text content; no output schema, resources, subscriptions,
cancellation of in-flight calls, or remote transport is advertised. Init
leaves client-specific MCP registration to the user; the command is
`baton mcp`. Integration tests exercise a real subprocess exchange, and a
compiled-binary test executes the installed hook and its returned command
with quoted argv before uninstalling, under temporary HOME/XDG roots.

## Documentation

Source builds are the current installation path. README release commands
are explicitly preparatory: distribution and publication are separate queue
items, and no release availability is inferred from a working checkout.
Recipe commands require the reader's existing project, tools, inputs, and
models. Persistent servers such as Ollama retain memory outside a CLI
client's sampled tree; docs distinguish a reservation from a memory limit.
README sleep examples use a two-second memory wait so the quick start stays
bounded on an already pressured host. Real heavy-work recipes retain the
lane default; the tiny demonstration jobs do not establish pressure safety.

`scripts/config-reference.sh` generates `docs/defaults.toml` through the
compiled CLI in an empty environment with temporary HOME/XDG paths. This
avoids a second schema. Hardware-derived slot counts remain the generation
host's snapshot, with formulas in configuration.md. The temporary cache path
is replaced by a comment so copying the reference retains XDG resolution.

The VHS tape uses a single-slot demo lane with memory gating disabled. It
demonstrates slot serialization without allocating memory or claiming a
pressure benchmark. The recording command isolates HOME before shell
startup; the tape isolates its own state before any baton call. VHS literal
strings use backticks when shell quotes are present; its lexer does not
interpret backslash-escaped string delimiters. Parser validation and actual
shell-command execution are separate checks from rendering the GIF.

Manual runs and shims share lane inheritance. An explicit child
`env BATON=off` remains available to cover a complete tree with only the
outer reservation; it also bypasses descendant memory checks and history.

Community policies point to GitHub's private reporting entry point
conditionally. Its availability is unverified; a dedicated private contact
channel and response-time commitment remain unset. Release publication,
machine benchmarks, and hosted CI proof belong to their separate gates.

## Distribution

GoReleaser v2.19.0 builds exactly the five spec targets without cgo. Archive
names omit the version (`baton_linux_arm64.tar.gz`, for example); the release
URL supplies it. This lets the installer use GitHub's `latest/download`
route without an API call or JSON parser. `checksums.txt` contains SHA-256
digests. Unix archives are tar.gz; Windows is zip. Go build metadata remains
embedded. Signing, notarization, and optional SBOM generation are unconfigured.

```text
tag -> native gates -> GoReleaser -> draft archives + checksums
                                      |
                           formula renderer -> attached baton.rb

installer -> HTTPS archive + manifest -> SHA-256 -> stdout extraction
                                                        |
                                    destination staging -> rename baton
```

The installer is a standalone POSIX shell script suitable for piping into
sh. It defaults to `~/.local/bin`; environment overrides choose a version
or absolute destination directory. It ignores curlrc, permits only HTTPS,
requires a unique exact archive digest, extracts only the binary to stdout,
and replaces a regular destination only after verification. Symlink and
nonregular destinations fail. It never edits shell or agent files. Rename
is atomic on the destination filesystem; no power-loss durability or
hostile same-user filesystem-race isolation is promised. Concurrent
successful installers may replace each other's verified binary.

Release tags prepare drafts. The
workflow gates its tagged source on current Linux/macOS runners and Windows
core checks. The retired macos-13 entry remains in ordinary CI because the
spec is immutable; it is omitted from this separate draft workflow. This
does not establish a green run of the spec's complete hosted CI matrix.

The Homebrew formula is rendered from the checked manifest and attached to
the draft. Copying it into sushilk1991/homebrew-tap is a later explicit
operation; this repo receives no tap credentials and makes no remote write
while preparing distribution. Formula tests replace HOME, USERPROFILE,
every inherited XDG key, and standard XDG paths. Unit tests pipe the real
installer into sh with fake platform/network boundaries and real archives,
hashing, installation, and executable invocation. Local snapshot packaging
and fixtures do not prove published release or tap availability. See
[distribution](distribution.md) for the checked primary references.

## Stress benchmark

`scripts/stress.sh` builds baton and a private `internal/stress` helper with
cgo disabled. Build caches, HOME, USERPROFILE, and all XDG paths are temporary;
the launcher clears inherited agent settings and bypasses shims during builds.
`-modcacherw` keeps downloaded modules removable. The helper uses a new native
temporary HOME and scheduler state per phase, then copies closed state into
the artifacts and removes the temporary HOME. Artifacts use a new output
directory; an existing directory is rejected. Neither the installed baton nor
the user's configuration is changed. Building can fetch the pinned Go module
dependency.

```text
sequential: worker -> wait -> worker -> wait ...
unqueued:   N workers -> shared start barrier -> N allocations
queued:     N baton run contenders -> one gated slot -> worker -> allocation
                                                    |
200 ms memory samples + worker records + kernel events -> JSON + PASS/FAIL
```

All phases run the same number of workers and the same allocation/hold
work. Workers fill their allocation with pseudorandom bytes, then write to
each OS page every 100 ms. They retain the allocation until the hold timer
expires. Start/end records measure overlap independently of baton status.
The queued phase declares the allocation with `--mem`, uses one slot, keeps
memory gating enabled, and retains the production margin, poll interval, and
recovery window. The benchmark explicitly sets max_wait to 30 seconds rather
than the production default of 30 minutes. Every timeout warning is counted;
queued wall time includes that wait and process startup. The default 45-second
hold and four jobs allow a bounded run on a host already at warning pressure.
A timeout admission does not prove pressure recovery.

Before each phase, allocation plus a 512 MiB reserve must fit within both
one quarter of effective RAM and half the reported available memory. Critical
preflight pressure refuses the benchmark. Critical samples, 1 GiB phase swap
growth, observation errors, nonzero jobs, or a phase deadline stop only owned
children and fail. A private stdin pipe reaches workers through baton; EOF
cancels them even if the parent dies. Normal cancellation also sends TERM to
the owned direct child; a three-second exec deadline bounds cleanup. Workers
have a finite hold even if other cleanup fails. No process-name scan or reap
command is used.

macOS queries structured `log show` output for kernel memorystatus, jetsam,
and out-of-memory messages, including info messages. The query widens second
boundaries and counts all matches conservatively. A valid completion marker
and matching count are required; empty or malformed output fails. Logs can
omit unpersisted/redacted events, so the report says observed events, not an
exhaustive host kill census. Linux compares `/proc/vmstat`'s `oom_kill` counter
before and after each phase; an absent or decreasing counter fails. This is
host-wide, so an unrelated OOM can fail the benchmark. Exit statuses separately
detect killed owned jobs; an arbitrary SIGKILL is never labelled a proven OOM.
Windows builds the helper but does not support these stress measurements.

The first Linux smoke put scheduler state beneath a Docker host-bind artifact
directory. On this machine's `fakeowner` mount, two contenders both acquired
an exclusive flock; the same probe on native `/tmp` blocked the second one.
Both queue writers chose sequence 1, and exclusive ticket-file creation
detected the collision before a worker ran. The smoke failed. Normal tests
used native temporary directories, so the full Linux gate had not exposed
this filesystem mismatch. The stress helper now keeps active lock state on
the native temporary filesystem and copies evidence out after all children
exit. Core lock and queue code are unchanged. This host bind mount remains
unsuitable for shared baton state; use a filesystem with working local locks.

PASS requires all three phases: pressure below critical, peak-minus-start
swap below 1 GiB, zero observed kill events/killed jobs, every worker complete,
expected overlap, and each wall time at most 1.2 times measured sequential
time. Missing phases or observations fail. These are sampled thresholds for
one bounded workload; they cannot establish safety under arbitrary load or
an improvement when the unqueued baseline also passes. Raw samples, job
records, warning logs, event data, config, history, and a binary SHA-256 remain
beside the machine-readable result. `docs/benchmarks.md` records real runs,
including a failed short-workload smoke, separately from synthetic tests.

## Final-gate browser observation

A repeated lifecycle test exposed the shutdown interval between removing
`session.json` and releasing `browser.lock`. `status` and `stop` previously
returned a missing-file error during that interval. Management commands now
retry that missing-session observation within their existing 15-second
deadline. They recheck the lifetime lock each time, so shutdown resolves to
stopped and startup resolves to a ready session. Invalid JSON, control errors,
and other failures still return immediately. Observation neither starts a
supervisor nor signals a process.

```text
session removed -> lifetime lock held -> observer waits
cleanup ends    -> lifetime lock free -> observer reports stopped
```
