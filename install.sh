#!/bin/sh
# Install a verified Unix release without sudo or shell configuration edits.
set -eu

release_root=https://github.com/sushilk1991/baton/releases
checksum_file=checksums.txt
sha256_length=64

fail() {
  printf 'baton install: %s\n' "$*" >&2
  exit 1
}

# Resolve only published target names; reject unsupported hosts before download.
case "$(uname -s)" in
  Darwin) target_os=darwin ;;
  Linux) target_os=linux ;;
  *) fail 'unsupported OS; download the Windows zip manually' ;;
esac
case "$(uname -m)" in
  x86_64|amd64) target_arch=amd64 ;;
  arm64|aarch64) target_arch=arm64 ;;
  *) fail 'unsupported architecture; supported: amd64, arm64' ;;
esac

# Fixed asset names let latest/download work without parsing a remote API.
version=${BATON_VERSION:-latest}
case "$version" in
  latest) download_root=$release_root/latest/download ;;
  v[0-9]*)
    case "$version" in
      *[!a-zA-Z0-9.+-]*) fail 'invalid BATON_VERSION; use a v-prefixed release tag' ;;
    esac
    download_root=$release_root/download/$version
    ;;
  *) fail 'invalid BATON_VERSION; use latest or a v-prefixed release tag' ;;
esac
install_dir=${BATON_INSTALL_DIR:-${HOME:?HOME must be set}/.local/bin}
case "$install_dir" in
  /*) ;;
  *) fail 'BATON_INSTALL_DIR must be absolute' ;;
esac
destination=$install_dir/baton
[ ! -L "$destination" ] || fail 'destination is a symlink; choose another BATON_INSTALL_DIR'
[ ! -e "$destination" ] || [ -f "$destination" ] || fail 'destination is not a regular file'

# macOS provides shasum; Linux commonly provides sha256sum. Never skip hashing.
for tool in curl tar mktemp awk; do
  command -v "$tool" >/dev/null 2>&1 || fail "required tool missing: $tool"
done
if command -v sha256sum >/dev/null 2>&1; then
  hash_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  hash_tool=shasum
else
  fail 'SHA-256 requires sha256sum or shasum'
fi

# Download -> exact manifest entry -> hash -> extract to stdout -> atomic rename.
# Private scratch files and same-directory staging keep failed upgrades intact.
scratch=$(mktemp -d)
staged=
cleanup() {
  rm -f "$scratch/archive" "$scratch/$checksum_file" "$scratch/baton"
  rmdir "$scratch"
  if [ -n "$staged" ]; then
    rm -f "$staged"
  fi
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

fetch() {
  # Ignore curlrc and forbid HTTP redirects; release transport stays HTTPS.
  curl --disable -fsSL --proto '=https' --proto-redir '=https' \
    --connect-timeout 10 --max-time 120 --retry 3 "$1" -o "$2" || fail "download failed: $1"
}

asset=baton_${target_os}_${target_arch}.tar.gz
fetch "$download_root/$checksum_file" "$scratch/$checksum_file"
fetch "$download_root/$asset" "$scratch/archive"
expected=$(awk -v asset="$asset" -v size="$sha256_length" '
  $2 == asset { count++; digest = $1 }
  END {
    if (count != 1 || length(digest) != size || digest ~ /[^0-9a-f]/) { exit 1 }
    print digest
  }
' "$scratch/$checksum_file") || fail "invalid or missing checksum entry for $asset"
if [ "$hash_tool" = sha256sum ]; then
  actual=$(sha256sum "$scratch/archive") || fail 'SHA-256 failed'
else
  actual=$(shasum -a 256 "$scratch/archive") || fail 'SHA-256 failed'
fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || fail "checksum mismatch for $asset"

# Extract only the binary to a controlled file, never archive paths on disk.
tar -xzOf "$scratch/archive" baton > "$scratch/baton" || fail 'cannot extract baton'
[ -s "$scratch/baton" ] || fail 'archive contains an empty baton binary'
mkdir -p "$install_dir"
staged=$(mktemp "$install_dir/.baton.XXXXXX")
cp "$scratch/baton" "$staged"
chmod 755 "$staged"
mv -f "$staged" "$destination"
staged=

printf 'Installed %s (SHA-256 verified)\n' "$destination"
printf 'Run: "%s" help\n' "$destination"
printf 'Add %s to PATH if needed.\n' "$install_dir"
