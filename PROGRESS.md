# Progress

Done: scaffold, private CLI help/dispatch, isolated unit tests, internal
package boundary, quality gate, CI matrix, Apache-2.0 license.

Verified: `make check` on macOS and in the required `golang:1.27` Docker
image; all five static cross-build targets; actual macOS help and unknown
command exit status. Failure injection confirms lint/compiler errors stop
the gate. golangci-lint is absent and was skipped.

Next: implement the lanes/slots item in `TASKS.md`, starting with real-process
tests for the concurrency bound and crash release.

Pending: hosted CI has not run. The spec requires the retired `macos-13`
runner; see `docs/architecture.md`. Scheduling commands are not implemented.
