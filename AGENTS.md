# Working on baton

- `SPEC.md` is the goal and does not change. `TASKS.md` is the queue: one
  item per iteration, checked off only when its tests pass.
- Verify with `make check`. Linux, on a copy inside the container (git
  inside a macOS bind mount intermittently fails with EACCES on loose
  objects while tests build the CLI):
  `docker run --rm -v "$PWD":/mnt:ro golang:1.27 sh -c 'cp -a /mnt /src && cd /src && make check'`.
- Lint must report 0 issues: `docker run --rm -v "$PWD":/src -w /src golangci/golangci-lint:v2.15.0 golangci-lint run ./...`
  (`make check` skips lint when golangci-lint is not installed).
- Tests never touch the real `$HOME`, `~/.claude`, `~/.codex`, `~/.agents`
  or the user's shell config: set `HOME` and every `XDG_*` to temp dirs.
- Never kill processes you did not start in a test. `reap` tests spawn their
  own fake processes.
- Do not push, tag, publish releases, or change git remotes.
- Commit after each completed task. Subject: imperative, capitalized,
  ≤ 50 chars, no trailing period. Blank line, then a body wrapped at 72 that
  says what and why.
- Code: named constants for spec values, flat control flow with early
  returns, small functions, a short comment on every exported identifier and
  every non-obvious block (what and why), ASCII diagrams where things move.
- Keep exported surface small: `internal/` for everything but `cmd/baton`.
