# Progress

## Re-review fixes, second pass, iteration 9 (2026-10-28)

Shipped locally: settings bytes; only that queue item is checked. Hook
installation inserts one entry and missing parents into the original JSON.
Removal validates semantic ownership, then edits raw spans. User key order,
whitespace, escapes, literal && and >, and unrelated edits survive. Moved or
reformatted owned entries still remove safely. Architecture records the
offset-based edits and rejection of duplicate keys on edited paths.

Verified: native make check and Linux copy-in Docker make check exited 0,
including vet, race tests, and five cross-builds. Pinned golangci-lint
v2.15.0 reported 0 issues and exited 0. Five format fixtures cover existing
hooks, empty containers, CRLF, tabs, escaped strings, and numeric spelling.
Removal tests cover first/middle/last positions and user events added under
a created parent. Eight compiled-CLI cases cover both hook policies,
preview, idempotence, and uninstall after user edits. Existing hook commands
using && and > execute with the expected output and redirected file.
All checks isolate HOME, USERPROFILE, XDG paths, TMPDIR, and Go writes.

Evidence: .yoyo/re-review-settings-bytes-red-native.log records all five
format failures before production edits. The final fixtures also reject
the original writers through a source overlay in -red-refined.log. Green
logs are -targeted, -integration, -cli, -native, -linux, and -lint under the
same prefix. The runner and report record commands, output, and limits.

Pending: reap listing reasons and the second-pass final gate. Live Claude
loading and Windows runtime were not exercised. No push, tag, publication,
remote change, or real agent settings write occurred. SPEC.md is unchanged.

Next: read .yoyo/loop-state.md, then write the reap-listing reason red.

## Re-review fixes, second pass, iteration 8 (2026-10-28)

Shipped locally: hook classification gaps; only that queue item is checked.
Literal BATON=off assignments skip their segment; the last assignment wins.
Help/version flags, the documented Ollama/uv/xcodebuild queries, and known
script extensions bypass lane matching, including launcher targets. Heavy
operations and later segments still queue. Architecture records the rules
and the if/then and extensionless-script limits.

Verified: native make check and Linux copy-in Docker make check exited 0,
including vet, race tests, and five cross-builds. Pinned golangci-lint
v2.15.0 reported 0 issues and exited 0. Ninety hook command cases cover
both rewrite and blocking policies. Thirteen compiled-CLI cases preserve
fake-tool stdout and exit 3; two heavy controls acquire holds, then public
status reports running=0 and queue=0. All checks isolate HOME, USERPROFILE,
XDG paths, TMPDIR, and Go writes.

Evidence: .yoyo/re-review-hook-gaps-red-native.log records 30 failures before
production edits. The launcher refutation records three more failures after
the first fix; launcher targets now receive the same filters. Green logs
are -targeted, -cli, -native, -linux, and -lint under the same prefix.
The first native gate failed in unchanged TestInstalledHookFlow's TempDir
cleanup after its assertions passed. Five isolated repeats and the full
gate passed unchanged. A detached holder finishing state writes is a
possible contributor; the cleanup error alone does not prove the cause.
The failed gate remains in -native-first.log; repeats are -installed-native.

Pending: settings bytes, reap listing reasons, and the second-pass final
gate. Live Claude loading and Windows runtime were not exercised. No push,
tag, publication, remote change, or real agent settings write occurred.
SPEC.md is unchanged.

Next: read .yoyo/loop-state.md, then write the settings-byte round-trip red.

## Re-review fixes, second pass, iteration 7 (2026-10-28)

Shipped locally: runtime-only agent labels; only that queue item is checked.
Exact, nonempty runtime markers replace broad environment prefixes. Config
and credentials alone fall through to TERM_PROGRAM or terminal. Explicit
--agent labels retain precedence; Aider uses --agent aider. Architecture
records the marker policy, upstream sources, and coverage limits.

Verified: native make check and Linux copy-in Docker make check exited 0,
including vet, race tests, and five cross-builds. Pinned golangci-lint
v2.15.0 reported 0 issues and exited 0. Fifteen compiled-CLI cases exercise
run -> persisted history -> public JSON, including all three requested
negative cases, OpenCode/Goose configuration, runtime controls, terminal
fallback, and overrides. Unit regressions reject 13 config/lookalike names.
All checks isolate HOME, USERPROFILE, XDG paths, TMPDIR, and Go writes.

