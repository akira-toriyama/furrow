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

[priority]
# Sparse integer step so reordering edits one field instead of renumbering.
step = 10
default = 100

[ids]
# Frozen id format: prefix + zero-padded counter (from .furrow/seq). Never reused.
prefix = "t-"
width = 4 # t-0042

[labels]
# When true, every task must carry at least one label — ` + "`furrow add`" + ` rejects a
# label-less task and ` + "`furrow lint`" + ` flags one. Handy when labels mean something
# mandatory (e.g. a central tracker where the label is the owning repo).
# required = false

[archive]
# Default window for ` + "`furrow archive --older-than`" + ` (days; done tasks only).
older_than_days = 30

[ui]
# bubbletea TUI. NO_COLOR is always respected regardless of this value.
theme = "auto" # auto | dark | light
`
