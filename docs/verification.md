Not verified: the retired macos-13 runner, Windows beyond its core test
suite, live agent loading, and the Linux and Intel macOS Homebrew
installs.

## v0.1.0 release (2026-10-29)

Tag v0.1.0 on 4aa9396 was gated by CI run 40823379895: every runnable
job passed (Linux amd64/arm64 and Windows on Go 1.22 and 1.27, macOS 26
on Go 1.27, lint); macos-13 was cancelled. Before it, run 40823186048
went red on Windows in TestObserveBusySession: browser observation
treated a busy session.json as fatal. Release workflow 40823955753
passed its four checks and built the draft, which was then published.

Install checks on macOS arm64, each in private HOME/GOPATH paths:

- `shasum -a 256 -c checksums.txt` passed for all five archives; the
  darwin_arm64 binary carries LC_UUID and runs help, run, and status.
- `install.sh` from raw.githubusercontent.com installed `latest` with
  "SHA-256 verified", and the binary ran.
- `go install github.com/sushilk1991/baton/cmd/baton@v0.1.0` built
  module v0.1.0, which ran.
- Homebrew 7: `brew audit --formula --strict --online` first failed on a
  redundant `version` line, fixed in the template and the tap. Then
  style, audit, fetch, `brew install`, and `brew test` passed. With an
  empty trust store, the full-name reinstall succeeded and recorded its
  own trust entry.

## Hosted CI (2026-10-29)

Run 40822218812 on ba6bb65 passed all nine runnable jobs: ubuntu-latest
and ubuntu-24.04-arm with Go 1.22 and 1.27 (make check), macos-latest
(macOS 26) with Go 1.27 (make check), windows-latest with Go 1.22 and
1.27 (vet, `go test ./...`, build), and golangci-lint v2.15.0. The two
macos-13 jobs stayed queued because GitHub retired that runner; they
were cancelled.

The first hosted run, on 1c93656, failed five jobs. Fixes, each checked
on the runner that failed:

- Windows: Aider reinstall matched the raw instruction path, not the
  JSON-quoted entry it writes. TestAiderEscapedHome reproduces it on Unix
  with a home named `a&b\c`.
- Windows: `/absolute` passed as a relative gc pattern.
- Windows: concurrent jobs failed to read or replace job records with
  sharing violations (TestTwentyJobs). Record I/O now retries those
  errors for up to 2 s; TestReplaceRecordShared and TestReadRecordShared
  reproduce both collisions.
- Linux and macOS, Go 1.27: cmd/baton passed go test's 10m default
  because 30 tests each built baton with a cold Go cache. One shared
  build cut the package locally, with a cold inner cache, from 143 s to
  38 s.
