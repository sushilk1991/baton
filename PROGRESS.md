# Progress

Shipped locally: fourteen of seventeen TASKS.md items, through Docs.
Docs implementation: b1cc100. SPEC.md is unchanged. No runtime Go code or
existing test assertion changed. No push, tag, release, or remote change.
The overall goal remains incomplete.

Verified: native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and five cgo-free cross-builds. golangci-lint
was unavailable and skipped. Native Go was 1.27.0 on darwin/arm64.

README now has source installation, a bounded quick start, lane behavior,
recipes, a primary-source comparison, FAQ, and reversible uninstall steps.
Release install commands are explicitly preparatory. Reference docs cover
configuration, shims, cache, observation, cleanup, and workload recipes.
Community files and GitHub issue/PR templates are present. Architecture
records the documentation decisions. Benchmarks remain explicitly pending.

The generated defaults come from the compiled CLI through
scripts/config-reference.sh, with fresh HOME/XDG paths and an empty inherited
environment. The temporary cache path becomes a comment. Native regeneration
matched the committed TOML. A refutation with invalid inherited config
succeeded without modifying its bytes. The script also generated valid TOML
inside Linux Docker. Shell syntax and both issue-form YAML files passed.
All 41 local Markdown links and fragments resolved. no-ai-slop final pass
applied to the new prose.

The real CLI smoke used temporary HOME, USERPROFILE, every inherited XDG_*
and standard XDG paths, plus isolated Go caches. It exercised make build,
go install from the checkout, help, the exact README quick start, history,
status, all config commands, fake-agent install/idempotency/byte restoration,
shim install/list/bypass/uninstall, cache hit and missing-output restoration,
GC listing/deletion of one owned file, observational reap, browser stop,
and an owned watcher's TERM exit 0. No unowned process was signalled.

Host pressure changed to warn during verification. The first tiny sleep
waited at the default memory gate and hit the smoke harness's 90-second
limit. README sleep examples now use --max-wait 2s; the exact quick start
passed under real warn pressure, with two successful nonoverlapping jobs.
Real heavy-work recipes retain the default memory wait.

The VHS parser initially rejected escaped quotes. Backtick string literals
fixed the tape; the upstream v0.12.1 lexer/parser reported
"VHS parser: valid tape". Its actual shell commands ran successfully and
showed demo 1/1 with one queued job. Rendering remains unverified because
VHS is unavailable. The recording instructions isolate HOME before shell
startup and disable VHS publication.

Open risk: the first native gate failed TestBrowserLifecycleCLI when
browser status raced shutdown and found no session.json while the lifetime
lock remained held. The unchanged test passed three isolated runs, then
the full native and Docker gates passed. This existing observation race is
documented; it was not fixed in the Docs item. Keep the failure for the
Final gate rather than assuming one green run establishes stability.

Pending: Distribution next, then machine stress and the Final gate.
Release installation, full external renderer/model/MCP-client flows,
rendered demo, hosted CI, and Windows runtime remain unverified. No benchmark
or publication result is claimed.

Evidence is under ignored .yoyo/: docs-native-check.log,
docs-linux-check.log, docs-smoke.log, docs-static-check.log,
docs-linux-reference.log, docs-tape-check.log, docs-tape-red.log,
docs-native-red.log, and docs-browser-retest.log. Only Docs was checked off.

Next: read the Distribution item in TASKS.md and the immutable SPEC.md.
Prepare distribution without pushing, tagging, publishing, or changing
remotes. Preserve fake-HOME and owned-process boundaries.
