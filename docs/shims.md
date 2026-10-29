# Command shims

```sh
baton shim install ffmpeg npm
baton shim list
```

Install only commands you already use. Explicit missing commands fail;
`baton shim install` without names tries the configured defaults and reports
unavailable commands. Defaults cover ffmpeg, ffprobe, npm, pnpm, yarn, bun,
pip, pip3, uv, cargo, go, and xcodebuild.

The installer prints a PATH instruction for your shell. For POSIX shells:

```sh
export PATH="${XDG_DATA_HOME:-$HOME/.local/share}/baton/shims:$PATH"
```

Apply it to the current shell first. Persist it yourself if wanted; baton
does not edit shell configuration. Fish and PowerShell users should use
the shell-specific line printed by the installer. Start a fresh shell or
clear its command lookup cache after changing PATH.

## Resolution and bypass

```text
ffmpeg args -> shim -> PATH search excluding shim directory -> real ffmpeg
                         |
                    auto lane -> baton run -> child
                         |
                   fast/off -> direct child
```

Shims live under `$XDG_DATA_HOME/baton/shims` and bind to the absolute path
used to invoke baton during installation. Symlinks stay unresolved, so a
Homebrew/Linuxbrew upgrade can replace their targets. Resolution searches
the current PATH on every invocation, excluding the shim directory and
aliases that resolve back into it. A recursion chain rejects repeated
identical invocations with exit 126.

On macOS/Linux, a missing or non-executable baton makes the wrapper remove
its own PATH entries, including aliases, and exec the real command directly.
`BATON=off` also works when baton is unavailable. Arguments, stdio, and exit
status pass through; fallback work has no scheduling or history. Uninstall
and install shims again if baton moves permanently to a different path.

`BATON=off ffmpeg ...` goes directly to the real executable and remains
usable with broken configuration. These default fast paths also bypass
scheduling and history:

| Command | Bypass |
| --- | --- |
| `ffprobe` | Every invocation |
| `ffmpeg` | `-version`, `-h`, or `-help` as a separate argument |
| `ffmpeg` | `-f null` |

Configure regex arrays in `[shims.fast_paths]` to change these rules. A
nonmatching invocation uses auto classification. A command no lane matches,
such as `npm test`, execs the real tool directly with no history row. Shims
preserve argument boundaries and inherited stdio.

## Whole-command reservations

Every admitted `baton run`, including a shim, marks the lanes held by its
ancestors. A child run or shim in any lane shares that reservation and
records `nested: true` in history. For example, `baton run --lane install --
npm ci` works with npm shims installed, including npm calls from its scripts.
It grants no extra slots or memory; limit the tool's internal parallelism.
Nested heavy jobs still check memory, bounded by their lane's `max_wait`.
Expired reservations grant no bypass.

To cover every descendant with only the outer reservation, bypass the child
tree explicitly:

```sh
baton run --lane build -- env BATON=off make test
```

The outer run still schedules and records the tree; `BATON=off` applies to
the child. This also prevents explicit baton calls inside the build from
creating nested reservations. Size the outer reservation for the whole job.

## Remove shims

```sh
baton shim uninstall
```

Uninstall checks the private manifest and removes only unchanged wrappers
it owns. Modified or replaced files cause a conflict. Remove the shim PATH
entry from your shell config separately. The shared management lock file
remains; never unlink active lock files to force admission.