The first native gate exposed a test synchronization error: zsh's fg text
omitted arguments, so waiting for the complete command timed out. The same
failure occurs with baseline detection via a Go overlay. The fixture now
checks kernel tty ownership using the existing helper. All input, history,
status, exit, and slot checks remain; five isolated native race runs pass.
The first Linux gate timed out on the second Ctrl-Z in TestJobControlPTY.
That unchanged test passed five isolated runs, then the full gate passed.
Its intermittent failure's cause remains unknown. Baseline/current zsh
diagnostics also emitted stty's Interrupted system call error; no product
job-control code changed.

Evidence: .yoyo/re-review-agent-{red-native,targeted,native,linux,lint}.log.
The first failures are -native-first.log and -linux-first.log; PTY diagnosis
and repeat runs share the same prefix. Full details are in -report.md.

Pending: four second-pass items, starting with hook classification gaps.
Live agent sessions and Windows runtime were not exercised. No push, tag,
publication, remote change, or real agent settings write occurred. SPEC.md
is unchanged.

Next: read .yoyo/loop-state.md, then write the hook-classification gap reds.

## Re-review fixes, second pass, iteration 6 (2026-10-28)

Shipped locally: stable hook path; only that queue item is checked.
Installed hook commands, returned hold prefixes, and repeated-command
detection use the absolute invoked path with symlinks retained, matching
shims. Architecture records the path policy.

Verified: native make check and Linux copy-in Docker make check exited 0,
including vet, race tests, and five cross-builds. Pinned golangci-lint
v2.15.0 reported 0 issues and exited 0. Compiled-CLI tests cover absolute,
PATH, relative, and relative-PATH invocation through a quoted symlink name.
They remove the old target, retarget the link, execute the saved hook and
returned hold prefix, and confirm repeated hook evaluation emits no rewrite.
The fake tool prints path-ok and exits 0. All checks isolate HOME,
USERPROFILE, XDG paths, TMPDIR, and Go writes.

Evidence: .yoyo/re-review-hook-path-red-linux.log records all four path
failures before production edits; the existing shim and installed-hook
controls passed. The macOS baseline passed and is saved as -before-native.log.
Green logs are -targeted.log, -native.log, -linux.log, and -lint.log; the
report and runner share the re-review-hook-path prefix.

Pending: five second-pass items, starting with runtime-only agent labels.
Existing installed hooks are not migrated. Live Claude loading and Windows
runtime were not exercised. No real agent settings were written. No push,
tag, publication, or remote change occurred. SPEC.md is unchanged.

Next: read .yoyo/loop-state.md, then write the runtime-marker label reds.

## Re-review fixes, second pass, iteration 5 (2026-10-28)

Shipped locally: hand-removed integration; only that queue item is checked.
Uninstall accepts missing instruction blocks, hook entries, or hook keys and
removes the remaining integration. Re-init recreates them from current bytes,
retaining blank lines, settings, modes, and the recorded hook policy without
another opt-in flag. Rebased snapshots are saved before replay. JSON settings
take precedence over marker text inside strings. Architecture records the
ownership rules and the conservative treatment of ambiguous hook edits.

Verified: native make check and the Linux copy-in Docker make check exited 0,
including vet, race tests, and five cross-builds. Pinned golangci-lint v2.15.0
reported 0 issues and exited 0. Ten compiled-CLI deletion cases cover preview,
uninstall, re-init, repeated re-init, user settings/hooks, blank lines, modes,
and removal of the remaining skill and manifest. Edited/duplicate content
still fails safely. Unit checks cover partial markers and numeric precision.
HOME, USERPROFILE, every XDG path, TMPDIR, and Go writes were private.

Evidence: .yoyo/re-review-removed-init-red-native.log records all ten original
failures before production edits. The corrected marker-string refutation in
-refute-marker.log caught a flaw in the first implementation; valid JSON now
selects hook handling before marker handling. The earlier marker fixtures
left encoded delimiters and passed; they are retained as -marker-first.log
and -marker-second.log. Final gates are -native.log, -linux.log, and -lint.log;
the report and runner share the re-review-removed-init prefix.

Pending: six second-pass items, starting with stable hook path. Ambiguous
remaining hook arrays still require manual resolution. Live Claude loading,
replay crash fault injection, and Windows runtime were not exercised. No
push, tag, publication, remote change, or real agent settings write occurred.
SPEC.md is unchanged.

