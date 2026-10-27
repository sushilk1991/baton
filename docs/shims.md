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

Shims live under `$XDG_DATA_HOME/baton/shims` and bind to the absolute baton
binary used to install them. Keep that binary in place. Resolution searches
the current PATH on every invocation, excluding the shim directory and
aliases that resolve back into it. A recursion chain rejects repeated
identical invocations with exit 126.

`BATON=off ffmpeg ...` goes directly to the real executable and remains
usable with broken configuration. These default fast paths also bypass
scheduling and history:

| Command | Bypass |
| --- | --- |
| `ffprobe` | Every invocation |
| `ffmpeg` | `-version`, `-h`, or `-help` as a separate argument |
| `ffmpeg` | `-f null` |

Configure regex arrays in `[shims.fast_paths]` to change these rules. A
nonmatching invocation uses auto classification; unmatched commands execute
without a lane. Shims preserve argument boundaries and inherited stdio.

## Whole-command reservations

An admitted shim marks its descendants so they use the same reservation.
For example, an npm install that invokes another shim does not wait on its
own lane. It does not receive extra slots for its descendants.

Manual `baton run` does not set that shim marker. Wrapping a shimmed command
manually can make the child wait on its parent's slot. Use one wrapping
method. To reserve an entire shell/build tree and bypass descendant shims:

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
