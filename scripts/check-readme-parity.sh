#!/bin/sh
# check-readme-parity.sh — guard the EN/JA README pin-parity invariant. The two
# READMEs are intentionally NOT structural mirrors (JA is a deliberate superset);
# only the shared, load-bearing FACTS must stay in lockstep. The most rot-prone
# is the concrete workflow-pin tag both docs teach in their `uses:` example
# (sync-task-status.yml@vX.Y.Z) — if one is bumped and the other is not, a reader
# copies a stale pin. This asserts README.md's pin tag == README.ja.md's, with no
# git-tag or network dependency (deterministic — pure text extraction).
set -eu
cd "$(dirname "$0")/.."

# Extract the first sync-task-status.yml@vX.Y.Z tag from a README.
pin_tag() {
  sed -n 's/.*sync-task-status\.yml@\(v[0-9][0-9.]*\).*/\1/p' "$1" | head -1
}

en="$(pin_tag README.md)"
ja="$(pin_tag README.ja.md)"

if [ -z "$en" ] || [ -z "$ja" ]; then
  echo "✖ could not find a sync-task-status.yml@vX.Y.Z pin tag in both READMEs:" >&2
  echo "  README.md:    ${en:-<none>}" >&2
  echo "  README.ja.md: ${ja:-<none>}" >&2
  exit 1
fi

if [ "$en" != "$ja" ]; then
  echo "✖ README workflow-pin tags disagree:" >&2
  echo "  README.md:    $en" >&2
  echo "  README.ja.md: $ja" >&2
  echo >&2
  echo "The concrete sync-task-status.yml@vX.Y.Z example must be identical in both" >&2
  echo "READMEs — bump them together." >&2
  exit 1
fi

echo "ok — README pin tags match ($en)"

# The other load-bearing shared fact: the board's layout version. Both READMEs
# print it as {"schema_version": N}, and it must equal the const the binary
# writes. This used to be claimed and NOT enforced, which is exactly how both
# READMEs drifted to "board layout v3" against a v4 board. Pure text extraction
# — no binary, no network.
schema_lit() {
  sed -n 's/.*"schema_version"[: ]*\([0-9][0-9]*\).*/\1/p' "$1" | head -1
}
code="$(sed -n 's/^const SchemaVersion = \([0-9][0-9]*\).*/\1/p' internal/core/task.go | head -1)"
en_s="$(schema_lit README.md)"
ja_s="$(schema_lit README.ja.md)"

if [ -z "$code" ] || [ -z "$en_s" ] || [ -z "$ja_s" ]; then
  echo "✖ could not find schema_version in all three places:" >&2
  echo "  internal/core/task.go: ${code:-<none>}" >&2
  echo "  README.md:             ${en_s:-<none>}" >&2
  echo "  README.ja.md:          ${ja_s:-<none>}" >&2
  exit 1
fi

if [ "$en_s" != "$code" ] || [ "$ja_s" != "$code" ]; then
  echo "✖ the documented board layout version does not match the code:" >&2
  echo "  internal/core/task.go: $code" >&2
  echo "  README.md:             $en_s" >&2
  echo "  README.ja.md:          $ja_s" >&2
  echo >&2
  echo "Bumping core.SchemaVersion is a FLAG DAY — every board still on the old" >&2
  echo "layout goes read-only, including any CI pinned to an older release. Say so" >&2
  echo "in both READMEs in the same change." >&2
  exit 1
fi

# The JSON literal is not the only place the version is written down: both
# READMEs also say "board layout vN" in prose, and that is what actually rotted
# (they still claimed v3 against a v4 board long after the bump). Assert every
# prose occurrence too — a version stated in two forms drifts in exactly one.
prose="$(grep -nE '(board layout|レイアウト版) v[0-9]+' README.md README.ja.md \
  | grep -vE "(board layout|レイアウト版) v$code([^0-9]|$)" || true)"
if [ -n "$prose" ]; then
  echo "✖ a README says a board layout version other than core.SchemaVersion ($code):" >&2
  echo "$prose" >&2
  echo >&2
  echo "The layout version is written down in prose as well as in the" >&2
  echo "{\"schema_version\": N} literal. Both must track the const." >&2
  exit 1
fi

echo "ok — README board layout matches core.SchemaVersion ($code), literal and prose"
