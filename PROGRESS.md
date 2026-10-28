# Progress

## Review fixes, iteration 6 (2026-10-28)

Shipped locally: Job control only; the sixth review item is checked off.
The Unix runner observes child stops without reaping exit status, stops its
owned child group and itself, and returns terminal control through the shell.
After fg, it hands the terminal to the child before continuing that group.
Stopped jobs retain lane slots. Completion and failed exec restore terminal
ownership; terminal-handoff failure resumes the child before waiting.

Verified: native make check and golang:1.27 Docker make check exited 0,
including vet, race tests, and all five cross-builds. Pinned
golangci/golangci-lint:v2.15.0 reported 0 issues and exited 0. Five targeted
race runs passed on each OS. Real PTYs run the built CLI through Bash on
macOS/Linux and zsh on macOS with line editing disabled. Each child stops
and resumes twice, reads after fg, traps Ctrl-C, and returns exit 23. The
shell regains input, stopped capacity stays occupied, a subsequent one-slot
job runs, history records completion, and failed exec returns 127. A driver
regression verifies ENOTTY recovery without stranding a stopped child.
All checks isolate HOME, USERPROFILE, every XDG key, and Go paths; only owned
children were signaled.

Red evidence: .yoyo/review-job-control-red-{native,linux}.log records the
original Ctrl-Z hang before production edits. The synchronized PTY test also
fails against the committed baseline through a Go source overlay in
review-job-control-red-refined.log. An intermediate Linux attempt resumed
the child before the supervisor had actually stopped; the captured exchange
is review-job-control-resume-red-linux.log. Waiting for SIGCONT fixes that
race. review-job-control-tty-red.log records the stopped child stranded by
a terminal error before the recovery fix. Early fixture failures involved
prompt/input timing, querying a terminal outside its controlling session,
and zsh line editing; the final fixture waits for shell and child markers.
Final logs: review-job-control-{native,linux,lint,targeted,targeted-linux}.log.
Full evidence and limits: .yoyo/review-job-control-report.md.

Pending: Agent label and Final gate. Other shells, full-screen terminal
applications, native Windows runtime, and bg behavior were not exercised.
The architecture records the stop/continue protocol and slot-retention
choice. SPEC.md, remotes, real agent configuration, tags, and releases are
unchanged. No push or publication.

Next: read .yoyo/loop-state.md, then add the Agent label red regression.

## Review fixes, iteration 5 (2026-10-28)

Shipped locally: Hook classification only; the fifth review item is checked
off. The hook matches executable basenames and known dispatch operations in
each literal shell segment after assignments and sudo/time/nice/env wrappers.
Argument keywords, comments, redirection targets, and tool directory names
do not select lanes. The first matching segment selects the whole command's
lane. Existing baton reservations skip only their own segment. Rewritten
commands use the quoted `$SHELL` path, falling back to `/bin/sh` when empty.

Verified: native `make check`, `golang:1.27` Docker `make check`, and
`golangci/golangci-lint:v2.15.0` exited 0; lint reported `0 issues.` Both
full gates passed vet, race tests, and all five cross-builds. Public-hook
tests cover the required unqueued/queued commands, wrappers and their option
values, quoted/escaped separators, pipelines, and the blocking fallback.
Compiled-CLI tests execute the selected shell with a path containing spaces
and a quote, execute the `/bin/sh` fallback, and exercise the installed hook
with nested shims and quoted argv. Every check used private HOME, USERPROFILE,
all inherited XDG keys, and Go paths. Only owned child processes were signaled.

Red evidence: `.yoyo/review-hook-red-native.log` records exit 1 before
production edits, including all four required argument false positives and
both shell-selection failures. Refutations caught `&>` redirection and a
heavy keyword in an executable directory; their red runs are
`review-hook-{refute,basename}-red.log`. An intermediate targeted run retained
the old Bash assertion and failed; the assertion now requires the selected
shell. Final green logs: `review-hook-{native,linux,lint,targeted}.log`.
Evidence and limits: `.yoyo/review-hook-report.md`.

Pending: Job control, Agent label, and Final gate. Shell expansions, functions,
scripts, here-docs, and `env -S` remain opaque; use explicit runs or shims.
Live Claude loading and Windows runtime were not exercised. Decisions are
recorded in `docs/architecture.md`; SPEC.md is unchanged. No push, tag,
publication, remote change, or real agent configuration write occurred.

Next: read `.yoyo/loop-state.md`, then add the Job control pty red test.

## Review fixes, iteration 4 (2026-10-28)

Shipped locally: Uninstall after user edits only; the fourth review item is
checked off. Reinstall and uninstall reconcile the recorded owned edit with
current contents. Text surrounding unchanged instruction blocks, unrelated
JSON settings and hook entries, supported Aider settings, and current modes
survive. Reinstall keeps edited files byte-identical. Edited or duplicated
owned blocks/entries still fail before restoration and name the preserved
path. Existing version-1 manifests and resolved instruction/settings links
remain supported.

