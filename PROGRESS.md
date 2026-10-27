# Progress

Shipped locally: fifteen of seventeen TASKS.md items, through Distribution.
Distribution implementation: f5cf46d. Only Distribution was completed this
iteration. SPEC.md is unchanged. No push, tag, release, or repository remote
change was made. The overall goal remains incomplete.

Verified: native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and all five cgo-free cross-builds.
golangci-lint was unavailable and skipped. Native Go was 1.27.0 darwin/arm64.

GoReleaser v2.19.0 validated the config and built a local snapshot with
five archives and SHA-256 checksums. Every archive's digest and exact
contents passed inspection. The native archive installed through the real
installer under temporary HOME/XDG paths with a fixture download boundary;
its binary ran help and a command. The packaged Linux/arm64 binary ran
help and a command inside golang:1.27 Docker. Both exposed CGO_ENABLED=0
in their build information. Linux/arm64 was statically linked.

Installer tests first failed because the scripts were absent, then passed.
They pipe the installer into sh and cover all four Unix platform mappings,
version/directory overrides, bad/missing/duplicate digests, failed downloads,
invalid/empty archives, unsupported input, and symlink protection. Failed
upgrades preserved the existing binary. Tests use private HOME, USERPROFILE,
and every inherited/standard XDG path; no unowned process was signalled.

The formula renderer rejects incomplete or ambiguous manifests before
emitting output. Homebrew's actual class loader validated all four target
URLs and digests from the rendered snapshot formula. Ruby and shell syntax,
YAML parsing, GoReleaser check, actionlint v1.7.12, and git diff checks passed.
no-ai-slop final pass applied to the changed prose. Architecture records the
fixed asset names, installer publication, formula handling, and draft policy.

The first Homebrew validation used an obsolete factory method. Its corrected
instance loader required a tap; disabling API installation then triggered a
core-tap clone. Only that owned validator and its descendants received TERM.
No core tap remained. Class-only loading with private HOME/cache then passed;
it neither instantiated the formula nor installed it.

Pending: Stress next, then the Final gate. Published release installation,
tap installation/tests, hosted workflow execution, and Windows runtime remain
unverified. The release workflow only prepares drafts and attaches a formula;
it does not publish or modify the tap. Existing CI retains the spec-required,
retired macos-13 runner; the draft workflow gates on current runners.

Prior open risk: browser status can race shutdown between session removal
and lifetime-lock release. It did not fail this iteration's native or Linux
gate. No runtime Go code or existing assertion changed here. Keep this
observation for the Final gate; one green run does not establish stability.
Rendered VHS demo and full external renderer/model/client flows also remain
unverified. Benchmark results are still pending.

Evidence: ignored .yoyo/distribution-{red,tests,native-check,linux-check,
snapshot,smoke,linux-smoke,homebrew,actionlint}.log. Snapshot artifacts are in
ignored dist/. Smoke and formula-check helpers remain under .yoyo/.

Next: read SPEC.md and the Stress item in TASKS.md. Implement only
scripts/stress.sh and record measured results in docs/benchmarks.md. Preserve
fake-HOME and owned-process boundaries; do not push or publish.
