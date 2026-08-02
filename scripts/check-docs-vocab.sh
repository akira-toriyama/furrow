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
# that enumerates one of those vocabularies, and a DIRECTION saying what the
# region promises:
#
#   complete — it lists the whole vocabulary, so every member must appear. This
#              is the check the five observed defects needed: all five were
#              omissions.
#   subset   — it gives EXAMPLES only (a list ending in "…"), so completeness is
#              NOT required — but every member it names must still EXIST.
#   both     — a complete list whose members are also shape-distinctive.
#
# NEITHER DIRECTION EVER FLAGS AN EXTRA TOKEN, and that restraint is the whole
# reason this guard survives: a region legitimately mentions things outside the
# vocabulary (prose, flags, other commands), so failing on unknown text would cry
# wolf on ordinary edits, and a guard that cries wolf gets deleted. `subset` is
# narrower than it sounds — it only asks whether a token that IS shaped like a
# member still exists in the registry.
#
# Why `subset` had to exist: with only `complete`, a partial list could not be
# claimed at all (it would report every unlisted member as missing), so
# `lint-codes` was unclaimed — and its own way of rotting was invisible in both
# directions. Rename or delete a code and the prose goes on naming one that is
# gone; `complete` looks only for absences and cannot see a stale presence.
#
# ADDING A CLAIM is a line in the claims table below — not a new script. If a
# doc's enumeration cannot be checked this way, the better fix is usually to stop
# enumerating: replace the list with a pointer at the registry, the way
# core.Problem's doc now does. An unlisted enumeration is a future finding.
#
# A vocabulary can still be legitimately unclaimed, but no longer for being
# partial — that is what `subset` is for. What genuinely cannot be claimed is a
# vocabulary of PLAIN WORDS in a partial list: the reverse direction needs a shape
# to tell a member from prose, and measuring `commands` (whose members are bare
# lowercase words) produced `list` as a candidate — an English word; the command
# is `ls`. So `commands` and `config-keys` are `complete`-only, and the shape
# field is mandatory for the other directions to make that structural rather than
# a note someone has to remember.
#
# VERIFY A NEW CLAIM BITES, because a green claim proves nothing on its own, in
# EITHER direction. For `complete`, redact one member from inside the region (on
# a copy) and re-run: the guard must fail and name it. For `subset`, rename a
# member the region names into something nonexistent: it must fail and name that.
# Do it for EVERY member, not one — measurement has caught three vacuous claims
# here that all looked green:
#   * `query-qualifiers` first ran to `^Deliberately not in v1`, swallowing the
#     `presence` and `computed flags` bullets, so 8 of its 20 members were
#     satisfied by neighbours rather than by the list they claim to guard.
#   * `revisit-summary-keys` looked like it had no candidate tokens at all,
#     because grep is line-oriented and that region's code span WRAPS.
#   * README's lint bullet appeared to name 7 nonexistent codes, because a bare
#     ``` in the prose left the line's backticks odd and paired prose with code.
# All 15 claims here were verified member by member in both directions (185
# forward, 30 reverse), plus a check that no `subset` claim fails when a member is
# merely absent — which is precisely what it must tolerate.
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

