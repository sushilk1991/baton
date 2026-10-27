# Progress

Done: scaffold, weighted lanes/slots, fair queues, and pressure admission.
Commit aa40526 adds macOS, Linux, and Windows providers plus a gate that
retains queue position, requires stable recovery and memory headroom, and
bypasses only memory policy after max_wait with a warning. Policy choices
and OS interfaces are recorded in docs/architecture.md.

Verified: `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` both exited
0 after the pressure implementation. Vet, race tests, and all five cgo-free
cross-builds passed. golangci-lint is absent and was explicitly skipped.
Real subprocesses verified warn-to-normal recovery, FIFO preservation, and
`WARNING: max_wait 100ms reached` under persistent critical pressure. Unit
tests cover interrupted recovery, strict headroom, overflow, cancellation,
probe failures, occupied-slot safety, warning-write failures, parent cgroup
limits, and malformed counters. The native macOS provider returned valid
RAM, swap, and warning pressure. A real Docker container with a 1073741824
byte memory.max reported TotalBytes:1073741824 and AvailableBytes:895901696.
The Windows pressure test binary also cross-compiled successfully.

Next: implement only the next TASKS.md item, `baton run`: execution, process
groups/signals, exit codes, notices, automatic lanes, identity, history,
process-tree peak RSS, and learned memory. Read cmd/baton/main.go and
internal/admission/gate.go first; write failing real-process tests before
wiring execution through queue tickets and the memory gate.

Pending: no CLI scheduling command exists yet. Process tests exercise the
internal APIs; literal `baton run` integrations remain in the run task.
Native Windows runtime, hosted CI, and machine-level stress targets remain
unverified. The existing CI runner limitation remains documented in
docs/architecture.md. No push, tag, release, or remote change was made.
