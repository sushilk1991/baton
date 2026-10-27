# Progress

Shipped locally: sixteen of seventeen TASKS.md items, through Stress.
Stress implementation: 062b965. Only Stress was completed this iteration.
SPEC.md is unchanged. No push, tag, release, or remote change was made.
The overall goal remains incomplete.

Verified: native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and five cgo-free cross-builds, including the
stress helper. golangci-lint was unavailable and skipped. Native Go was
1.27.0 darwin/arm64, macOS 27.0.1 (26A434).

The resumed stress draft was inspected against SPEC.md and its raw artifacts.
The default workload was then rerun on this machine: four 256 MiB workers,
45-second holds, one queued slot, memory gate enabled, max_wait 30 seconds.
`scripts/stress.sh --output .build/stress-resume-native` exited 0 and printed
PASS. Sequential/unqueued/queued wall times were 180.761/45.193/211.773 seconds.
Queued time was 1.172 times sequential; independent overlap was 1/4/1.
All twelve jobs completed, sampled swap growth was zero, and observed kill
counts were zero. All samples reported warning pressure; all four queued jobs
used timeout admission. The unqueued baseline passed too. This proves the
measured bounds, not reduced pressure under overload or recovery admission.

The repeat's 2,192 raw samples, twelve allocation/page-touch records, kernel
log completion counts, four finished history records, timeout warnings, and
native binary SHA-256 were independently checked. The five committed JSON
results retain native passes, a failed short-workload smoke, a Linux container
pass, and a failed bind-mount run. Docs explain each result and its limits.
A fresh native build matched the benchmark hash; Docker's cross-build output
had a different hash and was not substituted for that native comparison.

The helper isolates HOME/XDG and active scheduler state, uses owned child
pipes/signals for cancellation, and archives closed evidence. Tests cover
threshold boundaries, missing observations, process overlap, timeout cleanup,
an unaffected sentinel, launcher isolation/exit codes, and state placement.
Captured red logs precede the implementation and the cache/state fixes.
Shell syntax, formatting, and diff checks passed. no-ai-slop final pass applied.

Pending: only Final gate. Re-run both full gates and every README command.
Do not count prior unit tests, local archives, or documented recipes as proof
of hosted release availability, installed tools, Windows runtime, a rendered
VHS recording, or real renderer/model/client flows.

Open risks: this Docker host bind mount admitted concurrent exclusive flock;
use native temporary state, as the helper does. Core locks/queue are unchanged.
A prior browser shutdown/status race remains for Final gate review. Hosted CI
still includes retired macos-13 per SPEC.md; neither local gate proves that
hosted matrix green. Kernel logs and 200 ms samples can miss events/spikes.

Evidence: .yoyo/stress-resume-{native-check,linux-check,native,audit,
repeat-audit}.log; earlier .yoyo/stress-*.log; raw .build/stress-* artifacts.
Published binaries, taps, workflows, and services were not changed.

Next: read .yoyo/loop-state.md, SPEC.md, TASKS.md, and this file. Complete only
Final gate; start by enumerating README commands and their required fixtures.