- macOS 26 dyld aborts Go 1.22 binaries ("missing LC_UUID load
  command"), so macos-latest checks Go 1.27 only.

## Third-review fixes, final gates (2026-10-29)

Source: 68d5570. Linux passed on an instabox VM (Debian, Go 1.27.1,
linux/amd64, 4 vCPU) holding a byte-identical copy of the source (231
files, sha256 manifest compared), with private HOME, XDG, TMPDIR, and Go
paths: make check (vet, race tests, cross-builds) exited 0; a following
`go test -race -count=5 ./...` passed every package; pinned golangci-lint
v2.15.0 reported 0 issues.

Native macOS make check passed on c35adda (all 18 packages with -race,
vet, five cross-builds) at load averages of 7-32; 68d5570 changes only a
test's timing measurement, and internal/runner passed again natively.

Each third-review fix has a test that failed before it and passes after:
TestNestedHoldOtherLane, TestBackgroundTTYParentRead,
TestJobControlSelfStop, TestHookWithoutHold, and
TestBackgroundStartFailure (the last from a Codex review of the fixes).
TestCatchesSignal checks the caught-signal mask on both platforms; Codex
confirmed the darwin p_sigcatch offset (236 of 648 bytes) against the SDK
for arm64 and amd64. Repeated Linux runs exposed two test races, fixed
without changing assertions: the self-stop helper now waits for CONT, and
the memory-gate test starts timing before arming its deadline.

Windows code compiles (vet, test binaries, cross-build) but has not run.

## Completion-check recovery, final gates (2026-10-29)

Linux passed on an instabox VM (Debian, Go 1.27.1, linux/amd64, 4 vCPU)
holding a byte-identical copy of the final source (228 files, sha256
manifest compared). HOME, XDG, TMPDIR, and Go paths were private. make
check (vet, race tests, cross-builds) exited 0 in 2m20s; a following
`go test -race -count=5 ./...` passed every package. Pinned golangci-lint
v2.15.0 reported 0 issues.

Two native macOS make check runs on the final source failed only in
cmd/baton, each on a deadline. TestNestedInstalledRecipes missed its
4-second limit (the npm ci recipe, then the whisper recipe).
TestShimFlow received the once-per-job warning `RSS sampling unavailable:
signal: killed` after /bin/ps exceeded its 1-second budget; the command
still exited 0. Every other package passed. Host load averaged 450-760
with syspolicyd near 130% CPU; the earlier nested diagnosis measured
130-560 ms per ps sample. The serial native pass (`native-serial.log`)
predates only the TestRunWaitNotices fixture change; production code is
identical. Logs: `native-2302-failed.log`, `native-2359-nested-deadline.log`,
`cli-package.log`.

## Completion-check recovery, iterations 12-13

Recovery remains uncommitted because the required full gates have not
passed. Evidence below uses the `.yoyo/re-review-completion-` prefix.

The caller's completion check failed in TestHookUpgradePath/PATH during
TempDir removal: `directory not empty`. New assertions fail for all four
invoked-path spellings before synchronization: a keeper job record remains
and hold history does not exist. The fixtures now await record removal,
which follows history writes and slot release. The exact original deletion
interleaving was not traced; the exposed premature return permits that race.
`red-native.log` retains the failures; `targeted.log` passes. Hook upgrade
and installed-hook fixtures pass twenty repeats (`refutation-native.log`).

A delayed self-stop reproduced the tty-loss fixture's early-continuation
race. Observing the child stopped before invalidating its tty makes the
same injected case pass, preserving ENOTTY and exit 23. Evidence is
`tty-red.log`, `tty-green.log`, and twenty normal repeats in
`tty-targeted.log`. Existing assertions and deadlines remain unchanged.

A later full gate exposed browser recovery startup failure. An isolated
CLI reproduction captured `browser.lock: file lock busy`
(`browser-diagnosis.log`). Observation briefly owns that lock while checking
stale metadata; a new supervisor previously attempted it only once. The
new controlled probe test fails before the fix (`observer-red.log`) and
passes after it (`observer-green.log`). Startup now retries within the
existing 15-second budget, retaining exclusive lifetime ownership. The
compiled CLI's SIGKILL recovery passes five repeats, confirming two browser
launches and one replacement (`browser-targeted.log`).

Full native verification still fails (`native-fourth.log`): nested recipes
miss deadlines, one parent-PTY case times out awaiting AFTER, and the CLI
suite reaches Go's ten-minute limit while building another fixture.
The browser package passes. Unchanged runner and stress package suites pass
separately (`runner-package.log`, `stress-package.log`). The earlier terminal
diagnostic passes both bare and queued bash/zsh cases (`identity-diagnosis.log`),
so it does not explain the earlier identity failures. Earlier failed gates
remain in `native-first.log` through `native-third.log`; an interrupted gate
has its own log. Host load above 600 and RSS sampling warnings are observed
contributors to investigate, not established causes. No sampler, assertion,
timeout, Makefile, or gate was changed to accommodate them.

The earlier Linux copy-in gate failed unchanged pressure-process, browser
proxy, and lane-concurrency deadlines (`linux-first.log`). A new Linux/lint
pipeline remains in flight. A separate Docker health client timed out after
ten seconds (`docker-health.log`). Pinned golangci-lint v2.15.0 previously
reported 0 issues before the browser change; the final source still needs a
new successful lint run. Its first dependency TLS failure and successful
private-cache verification are retained separately.

README replay is prepared in `re-review-completion-readme.py` but has not
run. Earlier README and stress evidence keeps its original scope below.
Hosted CI, Windows runtime, and live agent loading were not checked.
Public installs remain pending owner publication under TASKS.md's exception.
SPEC.md, README.md, and TASKS.md are unchanged.

## Second-pass final gate, iteration 11

Verified on 2026-10-28 with Go 1.27.0, macOS 27.0.1, darwin/arm64.
Final evidence uses the `.yoyo/re-review-final-` prefix. Each runner log
records its command, private environment paths, raw output, and exit code.

| Command | Result | Log suffix |
| --- | --- | --- |
| macOS `make check` | Exit 0 | `native.log` |
| Linux copy-in `make check` from AGENTS.md | Exit 0 | `linux.log` |
| `golangci/golangci-lint:v2.15.0 golangci-lint run ./...` | `0 issues.`, exit 0 | `lint.log` |

Both full gates passed vet, race tests, and five cgo-free cross-builds.
They skipped optional local lint; the separate pinned linter passed.
Linux copied the read-only mount from /mnt into /src before make check.
All checks isolated HOME, USERPROFILE, inherited/standard XDG variables,
TMPDIR, and Go writes. The linter used a private tmpfs at its TMPDIR.

The first Linux gate timed out on the second Ctrl-Z in TestJobControlPTY.
A new paired PTY regression then reproduced an unwanted continuation:
SIGCONT sent to the supervisor resumed its independently stopped foreground
child. Bare bash/zsh controls passed; queued controls failed before the fix
(`red-native.log`) and passed afterward (`targeted.log`). The runner now
sends no continuation when the child already owns the tty. Background,
foreground, and parent PTY suites each passed ten repeats on both platforms
(`refutation-native.log`, `linux-pty.log`). macOS covered bash/zsh; Linux
covered bash. The original failed gate remains in `linux-first.log`.

README replay accounts for all nine shell blocks. Seventeen executed
invocations and all twenty-one behavior assertions passed, with no timeout.
The three public installs were recorded as pending owner publication and
not retried, as the queue permits. Actual summary:

```text
README gate: 9 shell blocks; 17 executed invocations; 0 failures; 3 pending owner publication
```

The replay confirmed serialized demo jobs, source installation, instruction
restoration byte for byte, shim removal, and owned headless-browser shutdown.
Final public JSON status had empty running/queue arrays and zero used slots.
The manifest and per-command logs are in `.yoyo/re-review-final-readme/`.

The initial readiness audit failed before fresh gate evidence existed
(`red-audit.log`). The final audit passed (`audit.log`): tested source,
immutable SPEC, binary/README hashes, every shell block, all assertions,
ten PTY repeats per suite/platform, and exactly three publication exceptions.
The binary was built before commit, with CGO_ENABLED=0 and vcs.modified=true;
`environment.log` records the measured source hash and image identities.

Inferred: delayed supervisor SIGCONT delivery can explain the first Linux
timeout; its exact signal ordering was not captured. The direct continuation
bug is reproduced and fixed. Hosted CI, Windows runtime, live agent loading,
and stress were not rerun. Earlier benchmark and publication evidence below
belongs to its named iterations.

Next: after owner publication, verify the three public installation paths.

## Earlier verification

Checked on 2026-10-28: Go 1.27.0, macOS 27.0.1 (26A434), darwin/arm64.
Iteration 8 tested source revision `26a6333ac3e4afd59654002ef4b05e0fad056263`.
That iteration changed verification records only; SPEC.md, README.md, and
production code were unchanged.

## Completion-check recovery, iteration 9

The caller's subsequent `make check` failed while removing the PTY test's
temporary module cache. Downloaded module directories had mode 0555. The
earlier gate's warm cache had hidden this cleanup failure.

The PTY test now forces a cold private module cache. Before the fix, its
captured run exited 1 after both Bash and zsh job-control flows passed,
with `TempDir RemoveAll cleanup` reporting `permission denied`. Building
the fixture CLI with `-modcacherw` keeps downloads removable. The same
test now exits 0, including cleanup. Production code is unchanged.

Fresh native and golang:1.27 Docker `make check` both exited 0: vet, race
tests, and five cross-builds passed. Pinned golangci-lint v2.15.0 reported
`0 issues.` and exited 0. All checks used private HOME/XDG/Go paths; the
native gate cleared inherited GOFLAGS, GOPATH, and GOMODCACHE to exercise
default module-cache resolution.

Evidence under `.yoyo/`: `gate-recovery-{red-native,targeted,native,linux,lint}.log`,
`gate-recovery-diagnosis.log`, and `gate-recovery-run.py`. README and stress
results below remain iteration 8 evidence; neither was rerun for this
test-only change. Public installation remains pending owner publication.

## Iteration 8 full gates

| Command | Result | Captured log under `.yoyo/` |
| --- | --- | --- |
| `make check` | Exit 0 | `final-gate-native.log` |
| `docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` | Exit 0 | `final-gate-linux.log` |
| `docker run --rm -v "$PWD":/src -w /src golangci/golangci-lint:v2.15.0 golangci-lint run ./...` | `0 issues.`, exit 0 | `final-gate-lint.log` |

Both full gates passed vet, race tests, and cgo-free builds for darwin/linux
amd64+arm64 and windows/amd64. They skipped their optional local lint step;
the separate pinned lint command passed. Container commands added private
HOME, USERPROFILE, Go, and XDG environment paths. Active container state
used native `/tmp`.

The runner is `.yoyo/final-gate-run.py`. Image identities, tool versions,
source hashes, and the release query are in `final-gate-environment.log`.
Image digests:

- Go: `sha256:9048d5e7751706200eb1719c7dae0436950d362f29a1da91fa05e5c63867c60f`.
- Lint: `sha256:fbfcfd12a6f8d7f1b97b3362ca80fc2a244d46863148c73cb88e7793af96c481`.

## README replay

All nine shell blocks ran again. Eight passed; the release-install block
has the three publication prerequisites listed below. The replay used a
freshly built native binary, private HOME/XDG/Go directories, fake agent
directories, and a copied Homebrew runtime with a verified temporary prefix.
Shells enabled `pipefail`, so an installer download failure remained visible.

| README lines | Verified behavior |
| --- | --- |
| 22, 31 | Build, PATH export, help, source Go install, and installed binary help |
| 5, 56 | Render wrapper and both demo jobs exit 0; history confirms no overlap |
| 73 | Completed demo history and status line |
| 80, 196 | Integration preview, install fixture, and uninstall; original instructions restored byte for byte |
| 206 | Installed shim removed; owned headless Chrome stopped; browser profile removed |

Actual replay summary:

```text
README gate: 9 shell blocks; 20 invocations; 3 failed; 3 pending owner publication; 0 unexpected failures
```

Seventeen invocations exited zero; all 21 behavior assertions passed. The
invocations include fixture preparation and independent observations. No
command timed out. `.yoyo/final-gate-readme.py` is the runner;
`final-gate-readme.log` and `final-gate-readme/manifest.json` retain commands,
exit codes, per-command logs, source revision, and binary/README hashes.

A separate audit compared the current README blocks and hashes against
the replay, checked every assertion and exit code, and confirmed that only
the public installs failed. `final-gate-audit.log` also records the native
binary's `vcs.revision`, `vcs.modified=false`, and `CGO_ENABLED=0`.

## Pending owner publication

Superseded by the v0.1.0 release above. Before publication these were
actual failures, excluded from the Final gate only by its explicit
TASKS.md exception.

| Command | Exit | Captured output excerpt |
| --- | --- | --- |
| `brew install sushilk1991/tap/baton` | 1 | `Warning: No available formula or cask with the name "sushilk1991/tap/baton".` |
| `curl -fsSL https://raw.githubusercontent.com/sushilk1991/baton/main/install.sh \| sh` | 56 | `curl: (56) The requested URL returned error: 404` |
| `go install github.com/sushilk1991/baton/cmd/baton@latest` | 1 | `no matching versions for query "latest"` |

The fresh read-only query returned:

```text
$ /opt/homebrew/bin/gh-axi release list
count: 0
releases: []
```

Verified: both full gates, pinned lint, and local README flows passed.
Earlier stress JSON still matches the committed passing reports in
[benchmarks.md](benchmarks.md). Stress was not rerun in this iteration.

Inferred: publishing the source, release assets, and tap formula should make
the public installation paths available. They require fresh end-to-end
checks after publication. Hosted CI, Windows runtime, VHS rendering, and
linked third-party renderer/model recipes were not exercised here.

Next: after owner publication, rerun the three public installation commands.
