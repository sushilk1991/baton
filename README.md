# baton

Queue heavy jobs from every agent and terminal on one machine.

```sh
baton run --lane render --max-wait 2s -- sleep 1
```

Two agents start Chrome, another loads a local model, and an encode takes
every core. None knows about the others. The laptop swaps until everything
slows down. baton puts those jobs in shared lanes and waits for capacity
and memory headroom before starting the next one.

One Go binary. No daemon for scheduling. Kernel locks release slots when a
runner dies. macOS and Linux are the primary targets; Windows builds with
best-effort process control. Memory admission is advisory, not a RAM limit.

## Install

This checkout is unreleased. Build from source with Go 1.22+ and Make:

```sh
make build
export PATH="$PWD/bin:$PATH"
baton --help
```

The binary is `bin/baton`; put it at a stable location before installing
shims or agent hooks. Go can also install from the checkout:

```sh
go install ./cmd/baton
```

Release packaging includes a Homebrew formula and a checksum-verifying
installer. These commands require a published release and are
unverified; they are not the current installation path:

```sh
brew install sushilk1991/tap/baton
curl -fsSL https://raw.githubusercontent.com/sushilk1991/baton/main/install.sh | sh
go install github.com/sushilk1991/baton/cmd/baton@latest
```

The installer defaults to `~/.local/bin`, verifies SHA-256, and leaves shell
configuration alone. Set `BATON_VERSION=v1.2.3` to choose a tag or
`BATON_INSTALL_DIR=/absolute/path` to choose a directory. See
[distribution](docs/distribution.md) for local packaging and the draft workflow.

## Try it in 60 seconds

After building, run these from a POSIX shell:

1. Start two jobs in a new, single-slot lane:

   ```sh
   baton run --lane demo --max-wait 2s --label first -- sleep 3 &
   first=$!
   baton run --lane demo --max-wait 2s --label second -- sleep 3 &
   second=$!
   baton status
   wait "$first"
   wait "$second"
   ```

   One runs while the other waits. Under memory pressure, these tiny demo
   jobs proceed after two seconds with a warning. Keep the default memory
   wait for real heavy work. Admission follows ticket arrival, not shell
   launch order.

2. Read completed jobs and machine pressure:

   ```sh
   baton history --since 1h --lane demo
   baton status --line
   ```

3. Preview integration with installed agents:

   ```sh
   baton init --dry-run
   ```

   The preview lists paths without writing agent files. Apply separately
   with `baton init --yes`; see [agent integration](docs/agents.md).

The reproducible terminal demo is [docs/demo.tape](docs/demo.tape).
[Recording instructions](docs/demo.md) keep its state separate from yours.
No recording is claimed yet. A bounded [machine benchmark](docs/benchmarks.md)
records measured results and their limits.

## How it works

```text
Claude / Codex / Cursor / terminal
              |
       explicit run or shim
              |
      argv -> automatic lane
              |
     priority + FIFO ticket <---- wait, keeping position
              |                         ^
       pressure + headroom ------------->|
              |
       kernel-held slot locks
              |
       child process group -> history + learned peak RSS
              |
        exit / crash -> locks released
```

Defaults are `render`, `encode`, `ml`, `install`, and `build`. Capacity
comes from RAM and CPU count. Unmatched commands run immediately; explicit
unknown lanes get one slot. FIFO applies within priority, with aging to
promote long waiters. `--weight` reserves several slots together.

`--mem 4G` declares expected RAM; otherwise baton uses the command's learned
peak RSS. Pressure recovery requires sustained healthy samples. The default
30-minute `max_wait` bypasses the memory gate with a warning; it never
bypasses occupied slots. Jobs can still exceed their estimates or allocate
after a healthy sample. [Configuration](docs/configuration.md) covers policy.

## Recipes

These wrap your existing tools and projects; baton installs none of them.

