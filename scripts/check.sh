#!/bin/sh
# check.sh — the full local verification, runnable by you or by Claude Code with
# no TTY. Mirrors what .github/workflows/{build,govulncheck}.yml enforce in CI, so
# a green run here means a green CI. Use GOTOOLCHAIN=local on a Go 1.25+ host.
set -eu
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local

echo "→ marshaller single-path guard"
sh scripts/check-marshal-singlepath.sh

echo "→ schema write guard (no ordinary write may raise a board's layout)"
sh scripts/check-schema-write-guard.sh

echo "→ board-hook template syntax guard (POSIX sh -n)"
for h in scripts/board-hooks/post-merge scripts/board-hooks/post-rewrite scripts/board-hooks/pre-push; do
  sh -n "$h"
done
echo "  scripts/board-hooks/* parse clean"

# Mirrors go-ci.yml's module-hygiene step (build.yml calls that reusable), so a
# green run here matches CI. `go mod tidy -diff` prints the needed changes and
# exits non-zero WITHOUT touching go.mod/go.sum — under `set -e` it aborts on
# drift on its own, so no bare-`diff` footgun applies here. `go mod verify`
# then checks the cached module downloads haven't been altered since download
# (a cache-integrity check, not a go.sum re-derivation).
echo "→ module hygiene (go mod tidy -diff + verify)"
go mod tidy -diff
go mod verify

echo "→ go build"
go build ./...

echo "→ go vet"
go vet ./...

echo "→ go test -race (all packages)"
go test -race ./...

if command -v golangci-lint >/dev/null 2>&1; then
  echo "→ golangci-lint"
  golangci-lint run ./...
else
  echo "→ golangci-lint (skipped — not installed; CI runs it)"
fi

if command -v govulncheck >/dev/null 2>&1; then
  echo "→ govulncheck"
  govulncheck ./...
else
  echo "→ govulncheck (skipped — not installed; CI runs it)"
fi

# The release pipeline only ever runs on a tag, so a defect in .goreleaser.yaml /
# release.yml normally surfaces AFTER the draft is published and the cask pushed
# (v0.8.0). A snapshot build exercises it for real — the same job runs on every
# PR, so this is just the local mirror. Needs syft: without it the `sboms:` pipe
# (the thing that broke) does not run at all.
if command -v goreleaser >/dev/null 2>&1 && command -v syft >/dev/null 2>&1; then
  echo "→ release dry-run (goreleaser snapshot + artifact-shape assertions)"
  goreleaser release --snapshot --clean --skip=publish,announce >/dev/null
  sh scripts/check-release-artifacts.sh dist
else
  echo "→ release dry-run (skipped — needs goreleaser + syft; CI runs it on every PR)"
fi

echo "→ build binary for live checks"
go build -o bin/furrow ./cmd/furrow
BIN="$(pwd)/bin/furrow"

# Every guard that interrogates the built binary lives in ONE shared script,
# called verbatim by CI (build.yml) — the two lists used to be hand-copied and
# drifted (CI missed the epic schema diff and most of the smoke).
FURROW_BIN="$BIN" sh scripts/check-live.sh

echo "→ nix flake version ⇄ release-pin lockstep guard"
sh scripts/check-version-lockstep.sh

# Release CONFIG/behavior invariants (ldflags -X path resolves to the real
# version package; GoReleaser publishes non-draft; release.yml keeps the soft
# exit-1 fold). Pure text — no goreleaser needed, so unlike the artifact dry-run
# it always runs.
echo "→ release-config invariants guard"
sh scripts/check-release-invariants.sh

echo "✓ all checks passed"
