# Architecture decisions

## Scaffold

`cmd/baton` starts with private command dispatch and injected output writers.
No CLI framework or exported Go API is needed. Packages under `internal/`
will own scheduler and platform code as those queue items are implemented.

```text
cmd/baton (arguments, stdio, exit status)
    |
    v
internal domain packages (later tasks)
    |
    v
internal platform drivers (later tasks)
```

`make check` runs vet, optional golangci-lint, race tests, and serial builds
for darwin/linux amd64+arm64 and windows amd64. Race tests enable cgo because
the Go race runtime requires it; release builds set `CGO_ENABLED=0`.

Tests replace `HOME`, all inherited `XDG_*` keys, and the standard XDG paths
with temporary directories before exercising commands. Tests that need
parallelism must pass an isolated environment to child processes instead
of combining `t.Setenv` with `t.Parallel`.

CI checks Go 1.22 compatibility and Go 1.27, matching the required Docker
image. Dependency caching is disabled until there are module dependencies.
Windows runs vet, core tests, and a build without cgo.

The required CI matrix retains `macos-13`. GitHub [retired that hosted
runner](https://github.blog/changelog/2025-09-19-github-actions-macos-13-runner-image-is-closing-down/)
in December 2025, so that job cannot run on a GitHub-hosted runner. A macOS
13 runner or a spec change is required before the full CI matrix can pass.
The immutable spec has not been changed.
