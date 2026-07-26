#!/bin/sh
# check-docs-vocab.sh — the prose-vs-code vocabulary drift guard.
#
# WHY THIS EXISTS. A 2026-07 audit of every comment and doc in this repo (six
# read-only passes, adversarially verified) produced one dominant defect class,
# five independent instances of the SAME failure: a closed vocabulary that lives
# in code was copied into prose, and then the prose was not updated.
#
#   core.Problem's doc — which calls ITSELF "The closed vocabulary" — was three
#   lint codes behind the registry it duplicated; docs/non-goals.md's "Built and
#   real today" list omitted brief/boards/doctor/ref; architecture.md's config
#   table omitted [types]/[review]/[lint]; README's annotated config example
#   omitted four more; and the revisit-signal list had lost the two schema-v5
#   container codes in three separate files.
#
# The pre-existing guard (check-docs-commands.sh) reads ONE section of ONE file,
# which is precisely why non-goals.md was free to lose the same four commands
# that guard exists to protect. Enumerations rot exactly where no guard looks.
#
# HOW IT WORKS. `furrow vocab <name>` prints a vocabulary straight from the
# registry that owns it (see internal/app/vocab.go — every entry delegates or
# reflects; none is a second hand-kept list). Each CLAIM below names a doc region
# that enumerates one of those vocabularies. For each claim, every member must
# appear somewhere in that region.
#
# DIRECTION IS DELIBERATE: we check for MISSING members only, never extra tokens.
# A doc region legitimately mentions things outside the vocabulary (prose, other
# commands, examples), so failing on extras would cry wolf on ordinary edits, and
# a guard that cries wolf gets deleted. Every one of the five observed defects
# was an omission, which is exactly what this catches.
#
# ADDING A CLAIM is a line in the claims table below — not a new script. If a
# doc's enumeration cannot be checked this way, the better fix is usually to stop
# enumerating: replace the list with a pointer at the registry, the way
# core.Problem's doc now does. An unlisted enumeration is a future finding.
#
# VERIFY A NEW CLAIM BITES, because a green claim proves nothing on its own: a
# region that overruns its enumeration can be satisfied by the neighbouring
# prose. Redact one member from inside the region (`sed`-edit a copy) and re-run
# — the guard must fail and name it. Do that for EVERY member, not one: when
# `query-qualifiers` first ran to `^Deliberately not in v1` it swallowed the
# `presence` and `computed flags` bullets, and 8 of its 20 members were then
# satisfied by those neighbours rather than by the qualifier list they claim to
# guard. All 11 claims here were verified this way, member by member.
#
# A vocabulary with NO claim is not an oversight either: README and CLAUDE.md
# enumerate lint codes only PARTIALLY, on purpose — the list ends in "…" and
# points at `--code`'s candidates. A partial list cannot be checked for omissions
# and must not be, so `lint-codes` is emitted by `furrow vocab` (for humans and
# agents) and claimed by nobody.
#
# WHY THERE IS A SELF-TEST. The first version of this script printed a real
# failure and then exited 0, because the `status=1` ran in a subshell on the
# right of a pipe. Its region extraction also interpolated each claim's anchor
# straight into a sed address, so the anchor
# `^`furrow lint` surfaces\. Read it through `internal/config`` closed the
# address early on its own slash: sed died and the region came back EMPTY, which
# reported all 13 config keys missing from a doc that lists every one. Both
# defects are silent in the direction that matters — a guard that says "ok" while
# a claim fails is worse than no guard, because it also retires the audit that
# would have caught the drift. So before trusting itself on the real docs, this
# script plants drift in a fixture and requires itself to catch it.
set -eu
cd "$(dirname "$0")/.."

# Same binary resolution as check-docs-commands.sh: check.sh passes the one it
# already built, and a bare run builds its own, so this is runnable standalone.
bin="${FURROW_BIN:-}"
if [ -z "$bin" ]; then
  GOTOOLCHAIN=local go build -o bin/furrow ./cmd/furrow
  bin="$(pwd)/bin/furrow"
