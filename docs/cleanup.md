# Inspect before cleanup

```sh
baton reap
baton gc
```

Both commands list candidates by default. An empty list means no candidates
were selected under the current rules; it does not prove every process or
file is useful. Re-run immediately before acting and review the selection.

## Orphaned processes

```sh
baton reap --older-than 2h
```

Reap considers same-user heavy processes old enough to match the age rule
and tied by recorded birth identity to a dead runner. Headless browsers and
automation drivers also qualify after verified host-init adoption, outside
containers. Encoders and renderers always need the matching job record.
Chrome-family candidates need `--headless`; `chrome-headless-shell` needs
no flag. Headed browsers and recognized interactive-browser profiles are
excluded. Configured regexes further restrict candidates; they cannot make
arbitrary processes eligible. See [host detection](architecture.md#reaping-orphan-processes)
for the conservative fallback when Linux container detection is unavailable.

After reviewing, `baton reap --older-than 2h --yes` sends TERM, waits the
configured five-second grace period, then revalidates surviving identities
before KILL. PID reuse or changed eligibility prevents signalling that
identity. The list omits full argv because it can contain credentials.
Reap is implemented on macOS/Linux; Windows discovery fails explicitly.

## Regenerable intermediates

GC is disabled until you configure absolute roots. Add a narrow root that
contains only work whose intermediate patterns you understand:

```toml
[gc]
roots = ["/absolute/path/to/video-project"]
patterns = ["**/out/slice-*.mp4", "**/.remotion/**"]
older_than = "168h"
```

`baton gc` prints selected regular files and sizes. Only after review,
`baton gc --yes` deletes revalidated candidates. Directories and symlinks
are never deleted or followed; traversal stays under opened roots. Roots
with symlink components fail, so resolve aliases such as macOS `/tmp` to
their physical paths first. Concurrent edits cause failure rather than
deletion of changed files. A failure can occur after earlier candidates
were deleted; the diagnostic reports progress. Windows configured GC is
unsupported and fails explicitly.

## Removing baton data

First uninstall integration and shims while their manifests and binary
still exist. Stop the shared browser and let all jobs and waiters exit.
Remove the shim PATH entry from your shell config separately.

Use [the XDG path reference](configuration.md#paths-on-every-os) to identify
your config, state, cache, and data directories. Inspect their exact paths
before removing them yourself. State contains shared lock inodes; deleting
them while jobs run can create independent locks and violate capacity.
The integration manifest contains originals needed for restoration. Preserve
it until uninstall succeeds. Crash-created browser profiles may remain in
state; normal browser stop removes only its own active temporary profile.
