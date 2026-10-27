# Workload recipes

These are templates for existing tools, input files, and projects. Start
with the command you already run, then add `baton run`. The examples assume
the executable is not also
shimmed; [choose one wrapper](shims.md#whole-command-reservations).

## Video pipelines

Inside an existing [Remotion](https://www.remotion.dev/docs/cli/render)
project with a `Main` composition and installed CLI:

```sh
baton run --lane render --label Main -- npx --no-install remotion render Main out/main.mp4
```

Inside an existing [HyperFrames](https://github.com/heygen-com/hyperframes)
project with its CLI installed:

```sh
baton run --lane render -- npx --no-install hyperframes render
```

Encode an existing input with ffmpeg:

```sh
baton run --lane encode --label delivery -- ffmpeg -i input.mp4 -c:v libx264 -preset slow -c:a aac output.mp4
```

The lane bounds concurrent commands, not threads or Chrome instances inside
one renderer. Set the renderer's own concurrency and ffmpeg thread options
to fit one reservation. `--weight 2` requires a lane with at least two
allowed slots; it does not make the child use fewer resources.

Use [cache flags](cache.md) only after declaring all project dependencies.
Automatic classification sees direct argv, including `npx remotion`, but
does not inspect commands hidden inside scripts. CDP-capable scripts can
reuse the [shared headless browser](browser.md); arbitrary renderers do not
adopt it merely because an endpoint variable exists.

## Local ML

With [Whisper](https://github.com/openai/whisper) installed, an existing
`audio.wav`, and your chosen model available:

```sh
baton run --lane ml --label transcription -- whisper audio.wav --output_dir out
```

Supply your usual model options; Whisper may download a missing model.
baton neither downloads models nor limits the child's network access.

For an already running [Ollama](https://docs.ollama.com/cli) server, set
`OLLAMA_MODEL` to an already downloaded local model:

```sh
baton run --lane ml -- ollama run "$OLLAMA_MODEL" 'Summarize this repository in one sentence.'
```

The slot follows the CLI command's lifetime. A persistent Ollama server can
retain its model after the command exits, and its memory is outside that
client's process tree. Learned client RSS therefore understates server
memory. Set `--mem` from a measured whole-workload budget and manage model
unloading through Ollama. The same limit applies to other resident servers.

## Monorepos

For two existing, independent npm workspaces with their own lockfiles:

```sh
(cd packages/api && baton run --lane install -- npm ci) &
api=$!
(cd packages/web && baton run --lane install -- npm ci) &
web=$!
wait "$api"
wait "$web"
```

The default install lane serializes them across all terminals using the
same state. Slots control machine load, not project correctness; do not run
two mutating installs against the same dependency directory.

For a Go build in a Go module:

```sh
baton run --lane build -- go build ./...
```

If a package manager starts several builds internally, baton sees one
reservation. Use that tool's parallelism setting as well.

## Laptops as CI runners

Wrap the complete build rather than individual nested steps:

```sh
baton run --lane build --label local-ci -- env BATON=off make test
```

The child bypass prevents installed shims or nested baton commands from
waiting on the outer reservation. Add a measured `--mem` budget and tune
the build lane for the whole process tree. Keep every runner under the
same user and state directory. An ephemeral CI HOME isolates its queue;
containers need an explicitly shared local state mount to coordinate.

Cancellation on Unix reaches descendants in the child's process group.
Detached workers, persistent services, and a SIGKILLed runner can outlive
it. Check [status and history](observability.md), then inspect
[cleanup candidates](cleanup.md) before terminating or deleting anything.
