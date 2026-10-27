# Progress

Shipped locally: eleven TASKS.md items, through Cache. Implementation:
f7456b3. SPEC.md is unchanged. No push, tag, release, or remote change.
The overall goal remains incomplete.

Verified: native `make check` and
`docker run --rm -v "$PWD":/src -w /src golang:1.27 make check` exited 0.
Both passed vet, race tests, and all five cgo-free cross-builds.
golangci-lint was unavailable and skipped. Windows compiled; its runtime
remains unverified.

Cache hashes declared input contents, names, permissions, argv, cwd/output,
and configured environment. Hits verify the payload, restore the file,
skip execution, and record cache_hit in history. Per-key kernel locks
serialize identical jobs. Global locking protects atomic publication,
restoration, and least-recently-used eviction. Stores enforce the configured
payload size cap; `cache ls|prune` expose entries and cap enforcement.

Real CLI tests cover miss, deleted-output restoration, hardlink corruption,
input/env/argv invalidation, history, pruning, failed commands, missing
output, unavailable storage, and BATON=off. Eight concurrent CLI callers
executed their renderer once. Native tests prove hardlink restoration;
a separate Linux run printed `EXDEV hardlink failure restored identical
bytes by copying`. Unit tests cover globstar, unmatched/invalid globs,
symlinks, same-size input rewrites with restored mtime, unset versus empty
environment, stable-input checks, LRU order, oversize outputs, lock
cancellation, abandoned staging, corrupt metadata, destination protection,
and absence of plaintext arguments/environment in cache metadata.

Red evidence: CLI tests failed with `flag provided but not defined:
-cache-key`. A later regression failed because corrupt metadata blocked
pruning; pruning now removes malformed generated entries. No gate was
weakened. Architecture decisions are recorded in docs/architecture.md;
the no-ai-slop prose pass was applied.

Pending: complete only the browser item next. Read SPEC.md section 3.7,
TASKS.md, docs/architecture.md, and existing config/process/lock helpers.
Implement start, stop, status, endpoint, client counting, and idle shutdown.
Do not start init/MCP, docs, distribution, stress, or final verification.

Limits: one regular output file; callers must declare all dependencies,
keep inputs stable, and use distinct output paths for concurrent different
jobs. In-place edits to a restored hardlink change the cached inode and
invalidate its next hit. The size cap counts logical payload bytes, not
metadata, persistent lock files, or external hardlinks. Hostile same-user
filesystem races and Windows runtime remain unverified. Earlier platform
limits are in docs/architecture.md. Hosted CI, stress, and every README
command remain pending.