fi

# region_of <file> <start-anchor> <end-anchor>
#
# Prints the doc region a claim is about: the line matching <start-anchor>
# through the line BEFORE the next line matching <end-anchor>. Start is
# INCLUSIVE, because a one-line enumeration (CLAUDE.md's `Canonical commands:`
# bullet) has no "between" to speak of; end is exclusive, so the anchor can be
# the next structural line without dragging it into the region.
#
# The anchors are EREs from the claims table — i.e. data — and every layer that
# would re-parse them is avoided on purpose:
#   * they reach `grep -E` as an ARGUMENT, so there is no delimiter for them to
#     close early (a sed `/…/,/…/p` address died on the slash in
#     `internal/config`) and no surrounding program text to escape out of;
#   * the region is then cut with NUMERIC sed addresses, which the anchors
#     cannot influence at all;
#   * `awk -v` was rejected for the same family of reasons: it applies
#     escape-sequence processing to the assigned value, so a claim's `\.` or
#     `\*\*` would arrive mangled and quietly mean something else.
# Exit: 0 both anchors matched / 1 start never matched / 2 end never matched
# after the start.
region_of() {
  ro_file="$1" ro_start="$2" ro_end="$3"

  ro_from="$(grep -nE -- "$ro_start" "$ro_file" | head -1 | cut -d: -f1)"
  [ -n "$ro_from" ] || return 1

  # Search for the end anchor strictly AFTER the start line, so an anchor pair
  # that also matches earlier in the file cannot invert the range.
  ro_off="$(tail -n "+$((ro_from + 1))" "$ro_file" | grep -nE -- "$ro_end" | head -1 | cut -d: -f1)"
  [ -n "$ro_off" ] || return 2

  sed -n "${ro_from},$((ro_from + ro_off - 1))p" "$ro_file"
}

# check_claim <vocabulary> <file> <start-anchor> <end-anchor>
# Returns 0 when the region names every member, 1 otherwise (message on stderr).
check_claim() {
  cc_vocab="$1" cc_file="$2" cc_start="$3" cc_end="$4"

  if [ ! -f "$cc_file" ]; then
    echo "✖ claim names a missing file: $cc_file" >&2
    return 1
  fi

  cc_rc=0
  cc_region="$(region_of "$cc_file" "$cc_start" "$cc_end")" || cc_rc=$?
  case "$cc_rc" in
  1)
    echo "✖ $cc_file: start anchor never matches: $cc_start" >&2
    echo "  (a doc was restructured — update the claim in scripts/check-docs-vocab.sh)" >&2
    return 1
    ;;
  2)
    echo "✖ $cc_file: end anchor never matches after the start: $cc_end" >&2
    echo "  (a doc was restructured — update the claim in scripts/check-docs-vocab.sh)" >&2
    return 1
    ;;
  esac

  # Fetch the members through a checked assignment, NOT `for m in $(vocab …)`:
  # a command substitution in a for-list is not a simple command, so `set -e`
  # does not fire on it. A claim naming a mistyped vocabulary would print
  # furrow's exit-2 error, iterate an EMPTY list, find nothing missing, and pass
  # — the same lie as the subshell defect, arriving by a different door.
  cc_members="$("$bin" vocab "$cc_vocab" 2>/dev/null)" || {
    echo "✖ claim names an unknown vocabulary: $cc_vocab" >&2
    echo "  known: $("$bin" vocab | tr '\n' ' ')" >&2
    echo "  (register it in internal/app/vocab.go, or fix the claim's spelling)" >&2
    return 1
  }
  if [ -z "$cc_members" ]; then
    echo "✖ vocabulary '$cc_vocab' is empty — this claim would pass on any text" >&2
    return 1
  fi

  cc_missing=""
  for cc_member in $cc_members; do
    # Standalone-token match, so `ref` is not satisfied by "referenced", and the
    # config key `lanes` is not satisfied by `[next].lanes` (a dot may not
    # precede a match). Members are [a-z_-] tokens straight from the registries,
    # so they carry no ERE metacharacters of their own.
    if ! printf '%s\n' "$cc_region" | grep -qE "(^|[^A-Za-z0-9_.-])$cc_member([^A-Za-z0-9_-]|$)"; then
      cc_missing="$cc_missing $cc_member"
    fi
  done

  if [ -n "$cc_missing" ]; then
    echo "✖ $cc_file enumerates the '$cc_vocab' vocabulary but is missing:$cc_missing" >&2
    echo "  region: /$cc_start/ .. /$cc_end/" >&2
    echo "  source: furrow vocab $cc_vocab" >&2
    return 1
  fi
  return 0
}

