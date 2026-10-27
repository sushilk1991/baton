# Progress

Done: scaffold and weighted lanes/slots. Commit 42aa421 adds Unix flock and
Windows LockFileEx drivers, all-or-nothing reservations, cancellation, and
idle-only capacity changes. No CLI scheduling command exists yet.

Verified: `make check` passed on macOS and in `golang:1.27` Docker after the
slot implementation. All packages cross-build without cgo for all five
targets. Real subprocesses measured 20 jobs with peak concurrency 2 for a
two-slot lane; a killed holder released both slots in 12.233834 ms on macOS.
Race tests also cover weights, rollback, cancellation, and capacity changes.
golangci-lint is absent and was skipped.

Next: implement queue tickets, FIFO within priority, aging, and dead-ticket
cleanup. Start by reading the lane API and writing real-process order tests.

Pending: Windows runtime tests and hosted CI have not run. See the existing
runner note in `docs/architecture.md`. The literal `baton run --lane x --
sleep` integration waits for the CLI task; current process tests exercise
the lane API. FIFO and starvation protection belong to the next queue item.
