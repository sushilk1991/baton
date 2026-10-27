# Progress

Done: scaffold, weighted lanes/slots, and fair queues. Commit b5656a9 adds
persistent tickets, FIFO within priority, five-minute aging promotions,
kernel-lock liveness, dead-ticket cleanup, and cancellable admission.
Queue policy and storage decisions are in docs/architecture.md.

Verified: `make check` passed on macOS and in `golang:1.27` Docker after the
queue implementation. Vet, race tests, and all five cgo-free cross-builds
passed. Real subprocesses recorded `[first second third]` at each priority
and `[high normal low]` across priorities. After SIGKILL, the next process
entered in 12.247583 ms on macOS. Unit tests cover aging boundaries, weighted
head blocking, cancellation, concurrent registration, and damaged metadata.
golangci-lint is absent and was skipped by both gates.

Next: implement pressure providers for macOS, Linux (including cgroup v2),
and Windows, plus hysteresis and max_wait with an injected fake provider.
Read the queue API first; keep ticket order while the memory gate waits.

Pending: no CLI scheduling command exists yet. Process tests exercise the
lane and queue APIs; the literal `baton run` integrations belong to the run
task. Windows runtime tests and hosted CI have not run. The existing CI
runner limitation remains documented in docs/architecture.md.