# self_test — require the machinery to catch planted drift before it is trusted
# on the real docs. Any registered vocabulary works; revisit-codes is small. The
# fixture's anchors deliberately carry a slash and backticks, the two
# metacharacters that broke the sed-based first version, so that failure cannot
# come back quietly either.
self_test() {
  st_dir="$(mktemp -d)"
  st_start='^`internal/config` region begins'
  st_end='^region ends `a/b`'

  st_members="$("$bin" vocab revisit-codes)"
  if [ -z "$st_members" ]; then
    echo "✖ SELF-TEST: 'revisit-codes' is empty — nothing to plant drift in." >&2
    rm -rf "$st_dir"
    exit 1
  fi
  st_first="$(printf '%s\n' "$st_members" | head -1)"

  st_fixture() { # <members> <path>
    {
      echo 'prose before the region'
      echo '`internal/config` region begins'
      printf '%s\n' "$1"
      echo 'region ends `a/b`'
      printf '%s\n' "$st_members"   # decoys BELOW the end anchor: a region that
      echo 'prose after the region' # overruns its end must not pass on these
    } >"$2"
  }
  st_fixture "$st_members" "$st_dir/complete.md"
  st_fixture "$(printf '%s\n' "$st_members" | tail -n +2)" "$st_dir/drifted.md"

  st_fail() {
    echo "✖ SELF-TEST: $1" >&2
    echo "  This guard cannot be trusted; a green run from it would mean nothing." >&2
    echo "  Fix scripts/check-docs-vocab.sh before believing it." >&2
    rm -rf "$st_dir"
    exit 1
  }

  # 1. A complete region must pass — a guard that cries wolf gets deleted.
  check_claim revisit-codes "$st_dir/complete.md" "$st_start" "$st_end" 2>"$st_dir/err" ||
    st_fail "a region naming every member was reported as drifting: $(cat "$st_dir/err")"

  # 2. A region missing one member must FAIL, and must say which. This is the
  #    subshell defect's permanent test: it printed exactly this failure, then
  #    exited 0.
  if check_claim revisit-codes "$st_dir/drifted.md" "$st_start" "$st_end" 2>"$st_dir/err"; then
    st_fail "a region missing '$st_first' was reported as clean"
  fi
  grep -q -- "$st_first" "$st_dir/err" ||
    st_fail "drift was detected but the message does not name '$st_first'"

  # 3. An end anchor that no longer matches must fail, not run to EOF and pass
  #    on the decoys down there.
  if check_claim revisit-codes "$st_dir/drifted.md" "$st_start" '^no line matches this$' 2>/dev/null; then
    st_fail "a claim whose end anchor never matches was reported as clean"
  fi

  # 4. A start anchor that no longer matches must fail, not check nothing.
  if check_claim revisit-codes "$st_dir/complete.md" '^no line matches this$' "$st_end" 2>/dev/null; then
    st_fail "a claim whose start anchor never matches was reported as clean"
  fi

  # 5. A claim naming a vocabulary that does not exist must fail, not check an
  #    empty member list and call it clean.
  if check_claim revisit-code "$st_dir/complete.md" "$st_start" "$st_end" 2>/dev/null; then
    st_fail "a claim naming an unknown vocabulary was reported as clean"
  fi

  # 6. And a claim pointing at a file that is not there.
  if check_claim revisit-codes "$st_dir/no-such-file.md" "$st_start" "$st_end" 2>/dev/null; then
    st_fail "a claim naming a missing file was reported as clean"
  fi

  rm -rf "$st_dir"
  echo "  self-test: planted drift is detected and named (6 assertions)"
}

