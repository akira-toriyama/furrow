#!/bin/sh
# check-marshal-singlepath.sh — guard the determinism invariant: core.Marshal is
# the ONLY place an *Index is serialized to JSON. Any other json.Marshal /
# json.NewEncoder on the index would bypass the canonical-sort + escape rules and
# reintroduce git churn (ROADMAP §6 / MEMO §3). CI runs this before the build.
set -eu
cd "$(dirname "$0")/.."

# Find json.Marshal / json.NewEncoder uses OUTSIDE the sanctioned spots:
#   - internal/core/marshal.go  (the one true path)
#   - internal/cli/output.go    (renders CLI views/errors, never the *Index)
#   - *_test.go                 (tests may encode freely)
hits="$(grep -rnE 'json\.(Marshal|NewEncoder)' --include='*.go' internal cmd \
  | grep -v '_test.go' \
  | grep -vE 'internal/core/marshal\.go' \
  | grep -vE 'internal/cli/output\.go' || true)"

if [ -n "$hits" ]; then
  echo "✖ json.Marshal/NewEncoder used outside the sanctioned paths:" >&2
  echo "$hits" >&2
  echo >&2
  echo "The index must only be serialized via internal/core.Marshal. If this is a" >&2
  echo "non-index view, render it in internal/cli/output.go; otherwise route it" >&2
  echo "through core.Marshal." >&2
  exit 1
fi
echo "ok — index serialization is single-path"
