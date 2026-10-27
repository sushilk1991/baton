#!/bin/sh
set -eu

# Build and schedule entirely in private HOME/XDG paths, even when a caller
# has installed shims or agent hooks. Only the requested artifacts persist.
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/baton-stress.XXXXXX")
trap 'rm -r -- "$scratch"' 0
trap 'exit 130' INT
trap 'exit 143' TERM HUP
env -i PATH="$PATH" LC_ALL=C BATON=off HOME="$scratch" USERPROFILE="$scratch" \
  XDG_CONFIG_HOME="$scratch/config" XDG_STATE_HOME="$scratch/state" \
  XDG_CACHE_HOME="$scratch/cache" XDG_DATA_HOME="$scratch/data" \
  XDG_RUNTIME_DIR="$scratch/runtime" XDG_CONFIG_DIRS="$scratch/config-dirs" \
  XDG_DATA_DIRS="$scratch/data-dirs" GOCACHE="$scratch/go-cache" \
  GOPATH="$scratch/go" GOTOOLCHAIN=local CGO_ENABLED=0 \
  sh -c '
    set -eu
    repo=$1 scratch=$2
    shift 2
    cd "$repo"
    go build -modcacherw -trimpath -o "$scratch/baton" ./cmd/baton
    go build -modcacherw -trimpath -o "$scratch/stress" ./internal/stress
    "$scratch/stress" --baton "$scratch/baton" "$@"
  ' sh "$repo" "$scratch" "$@"
