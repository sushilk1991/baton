#!/bin/sh
# Render a tap formula only after all four archive digests validate.
set -eu

fail() {
  printf 'baton formula: %s\n' "$*" >&2
  exit 1
}

[ "$#" -eq 2 ] || fail "usage: $0 VERSION CHECKSUMS"
version=${1#v}
case "$version" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) fail 'VERSION must be a release version, for example v1.2.3' ;;
esac
case "$version" in
  *[!a-zA-Z0-9.+-]*) fail 'invalid VERSION' ;;
esac
manifest=$2
template_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../packaging/homebrew" && pwd)
sha256_length=64

read_sha() {
  # Exact names and unique entries prevent a partial or ambiguous formula.
  awk -v asset="baton_$1.tar.gz" -v size="$sha256_length" '
    $2 == asset { count++; digest = $1 }
    END {
      if (count != 1 || length(digest) != size || digest ~ /[^0-9a-f]/) { exit 1 }
      print digest
    }
  ' "$manifest" || fail "invalid or missing checksum entry for $1"
}

# Validate every digest before emitting bytes, including on a Linux-only host.
darwin_arm64=$(read_sha darwin_arm64)
darwin_amd64=$(read_sha darwin_amd64)
linux_arm64=$(read_sha linux_arm64)
linux_amd64=$(read_sha linux_amd64)
sed -e "s/@VERSION@/$version/g" \
  -e "s/@DARWIN_ARM64_SHA256@/$darwin_arm64/g" \
  -e "s/@DARWIN_AMD64_SHA256@/$darwin_amd64/g" \
  -e "s/@LINUX_ARM64_SHA256@/$linux_arm64/g" \
  -e "s/@LINUX_AMD64_SHA256@/$linux_amd64/g" \
  "$template_dir/baton.rb.tmpl"
