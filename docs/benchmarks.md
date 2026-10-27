# Benchmarks

Run `scripts/stress.sh` from a source checkout with Go installed. The default
run takes about eight minutes on this machine, including isolated builds.
It prints a PASS/FAIL table and exits nonzero if any criterion fails.

The measured macOS run passed for four 256 MiB jobs. The unqueued baseline
also passed. This establishes the reported bounds and one-slot serialization
for this workload; it does not demonstrate reduced pressure under overload.

## Reproduce

```sh
scripts/stress.sh --output .build/stress-local
```

Use a new output directory for each run. `--jobs`, `--gib`, `--hold`,
`--max-wait`, and `--timeout` change the workload; `--help` lists defaults.
The launcher builds cgo-free binaries and clears inherited agent settings.
Builds and scheduled commands use private native temporary HOME/XDG
directories. Installed configuration, shell files, and agent files are
untouched. Building may fetch
the pinned Go dependency. The temporary build cache is removed after exit.
After every phase's jobs exit, the helper copies closed config/state/history
into the artifacts and removes active temporary state. Artifact storage never
holds active locks.

Before each phase, allocation plus 512 MiB must fit within both one quarter
of effective RAM and half the reported available memory. Reduce job count or
allocation if preflight rejects the budget. Critical pressure, 1 GiB swap
growth, a failed observation/job, or a phase deadline cancels owned workers.
The helper never scans for or signals unrelated processes.

Each worker fills its allocation with pseudorandom bytes and revisits each
OS page every 100 ms. Hold time starts after filling. The phases are
sequential, simultaneous without baton, and simultaneous through one baton
slot. The queued profile is:

```toml
[lanes.stress]
slots = 1
max_weight = 1
memory_gate = true
max_wait = "30s"
```

The normal 512 MiB margin, 250 ms admission polling, and two-second recovery
window remain enabled. The benchmark explicitly uses a 30-second timeout;
production lanes default to 30 minutes. Wall time includes startup and all
queue/memory waits. Every timeout bypass is reported. A short workload can
fail the throughput criterion on a host that stays at warning pressure.

PASS requires all three phases to finish every job, match the expected
worker overlap, stay below critical pressure and 1 GiB phase swap growth,
observe zero kill events/killed jobs, and finish within 1.2 times measured
sequential wall time. Missing or malformed observations fail.

## macOS arm64, 2026-10-27

Command: `scripts/stress.sh --output .build/stress-native-local`, exit 0.
macOS 27.0.1 (26A434), Go 1.27.0 darwin/arm64. The table below is the
helper's captured output, before the artifact-path line.

```text
Time: 2026-10-27T16:49:02Z

Host: darwin/arm64, 14 CPUs, 36.00 GiB RAM, 8.26 GiB available at preflight.

Work: 4 jobs, 0.2500 GiB each, hold 45s; one queued slot, memory gate on, max_wait 30s, phase timeout 10m0s.

| Mode | Wall s | Peak swap GiB | Growth GiB | Worst pressure | Kill events / killed jobs | Completed | Max active | Bypasses | Result |
| --- | ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: | --- |
| sequential | 180.725 | 7.906 | 0.000 | warn | 0 / 0 | 4/4 | 1 | 0 | PASS |
| unqueued | 45.194 | 7.906 | 0.000 | warn | 0 / 0 | 4/4 | 4 | 0 | PASS |
| queued | 211.882 | 7.899 | 0.000 | warn | 0 / 0 | 4/4 | 1 | 4 | PASS |

PASS: pressure below critical; swap growth < 1 GiB; no observed kills/events; wall <= 1.2x sequential.
```

[Machine-readable result](benchmarks/macos-arm64-2026-10-27.json) retains
full-precision timings, counters, sample counts, and the baton binary SHA-256.
Raw local artifacts remain under `.build/stress-native-local/`: each phase has
`samples.jsonl`, four worker records, command logs, and event-query results.
The queued phase also retains its isolated config, state, and history.

The 2,193 memory samples all reported warning pressure. No phase's sampled
swap rose above its starting value. All twelve worker records confirmed
268,435,456 allocated bytes and repeated page touches. Queued history recorded
four exit-zero jobs with peak RSS between 276,086,784 and 276,250,624 bytes.
Independent worker timestamps measured maximum overlap of 1, 4, and 1.

Queued wall time was 1.172 times sequential. All four queued jobs reached
max_wait and printed bypass warnings. This run verifies timeout admission
and serialization while the host stayed at warning pressure. It does not
verify recovery-based admission or the production 30-minute timeout profile.

A repeat at 17:04:53 UTC used the same defaults and binary and exited 0:
`scripts/stress.sh --output .build/stress-resume-native`. Sequential,
unqueued, and queued wall times were 180.761, 45.193, and 211.773 seconds.
Queued time was 1.172 times sequential. All twelve jobs completed; overlap
was 1/4/1, swap growth was zero, and observed kill counts were zero. Pressure
stayed at warning and all four queued jobs reached max_wait again.
[Repeat result](benchmarks/macos-arm64-2026-10-27-repeat.json); raw artifacts
remain in `.build/stress-resume-native/`.