Verified: native `make check`, `golang:1.27` Docker `make check`, and
`golangci/golangci-lint:v2.15.0` exited 0; lint reported `0 issues.` Both
full gates passed vet, race tests, and all five cross-builds. Compiled-CLI
tests cover preview, repeated reinstall, later edits after reconciliation,
and uninstall with user text above/below the block, existing/new user hooks,
an unrelated 64-bit integer, and original modes. Direct regressions cover
initially absent/existing files, text inserted directly above a marker,
linked settings, and edited/duplicated owned content. HOME, USERPROFILE,
every inherited XDG key, and Go paths were private throughout the checks.

Red evidence: `.yoyo/review-init-edits-red-native.log` records the original
settings-uninstall and instruction-reinstall failures. The initial Aider
fixture also changed its temporary path; the corrected fixture failed
against the committed installer via Go's source overlay, saved as
`review-init-edits-aider-red.log`. Direct instruction uninstall against that
installer is `review-init-edits-snippet-red.log`. Additional red regressions
are `review-init-edits-{rebase,symlink,adjacent}-red.log`. Final green logs:
`review-init-edits-{native,linux,lint,targeted}.log`. Full evidence and limits:
`.yoyo/review-init-edits-report.md`.

Pending: three review fixes and Final gate. Edited JSON is reformatted on
removal; ambiguous Aider YAML read-field changes require manual resolution.
Live agent sessions and Windows runtime were not exercised; Windows builds
passed. Decisions are recorded in `docs/architecture.md`.

Next: read `.yoyo/loop-state.md`, then add the Hook classification red tests.

## Review fixes, iteration 3 (2026-10-28)

Shipped locally: Shim fallback only; the third review item is checked off.
macOS/Linux shims exec the real tool when baton is missing or lacks execute
permission. Fallback removes every occurrence of the current wrapper from
PATH, including aliases; copied wrappers remove their entries in turn.
Installations preserve the absolute invoked path and its symlinks, including
relative paths and PATH entries, so package upgrades can replace the target.

Verified: native `make check`, `golang:1.27` Docker `make check`, and
`golangci/golangci-lint:v2.15.0` exited 0; lint reported `0 issues.`
Both full gates passed vet, race tests, and all five cross-builds. Real CLI
regressions cover moved/non-executable binaries, normal/off mode, exits 0/3,
quoted and empty arguments, symlink aliases, copied wrappers, and absent
real commands. A real executable in the cwd remains unselected when all
PATH entries were removed. A replaced install symlink still schedules the
tool in encode and records history. All tests used private HOME/XDG paths.

Red evidence: `.yoyo/review-shim-red-{native,linux}.log` (exit 1). All eight
fallback cases failed on both systems; Linux also lost the install symlink.
Native already preserved that link. An additional red regression caught
`exec.ErrDot` for an already-running invocation found through `PATH=.`;
`.yoyo/review-shim-relative-red.log` preserves it. Final green evidence:
`.yoyo/review-shim-{native,linux,lint,targeted}.log`.
Full evidence: `.yoyo/review-shim-report.md`.

Pending: four review fixes and Final gate. Windows batch wrappers remain
best effort without this Unix fallback; only Windows cross-builds ran.
A binary disappearing between the availability check and exec can still
fail. Fallback creates no scheduling/history records. Decisions are in
`docs/architecture.md`; actual package manager upgrades were not run.

Next: read `.yoyo/loop-state.md`, then write the uninstall-after-edits red
regressions for settings.json and instruction snippets.

## Review fixes, iteration 2 (2026-10-28)

Shipped locally: nested jobs only; the second review item is checked off.
Explicit runs, shims, and hook wrappers share live ancestor reservations
for the same lane. Nested jobs retain history with `nested: true`; other
lanes still queue. The child environment carries lane/job IDs without
changing the runner's own environment. Installed skill and AGENTS snippet
commands require no changes.

Verified: native `make check`, `golang:1.27` Docker `make check`, and
`golangci/golangci-lint:v2.15.0` exited 0; lint reported `0 issues.`
Both full gates passed vet, race tests, and all five cross-builds. Real CLI
regressions execute the four installed skill recipes, the instantiated
AGENTS template, and explicit npm wrapping with recursive fake tools and
installed shims. Each produces three history records, two nested. A direct
install -> encode -> install chain preserves exit 3 and releases both lanes.
A different-lane shim waits behind an occupied slot. Unit tests reject stale,
malformed, and unmatched markers. All tests used private HOME/XDG paths and
only signalled their own children.