# check_claim <direction> <vocabulary> <shape> <file> <start-anchor> <end-anchor>
#
# Returns 0 when the region satisfies the claim, 1 otherwise (message on stderr).
# The DIRECTION says what the region promises:
#
#   complete — it enumerates the whole vocabulary, so every member must appear.
#              This is the original check. <shape> must be empty.
#   subset   — it only gives EXAMPLES (a list ending in "…"), so completeness
#              must NOT be required — but every member it does name must still
#              EXIST. <shape> is required.
#   both     — a complete enumeration whose members are also shape-distinctive.
#
# Why `subset` exists at all: the docs enumerate lint codes partially on purpose
# (33 of them; README and CLAUDE.md list 8 and 7, then "…" and a pointer at
# `--code`'s candidates). Requiring completeness there would report 25 false
# omissions on every run, and a guard that cries wolf gets deleted — so before
# this direction existed, `lint-codes` was simply unclaimed, and its OTHER way of
# rotting was invisible: rename or delete a code and the prose goes on naming one
# that no longer exists. `complete` cannot see that (it only looks for absences),
# which is why leaving partial lists unclaimed was the last hole in this guard.
check_claim() {
  cc_dir="$1" cc_vocab="$2" cc_shape="$3" cc_file="$4" cc_start="$5" cc_end="$6"

  # The direction/shape pairing is validated FIRST and strictly, because it is
  # what keeps a claim from being vacuous: see the shape discussion below.
  case "$cc_dir" in
  complete)
    if [ -n "$cc_shape" ]; then
      echo "✖ claim ($cc_vocab, $cc_file): a 'complete' claim must leave the shape empty" >&2
      return 1
    fi
    ;;
  subset | both)
    if [ -z "$cc_shape" ]; then
      echo "✖ claim ($cc_vocab, $cc_file): a '$cc_dir' claim needs a shape" >&2
      echo "  (the shape is what tells a member from ordinary prose — see the claims table)" >&2
      return 1
    fi
    ;;
  *)
    echo "✖ claim ($cc_vocab, $cc_file): unknown direction '$cc_dir'" >&2
    echo "  known: complete, subset, both" >&2
    return 1
    ;;
  esac

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

  cc_status=0

  # --- direction: every member present (complete / both) ---------------------
  if [ "$cc_dir" != subset ]; then
    cc_missing=""
    for cc_member in $cc_members; do
      # Standalone-token match, so `ref` is not satisfied by "referenced", and
      # the config key `lanes` is not satisfied by `[next].lanes` (a dot may not
      # precede a match). Members are [a-z_-] tokens straight from the
      # registries, so they carry no ERE metacharacters of their own.
      if ! printf '%s\n' "$cc_region" | grep -qE "(^|[^A-Za-z0-9_.-])$cc_member([^A-Za-z0-9_-]|$)"; then
        cc_missing="$cc_missing $cc_member"
      fi
    done
    if [ -n "$cc_missing" ]; then
      echo "✖ $cc_file enumerates the '$cc_vocab' vocabulary but is missing:$cc_missing" >&2
      echo "  region: /$cc_start/ .. /$cc_end/" >&2
      echo "  source: furrow vocab $cc_vocab" >&2
      cc_status=1
    fi
  fi

  # --- direction: no member named that no longer exists (subset / both) ------
  #
  # Candidates are the region's BACKTICKED tokens that match the vocabulary's
  # shape. Both halves of that are load-bearing, and both were settled by
  # measurement rather than taste:
  #
  #   * Backticks only. Dropping them makes ordinary prose a candidate: the very
  #     README bullet that lists lint codes also contains "kebab-case",
  #     "allow-list" and "deny-list", which match a kebab shape perfectly and are
  #     not codes. Identifiers are written in backticks here; prose is not.
  #   * Tokenized INSIDE the span, not span-per-token: CLAUDE.md documents the
  #     revisit summary as one span, `{dep_done:[ids], stale:[ids], …}`, so
  #     treating a span as a single token found nothing there and the claim was
  #     refused as vacuous. Splitting keeps `-` attached to the token on purpose —
  #     that is what leaves `--exclude-code` starting with a dash and therefore
  #     failing a `^[a-z]…` shape, instead of becoming a bogus `exclude-code`
  #     candidate in the very region that lists lint codes.
  #   * A shape, declared per claim. Measured across the real docs, a kebab shape
  #     (`-` required) over the lint-code regions yields 7 and 3 candidates with
  #     ZERO non-members, and a snake shape over the revisit regions 6 with zero
  #     — but `commands`, whose members are plain lowercase words, yields `list`
  #     as a false positive (the command is `ls`; "list" is just an English
  #     word). So this direction is only sound for a vocabulary whose members are
  #     shape-distinctive, and requiring the shape in the claim is what stops
  #     someone declaring it for one that is not.
  if [ "$cc_dir" != complete ]; then
    # Flatten the region to ONE line and drop runs of 2+ backticks first. Both
    # steps fix a way the naive extraction silently lied, and both were found by
    # running it:
    #   * grep is line-oriented, so a code span that WRAPS — CLAUDE.md documents
    #     the revisit summary as `{dep_done:[ids], stale:[ids], …}` across three
    #     lines — is never matched at all, and the claim was refused as vacuous
    #     while the region in fact named every key.
    #   * a run of 3 backticks is prose in this repo (README says markers inside
    #     a ``` fence are not flagged), which leaves the line's backtick count
    #     ODD, so every following span pairs prose-to-code: that is where
    #     `allow-list`, `clamp-don` and `t-reject` came from as "members that do
    #     not exist".
    cc_flat="$(printf '%s\n' "$cc_region" | tr '\n' ' ' | sed -E 's/`{2,}/ /g')"

    # After that normalization an odd count is a genuine unbalanced code span —
    # a rendering bug in the doc AND a silent false-positive machine here. Refuse
    # rather than report the prose it would swallow.
    cc_ticks="$(printf '%s' "$cc_flat" | tr -cd '`' | wc -c | tr -d ' ')"
    if [ $((cc_ticks % 2)) -ne 0 ]; then
      echo "✖ $cc_file: the region has an unbalanced code span ($cc_ticks backticks)" >&2
      echo "  region: /$cc_start/ .. /$cc_end/" >&2
      echo "  (fix the doc: an unclosed backtick pairs prose with code and would" >&2
      echo "   make ordinary words look like '$cc_vocab' members)" >&2
      return 1
    fi

    cc_tokens="$(printf '%s\n' "$cc_flat" | grep -oE '`[^`]+`' | tr -d '`' |
      tr -c 'A-Za-z0-9_-' '\n' | sort -u | grep -E -- "$cc_shape" || true)"

    # A claim that finds nothing to check is the silent-pass failure this script
    # exists to refuse — it would go green forever while guarding nothing.
    if [ -z "$cc_tokens" ]; then
      echo "✖ $cc_file: no backticked token matches the '$cc_vocab' shape — this claim checks nothing" >&2
      echo "  region: /$cc_start/ .. /$cc_end/  shape: $cc_shape" >&2
      echo "  (a source-file doc comment has no backticks, so it cannot carry a '$cc_dir' claim)" >&2
      return 1
    fi

    cc_dead=""
    for cc_token in $cc_tokens; do
      # `--` because a candidate may begin with a dash: without it, grep reads a
      # token like `--drafts` as its own option and prints a usage error while
      # reporting the token as unknown. Measured, not hypothetical.
      printf '%s\n' "$cc_members" | grep -qxF -- "$cc_token" || cc_dead="$cc_dead $cc_token"
    done
    if [ -n "$cc_dead" ]; then
      echo "✖ $cc_file names '$cc_vocab' members that do not exist:$cc_dead" >&2
      echo "  region: /$cc_start/ .. /$cc_end/" >&2
      echo "  (renamed or deleted? the registry is the truth: furrow vocab $cc_vocab)" >&2
      cc_status=1
    fi
  fi

  return "$cc_status"
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

  # For the subset direction the candidates must be BACKTICKED, and one fixture
  # names a member that does not exist (the rename/delete failure mode).
  st_snake='^[a-z]+(_[a-z]+)+$'
  st_shaped="$(printf '%s\n' "$st_members" | grep -E -- "$st_snake")"
  st_fixture "$(printf '%s\n' "$st_shaped" | sed 's/^/`/; s/$/`/')" "$st_dir/live.md"
  st_fixture "$(printf '%s\n' "$st_shaped" | sed 's/^/`/; s/$/`/'; echo '`no_such_signal`')" "$st_dir/dead.md"

  st_fail() {
    echo "✖ SELF-TEST: $1" >&2
    echo "  This guard cannot be trusted; a green run from it would mean nothing." >&2
    echo "  Fix scripts/check-docs-vocab.sh before believing it." >&2
    rm -rf "$st_dir"
    exit 1
  }

  # 1. A complete region must pass — a guard that cries wolf gets deleted.
  check_claim complete revisit-codes '' "$st_dir/complete.md" "$st_start" "$st_end" 2>"$st_dir/err" ||
    st_fail "a region naming every member was reported as drifting: $(cat "$st_dir/err")"

  # 2. A region missing one member must FAIL, and must say which. This is the
  #    subshell defect's permanent test: it printed exactly this failure, then
  #    exited 0.
  if check_claim complete revisit-codes '' "$st_dir/drifted.md" "$st_start" "$st_end" 2>"$st_dir/err"; then
    st_fail "a region missing '$st_first' was reported as clean"
  fi
  grep -q -- "$st_first" "$st_dir/err" ||
    st_fail "drift was detected but the message does not name '$st_first'"

  # 3. An end anchor that no longer matches must fail, not run to EOF and pass
  #    on the decoys down there.
  if check_claim complete revisit-codes '' "$st_dir/drifted.md" "$st_start" '^no line matches this$' 2>/dev/null; then
    st_fail "a claim whose end anchor never matches was reported as clean"
  fi

  # 4. A start anchor that no longer matches must fail, not check nothing.
  if check_claim complete revisit-codes '' "$st_dir/complete.md" '^no line matches this$' "$st_end" 2>/dev/null; then
    st_fail "a claim whose start anchor never matches was reported as clean"
  fi

  # 5. A claim naming a vocabulary that does not exist must fail, not check an
  #    empty member list and call it clean.
  if check_claim complete revisit-code '' "$st_dir/complete.md" "$st_start" "$st_end" 2>/dev/null; then
    st_fail "a claim naming an unknown vocabulary was reported as clean"
  fi

  # 6. And a claim pointing at a file that is not there.
  if check_claim complete revisit-codes '' "$st_dir/no-such-file.md" "$st_start" "$st_end" 2>/dev/null; then
    st_fail "a claim naming a missing file was reported as clean"
  fi

  # 7. subset: a region naming only LIVE members passes — even though it is
  #    missing the members that have no snake shape. Requiring completeness here
  #    is exactly the false alarm this direction exists to avoid.
  check_claim subset revisit-codes "$st_snake" "$st_dir/live.md" "$st_start" "$st_end" 2>"$st_dir/err" ||
    st_fail "a partial list of live members was reported as drifting: $(cat "$st_dir/err")"

  # 8. subset: a region naming a member that does NOT exist must fail and name
  #    it — the rename/delete rot that `complete` structurally cannot see.
  if check_claim subset revisit-codes "$st_snake" "$st_dir/dead.md" "$st_start" "$st_end" 2>"$st_dir/err"; then
    st_fail "a region naming the nonexistent member 'no_such_signal' was reported as clean"
  fi
  grep -q -- 'no_such_signal' "$st_dir/err" ||
    st_fail "a dead member was detected but the message does not name it"

  # 9. subset with nothing to check must FAIL, not go green forever. The complete
  #    fixture has no backticks, so no candidate matches the shape.
  if check_claim subset revisit-codes "$st_snake" "$st_dir/complete.md" "$st_start" "$st_end" 2>/dev/null; then
    st_fail "a subset claim with no candidate token was reported as clean"
  fi

  # 10. both: fails on a missing member AND on a dead one, so upgrading a claim
  #     never silently loses the completeness half.
  if check_claim both revisit-codes "$st_snake" "$st_dir/dead.md" "$st_start" "$st_end" 2>/dev/null; then
    st_fail "a 'both' claim passed on a region that is incomplete AND names a dead member"
  fi

  # 11. The direction/shape pairing is enforced, since it is what keeps someone
  #     from declaring `subset` for a vocabulary of plain words (measured: the
  #     `commands` region yields `list`, which is not a command).
  if check_claim subset revisit-codes '' "$st_dir/live.md" "$st_start" "$st_end" 2>/dev/null; then
    st_fail "a subset claim with an empty shape was accepted"
  fi
  if check_claim complete revisit-codes "$st_snake" "$st_dir/complete.md" "$st_start" "$st_end" 2>/dev/null; then
    st_fail "a complete claim carrying a shape was accepted"
  fi
  if check_claim sideways revisit-codes "$st_snake" "$st_dir/live.md" "$st_start" "$st_end" 2>/dev/null; then
    st_fail "an unknown direction was accepted"
  fi

  rm -rf "$st_dir"
  echo "  self-test: planted drift is detected and named (14 assertions, both directions)"
}