## Failed short-workload smoke

Command: `scripts/stress.sh --jobs 2 --gib 0.015625 --hold 3s --max-wait 1s
--output .build/stress-smoke`, exit 1.

Time: 2026-10-27T16:27:17Z

Host: darwin/arm64, 14 CPUs, 36.00 GiB RAM, 8.67 GiB available at preflight.

Work: 2 jobs, 0.0156 GiB each, hold 3s; one queued slot, memory gate on, max_wait 1s, phase timeout 10m0s.

| Mode | Wall s | Peak swap GiB | Growth GiB | Worst pressure | Kill events / killed jobs | Completed | Max active | Bypasses | Result |
| --- | ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: | --- |
| sequential | 6.035 | 8.369 | 0.000 | warn | 0 / 0 | 2/2 | 1 | 0 | PASS |
| unqueued | 3.017 | 8.369 | 0.000 | warn | 0 / 0 | 2/2 | 2 | 0 | PASS |
| queued | 7.763 | 8.369 | 0.000 | warn | 0 / 0 | 2/2 | 1 | 2 | FAIL |

queued: wall time > 1.2x sequential or missing

FAIL: one or more stress criteria were not met.

[Captured smoke result](benchmarks/macos-arm64-2026-10-27-smoke.json).
The 1.286 times sequential result exceeded the unchanged 1.2 limit. Both jobs
completed, but queue startup and the memory wait were too large relative to
three-second holds. The launcher also exposed read-only module-cache cleanup;
a regression reproduced it before `-modcacherw` fixed cleanup and preserved
helper exit codes. The default measured run used its original 45-second hold.

## Linux arm64 container smoke, 2026-10-27

Command, exit 0:

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.27 scripts/stress.sh \
  --jobs 2 --gib 0.015625 --hold 6s --max-wait 1s \
  --output .build/stress-linux-local
```

This is a 16 MiB-per-job container smoke, separate from the macOS workload.
The native Linux OOM source was exercised: all three `/proc/vmstat` before
and after snapshots had `oom_kill 0`. The queued run emitted one timeout
warning while waiting for the single occupied slot, despite normal pressure.

| Mode | Wall s | Peak swap GiB | Growth GiB | Worst pressure | Kill events / killed jobs | Completed | Max active | Bypasses | Result |
| --- | ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: | --- |
| sequential | 12.032 | 0.083 | 0.000 | normal | 0 / 0 | 2/2 | 1 | 0 | PASS |
| unqueued | 6.017 | 0.083 | 0.000 | normal | 0 / 0 | 2/2 | 2 | 0 | PASS |
| queued | 12.138 | 0.083 | 0.000 | normal | 0 / 0 | 2/2 | 1 | 1 | PASS |

[Machine-readable container result](benchmarks/linux-arm64-2026-10-27.json).
Raw artifacts remain in `.build/stress-linux-local/`.

The first container attempt kept active state beneath the artifact directory
on the host bind mount. It failed before any queued worker started with
`00000000000000000001.json: file exists`.
[Failed result](benchmarks/linux-arm64-2026-10-27-bind-failure.json).
A separate real `flock` probe captured:

```text
/src/.build/stress-lock-probe.aPaTu7: UNSAFE concurrent flock admitted
/tmp/tmp.rX5oVfO75u: second flock blocked
/run/host_mark/Users /src fakeowner rw,nosuid,nodev,relatime,fakeowner 0 0
```

This mount admitted concurrent exclusive locks; both queue writers chose
ticket 1. Exclusive file creation caught the collision. Native `/tmp` enforced
the lock. A failing state-location regression preceded the helper's switch to
native temporary state and copying evidence out after jobs exit. Queue and
lock code remain unchanged. Do not keep shared baton state on this host mount.
The full Linux gate uses native temporary directories and had already passed;
that gate alone did not establish correctness on the host bind mount.

## Observation limits

Memory is sampled every 200 ms with the same native provider used by baton.
Samples can miss shorter spikes. Swap includes unrelated machine activity;
growth means phase peak minus phase start, not peak swap's absolute size.

macOS queries `log show` for kernel memorystatus, jetsam, and out-of-memory
messages, including info messages, across a widened second boundary. It
requires a structured completion count and stores the raw NDJSON. Counts
are conservative matches; unpersisted or redacted logs can hide events.
Linux compares the host `/proc/vmstat` `oom_kill` counter before and after a
phase. An unavailable, malformed, or decreasing source fails. Owned job
exit statuses provide a separate SIGKILL check. Neither source attributes
unrelated host events to baton, and no OOM was deliberately induced.

Unit tests inject observations to exercise failure paths. Real-process tests
check overlap, timeout cleanup, an unaffected independent sentinel, launcher
isolation, and exit-code preservation. These tests are separate from the
measured macOS result. Windows builds the helper; stress measurements require
macOS or Linux. Other hardware, larger allocations, real render/ML workloads,
and a machine driven into critical pressure remain unmeasured.