Next: read .yoyo/loop-state.md, then write the stable hook path Linux red.

## Re-review fixes, second pass, iteration 4 (2026-10-28)

Shipped locally: job control under any parent; only that queue item is checked.
Terminal stops suspend the caller's whole shell job, including a waiting
non-interactive parent. Outside child STOP/CONT leaves baton runnable.
A 20 ms tty ownership check handles fg of an already-running background job
when the shell sends no CONT. Each suspension has a fresh CONT subscription;
stop/continue metadata still passes through the single monitor. Architecture
notes record the process-group and tty decisions.

Verified: native make check and the Linux copy-in make check exited 0,
including vet, race tests, and five cross-builds. Pinned golangci-lint
v2.15.0 reported 0 issues and exited 0. Saved native/Linux red runs precede
production edits: every bare control passes and all three queued scenarios
fail. Three targeted race runs passed on both platforms before the final
consumed-transition safeguard; both full gates cover the final source.
Real Bash/zsh PTYs on macOS and Bash PTYs on Linux verify one Ctrl-Z below
sh -c, outside STOP/CONT, Ctrl-Z -> bg -> fg -> Ctrl-Z, kernel child states,
tty ownership, public stopped status, exit 23, history, and slot release.
Checks isolate HOME, USERPROFILE, XDG paths, TMPDIR, and Go writes; cleanup
signals only owned process groups with birth checks.

The first native gate failed in unchanged TestShimFlow with an RSS sampling
warning (signal: killed). That test passed five isolated race runs, then the
full native gate passed. The sampler's one-second ps deadline is a possible
contributor; the captured error does not prove the cause. No sampler,
assertion, timeout, or gate was changed. The terminal-loss fixture now uses
TSTP because outside STOP deliberately does not trigger a tty handoff; its
ENOTTY, no-stranding, and exit assertions are unchanged.

Evidence: .yoyo/re-review-parent-control-{red-native,red-linux,targeted,
targeted-linux,native,linux,lint,shim-native}.log and the report/diagnosis.
The first native failure is retained in -native-first.log. Earlier fixture
and first-fix diagnostic logs are also retained.

Pending: seven second-pass items, starting with hand-removed integration.
Other target runtimes and public installation commands were not exercised
for this item. No push, tag, release, remote change, or real agent settings
write occurred. SPEC.md is unchanged.

Next: read .yoyo/loop-state.md, then write the hand-removed integration reds.

## Re-review fixes, second pass, iteration 3 (2026-10-28)

Shipped locally: nested runs across lanes; only that queue item is checked.
Descendants reuse any live ancestor reservation instead of acquiring another
lane. Each nested heavy job retains its declared or learned memory gate,
hysteresis, cancellation, and max_wait warning. Architecture and shim docs
record the policy; history continues to mark descendants nested.

Verified: native make check and the Linux copy-in Docker make check exited 0,
including vet, race tests, and five cross-builds. Pinned golangci-lint v2.15.0
reported 0 issues and exited 0. The saved red run predates production edits:
mirrored x -> y and y -> x jobs timed out after 10 seconds; nested memory
checks sampled pressure zero times. Three native targeted race runs now
finish the mirrored CLI jobs in 1.37-1.77 seconds, record two nested histories,
and release both lanes. An installed encode shim also finishes inside
install while the unrelated encode slot remains occupied.

Memory regressions cover warning recovery/hysteresis, insufficient budget
headroom, timeout warnings, cancellation, and disabled gating without a
ticket or lease. A direct native CLI run with x -> y and --quiet waits
51.9 ms for an impossible declared memory budget, emits one max_wait warning,
then exits 0. Public history preserves that budget, wait, and nested=true;
public status shows no running/waiting jobs and all slots released. Checks
isolate HOME, USERPROFILE, every XDG path, TMPDIR, and Go writes. Tests signal
only their owned runners, which forward cancellation to their descendants.

Evidence: .yoyo/re-review-cross-lanes-{red-native,targeted,native,linux,lint,
cli,hook-linux}.log and .yoyo/re-review-cross-lanes-report.md. The first full
Linux run timed out in the existing installed-hook test with empty stderr;
its log is linux-first. That unchanged test passed 10 isolated race runs,
then the unchanged full Linux gate passed. The first timeout's cause remains
unknown; no timeout or gate was weakened.