self_test

# claims: <direction>|<vocabulary>|<shape>|<file>|<start anchor>|<end anchor>
#
# The region is the start-anchor line through the line before the next
# end-anchor match (see region_of). Anchors and shape are extended regexes.
# Anchors are chosen to be structural (a heading, a bullet, a bolded lead-in)
# rather than prose, so rewording inside a region never moves them. A `|` cannot
# appear in any field — it is the separator.
#
# DIRECTION (see check_claim): `complete` = every member must appear (shape
# empty); `subset` = a list of EXAMPLES, so only "names nothing that no longer
# exists" is required (shape required); `both` = a complete enumeration whose
# members are shape-distinctive, checked either way.
#
# Which direction a region gets is NOT taste — it follows from what the prose
# promises, and the shape from measurement. Every `subset`/`both` shape below was
# run against its real region and yielded ZERO non-member candidates; the
# `commands` and `config-keys` regions are `complete`-only because their members
# are plain lowercase words, and measuring `commands` produced `list` (an English
# word; the command is `ls`) — proof that a shape-less vocabulary cannot carry
# this direction without crying wolf.
#
# CLAUDE.md is claimed first and most: it is the canon an agent reads INSTEAD of
# the code, so a vocabulary that drifts there misinforms every session. A claim
# may also name a SOURCE file — a doc comment that enumerates a vocabulary is the
# same defect with a shorter blast radius, and it is where the audit's worst
# instance lived: core.Problem's comment called itself "the closed vocabulary"
# while sitting three lint codes behind the registry one screen below it. Source
# files take `complete` only: doc comments carry no backticks, so there is no
# candidate token to check, and the guard refuses such a claim outright rather
# than passing on nothing.
status=0
checked=0
while IFS='|' read -r dir vocab shape file start end; do
  case "$dir" in '' | '#'*) continue ;; esac
  # NB: this loop body runs in THIS shell — the claims arrive by heredoc, not
  # through a pipe. That is what makes `status=1` survive to the exit below.
  check_claim "$dir" "$vocab" "$shape" "$file" "$start" "$end" || status=1
  checked=$((checked + 1))
