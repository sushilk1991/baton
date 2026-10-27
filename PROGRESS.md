# Progress

Not verified: Final gate remains open. All three public README install
commands fail. Source/release/tap publication is forbidden in this task.

Shipped locally: sixteen of seventeen TASKS.md items, plus a browser fix
found during Final gate. SPEC.md, README.md, and TASKS.md are unchanged.
No push, tag, release, or remote change was made.

Previously verified: post-fix native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and all five cgo-free cross-builds. Lint was
unavailable and skipped. Native Go was 1.27.0 darwin/arm64, macOS 27.0.1.

A lifecycle repeat failed twice in twenty runs: the supervisor had removed
session.json but still held browser.lock. A deterministic status/stop test
failed before the fix and passed after it. Management commands now wait
through that interval within the existing deadline. Deadline and corrupt-JSON
checks passed. The unchanged CLI lifecycle test then passed all twenty runs.
An owned headless Chrome returned Chrome/156.0.8078.48 through real CDP and
stopped cleanly. Only missing-session observations are retried.

All nine README shell blocks were rerun under private HOME/XDG/Go paths.
A copied Homebrew runtime kept tap writes in a temporary prefix. Sixteen
of nineteen invocations passed; all twenty-one behavior assertions passed.
The other three invocations failed: Homebrew has no baton formula; the raw
installer URL returned HTTP 404 (curl exit 56); Go found no latest version.
A fresh GitHub release query returned zero releases. All three failures
remain part of the Final gate result.

Pending: public installation prerequisites and successful reruns of those
three commands. Final gate stays unchecked; STATUS: DONE was not written.
See [the verification report](docs/verification.md) for commands and evidence.

Iteration 23 checked the release prerequisite without rerunning installs.
`gh-axi release list` returned `count: 0` and `releases: []`.
This fresh check rules out a newly published release as an unblocker.
The earlier iteration 22 check found no public branch refs; that query was
not repeated. Neither full gate ran and no production code changed here.
Evidence: `.yoyo/iteration23-prerequisites.log` and the earlier
`.yoyo/iteration22-prerequisites.log`. Stop automatic retries until the caller
resolves publication outside this task; another local gate cannot supply
the missing public sources.

The prior stress task remains complete: native default runs printed PASS,
including 211.773 seconds queued / 180.761 seconds sequential (1.172), overlap
1/4/1, zero sampled swap growth, and zero observed kills. All samples were
warning pressure and every queued job used timeout admission; the unqueued
baseline also passed. [Benchmarks](docs/benchmarks.md) retain the measured
scope and failed smokes. Stress was not rerun during this iteration.

Open risks: the Docker host bind mount previously admitted concurrent flock;
active state must use working local locks. Hosted CI retains retired macos-13
per SPEC.md. Hosted workflows, Windows runtime, VHS rendering, linked
renderer/model recipes, and lint remain unverified. Browser control transport
errors still fail immediately.

Evidence: `.yoyo/final-{native,linux}-fixed-check.log`,
`.yoyo/final-browser-{repeat,repeat-green,transition-red,tests,real}.log`,
`.yoyo/final-readme.log`, `.yoyo/final-readme/`, and `.yoyo/final-github.log`.
Earlier stress evidence remains `.yoyo/stress-*` and `.build/stress-*`.

Next: the caller must resolve the publication prerequisite. Do not repeat
the same failed public checks or weaken Final gate while remote writes
remain forbidden. After authorized publication outside this task, rerun
both full gates and every README command before checking it off.
