#!/bin/sh
# check-readme-parity.sh — keep the README's load-bearing facts in lockstep with
# the code/CI that own them. There is no README.ja.md anymore (canonical docs are
# English-only; translations are not stored), so "parity" now means README ⇄ the
# source of truth, not README ⇄ README. Two pure-text-extraction guards, no
# git-tag or network dependency (deterministic):
#   1. the concrete sync-task-status.yml@vN.N.N pin the README teaches in its
#      `uses:` example must match flake.nix's `version` — the release pin, the
#      one version literal release-prep bumps (check-version-lockstep.sh holds
#      it to the tag at release time) — so a reader never copies a stale pin.
#      NOT the reusable's own `furrow-version` input: it has no literal any
#      more, it derives the version from the tag. And NOT
#      .github/workflows/task-status.yml: that file is a fleet-synced COPY
#      owned by akira-toriyama/.github's fleet/task-status.yml (its header says
#      so), and this guard used to compare against it, which made release-prep
#      bump a file furrow does not own — the next scheduled fleet-sync then
#      overwrote it back, reverting 3 of 4 caller-bump releases (once for ~46h,
#      t-2268). It catches up when the hub rollout lands; release-prep must
#      leave it alone.
#   2. every {"schema_version": N} literal in README.md, CLAUDE.md and docs/*.md
#      must equal const SchemaVersion in internal/core/task.go, in the JSON
#      literal AND in "board layout vN" prose (both forms drifted to v3 against a
#      v4 board once) — "board layout vN" always means the CURRENT layout; a
#      historic one is written "layout vN".
#   3. every Go path the docs teach (`go install …/cmd/furrow@latest`, the
#      module path) must carry go.mod's major: without it `go install @latest`
#      resolves the v1 module and installs v1.0.0 (757cfbe, #460).
set -eu
cd "$(dirname "$0")/.."

# Extract the first concrete sync-task-status.yml@vN.N.N pin from a file. The
# vX.Y.Z placeholder used in prose has no digit after @v, so it is skipped; a
# pre-release suffix is kept, so an -rc pin compares like-for-like.
pin_tag() {
  sed -n 's/.*sync-task-status\.yml@\(v[0-9][0-9A-Za-z.+-]*\).*/\1/p' "$1" | head -1
}

readme="$(pin_tag README.md)"

# The release pin: flake.nix's `version`, extracted exactly as
# check-version-lockstep.sh does (the sole `version = "X";` assignment).
flake_ver="$(sed -n 's/^[[:space:]]*version = "\([^"]*\)";.*/\1/p' flake.nix | head -1)"
release="${flake_ver:+v$flake_ver}"

if [ -z "$readme" ] || [ -z "$release" ]; then
  echo "✖ could not find a concrete release pin in both files:" >&2
  echo "  README.md sync-task-status.yml@vN.N.N: ${readme:-<none>}" >&2
  echo "  flake.nix version:                     ${flake_ver:-<none>}" >&2
  exit 1
fi

if [ "$readme" != "$release" ]; then
  echo "✖ README's sync-task-status pin does not match the release pin:" >&2
  echo "  README.md:         $readme" >&2
  echo "  flake.nix version: $flake_ver" >&2
  echo >&2
  echo "The concrete pin in README's uses: example must match flake.nix's version" >&2
  echo "— bump them together (release-prep) so a reader never copies a stale pin." >&2
  echo "Do NOT bump .github/workflows/task-status.yml to match: that file is a" >&2
  echo "fleet-synced copy owned by the hub canonical; it catches up when the hub" >&2
  echo "rollout lands." >&2
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
prose="$(grep -nE 'board layout v[0-9]+' README.md CLAUDE.md docs/*.md \
  | grep -vE "board layout v$code([^0-9]|$)" || true)"
if [ -n "$prose" ]; then
  echo "✖ a doc says a board layout version other than core.SchemaVersion ($code):" >&2
  echo "$prose" >&2
  echo >&2
  echo "The layout version is written down in prose as well as in the" >&2
  echo "{\"schema_version\": N} literal. Both must track the const; a historic" >&2
  echo "layout is written \"layout vN\", never \"board layout vN\"." >&2
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
# legitimate; contradicting the code is not. CLAUDE.md is in the loop: it is
# the one document every session reads in full, and it said 9 against a v10
# board (2026-09-15) — the README's v3-against-v4 all over again, in the file
# this guard was not reading.
bad=""
for f in README.md CLAUDE.md docs/*.md; do
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

# The module path's major. A doc that teaches `go install
# github.com/akira-toriyama/furrow/cmd/furrow@latest` (no /vN) installs the v1
# module — that shipped from v2 until #460 — and a stale /vN installs an old
# major. Every Go path the docs name (a non-URL github.com/akira-toriyama/furrow
# followed by /vN, /cmd/ or /internal/) must carry go.mod's.
module="$(sed -n 's/^module[[:space:]][[:space:]]*//p' go.mod | head -1)"
base="github.com/akira-toriyama/furrow"
case "$module" in
  "$base"/v[0-9]*) major="/${module##*/}" ;;
  "$base") major="" ;;
  *) echo "✖ go.mod's module is not $base[/vN]: $module" >&2; exit 1 ;;
esac
gobad=""
for f in README.md CLAUDE.md docs/*.md; do
  for p in $(grep -oE '(^|[^/:.A-Za-z0-9])github\.com/akira-toriyama/furrow(/v[0-9]+)?/(v[0-9]+|cmd|internal)[A-Za-z0-9_./@-]*' "$f" \
      | sed -E 's#^[^g]*(github\.com)#\1#' | sort -u); do
    case "$p" in
      "$base$major"/*|"$base$major") ;;
      *) gobad="$gobad
  $f: $p" ;;
    esac
  done
done
if [ -n "$gobad" ]; then
  echo "✖ a doc names a Go path that does not carry go.mod's major ($module):$gobad" >&2
  echo >&2
  echo "Write ${base}${major}/… — a path without the major makes go install @latest" >&2
  echo "resolve the v1 module, and a stale major installs an old release line." >&2
  exit 1
fi

echo "ok — docs' Go paths carry go.mod's major (${major:-v0/v1})"
