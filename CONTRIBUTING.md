# Contributing

```sh
make check
```

Use Go 1.22 or later. The full local gate runs vet, golangci-lint when
installed, race tests, and cgo-free builds for darwin/linux amd64+arm64 and
windows amd64. Race tests require a C toolchain. A skipped optional linter
is not proof that lint passed.

1. Read [SPEC.md](SPEC.md), [AGENTS.md](AGENTS.md), and [TASKS.md](TASKS.md).
   Keep changes to one queue item. SPEC is immutable; record decisions it
   leaves open in [architecture](docs/architecture.md).
2. Reproduce bugs with a failing regression before fixing them. Keep tests
   focused on behavior across concurrency, crashes, and process ownership.
3. Run both the native gate and the Linux gate:

   ```sh
   docker run --rm -v "$PWD":/src -w /src golang:1.27 make check
   ```

4. Update affected docs. Regenerate the defaults reference when policy
   changes, using the command in [configuration](docs/configuration.md).
5. Commit with an imperative, capitalized subject of at most 50 characters,
   followed by a blank line and a body explaining what and why, wrapped at
   72 characters. Describe validation and remaining limits in the PR.

## Test boundaries

Tests must set HOME, USERPROFILE, every inherited `XDG_*`, and standard XDG
directories to temporary locations before invoking baton. Never install
integration or shims into a real user configuration. Never kill a process
you did not start; reap tests spawn fake processes. Do not run machine stress
tests as part of the ordinary gate.

Unix process-group behavior and kernel-lock crash recovery need real-process
tests. Fake pressure providers test admission without exhausting RAM.
Cross-compilation does not prove Windows runtime behavior or a remote CI
result. Report those separately. Native Apple simulators are not required.

Keep public API surface small: domain code belongs under `internal/`, raw
OS operations behind drivers, and command dispatch under `cmd/baton`.
Prefer the standard library. New dependencies need a concrete justification.
Do not push, tag, publish, or change remotes during the implementation loop.

## Reports and review

Use the issue templates for bugs and proposals. Include exact commands,
OS/architecture, relevant redacted config, and observed output. Do not paste
agent settings, private paths, tokens, or integration manifests. Read
[SECURITY.md](SECURITY.md) for vulnerabilities and
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for participation rules.