Pending: eight second-pass items, starting with job control under any parent.
Windows was cross-built but its runtime was not exercised. Parallel nested
children share ancestor capacity; memory checks do not enforce a RAM limit.
SPEC.md, real agent settings, remotes, tags, and releases are unchanged.

Next: read .yoyo/loop-state.md, then write the non-interactive-parent PTY
regression before editing job control.

## Re-review fixes, second pass, iteration 2 (2026-10-28)

Shipped locally: background tty jobs; that queue item is checked off.
Background callers retain their tty descriptor for stop/resume
without taking the terminal at startup. The waiter publishes stopped/running
transitions through the existing monitor, with acknowledgement before
suspension. Status retains stopped jobs and displays their state. Architecture
docs record terminal ownership, metadata synchronization, and slot retention.

Verified: native make check and the Linux copy-in Docker make check exited 0,
including vet, race tests, and all five cross-builds. Pinned golangci-lint
v2.15.0 reported 0 issues and exited 0. Before production edits, PTY tests
failed on macOS and Linux: bare sh stopped, while baton remained running.
The stopped-status unit test also failed. The final fixture rejects the
committed baseline through a Go overlay. Three native targeted race runs
passed, including prior foreground job control, signals, and terminal loss.
Real PTYs exercise Bash on both OSes and zsh on macOS: the shell reports the
stop, public JSON/text status shows stopped with jc used=1, fg delivers hello,
the child prints GOT=hello and exits 0, and the next job acquires the slot.
All checks isolate HOME, USERPROFILE, XDG paths, TMPDIR, and Go writes. Cleanup
signals only owned process groups after checking their kernel birth tokens.

Evidence: .yoyo/re-review-background-{red-native,red-linux,red-refined,
targeted,native,linux,lint}.log and .yoyo/re-review-background-report.md.
The first lint launch lacked its configured TMPDIR and failed before analysis;
mounting a private tmpfs there allowed the unchanged lint checks to pass.

Pending: nine second-pass items, starting with nested runs across lanes.
Non-interactive parents, external child STOP/CONT, and bg/fg cycles remain
the later job-control item. Windows runtime was not exercised. SPEC.md,
remotes, tags, releases, and real agent settings are unchanged.

Next: read .yoyo/loop-state.md, then write the cross-lane nested-run red test.

## Re-review fixes, second pass, iteration 1 (2026-10-28)

Shipped locally: hook shell semantics only; the first second-pass item is
checked off. The hook prefixes the untouched original with a shell-lifetime
hold. Claude's Bash/zsh keeps its functions, cwd, and redirections, and the
original remains top-level syntax. A detached keeper admits through the
existing queue/memory gate, watches the shell's birth token, and releases
capacity on exit. Same-lane shims discover the hold through ancestry.
Architecture and agent docs record the design and reservation lifetime.

Verified: final native make check and the Linux copy-in Docker make check
exited 0, including vet, race tests, and all five cross-builds. Pinned
golangci-lint v2.15.0 reported 0 issues and exited 0. Paired Bash/zsh cases
compare output and exits 0/3 against the unmodified original with SHELL set
to tcsh and unset, including functions, loops, &>, and caller cwd after cd.
Lifetime and queue tests cover normal exit, shell SIGKILL, occupied lanes,
and cancellation while queued. The built native CLI's status --json showed
one shell hold, then zero used slots and running jobs after shell SIGKILL.
All checks isolated HOME, USERPROFILE, inherited/standard XDG paths, TMPDIR,
and Go writes. Only owned processes were signaled.

Evidence: .yoyo/re-review-shell-{red,red-refined,targeted,native,linux,lint,
cli}.log and .yoyo/re-review-shell-report.md. The initial red run preceded
production changes; the refined run uses the committed hook via a Go
source overlay and includes the final fixture's wrapper-shape assertions.

Pending: ten second-pass items, starting with background tty jobs. A live
Claude permission-policy session and Windows hold runtime were not tested;
Windows explicitly rejects hold, while cross-builds pass. Holder history
records reservation lifetime with exit 0, not workload RSS or exit status.
SPEC.md, remotes, tags, releases, and real agent settings are unchanged.

Next: read .yoyo/loop-state.md, then write the background-tty PTY red test.

