#!/bin/sh
# check.sh — the full local verification, runnable by you or by Claude Code with
# no TTY. Mirrors what .github/workflows/build.yml enforces in CI, so a green
# run here means a green CI. Use GOTOOLCHAIN=local on a Go 1.23 host.
set -eu
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local

echo "→ marshaller single-path guard"
sh scripts/check-marshal-singlepath.sh

echo "→ go build"
go build ./...

echo "→ go vet"
go vet ./...

echo "→ go test (core + store + app + cli + migrate + TUI incl. teatest e2e)"
go test ./...

if command -v golangci-lint >/dev/null 2>&1; then
  echo "→ golangci-lint"
  golangci-lint run ./...
else
  echo "→ golangci-lint (skipped — not installed; CI runs it)"
fi

echo "→ build binary for live checks"
go build -o bin/furrow ./cmd/furrow
BIN="$(pwd)/bin/furrow"

echo "→ schema drift guard"
"$BIN" schema | diff -u docs/schema/furrow.index.v1.json - >/dev/null \
  && echo "  schema matches docs/schema/furrow.index.v1.json"

echo "→ config template drift guard"
tmp="$(mktemp -d)"
( cd "$tmp" && "$BIN" init >/dev/null )
diff -u config.toml "$tmp/.furrow/config.toml" >/dev/null && echo "  config.toml matches init template"

echo "→ smoke: init / add / ls --json / next / done / lint"
sb="$(mktemp -d)"
( cd "$sb"
  "$BIN" init >/dev/null
  id="$("$BIN" --json add "smoke" -s ready | sed -n 's/.*"id": "\([^"]*\)".*/\1/p' | head -1)"
  "$BIN" ls --json | grep -q '"smoke"'
  "$BIN" next --json | grep -q '"smoke"'
  "$BIN" done "$id" >/dev/null
  "$BIN" lint
)
echo "✓ all checks passed"