Red evidence: `.yoyo/review-nested-red-native.log` (exit 1, all seven original
cases timed out). The first full native gate exposed premature running
metadata without a birth token; `.yoyo/review-nested-native-first.log`
retains that failure. The final code preserves the existing birth-token
publication rule and its unchanged regression. Green evidence:
`.yoyo/review-nested-{native,linux,lint,targeted}.log`.
Full evidence: `.yoyo/review-nested-report.md`.

Pending: five review fixes and Final gate. Same-lane descendants share the
parent's slots and memory budget; internal parallelism needs tool limits.
Inheritance expires with the owner lock and does not retain slots after a
parent crash. Decisions are recorded in `docs/architecture.md`.

Next: read `.yoyo/loop-state.md`, then write the Shim fallback red tests.

## Review fixes, iteration 1 (2026-10-28)

Shipped locally: reap safety only; the first review item is checked off.
Headed browsers are excluded even with a matching job record. Encoders and
renderers require a matching birth record from a dead owner. Unrecorded
headless browsers and automation drivers require verified host-init adoption.

Verified: native `make check`, `golang:1.27` Docker `make check`, and
`golangci/golangci-lint:v2.15.0` all exited 0; lint reported `0 issues.`
Both checks passed race tests, vet, and all five cross-builds. Tests used
private HOME/XDG paths and only signalled their own fakes. Native CLI tests
preserved five protected fakes and removed two eligible ones. The Docker
regression exercised non-init PID 1 adoption. Red evidence precedes the fix:
`.yoyo/review-reap-red-native.log` and `.yoyo/review-reap-red-linux.log`.
Green evidence: `.yoyo/review-reap-{native,linux,lint,targeted}.log`.

Pending: six review fixes, starting with nested jobs, then Final gate.
Linux host adoption needs `/usr/bin/systemd-detect-virt`; missing or
ambiguous detection requires job provenance. Bare-host Linux adoption was
not exercised. This decision is documented in `docs/architecture.md`.
The current queue permits recording public installs as pending owner
publication; the earlier publication blocker below is superseded.

Next: read `.yoyo/loop-state.md`, then add the nested-job red regression.

## Earlier phase-1 progress

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

Iteration 25 checked for external publication without rerunning installs.
`gh-axi release list` returned `count: 0` and `releases: []`.
`git ls-remote --heads --tags https://github.com/sushilk1991/baton.git`
exited 0 with no refs. The README installer URL returned HTTP 404; curl
exited 56. Neither full gate ran and no production code changed.
Evidence: `.yoyo/iteration25-prerequisites.log`. Stop automatic retries until
the caller resolves publication outside this task. Final gate stays open.

Iteration 26 read the state and captured evidence. README.md says the release
commands require publication; SPEC.md requires every README command to work;
AGENTS.md forbids publication. No runtime or remote checks were repeated.
Only this handoff and `.yoyo/loop-state.md` changed. The caller must stop the
loop until publication is authorized and completed outside this task.

Iteration 27 rechecked the publication prerequisites. `gh-axi release list`
returned `count: 0` and `releases: []`; `git ls-remote --heads --tags`
returned no refs. The README installer URL returned HTTP 404, curl exit 56.
Neither full gate ran. No code or queue changes; Final gate stays open.
Evidence: `.yoyo/iteration27-prerequisites.log`. Stop automatic retries
until the caller resolves publication outside this task.

Iteration 28 checked whether publication had appeared. Release count is still
zero, remote branch/tag refs are empty, and the installer returns HTTP 404
(curl exit 56). Evidence: `.yoyo/iteration28-prerequisites.log`. No code or
queue changes; neither full gate nor README installs were repeated. The
caller must stop retries and resolve publication under separate authorization.

Iteration 29 checked the installer prerequisite once: HTTP 404, curl exit 56.
Evidence: `.yoyo/iteration29-prerequisite.log`. Publication remains forbidden;
Final gate stays unchecked. Full gates, README installs, and stress were not
rerun. No code change or task commit. Stop the loop until the caller resolves
publication outside this task.

Iteration 30 reran both full gates: native `make check` and the exact
`golang:1.27` Docker check exited 0. Both passed vet, race tests, and five
cgo-free cross-builds; lint was unavailable. README replay covered all nine
shell blocks: 16/19 invocations passed and all 21 behavior assertions passed.
The three public installs still failed: no Homebrew formula, installer HTTP
404 (curl exit 56), and no Go version matching latest. The release query
returned zero releases; the remote branch/tag query returned no refs.
Evidence: `.yoyo/iteration30-{native,linux,readme,prerequisites}.log` and
`.yoyo/iteration30-readme/manifest.json`. Tests and README commands used
temporary HOME/XDG paths; Homebrew used a temporary prefix. No production
code changed, no task completed, and no commit was made. Final gate stays
unchecked. The caller must resolve publication outside this task.

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
