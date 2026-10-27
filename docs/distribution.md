# Distribution

Build from source until a release is published. Local packaging tests do
not establish that GitHub assets or the Homebrew tap are available.

## Installer

After publication, download and inspect the installer before running it:

```sh
curl -fsSL https://raw.githubusercontent.com/sushilk1991/baton/main/install.sh -o install.sh
sh install.sh
"$HOME/.local/bin/baton" help
```

`BATON_VERSION` defaults to `latest`; a tag such as `v1.2.3` selects that
release. `BATON_INSTALL_DIR` defaults to `~/.local/bin` and must be absolute.
For example, `BATON_VERSION=v1.2.3 sh install.sh`. No sudo, agent setup, or
shell edits occur. Add the installation directory to PATH yourself.

The POSIX shell installer supports macOS/Linux on amd64/arm64. It requires
curl, tar, awk, mktemp, standard filesystem utilities, and either sha256sum
or shasum. Downloads use HTTPS, ignore curlrc, and have bounded timeouts.
The exact archive must have one valid SHA-256 entry in `checksums.txt`.
Hash and extraction failures leave an existing binary intact. A verified
binary is staged beside its destination and renamed into place; existing
symlink or nonregular destinations are rejected. Temporary files are
removed on normal exit and handled signals. SIGKILL can leave scratch files.

The installer trusts GitHub's HTTPS response and the repository's release
assets. Checksums detect corruption against that manifest. Linux binaries
use no cgo and need no libc.
macOS binaries are unsigned; notarization is not configured.

Windows users download `baton_windows_amd64.zip` and `checksums.txt` from
the same release, compare `Get-FileHash -Algorithm SHA256` to the manifest,
then extract `baton.exe` to a directory on PATH. The shell installer does
not support Windows. Windows runtime behavior remains best effort.

## Local packaging

With GoReleaser v2.19.0 on PATH, run in a checkout without an existing
`dist/` directory:

```sh
make check
goreleaser check
goreleaser release --snapshot --parallelism 1
scripts/homebrew-formula.sh 0.0.0-snapshot dist/checksums.txt > dist/baton.rb
```

Snapshot mode skips publication and uses version `0.0.0-snapshot`. It writes
five archives, `checksums.txt`, and build metadata under `dist/`. Snapshot
formula URLs describe unpublished assets and are suitable only for review.
The archives contain the binary, LICENSE, README, and CHANGELOG. Unix uses
tar.gz; Windows uses zip. All builds set `CGO_ENABLED=0` and `-trimpath`.
Go's build information remains available through `go version -m`.

## Tagged release preparation

The tag-triggered workflow matches `v*`. It runs `make check` on Linux
amd64/arm64 and macOS, plus Windows vet, core tests, and a cgo-free build.
Only after those jobs pass does GoReleaser create a GitHub **draft** with
the five archives and checksums. A separate step renders and attaches
`baton.rb`. Failures leave a draft to inspect; the workflow does not publish
it or push to a tap.

For a future approved release, review the draft assets, publish the release,
then copy the rendered formula to `Formula/baton.rb` in
`sushilk1991/homebrew-tap`. Verify `brew install sushilk1991/tap/baton` and
`brew test sushilk1991/tap/baton` against those published assets. No
cross-repository token is configured. The existing CI matrix still includes
the retired macos-13 runner required by SPEC; this draft workflow uses
available runners.

The formula renderer accepts a release version and checksums file. It
requires one digest per Unix target before emitting any output. The formula
installs only the binary and tests help and command execution in private
HOME/XDG paths. It does not register agents or change shell configuration.

Primary references checked for this configuration:
[GoReleaser builds](https://goreleaser.com/customization/builds/builders/go/),
[archives](https://goreleaser.com/customization/package/archives/),
[checksums](https://goreleaser.com/customization/package/checksum/),
[draft releases](https://goreleaser.com/customization/publish/scm/),
[GitHub Actions](https://goreleaser.com/customization/ci/actions/), and
[Homebrew conditional blocks](https://docs.brew.sh/Formula-Cookbook#handling-different-system-configurations).
