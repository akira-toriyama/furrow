#!/bin/sh
# build.sh — build furrow into bin/furrow with the version stamped from git.
# Used by install.sh.
set -eu
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

# --match: only a version tag may name the build; any other tag reachable from
# HEAD would otherwise be described instead.
VERSION="$(git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse HEAD 2>/dev/null || echo '')"
DATE="$(git show -s --format=%cI HEAD 2>/dev/null || echo '')"

PKG=github.com/akira-toriyama/furrow/internal/version
mkdir -p bin
GOTOOLCHAIN=local go build -trimpath \
  -ldflags "-s -w \
    -X '${PKG}.Version=${VERSION}' \
    -X '${PKG}.Commit=${COMMIT}' \
    -X '${PKG}.Date=${DATE}'" \
  -o bin/furrow ./cmd/furrow

echo "built: $DIR/bin/furrow  (${VERSION})"
