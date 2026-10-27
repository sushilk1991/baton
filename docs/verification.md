Not verified: the three public README installation commands fail. Final gate
remains unchecked; this task forbids the remote writes needed to make those
installation paths available.

Checked on 2026-10-27 with Go 1.27.0, macOS 27.0.1 (26A434), darwin/arm64.
SPEC.md and README.md are unchanged.

## Local checks

| Command | Result | Captured log under `.yoyo/` |
| --- | --- | --- |
| `make check` | Exit 0 after the fix | `final-native-fixed-check.log` |
| `docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` | Exit 0 after the fix | `final-linux-fixed-check.log` |
| `go test -race ./internal/browser -run '^TestObserve' -count=1 -v` | Exit 0 | `final-browser-tests.log` |
| `go test -race ./cmd/baton -run '^TestBrowserLifecycleCLI$' -count=20 -v` | 20 passes, exit 0 | `final-browser-repeat-green.log` |
| `BATON_TEST_REAL_BROWSER='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' go test -race ./cmd/baton -run '^TestBrowserRealCLI$' -count=1 -v` | Real CDP response, exit 0 | `final-browser-real.log` |

Both full gates passed vet, race tests, and cgo-free builds for darwin/linux
amd64+arm64 and windows/amd64. Both printed:

```text
golangci-lint not installed; skipping lint
```

The Linux check used image digest
`sha256:9048d5e7751706200eb1719c7dae0436950d362f29a1da91fa05e5c63867c60f`.
Initial full gates also passed, before the repeat test exposed the race.

## Browser regression

The original 20-run lifecycle check failed twice while reading a session
removed during shutdown. The deterministic regression held that state open:

```text
$ go test -race ./internal/browser -run ^TestObserveDuringShutdown$ -count=1 -v
--- FAIL: TestObserveDuringShutdown (0.00s)
    --- FAIL: TestObserveDuringShutdown/status (0.00s)
    --- FAIL: TestObserveDuringShutdown/stop (0.00s)
EXIT: 1
```

`final-browser-transition-red.log` retains the missing-file diagnostics.
Management commands now wait for the lifetime lock or session to settle,
within the existing deadline. The regression then passed. A separate check
confirmed that a missing session reaches its deadline and malformed JSON
still fails. The unchanged CLI lifecycle test then printed:

```text
PASS
ok  	github.com/sushilk1991/baton/cmd/baton	63.227s
EXIT: 0
```

The real browser test returned `"product":"Chrome/156.0.8078.48"` from
`Browser.getVersion`, then printed `browser stopped`. It used an owned
headless browser and a disposable profile.

## README commands

Every shell block was executed again after the fix. The audit used private
HOME, USERPROFILE, XDG directories, Go paths, and a copied Homebrew runtime
with a verified temporary prefix. It never installed into the user's prefix
or agent directories. Each shell used `pipefail`, so the failed installer
download could not be hidden by `sh` exiting zero.

Eight blocks passed:

| README lines | Executed flow |
| --- | --- |
| 22, 31 | Build, PATH export, help, and `go install ./cmd/baton`; installed executable also ran help |
| 5, 56 | Render wrapper and both background demo jobs; history confirmed exit 0 and no overlap |
| 73 | History and status line |
| 80, 196 | Agent preview and uninstall after installation into fake agent directories; original instructions restored byte for byte |
| 206 | Shim uninstall and browser stop after creating owned fixtures; shim and browser profile removed |

The release-install block at line 39 failed for all three commands:

| Command | Exit | Actual output excerpt |
| --- | --- | --- |
| `brew install sushilk1991/tap/baton` | 1 | `Warning: No available formula or cask with the name "sushilk1991/tap/baton".` |
| `curl -fsSL https://raw.githubusercontent.com/sushilk1991/baton/main/install.sh \| sh` | 56 | `curl: (56) The requested URL returned error: 404` |
| `go install github.com/sushilk1991/baton/cmd/baton@latest` | 1 | `go: github.com/sushilk1991/baton/cmd/baton@latest: no matching versions for query "latest"` |

The audit summary was:

```text
README gate: 9 shell blocks; 19 invocations; 3 failed
```

The invocations include fixture preparation and independent observations;
16 exited zero. All 21 behavior assertions passed. The manifest and per-command
logs are in `.yoyo/final-readme/`; the launcher is `.yoyo/final-readme.py`.
README SHA-256:
`f189f23c94cbb32a0b0ba26339c508aa864ef45d4344a52292d232021f8ae945`.

A fresh primary-source check in `final-github.log` returned:

```text
$ /opt/homebrew/bin/gh-axi release list
count: 0
releases: []
```

## Remaining work

Verified: the regression failed before the fix and passed after it. Repeated
CLI processes, real Chrome, and both full local gates also passed.

Inferred: authorized source publication, release assets, and a tap formula
would unblock the public installations. None was published or tested here.
Those commands must pass against their real public sources before Final
gate can be checked off. STATUS: DONE was not written.

Lint, hosted CI, Windows runtime, a VHS recording, and linked third-party
renderer/model recipes remain unverified. The previous stress results remain
in [benchmarks.md](benchmarks.md); stress was not rerun in this iteration.
Control transport failures during browser transitions still return errors;
the fix retries only missing session files.

Next: the caller must resolve the publication prerequisite before another
Final gate attempt. Do not replace these failed public checks with fixtures.
