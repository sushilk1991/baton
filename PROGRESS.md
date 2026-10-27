# Progress

Shipped locally: twelve TASKS.md items, through Browser. Implementation:
7f01d8a. SPEC.md is unchanged. No push, tag, release, or remote change.
The overall goal remains incomplete.

Verified: native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and all five cgo-free cross-builds.
golangci-lint was unavailable and skipped. Windows compiled; its runtime
remains unverified.

`browser start|stop|status|endpoint` manages one warm headless browser per
state directory. Discovery checks PATH, OS installations, and Playwright
cache layouts; configuration controls executable, flags, and idle timeout.
A detached supervisor holds a lifetime kernel lock, creates a private
profile, and proxies loopback CDP. Successful WebSocket connections count
as clients; the final disconnect starts the idle interval. HTTP discovery
and status polling do not extend it. Stop closes connections and terminates
the owned browser. Unix crash recovery checks process birth, user, and
private-profile argv before retiring a recorded orphan.

Real CLI process tests on macOS/Linux cover six simultaneous starts sharing
one browser, clients 2 -> 1 -> 0, active clients surviving the idle timeout,
idle expiry, restart, explicit stop, startup failure, and supervisor SIGKILL
recovery. An opt-in native test exchanged Browser.getVersion through the
proxy with Chrome/156.0.8078.48, then stopped it. Unit tests cover executable
layouts, managed flags, failed upgrades, discovery rewriting, origin/control
rejection, idle timing, and recovery identity guards. Every test uses
isolated HOME/XDG paths and stops only its owned processes.

Red evidence: cache discovery selected chrome-headless-shell.pak instead of
the executable. Exact executable basenames fix that Windows hazard. Native
make check also exposed a status snapshot race: a new queue ticket could
lose its label and agent. A deterministic regression failed with empty
identity; refreshing metadata once on a missing match fixes it. The original
real-process assertion passed ten repeated race runs and both full gates.
No checks were weakened.

Architecture decisions and limits are in docs/architecture.md. Browser
recipes use primary-source API checks; the JavaScript/MCP tool flows remain
unexecuted. The no-ai-slop prose pass was applied.

Pending: complete only init and MCP next. Read SPEC.md section 3.8, TASKS.md,
docs/architecture.md, and existing shim manifest/atomic-write patterns.
Implement fake-HOME install, idempotency, byte-identical uninstall, and the
read-only MCP tools. Do not start the general docs, distribution, stress,
or final-gate items in that iteration.

Limits: a supervisor crash before ownership publication can leave an
unrecorded orphan for reap; crash profiles remain. Direct connections to
Chrome's private port bypass counting. CDP is locally accessible and is
not a same-user sandbox. Windows runtime and crash recovery, hosted CI,
stress results, and every README command remain unverified. Prior cache,
gc, reap, scheduling, and platform limits remain in docs/architecture.md.
