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

echo "→ go test -race (core + store + app + cli + migrate)"
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

# NB: run each `diff` as a bare command (not `diff … && echo`). Under `set -e`,
# a command on the LEFT of `&&` is exempt from errexit, so `diff … && echo`
# would SWALLOW a real drift and let check.sh exit 0 — diverging from CI, whose
# bare `diff` (build.yml) fails the run. A standalone `diff` aborts on drift and
# prints the offending diff; the confirmation echo only runs when it matched.
echo "→ schema drift guard"
"$BIN" schema task | diff -u docs/schema/furrow.task.v2.json -
echo "  task schema matches docs/schema/furrow.task.v2.json"
"$BIN" schema meta | diff -u docs/schema/furrow.meta.v2.json -
echo "  meta schema matches docs/schema/furrow.meta.v2.json"
"$BIN" schema repo | diff -u docs/schema/furrow.repo.v1.json -
echo "  repo schema matches docs/schema/furrow.repo.v1.json"

echo "→ config template drift guard"
tmp="$(mktemp -d)"
( cd "$tmp" && "$BIN" init >/dev/null )
diff -u config.toml "$tmp/.furrow/config.toml"
echo "  config.toml matches init template"

echo "→ global config template drift guard"
gtmp="$(mktemp -d)"
# Run from a dir with no enclosing .furrow so `config init` derives nothing and
# writes the placeholder template; XDG_CONFIG_HOME isolates where it lands.
( cd "$gtmp" && XDG_CONFIG_HOME="$gtmp/xdg" "$BIN" config init >/dev/null )
diff -u config.global.toml "$gtmp/xdg/furrow/config.toml"
echo "  config.global.toml matches config-init placeholder template"

echo "→ README EN/JA pin-parity guard"
sh scripts/check-readme-parity.sh

# The command table between the READMEs' commands:begin/end markers is
# GENERATED from the cobra tree (`furrow commands`, spliced by
# scripts/gen-command-table.sh). Hand-kept lists kept losing commands (the
# audit found four missing), so the block must equal a fresh run byte-for-byte
# — in BOTH files, since the block is deliberately identical EN/JA.
echo "→ README command-table drift guard"
ctmp="$(mktemp -d)"
"$BIN" commands > "$ctmp/want.md"
for f in README.md README.ja.md; do
  awk '/<!-- commands:begin/{f=1;next} /<!-- commands:end/{f=0} f' "$f" > "$ctmp/got.md"
  diff -u "$ctmp/want.md" "$ctmp/got.md"
done
echo "  command table matches the binary (README.md + README.ja.md; regen: scripts/gen-command-table.sh)"

echo "→ nix flake version ⇄ release-pin lockstep guard"
sh scripts/check-version-lockstep.sh

echo "→ smoke: init / add / ls --json / next / done / lint / board / upgrade / config init|path"
sb="$(mktemp -d)"
( cd "$sb"
  export XDG_CONFIG_HOME="$sb/xdg"   # isolate from the dev's real ~/.config/furrow
  "$BIN" init >/dev/null
  id="$("$BIN" --json add "smoke" -s ready | sed -n 's/.*"id": "\([^"]*\)".*/\1/p' | head -1)"
  "$BIN" ls --json | grep -q '"smoke"'
  "$BIN" next --json | grep -q '"smoke"'
  "$BIN" done "$id" >/dev/null
  "$BIN" lint
  # A fresh init must land WRITABLE under the strict write gate (it is the one
  # place Save may stamp meta.json), and upgrade must be a clean no-op on it.
  "$BIN" board --json | grep -q '"writable": true'
  "$BIN" upgrade --json | grep -q '"changed": false'
  "$BIN" config init >/dev/null
  "$BIN" config path | grep -q "furrow/config.toml"
)
echo "✓ all checks passed"