## Completion-check recovery, iteration 9 (2026-10-28)

Shipped locally: repaired the PTY test's temporary-module cleanup. The test
now uses a cold private module cache and builds its CLI with `-modcacherw`.
Production code, SPEC.md, and TASKS.md are unchanged. Every queue item was
already checked; no additional item was checked off.

Verified: the strengthened test failed before the build flag with a
permission-denied TempDir cleanup error; the downloaded module directory
had mode 0555. After the flag, Bash and zsh job-control flows and cleanup
passed. Fresh native and golang:1.27 Docker make check exited 0, including
vet, race tests, and five cross-builds. Native verification cleared inherited
GOFLAGS, GOPATH, and GOMODCACHE to exercise default cache resolution under
temporary HOME. Pinned golangci-lint v2.15.0 reported 0 issues and exited 0.
All checks isolated HOME, USERPROFILE, XDG paths, and Go writes.

Evidence: .yoyo/gate-recovery-{red-native,targeted,native,linux,lint}.log,
.yoyo/gate-recovery-diagnosis.log, and .yoyo/gate-recovery-run.py.

Pending: the three public installs still require owner publication, as
recorded in docs/verification.md. README commands and stress were not rerun
for this test-only change. No push, tag, release, or remote change occurred.

Next: let the caller rerun its completion check.

## Final gate, iteration 8 (2026-10-28)

Shipped locally: Final gate only; every TASKS.md item is checked off.
Verification records changed; production code, SPEC.md, and README.md did
not. No push, tag, release, remote change, or real agent-file write occurred.

Verified: fresh native and golang:1.27 Docker make check exited 0, including
vet, race tests, and five cross-builds. Pinned golangci-lint v2.15.0 reported
0 issues and exited 0. All nine README shell blocks ran under private
HOME/XDG/Go paths and an isolated Homebrew prefix: 17/20 invocations exited
0, all 21 behavior assertions passed, and no invocation timed out. Demo jobs
did not overlap; integration uninstall restored original bytes; an owned
headless browser stopped and its profile was removed. The audit confirmed
current README coverage and binary provenance. Existing passing stress JSON
matches the committed benchmark reports; stress was not rerun.

Pending: owner publication and successful public Homebrew, installer, and
Go @latest checks. Their actual exits were 1, 56 (HTTP 404), and 1; the fresh
release query returned zero releases. The current TASKS.md exception makes
these pending publication, rather than a Final gate blocker. Hosted CI,
Windows runtime, VHS rendering, and third-party recipes remain unverified.

Evidence: .yoyo/final-gate-{native,linux,lint,readme,environment,audit}.log,
.yoyo/final-gate-readme/manifest.json, and docs/verification.md. No new bug
fix was needed, so this verification-only iteration has no new red test.
The earlier seven fixes retain their captured red runs under .yoyo/.

Next: after owner publication, rerun the three public installation commands.

## Review fixes, iteration 7 (2026-10-28)

Shipped locally: Agent label only; the seventh review item is checked off.
Gemini detection requires the exact, nonempty GEMINI_CLI key. API credentials
and Gemini configuration no longer label terminal jobs as Gemini. Agent
precedence, explicit overrides, and the other agents' prefix rules remain.
docs/architecture.md records the marker policy and checked Google source.

Verified: native make check and golang:1.27 Docker make check exited 0,
including vet, race tests, and all five cross-builds. Pinned
golangci/golangci-lint:v2.15.0 reported 0 issues and exited 0. Before the
production edit, unit and CLI-history regressions failed on API-key and
configuration false positives; .yoyo/review-agent-red-native.log records
exit 1. The same targeted race tests now pass. The built native CLI records
GEMINI_API_KEY=x baton run -- true as agent=terminal, ignores GEMINI_CLI_HOME,
and recognizes GEMINI_CLI=1 as agent=gemini. All runs and public history
queries exited 0. All checks isolated HOME, USERPROFILE, every XDG key, and
Go paths. Evidence: .yoyo/review-agent-{native,linux,lint,targeted,cli}.log;
report: .yoyo/review-agent-report.md.

Pending: Final gate, including fresh README command verification. A live
Gemini session and native Windows runtime were not exercised. No push,
publication, remote change, or real agent configuration write occurred.
SPEC.md is unchanged.

Next: read .yoyo/loop-state.md, then complete only the Final gate item.

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
