#!/bin/sh
# check.sh — the full local verification, runnable by you or by Claude Code with
# no TTY. Mirrors what .github/workflows/{build,govulncheck}.yml enforce in CI, so
# a green run here means a green CI. Use GOTOOLCHAIN=local on a Go 1.25+ host.
set -eu
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local

echo "→ marshaller single-path guard"
sh scripts/check-marshal-singlepath.sh

echo "→ board-hook template syntax guard (POSIX sh -n)"
for h in scripts/board-hooks/post-merge scripts/board-hooks/post-rewrite scripts/board-hooks/pre-push; do
  sh -n "$h"
done
echo "  scripts/board-hooks/* parse clean"

echo "→ go build"
go build ./...

echo "→ go vet"
go vet ./...

echo "→ go test -race (core + store + app + cli + migrate + TUI incl. teatest e2e)"
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

echo "→ smoke: init / add / ls --json / next / done / lint / config init|path"
sb="$(mktemp -d)"
( cd "$sb"
  export XDG_CONFIG_HOME="$sb/xdg"   # isolate from the dev's real ~/.config/furrow
  "$BIN" init >/dev/null
  id="$("$BIN" --json add "smoke" -s ready | sed -n 's/.*"id": "\([^"]*\)".*/\1/p' | head -1)"
  "$BIN" ls --json | grep -q '"smoke"'
  "$BIN" next --json | grep -q '"smoke"'
  "$BIN" done "$id" >/dev/null
  "$BIN" lint
  "$BIN" config init >/dev/null
  "$BIN" config path | grep -q "furrow/config.toml"
)
echo "✓ all checks passed"
