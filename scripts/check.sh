#!/bin/sh
# check.sh — the full local verification, runnable by you or by Claude Code with
# no TTY. It mirrors the Go core of CI (build/vet/race-test/module hygiene/
# golangci-lint/govulncheck of source AND binary) plus every repo-specific guard,
# says when the local Go or golangci-lint is not the one CI runs, and runs the
# workflow/TOML linters (actionlint, taplo, zizmor) and the commit lint (glyph,
# over origin/main..HEAD) when they are on PATH. A green run here is NOT a
# green CI: go-bite (does each new test fail against the pre-PR source?), the
# PR-title half of commit-lint and repo-policy have no local pass, and a linter
# that is not installed is skipped with a note, not failed — read the "skipped"
# lines. Use GOTOOLCHAIN=local on a Go 1.26+ host.
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

# CI runs `go-version: stable` (build.yml), the CURRENT Go release, while a
# host may sit on an older one; the lint broke twice on exactly that skew
# (build.yml's golangci note), so say when the two differ — like the
# golangci-lint pin check below, pure text plus one 3-second lookup of what
# stable is today, skipped quietly offline.
ci_go="$(sed -n 's/^[[:space:]]*go-version:[[:space:]]*\([^[:space:]#]*\).*/\1/p' .github/workflows/build.yml | head -1)"
local_go="$(go env GOVERSION)"
if [ "$ci_go" = "stable" ]; then
  stable_go="$(curl -fsS --max-time 3 'https://go.dev/VERSION?m=text' 2>/dev/null | head -1 || true)"
  if [ -z "$stable_go" ]; then
    echo "→ go toolchain (local $local_go; CI runs go-version: stable — could not resolve stable offline, verdicts can differ)"
  elif [ "$stable_go" != "$local_go" ]; then
    echo "→ go toolchain (WARNING: local $local_go but CI runs stable = $stable_go, build.yml go-version: stable — verdicts can differ)"
  else
    echo "→ go toolchain ($local_go, matches CI's stable)"
  fi
elif [ -n "$ci_go" ] && [ "go$ci_go" != "$local_go" ] && [ "$ci_go" != "$local_go" ]; then
  echo "→ go toolchain (WARNING: local $local_go but CI pins go-version: $ci_go in build.yml — verdicts can differ)"
else
  echo "→ go toolchain ($local_go, matches build.yml)"
fi

echo "→ go build"
go build ./...

echo "→ go vet"
go vet ./...

echo "→ go test -race (all packages)"
go test -race ./...

if command -v golangci-lint >/dev/null 2>&1; then
  # CI pins the linter version in build.yml (the reusable's default once broke
  # every PR against the current Go); a local binary on another version can
  # pass here and fail there or vice versa, so say so — pure text extraction,
  # like check-version-lockstep.sh.
  ci_lint="$(sed -n 's/^[[:space:]]*golangci-lint-version:[[:space:]]*v\([0-9][^[:space:]]*\).*/\1/p' .github/workflows/build.yml | head -1)"
  local_lint="$(golangci-lint version 2>/dev/null | sed -n 's/.*version \([0-9][^ ]*\).*/\1/p' | head -1)"
  if [ -n "$ci_lint" ] && [ "$ci_lint" != "$local_lint" ]; then
    echo "→ golangci-lint (WARNING: local v${local_lint:-?} but CI pins v$ci_lint in build.yml — verdicts can differ)"
  else
    echo "→ golangci-lint (v${local_lint:-?}, matches the CI pin)"
  fi
  golangci-lint run ./...
else
  echo "→ golangci-lint (skipped — not installed; CI runs it)"
fi