self_test

# claims: <vocabulary>|<file>|<start anchor>|<end anchor>
#
# The region is the start-anchor line through the line before the next
# end-anchor match (see region_of). Both are extended regexes. Anchors are
# chosen to be structural (a heading, a bullet, a bolded lead-in) rather than
# prose, so rewording inside a region never moves them. A `|` cannot appear in
# an anchor — it is the field separator.
#
# CLAUDE.md is claimed first and most: it is the canon an agent reads INSTEAD of
# the code, so a vocabulary that drifts there misinforms every session. A claim
# may also name a SOURCE file — a doc comment that enumerates a vocabulary is the
# same defect with a shorter blast radius, and it is where the audit's worst
# instance lived: core.Problem's comment called itself "the closed vocabulary"
# while sitting three lint codes behind the registry one screen below it.
status=0
checked=0
while IFS='|' read -r vocab file start end; do
  case "$vocab" in '' | '#'*) continue ;; esac
  # NB: this loop body runs in THIS shell — the claims arrive by heredoc, not
  # through a pipe. That is what makes `status=1` survive to the exit below.
  check_claim "$vocab" "$file" "$start" "$end" || status=1
  checked=$((checked + 1))
done <<'CLAIMS'
commands|CLAUDE.md|^- Canonical commands:|^  \*\*`furrow brief \[--json\]`
commands|docs/non-goals.md|^- \*\*Built and real today\*\*|^  Destructive ops are guarded
config-keys|CLAUDE.md|^`furrow lint` surfaces\. Read it through `internal/config`|^user-level central-board config
config-keys|docs/architecture.md|^Sections and their defaults:|^`status` is just a lane
config-keys|README.md|^## Configuration|^A board `\[alias\]` names
revisit-codes|CLAUDE.md|`revisit --json` a `revisit` array|^- \*\*Batch reads by id
revisit-codes|README.md|^- \*\*`revisit`\*\* — read-only|^- \*\*`search`\*\*
revisit-codes|internal/app/revisit.go|^// Revisit lists open tasks that may need a fresh judgment|^func \(a \*App\) Revisit\(
revisit-summary-keys|CLAUDE.md|A successful sync also gains a `revisit` key|^- \*\*The board.s layout version gates writes
revisit-summary-keys|internal/app/revisit.go|^// RevisitSummary tallies the loop-visible signals|^func \(a \*App\) RevisitSummary\(
query-qualifiers|README.md|^- \*\*qualifiers\*\*|^- \*\*presence\*\*
query-presence|README.md|^- \*\*presence\*\*|^- \*\*computed flags\*\*
query-is|README.md|^- \*\*computed flags\*\*|^- \*\*free text\*\*
CLAIMS

if [ "$status" -ne 0 ]; then
  echo >&2
  echo "A documented vocabulary enumeration disagrees with the code (see above)." >&2
  echo "Where members are missing: add them to the region — or, better where the" >&2
  echo "list is incidental, replace the enumeration with a pointer at the registry" >&2
  echo "that owns it. Where an anchor or a name no longer resolves: fix the claim" >&2
  echo "in scripts/check-docs-vocab.sh, since it is now guarding nothing." >&2
  exit 1
fi

echo "ok — $checked documented vocabulary enumerations match the code"
