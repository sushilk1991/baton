Not verified: the three public installation paths require owner publication.
The Final gate passes under TASKS.md's publication exception. Nothing was
pushed, tagged, or published.

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

These are actual failures, excluded from the Final gate only by its explicit
TASKS.md exception. Public installation remains unverified.

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
