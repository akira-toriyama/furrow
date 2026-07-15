package config

// Template is the canonical .furrow/config.toml shipped by `furrow init` and
// mirrored at the repo root as config.toml. furrow only ever READS this file;
// unknown keys and out-of-range values are clamped to the defaults above with a
// warning, so a typo can never break the tool.
//
// Keep this in sync with the repo-root config.toml (a from-source reference).
const Template = `# furrow — repo-local configuration (.furrow/config.toml).
# https://github.com/akira-toriyama/furrow
#
# furrow only READS this file; it never writes or regenerates it. Unknown keys
# and out-of-range values are silently clamped to safe defaults (a typo can't
# break the tool); ` + "`furrow lint`" + ` reports what it clamped.

[lanes]
# The status enum AND the top->bottom sort rank. Mirrors GitHub Projects #5.
# Editing this list is how you rename or reorder statuses. A task whose status
# is not in this list sorts last and is flagged by ` + "`furrow lint`" + `.
# "waiting" is the GTD Waiting-For lane (delegated / blocked on someone else).
order = ["inbox", "backlog", "ready", "in-progress", "waiting", "done", "icebox"]
# Lane assigned by ` + "`furrow add`" + ` when --status is omitted.
default = "inbox"
# Lane ` + "`furrow done`" + ` moves a task into (and where Closed is stamped).
done = "done"
# Lanes whose tasks are NOT actionable for ` + "`furrow next`" + ` (done, parked,
# and waiting-for — the last two are parked, so they never stamp Closed).
terminal = ["done", "icebox", "waiting"]

[next]
# Lanes ` + "`furrow next`" + ` considers "ready to work" (besides the deps-done
# check). Intake/planning lanes are excluded so next stays focused. Set to all
# non-terminal lanes if you want next to show everything actionable.
lanes = ["ready", "in-progress"]

[types]
# Work-item type vocabulary (a closed set like [lanes].order). "task" is an
# ordinary item; "epic" is a container — a box that groups child tasks (via
# ` + "`furrow parent`" + `) and is itself skipped by ` + "`furrow next`" + ` (surface boxes with
# ` + "`furrow next --containers`" + `). An unknown --type is rejected with the configured
# types as candidates, exactly like an unknown lane.
order = ["task", "epic"]
# Type a task gets when --type is omitted. MUST NOT be a container, or every
# type-less task would disappear from next (furrow clamps it back if it is).
default = "task"
# Types that are containers (boxes): excluded from next; ` + "`furrow ls --tree`" + `
# shows their rolled-up child progress instead.
containers = ["epic"]

[priority]
# Sparse integer step so reordering edits one field instead of renumbering.
step = 10
default = 100

[ids]
# Random id format: prefix + a random Crockford-base32 suffix (e.g. t-k3m9p).
# Ids are frozen (never reused); randomness avoids collisions when several
# operators add tasks concurrently. width = number of random suffix chars.
prefix = "t-"
width = 5 # t-k3m9p

[labels]
# When true, every task must carry at least one label — ` + "`furrow add`" + ` rejects a
# label-less task and ` + "`furrow lint`" + ` flags one. Handy when labels mean something
# mandatory (e.g. a central tracker where the label is the owning repo).
# required = false

[archive]
# Default window for ` + "`furrow archive --older-than`" + ` (days; done tasks only).
older_than_days = 30

[lint]
# ` + "`furrow lint`" + ` warns when at least this many done tasks are older than
# [archive].older_than_days and ready to archive — a nudge to run ` + "`furrow archive`" + `.
# 0 (the default) disables the nudge.
# archive_done = 0
# Lint codes to suppress on every run (a permanently-dead check that keeps firing).
# An entry naming no real code only warns (clamp-don't-reject). Filtering drives the
# exit code, so ignoring the last error makes lint exit 0. CLI twins: ` + "`--code`" + ` /
# ` + "`--exclude-code`" + ` / ` + "`--severity error`" + ` (those reject an unknown code with exit 2).
# ignore_codes = ["reconcile-gap", "dep-mirrors-children"]

[revisit]
# Days a task may go without an update before ` + "`furrow revisit`" + ` flags it
# stale. 0 disables the stale signal (the other revisit signals still fire).
stale_days = 30

[review]
# Days a repo may go without a human ` + "`furrow review <repo>`" + ` before
# ` + "`furrow sync`" + ` nudges it as unreviewed (on the revisit line). 0 disables.
stale_after_days = 14

[ui]
# Display-theme preference for a furrow front-end (furrow itself is CLI-only; a
# TUI/GUI lives in a separate repo). NO_COLOR is always respected regardless.
theme = "auto" # auto | dark | light
`
