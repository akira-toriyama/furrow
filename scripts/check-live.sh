#!/bin/sh
# check-live.sh — every guard that interrogates a BUILT furrow binary: the four
# schema drift diffs, the two config-template drift diffs, README parity, the
# generated command table, the docs command-list and vocabulary guards, and the
# CLI smoke. ONE list, called by BOTH scripts/check.sh and CI (build.yml), so
# the two can never disagree about what "the guard set" is — they used to be
# hand-copied twins, and the copies had already drifted twice: CI diffed only 3
# of the 4 schemas (epic drift shipped green), and CI's smoke had lost the
# next/done leg and the two 2026-07-13 invariants (fresh init lands writable;
# upgrade is a no-op on it).
#
# Usage: FURROW_BIN=/path/to/furrow sh scripts/check-live.sh
set -eu
cd "$(dirname "$0")/.."

BIN="${FURROW_BIN:?set FURROW_BIN to the built furrow binary}"

# NB: run each `diff` as a bare command (not `diff … && echo`). Under `set -e`,
# a command on the LEFT of `&&` is exempt from errexit, so `diff … && echo`
# would SWALLOW a real drift and exit 0. A standalone `diff` aborts on drift
# and prints the offending diff; the confirmation echo only runs when it
# matched.
echo "→ schema drift guard"
"$BIN" schema task | diff -u docs/schema/furrow.task.v2.json -
echo "  task schema matches docs/schema/furrow.task.v2.json"
"$BIN" schema meta | diff -u docs/schema/furrow.meta.v2.json -
echo "  meta schema matches docs/schema/furrow.meta.v2.json"
"$BIN" schema repo | diff -u docs/schema/furrow.repo.v1.json -
echo "  repo schema matches docs/schema/furrow.repo.v1.json"
"$BIN" schema epic | diff -u docs/schema/furrow.epic.v2.json -
echo "  epic schema matches docs/schema/furrow.epic.v2.json"

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

echo "→ README pin + schema-version guard"
sh scripts/check-readme-parity.sh

# The command table between the README's commands:begin/end markers is
# GENERATED from the cobra tree (`furrow commands`, spliced by
# scripts/gen-command-table.sh). Hand-kept lists kept losing commands (the
# audit found four missing), so the block must equal a fresh run byte-for-byte.
echo "→ README command-table drift guard"
ctmp="$(mktemp -d)"
"$BIN" commands > "$ctmp/want.md"
awk '/<!-- commands:begin/{f=1;next} /<!-- commands:end/{f=0} f' README.md > "$ctmp/got.md"
diff -u "$ctmp/want.md" "$ctmp/got.md"
echo "  command table matches the binary (README.md; regen: scripts/gen-command-table.sh)"

# docs/architecture.md keeps its OWN hand-written command list (not the generated
# table), which had no guard and silently lost commands. Assert it names every
# top-level command the binary registers.
echo "→ docs command-list drift guard"
FURROW_BIN="$BIN" sh scripts/check-docs-commands.sh

# The generalization of the guard above, over every closed vocabulary rather than
# just the commands: each claimed prose region must name every member the owning
# registry has (`complete`), or — for a region that lists examples only — must
# name none that has ceased to exist (`subset`). It self-tests first: the first
# version printed a real failure and then exited 0, so it now plants drift in a
# fixture, in both directions, and requires itself to catch that before believing
# a green run on the real docs.
echo "→ docs vocabulary drift guard"
FURROW_BIN="$BIN" sh scripts/check-docs-vocab.sh

echo "→ smoke: version / init / add / ls --json / next / done / lint / board / upgrade / config init|path"
"$BIN" version >/dev/null
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
echo "ok — live checks passed (schemas, templates, docs, smoke)"