# The release pipeline only ever runs on a tag, so a defect in .goreleaser.yaml /
# release.yml normally surfaces AFTER the draft is published and the cask pushed
# (v0.8.0). A snapshot build exercises it for real — the same job runs on every
# PR, so this is just the local mirror. Needs syft: without it the `sboms:` pipe
# (the thing that broke) does not run at all.
if command -v goreleaser >/dev/null 2>&1 && command -v syft >/dev/null 2>&1; then
  echo "→ release dry-run (goreleaser snapshot + artifact-shape assertions)"
  # The cask template reads HOMEBREW_TAP_DEPLOY_KEY even under --snapshot; the
  # dummy mirrors build.yml's, so the dry-run fails only for a real defect
  # (measured: without it every machine with syft failed here on the template).
  HOMEBREW_TAP_DEPLOY_KEY="${HOMEBREW_TAP_DEPLOY_KEY:-dry-run-not-a-real-key}" \
    goreleaser release --snapshot --clean --skip=publish,announce >/dev/null
  sh scripts/check-release-artifacts.sh dist
else
  echo "→ release dry-run (skipped — needs goreleaser + syft; CI runs it on every PR)"
fi

echo "→ build binary for live checks"
go build -o bin/furrow ./cmd/furrow
BIN="$(pwd)/bin/furrow"

# Both govulncheck modes CI runs (govulncheck.yml via the hub's go-vuln.yml):
# the source, and the compiled binary — the latter is what catches a stdlib
# vuln reachable in the shipped artifact (GO-2025-3595 shipped past a
# source-only scan, t-e8hm).
if command -v govulncheck >/dev/null 2>&1; then
  echo "→ govulncheck (source)"
  govulncheck ./...
  echo "→ govulncheck (shipped binary)"
  govulncheck -mode binary "$BIN"
else
  echo "→ govulncheck (skipped — not installed; CI runs it)"
fi

# Every guard that interrogates the built binary lives in ONE shared script,
# called verbatim by CI (build.yml) — the two lists used to be hand-copied and
# drifted (CI missed the epic schema diff and most of the smoke).
FURROW_BIN="$BIN" sh scripts/check-live.sh

echo "→ release-pin guard (no version literal in sync-task-status.yml, flake vendorHash freshness)"
sh scripts/check-version-lockstep.sh

# Release CONFIG/behavior invariants (ldflags -X path resolves to the real
# version package; GoReleaser publishes non-draft; release.yml keeps the soft
# exit-1 fold). Pure text — no goreleaser needed, so unlike the artifact dry-run
# it always runs.
echo "→ release-config invariants guard"
sh scripts/check-release-invariants.sh

echo "→ docs word-budget guard"
sh scripts/check-docs-budget.sh

# The workflow/TOML linters CI runs as separate gates (actionlint.yml, taplo.yml,
# zizmor.yml — each a thin caller of the hub reusable). Run when installed, with
# the reusables' own flags; skipped with a note otherwise.
if command -v actionlint >/dev/null 2>&1; then
  echo "→ actionlint (workflow syntax + shellcheck over run: blocks)"
  actionlint -color
else
  echo "→ actionlint (skipped — not installed; CI runs it)"
fi
if command -v taplo >/dev/null 2>&1; then
  echo "→ taplo lint + fmt --check"
  taplo lint
  taplo fmt --check
else
  echo "→ taplo (skipped — not installed; CI runs it)"
fi
if command -v zizmor >/dev/null 2>&1; then
  echo "→ zizmor (Actions security, offline audits)"
  zizmor --no-online-audits .github/workflows
else
  echo "→ zizmor (skipped — not installed; CI runs it)"
fi

# The commit half of CI's commit-lint gate (the PR title has no local pass):
# every commit on the branch, the range CI lints. A push that fails it costs a
# round trip, so it runs here when glyph is on PATH, skipped with a note like
# the other linters. origin/main may be absent on a fresh clone — then there is
# no range to lint and CI remains the authority.
if ! command -v glyph >/dev/null 2>&1; then
  echo "→ glyph lint (skipped — not installed; CI runs it)"
elif ! git rev-parse --verify -q origin/main >/dev/null 2>&1; then
  echo "→ glyph lint (skipped — no origin/main to range against; CI runs it)"
else
  echo "→ glyph lint (commit convention over origin/main..HEAD, what commit-lint runs in CI)"
  glyph lint --range origin/main..HEAD
fi

echo "✓ all checks passed (CI-only gates not covered here: go-bite, the PR-title half of commit-lint, repo-policy)"
