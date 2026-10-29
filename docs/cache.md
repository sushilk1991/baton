# Output cache

From a directory containing `source.txt`, run this twice:

```sh
baton run --cache-key 'source.txt' --output result.txt -- cp source.txt result.txt
baton run --cache-key 'source.txt' --output result.txt -- cp source.txt result.txt
baton history --json --since 1h
baton cache ls
```

The second run restores `result.txt` and skips `cp`, printing
`baton: cache hit; restored result.txt without running the command` unless
`--quiet` is set. Its history record has `cache_hit: true`. Delete the output and repeat to exercise restoration.
The cache accepts one regular output file, not directories or symlinks.

## Keys and dependencies

`--cache-key` and `--output` must appear together. Quote comma-separated
relative slash globs, such as `'src/**,audio/*.wav'`. `**` spans directory
components; ordinary `*` stays within one. Each pattern must match at least
one regular input. Traversal, absolute patterns, and input symlinks fail.

The key hashes input names, permissions and contents, argv, cwd, output
path, patterns, and allowlisted environment values. Input ordering does not
change the key. The cache directory and declared output are excluded from
inputs. Keys store hashes; cache entries also store the output's bytes.

```toml
[cache]
max_size = "10G"
env = ["NODE_ENV", "RENDER_QUALITY"]
```

Declare every input and environment dependency. Tool versions, installed
packages, remote data, and model changes are not discovered automatically.
Include lockfiles or a tool-version file in the globs when they affect the
output. A command with side effects should not use an output cache.

## Restore, failure, and eviction

baton restores with a hardlink when possible, otherwise copies. Do not edit
a hardlinked output in place: that also changes the cached inode. A later
digest check detects corruption and rebuilds, but cannot undo the edit.
Keep inputs stable during a job and give concurrent jobs distinct outputs.

Only successful commands store outputs. Missing or invalid outputs, cache
errors, and changed inputs produce diagnostics; cache failures do not
replace a command's exit code or prevent useful work from running. Caching
is skipped entirely with `BATON=off`.

```sh
baton cache prune
```

Stores and explicit pruning enforce `cache.max_size` using least-recently
used entries. `ls` prints keys, bytes, last-use times, and the cap. `prune`
deletes entries immediately; it has no dry-run flag.
