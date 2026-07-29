#!/bin/sh
# check-readme-parity.sh — keep the README's load-bearing facts in lockstep with
# the code/CI that own them. There is no README.ja.md anymore (canonical docs are
# English-only; translations are not stored), so "parity" now means README ⇄ the
# source of truth, not README ⇄ README. Two pure-text-extraction guards, no
# git-tag or network dependency (deterministic):
#   1. the concrete sync-task-status.yml@vN.N.N pin the README teaches in its
#      `uses:` example must match the `furrow-version` default of the reusable
#      itself (.github/workflows/sync-task-status.yml) — the authoritative
#      "current release" pin, bumped right before tagging — so a reader never
#      copies a stale pin. NOT .github/workflows/task-status.yml: that file is a
#      fleet-synced COPY owned by akira-toriyama/.github's fleet/task-status.yml
#      (its header says so), and this guard used to compare against it, which
#      made release-prep bump a file furrow does not own — the next scheduled
#      fleet-sync then overwrote it back, reverting 3 of 4 caller-bump releases
#      (once for ~46h, t-2268). It catches up when the hub rollout lands;
#      release-prep must leave it alone.
#   2. every {"schema_version": N} literal in README.md and docs/*.md must equal
#      const SchemaVersion in internal/core/task.go, in the JSON literal AND in
#      "board layout vN" prose (both forms drifted to v3 against a v4 board once).
set -eu
cd "$(dirname "$0")/.."

# Extract the first concrete sync-task-status.yml@vN.N.N pin from a file. The
# vX.Y.Z placeholder used in prose has no digit after @v, so it is skipped.
pin_tag() {
  sed -n 's/.*sync-task-status\.yml@\(v[0-9][0-9.]*\).*/\1/p' "$1" | head -1
}

readme="$(pin_tag README.md)"

# The release pin the repo itself maintains: sync-task-status.yml's
# `furrow-version` default. The same anchored extraction as
# check-version-lockstep.sh, for the same reasons (only the 8-space input-default
# indent matches; the description prose, indented deeper, cannot).
release="$(sed -n 's/^        default:[[:space:]]*\(v[0-9][^[:space:]]*\).*/\1/p' \
  .github/workflows/sync-task-status.yml | head -1)"

if [ -z "$readme" ] || [ -z "$release" ]; then
  echo "✖ could not find a concrete release pin in both files:" >&2
  echo "  README.md sync-task-status.yml@vN.N.N:      ${readme:-<none>}" >&2
  echo "  sync-task-status.yml furrow-version default: ${release:-<none>}" >&2
  exit 1
fi

if [ "$readme" != "$release" ]; then
  echo "✖ README's sync-task-status pin does not match the release pin:" >&2
  echo "  README.md:                                   $readme" >&2
  echo "  sync-task-status.yml furrow-version default: $release" >&2
  echo >&2
  echo "The concrete pin in README's uses: example must match the furrow-version" >&2
  echo "default of the reusable itself — bump them together (release-prep) so a" >&2
  echo "reader never copies a stale pin. Do NOT bump .github/workflows/" >&2
  echo "task-status.yml to match: that file is a fleet-synced copy owned by the" >&2
  echo "hub canonical; it catches up when the hub rollout lands." >&2
  exit 1
fi

echo "ok — README pin matches the release pin ($readme)"

# The board's layout version: the README prints it as {"schema_version": N}, and
# it must equal the const the binary writes. This used to be claimed and NOT
# enforced, which is exactly how the README drifted to "board layout v3" against
# a v4 board. Pure text extraction — no binary, no network.
schema_lit() {
  sed -n 's/.*"schema_version"[: ]*\([0-9][0-9]*\).*/\1/p' "$1" | head -1
}
code="$(sed -n 's/^const SchemaVersion = \([0-9][0-9]*\).*/\1/p' internal/core/task.go | head -1)"
en_s="$(schema_lit README.md)"

if [ -z "$code" ] || [ -z "$en_s" ]; then
  echo "✖ could not find schema_version in both places:" >&2
  echo "  internal/core/task.go: ${code:-<none>}" >&2
  echo "  README.md:             ${en_s:-<none>}" >&2
  exit 1
fi

if [ "$en_s" != "$code" ]; then
  echo "✖ the documented board layout version does not match the code:" >&2
  echo "  internal/core/task.go: $code" >&2
  echo "  README.md:             $en_s" >&2
  echo >&2
  echo "Bumping core.SchemaVersion is a FLAG DAY — every board still on the old" >&2
  echo "layout goes read-only, including any CI pinned to an older release. Say so" >&2
  echo "in the README in the same change." >&2
  exit 1
fi

# The JSON literal is not the only place the version is written down: the README
# also says "board layout vN" in prose, and that is what actually rotted (it
# claimed v3 against a v4 board long after the bump). Assert every prose
# occurrence too — a version stated in two forms drifts in exactly one.
prose="$(grep -nE 'board layout v[0-9]+' README.md \
  | grep -vE "board layout v$code([^0-9]|$)" || true)"
if [ -n "$prose" ]; then
  echo "✖ README says a board layout version other than core.SchemaVersion ($code):" >&2
  echo "$prose" >&2
  echo >&2
  echo "The layout version is written down in prose as well as in the" >&2
  echo "{\"schema_version\": N} literal. Both must track the const." >&2
  exit 1
fi

echo "ok — README board layout matches core.SchemaVersion ($code), literal and prose"

# The docs/ tier writes the same literal (glossary's meta definition, non-goals'
# storage model, architecture's store diagram) but had no guard — which is
# exactly where the v5 bump rotted: the guarded README moved and the unguarded
# docs/ tier kept saying 4. So: EVERY {"schema_version": N} occurrence in every
# doc must equal the const. A historic version belongs in prose ("the 2026-07-13
# outage migrated 3 → 4"), never in the JSON-literal form. Unlike the README
# check above, a doc with zero occurrences is fine — reorganizing the docs is
# legitimate; contradicting the code is not.
bad=""
for f in README.md docs/*.md; do
  for v in $(sed -n 's/.*"schema_version"[: ]*\([0-9][0-9]*\).*/\1/p' "$f" | sort -u); do
    if [ "$v" != "$code" ]; then
      bad="$bad
  $f: {\"schema_version\": $v}"
    fi
  done
done
if [ -n "$bad" ]; then
  echo "✖ a doc writes a {\"schema_version\": N} literal other than core.SchemaVersion ($code):$bad" >&2
  echo >&2
  echo "Update the doc with the bump (or reword a historic mention as prose —" >&2
  echo "\"v4\" — instead of the JSON literal, which always means the CURRENT layout)." >&2
  exit 1
fi

echo "ok — docs/ tier schema_version literals match core.SchemaVersion ($code)"
