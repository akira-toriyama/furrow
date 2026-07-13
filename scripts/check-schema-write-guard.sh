#!/bin/sh
# check-schema-write-guard.sh — guard the write-side schema invariant: an
# ordinary write must NEVER name the BINARY's schema version.
#
# The 2026-07-13 outage: fsstore.Save() stamped meta.json with
# core.SchemaVersion on every write, so one routine `furrow sync` from a source
# build migrated the SHARED tracker 3->4 and every furrow release the fleet's CI
# pinned lost the board at once. The fix is that a board's declared version is an
# INPUT to a write (core.CheckWritable / Store.BoardVersion), never an output —
# and only `furrow upgrade` may raise it.
#
# That is a one-line regression away, and it fails silently (the tests that would
# catch it all pass on a fresh store). So the const is grep-guarded: only the
# places that legitimately need to know what layout THIS binary writes may name
# it. CI runs this before the build, next to the marshaller guard.
set -eu
cd "$(dirname "$0")/.."

# Sanctioned references to core.SchemaVersion / the const's own definition:
#   - internal/core/*.go                   the const, the read gate, the write gate
#   - internal/store/fsstore/fsstore.go    the fresh-store stamp + SetBoardVersion
#   - internal/store/memstore/memstore.go  its in-memory twin
#   - internal/app/upgrade.go              the ONE deliberate raiser
#   - internal/app/schema_state.go         folds the board's stores into one state
#                                          (REPORTING only — it never writes)
#   - internal/app/board.go                reports board-vs-binary (never writes)
#   - internal/app/lint.go                 warns when they diverge (never writes)
#   - internal/cli/cmd_board.go            renders that report
#   - internal/schema/schema.go            emits the JSON Schema's const
#   - *_test.go                            tests may say it freely
hits="$(grep -rnE '(core\.)?SchemaVersion' --include='*.go' internal cmd \
  | grep -v '_test.go' \
  | grep -vE '^internal/core/' \
  | grep -vE '^internal/store/fsstore/fsstore\.go' \
  | grep -vE '^internal/store/memstore/memstore\.go' \
  | grep -vE '^internal/app/(upgrade|schema_state|board|lint)\.go' \
  | grep -vE '^internal/cli/cmd_board\.go' \
  | grep -vE '^internal/schema/schema\.go' || true)"

if [ -n "$hits" ]; then
  echo "✖ SchemaVersion referenced outside the sanctioned paths:" >&2
  echo "$hits" >&2
  echo >&2
  echo "An ordinary write must never name the binary's schema version — stamping a" >&2
  echo "board with it is the 2026-07-13 outage (a routine sync silently migrated the" >&2
  echo "shared board and locked out every pinned CI). Read the board's own version" >&2
  echo "via Store.BoardVersion() and gate the write with core.CheckWritable()." >&2
  echo "Raising a board is furrow upgrade's job alone (internal/app/upgrade.go)." >&2
  exit 1
fi
echo "ok — no ordinary write names the binary's schema version"
