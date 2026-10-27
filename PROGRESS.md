# Progress

Shipped locally: ten TASKS.md items, through gc. Implementation: 0d8d23e.
SPEC.md is unchanged. No push, tag, release, or remote change.
The overall goal remains incomplete.

Verified: final native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and all five cgo-free cross-builds.
golangci-lint was unavailable and skipped. Windows compiled; its runtime
remains unverified.

GC reads configured roots, slash globs, and minimum file age. Default roots
are empty; default age is seven days. Dry-run prints sorted paths and sizes.
`--yes` revalidates each regular file before a parent-relative unlink.
Symlinks, special files, directories, fresh files, and unmatched files stay.
Duplicate and overlapping roots produce one candidate per absolute path.

Real native and Linux CLI tests selected three files totaling 16 bytes.
Dry-run changed nothing. `--yes` deleted the three files and preserved an
outside sentinel, symlinks, and fresh/unmatched files. Tests also cover
globstar, strict age boundaries, FIFO inspection, root/parent/leaf
replacement, same-size rewrites with restored mtime, traversal rejection,
closed/short output, config failures, and partial-deletion reporting.
Initial CLI regressions failed with `unknown command "gc"`; the short-write
regression failed before output handling was fixed.

The first Linux gate exposed an existing status publication race:
`TestObserveLiveCLI` saw queue sequence 0. A deterministic regression also
failed with sequence 0, expected 7. Status now copies the live ticket's
sequence into the projection. The regression and both full gates pass.

Pending: complete only Cache and test i next. Read SPEC.md cache clauses,
TASKS.md, docs/architecture.md, config cache schema, and runner options/store.
Implement input/argv/allowlisted-env hashing, hit restoration, miss storage,
and `cache ls|prune`. Test with temporary HOME/XDG/input/output roots.
Do not start browser, init/MCP, docs, distribution, stress, or final gate.

Limits: gc rejects symlinked root components, including macOS `/tmp` and
`/var` aliases; use canonical paths. The final metadata check and unlink
are separate syscalls. Hostile directory renames and privileged mount
changes exceed its isolation guarantees. Configured gc roots on Windows
fail explicitly. Directory shells remain; byte counts are logical lengths.
Earlier runner/shim/reap limits remain in docs/architecture.md. Hosted CI,
other-architecture runtime, stress, and README verification remain pending.
