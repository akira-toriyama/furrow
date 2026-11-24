#!/bin/sh
# check-apply-step.sh — runs sync-task-status.yml's "Apply SetStatus-task
# directives" step script, byte for byte, the way the runner starts it
# (`bash -e`), against a BUILT furrow and a scratch board.
#
# The step and the binary ship in one release and meet only on a runner: the
# step shipped for four months ending at `furrow apply`'s first non-zero exit,
# and a reading of the YAML had declared that fixed (t-264j). So this holds the
# two outcomes the step must tell apart, neither of which the exit code names
# (1 and 2 mean both):
#   - a report with a failed directive → the step succeeds and hands the code
#     and the report on, with the valid directive applied;
#   - no report at all → the step fails with apply's exit code.
#
# Usage: FURROW_BIN=/path/to/furrow sh scripts/check-apply-step.sh
set -eu
cd "$(dirname "$0")/.."

BIN="${FURROW_BIN:?set FURROW_BIN to the built furrow binary}"
wf=.github/workflows/sync-task-status.yml

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin" "$work/board" "$work/empty" "$work/rt"
case "$BIN" in
  /*) ;;
  *) BIN="$(pwd)/$BIN" ;;
esac
ln -s "$BIN" "$work/bin/furrow"

# The same isolation as check-live.sh's smoke: the user config could redirect
# the scratch board, and inside Claude Code the session guard would arm.
export XDG_CONFIG_HOME="$work/xdg"
unset CLAUDECODE

# The step's `run: |` block scalar: every line under it, down to the first
# non-blank line indented less than the block.
awk '
  /^      - name: Apply SetStatus-task directives$/ { step = 1; next }
  step && !run && /^      - / { exit }
  step && !run && /^        run: \|$/ { run = 1; next }
  run && !/^          / && !/^[[:space:]]*$/ { exit }
  run { sub(/^          /, ""); print }
' "$wf" > "$work/step.sh"
grep -q 'furrow apply' "$work/step.sh" || {
  echo "✖ could not extract the Apply step's run block from $wf — the step was renamed or reshaped; update the awk above" >&2
  exit 1
}

# $1 = directory to run in, $2 = PR body. Leaves the step's exit code in $rc,
# its GITHUB_OUTPUT in $work/out and its log in $work/log.
run_step() {
  : > "$work/out"
  rc=0
  ( cd "$1"
    PATH="$work/bin:$PATH" PR_BODY="$2" REF="guard#1" MODE=open OPEN_LANE=in-progress \
      RUNNER_TEMP="$work/rt" GITHUB_OUTPUT="$work/out" bash -e "$work/step.sh"
  ) > "$work/log" 2>&1 || rc=$?
}

die() {
  echo "✖ $1" >&2
  echo "--- step log ---" >&2
  cat "$work/log" >&2
  echo "--- GITHUB_OUTPUT ---" >&2
  cat "$work/out" >&2
  exit 1
}

( cd "$work/board"
  "$BIN" init >/dev/null
  "$BIN" --json add "apply-step guard" -s backlog > "$work/add.json"
)
id="$(sed -n 's/.*"id": "\([^"]*\)".*/\1/p' "$work/add.json" | head -1)"
[ -n "$id" ] || { echo "✖ could not read the scratch task's id" >&2; cat "$work/add.json" >&2; exit 1; }

run_step "$work/board" "SetStatus-task: $id in-progress
SetStatus-task: t-nosuchtask done"
[ "$rc" -eq 0 ] || die "a failed directive ended the Apply step with exit $rc — it must succeed so the publish and comment steps run"
grep -qx 'code=1' "$work/out" || die "the Apply step did not hand on code=1 for an unknown task id"
grep -q '"id": "t-nosuchtask"' "$work/out" || die "the Apply step's report output does not carry the failed directive"
grep -qF 'guard#1' "$work/board/.furrow/bodies/$id.md" || die "the valid directive beside a failed one was not applied"

run_step "$work/empty" "SetStatus-task: $id in-progress"
[ "$rc" -ne 0 ] || die "furrow apply failed without a report (no board here) and the Apply step still succeeded"

echo "  the Apply step keeps a failed directive non-blocking and fails on a report-less apply"
