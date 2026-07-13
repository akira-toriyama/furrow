#!/bin/sh
# check-marshal-singlepath.sh — guard the determinism invariant: core.Marshal is
# the ONLY place an *Index is serialized to JSON. Any other json.Marshal /
# json.NewEncoder on the index would bypass the canonical-sort + escape rules and
# reintroduce git churn. CI runs this before the build.
set -eu
cd "$(dirname "$0")/.."

# Find json.Marshal / json.NewEncoder / json.Unmarshal / json.NewDecoder uses
# OUTSIDE the sanctioned spots:
#   - internal/core/marshal.go      (the one true path)
#   - internal/core/passthrough.go  (its unknown-key half — same file family)
#   - internal/cli/output.go        (renders CLI views/errors, never the *Index)
#   - *_test.go                     (tests may encode freely)
#
# DECODERS are guarded too, and that is not symmetry for its own sake: a raw
# json.Unmarshal into a Task bypasses core.UnmarshalTask, so the shard's unknown
# keys are never parked — and the next write silently destroys a field a newer
# furrow put there (core/passthrough.go). A decoder that skips the single path is
# just as lossy as an encoder that does.
hits="$(grep -rnE 'json\.(Marshal|NewEncoder|Unmarshal|NewDecoder)' --include='*.go' internal cmd \
  | grep -v '_test.go' \
  | grep -vE 'internal/core/(marshal|passthrough)\.go' \
  | grep -vE 'internal/cli/output\.go' || true)"

if [ -n "$hits" ]; then
  echo "✖ encoding/json used outside the sanctioned paths:" >&2
  echo "$hits" >&2
  echo >&2
  echo "A Task/Index/Meta/RepoRecord must be serialized ONLY via core.Marshal*," >&2
  echo "and parsed ONLY via core.Unmarshal* — a raw json.Unmarshal drops the shard's" >&2
  echo "unknown keys, and the next write then destroys them (core/passthrough.go)." >&2
  echo "If this is a non-store view, render it in internal/cli/output.go." >&2
  exit 1
fi
echo "ok — store serialization AND parsing are single-path"
