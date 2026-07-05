#!/bin/sh
# build.sh — build furrow into bin/furrow with the version stamped from git.
# Used by install.sh and the Homebrew formula's from-source fallback.
set -eu
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
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
