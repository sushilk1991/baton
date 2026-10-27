# Record the terminal demo

From the repository root, with [VHS](https://github.com/charmbracelet/vhs),
its runtime dependencies, Go, and Make already installed:

```sh
make build
(
  demo_home=$(mktemp -d)
  trap 'rm -rf "$demo_home"' EXIT
  env -i PATH="$PWD/bin:$PATH" HOME="$demo_home" USERPROFILE="$demo_home" \
    TERM=xterm-256color VHS_PUBLISH=false vhs docs/demo.tape
)
```

The launcher isolates HOME before VHS starts a shell, so shell startup
cannot load the user's configuration. The tape writes `docs/demo.gif`. It
sets its own temporary HOME and all standard XDG paths before starting
baton; inherited XDG variables are cleared. A single-slot demo lane with a
disabled memory gate shows slot waiting without making the host allocate RAM.
It neither installs agent integration nor starts a shared browser.

The tape waits for its own jobs and removes its temporary workspace on
exit. Review every recorded frame before sharing the GIF. The demo has not
been rendered in this docs iteration because VHS is unavailable; its shell
commands are exercised separately. Commit the tape as the source of truth;
a GIF is optional and no performance result is implied.