done <<'CLAIMS'
complete|commands||CLAUDE.md|^- Canonical commands:|^  \*\*`furrow brief \[--json\]`
complete|error-kinds||README.md|^- \*\*`kind`\*\*|^- \*\*`retryable`\*\*
complete|sync-progress-keys||README.md|^.\{committed, pulled, pushed|^failure alike
complete|sync-progress-keys||docs/architecture.md|^  failure — carries|^  omitted when empty
complete|commands||docs/non-goals.md|^- \*\*Built and real today\*\*|^  Destructive ops are guarded
complete|config-keys||CLAUDE.md|^`furrow lint` surfaces\. Read it through `internal/config`|^user-level central-board config
complete|config-keys||docs/architecture.md|^Sections and their defaults:|^`status` is just a lane
complete|config-keys||README.md|^## Configuration|^A board `\[alias\]` names
both|revisit-codes|^[a-z]+(_[a-z]+)+$|CLAUDE.md|`revisit --json` a `revisit` array|^- \*\*Batch reads by id
both|revisit-codes|^[a-z]+(_[a-z]+)+$|README.md|^- \*\*`revisit`\*\* — read-only|^- \*\*`search`\*\*
complete|revisit-codes||internal/app/revisit.go|^// Revisit lists open tasks that may need a fresh judgment|^func \(a \*App\) Revisit\(
both|revisit-summary-keys|^[a-z]+(_[a-z]+)+$|CLAUDE.md|A successful sync also gains a `revisit` key|^- \*\*The board.s layout version gates writes
complete|revisit-summary-keys||internal/app/revisit.go|^// RevisitSummary tallies the loop-visible signals|^func \(a \*App\) RevisitSummary\(
complete|query-qualifiers||README.md|^- \*\*qualifiers\*\*|^- \*\*presence\*\*
complete|query-presence||README.md|^- \*\*presence\*\*|^- \*\*computed flags\*\*
complete|query-is||README.md|^- \*\*computed flags\*\*|^- \*\*free text\*\*
subset|lint-codes|^[a-z]+(-[a-z]+)+$|README.md|^- \*\*`lint`\*\* — every finding carries|^- \*\*`migrate`\*\*
subset|lint-codes|^[a-z]+(-[a-z]+)+$|CLAUDE.md|a stable kebab-case `code`|^  Mutations \(
# doctor-codes is registered in `furrow vocab` but deliberately UNCLAIMED: every
# doc region that names doctor codes (README's doctor bullet, CLAUDE.md's,
# glossary's) also names `doctor-unhealthy` — an ERROR KIND, kebab-shaped, not a
# doctor code — so a subset claim would flag it as nonexistent on every run, and
# the regions are partial so `complete` cannot apply. Cry-wolf kills guards;
# the registry + TestDoctorCodeRegistryCoversEmitted (both directions, single
# emission file) carry the freight instead.
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