| Work | Start here |
| --- | --- |
| Claude Code, Codex, Cursor | [Skills, global instructions, optional Claude hook, read-only MCP](docs/agents.md) |
| Remotion, HyperFrames, ffmpeg | [Video recipes](docs/recipes.md#video-pipelines) |
| Whisper, Ollama | [Local ML recipes and persistent-server limits](docs/recipes.md#local-ml) |
| Monorepo installs and builds | [Separate workspaces, shared lanes](docs/recipes.md#monorepos) |
| A laptop used as a CI runner | [Whole-build reservations](docs/recipes.md#laptops-as-ci-runners) |

For automatic routing of ordinary commands, install
[shims](docs/shims.md). For tools that support CDP, reuse the
[shared browser](docs/browser.md). For repeatable single-file outputs, use
the [cache](docs/cache.md).

## Choose the right tool

baton fits several independent agents sharing a workstation: foreground
commands, named lanes, memory admission, shims, and agent setup in one binary.
The alternatives below are often better for narrower or stronger needs.

| Tool | Use it when | Difference from baton |
| --- | --- | --- |
| [flock](https://man7.org/linux/man-pages/man1/flock.1.html) | You need one shell lock with few dependencies. | Mutual exclusion; you supply routing, queue policy, and pressure checks. |
| [GNU parallel / sem](https://www.gnu.org/software/parallel/parallel_tutorial.html#Semaphore) | You have a batch of inputs, or need a shared shell semaphore. | Parallel handles batch distribution; sem provides named concurrency limits. baton adds workstation and agent policy. |
| [task-spooler (ts)](https://viric.name/soft/ts/) | You want a per-user batch queue with stored output. | ts submits work to a server; baton run stays attached to your command's stdio. |
| [pueue](https://github.com/Nukesor/pueue) | You want daemon-managed background tasks, groups, pause, and resume. | pueued manages job lifetime; baton core needs no daemon. |
| [systemd-run / cgroups](https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html) | You need enforced Linux CPU or memory limits. | cgroups enforce budgets; baton observes pressure and controls starts. |

## FAQ

### Does it prevent every OOM or swap spike?

No. Admission samples memory; it does not reserve or cap it. Different lanes
can admit jobs concurrently, and unrelated tools can allocate at any time.
Tune slots and budgets for your workloads. The bounded
[machine stress run](docs/benchmarks.md) passed; its unqueued baseline did too.

### Does every terminal share a queue?

Only when it uses the same state directory, normally
`~/.local/state/baton`. Use a local filesystem with working advisory locks.
Separate users, containers without a shared state mount, and XDG overrides
have separate queues. Network filesystems are unverified.

### What happens on cancellation or a crash?

On Unix, INT, TERM, and HUP are forwarded to the child process group. Exit
codes pass through. SIGKILL releases the runner's kernel locks but can leave
children alive; inspect them with [reap](docs/cleanup.md#orphaned-processes).
A detached child can also outlive its reservation.

### Can I bypass baton or wrap a shell pipeline?

`BATON=off` bypasses scheduling, caching, and history, even with broken
configuration. Auto detection does not inspect shell scripts. Give shell
pipelines an explicit lane. Avoid nesting a manual reservation around
shimmed tools; see [whole-command reservations](docs/shims.md#whole-command-reservations).

### What does baton record or send?

Local metadata includes labels, agent, user, cwd, repo, timings, RSS, and exit
codes. Command signatures are hashes; raw argv is not written to history.
The integration manifest stores original agent-file bytes for restoration.
baton has no telemetry. Only the optional browser feature uses loopback
networking; child commands retain their normal network behavior.

## Uninstall

1. Restore agent integration before removing the binary:

   ```sh
   baton init --uninstall --dry-run
   baton init --uninstall --yes
   ```

   Review the preview. Edited installed files cause a conflict; resolve it
   before retrying so the manifest can restore your originals.

2. Remove command wrappers and stop baton's shared browser:

   ```sh
   baton shim uninstall
   baton browser stop
   ```

3. Remove the shim PATH entry from your shell config and remove the binary
   from wherever you installed it. Retain state until all jobs finish.
   Optional data removal is described in [cleanup](docs/cleanup.md#removing-baton-data).

Read [CONTRIBUTING.md](CONTRIBUTING.md) for checks and documentation updates,
[SECURITY.md](SECURITY.md) for reporting vulnerabilities, and
[CHANGELOG.md](CHANGELOG.md) for unreleased changes.

Licensed under [Apache-2.0](LICENSE).
