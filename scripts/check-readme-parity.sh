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
