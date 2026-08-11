# CLAUDE.md

Guidance for working in this repository (and for furrow itself as a tool).

## For Claude Code — integration contract (read first)

furrow's own tasks live on the **central board** (the private
`akira-toriyama/projects` repo) — this repo deliberately has **no local
`.furrow/`**, so `furrow` commands run here resolve to the central board via
the user-level config. When you work with any furrow store:

- Canonical commands: `furrow add|ls|show|next|brief|revisit|search|stats|board|boards|doctor|edit|note|attach|done|move|set|reorder|retitle|value|effort|check|dep|epic|label|repo|ref|review|sync|apply|archive|upgrade|lint|config|init|migrate|schema|version`.
  **`furrow brief [--json]` is the session-start read**: the sync → `next -r` →
  `show <id>` ritual in ONE process — the **due** band FIRST (`due`,
  `{overdue, today}`, longest-overdue first, omitted when nothing has arrived: a
  date is the only thing on the board that EXPIRES, everything else waits for
  you; board-wide as to epics, since promised work is usually parked outside the
  focus, and lane-filter-free, though it obeys the repo scope), the epic header (`active`, the open+active
  epic(s) `next` scopes to; `pinned`, the open pinned channel(s) whose tasks
  lead `next` regardless of the active scope — only channels with an open
  member are listed, the quiet rest collapsing to a `pinned_quiet` count; and `epics_declared`, which tells
  a non-participating board apart from "nothing active, so `next` is
  deliberately empty — except the pinned band"), the top
  `-n` (default 3) actionable tasks
  WITH `body_text`, `next_total` (the uncapped count — a cap never hides the
  queue), `blocked` (next-lane tasks with an unsatisfied dep + `blocked_by`,
  the in-flight work plain `next` hides), the `revisit` summary (sync's shape),
  the `drafts` count, and sync's `lint` error-count ride-along (omitted when
  clean). Read-only, never touches git — orient a shared board
  with `furrow sync && furrow brief`.
  `set <id>` combines lane/**priority**/value/effort/labels/**epic**/**due** in one
  write (the triage shortcut for move+reorder+value+effort+label): `--priority`
  is absolute, `--before/--after <ref>` relative in the DESTINATION lane — a
  cross-column drop (`-s <lane> --before <ref>`) is lane + position in ONE
  write, with the same respace/`renumbered` contract as reorder (the three
  position flags are mutually exclusive), and it takes SEVERAL ids — `set <id>...`
  applies the same edits to all of them in one all-or-nothing write (bulk
  triage), refusing the three position flags for ≥2 ids since a position places
  one task; **`--due` is also the snooze** (`set <id> --due +1d`, measured from
  NOW so pushing an overdue task always lands ahead) and `--clear-due` drops the
  date — an empty `--due ''` is exit 2, never a silent clear; `dep <id> <dep-id>...` is variadic
  (add/remove several in one write), and `dep <id> --list` is the read-only
  reverse-deps view — both directions (`depends_on` / `blocks`) resolved to
  id+title+lane, one `--json` object — so "what waits on this?" is a command,
  not a full-board dump; `done <id>...` / `move <id>... <lane>` accept several
  ids in ONE all-or-nothing index write (the write-side twin of `show <id>...`:
  a miss closes/moves NOTHING, exit 1 with every miss in `details.missing`;
  `--json` is ALWAYS an array of `{before,after,changed}` envelopes, one per
  id — a single id is a one-element array — and `--ndjson` one envelope per
  line. **Always-array is a design invariant, not an accident**: a command
  whose `Use` says `<id>...` has array cardinality by SIGNATURE (`show`,
  `done`, `move`, `set`), a command operating on exactly one id emits an
  object (`note`, `retitle`, …) — readable statically from `--help`, never
  from the runtime argv length. It deliberately overturned the 2026-07-16
  "one id keeps the classic object" arity convention, which made
  `... --json | jq '.[].id'` silently walk an object's VALUES whenever a
  caller's id list happened to resolve to one);
  `archive <id>...` retires specific done
  tasks by id (vs the age sweep). The READMEs' command table is **generated**
  from this very cobra tree (hidden `furrow commands`, spliced by
  `scripts/gen-command-table.sh`, drift-checked by check.sh/CI): to change a
  command's one-liner or flags, edit `Short`/flag definitions in
  `internal/cli`, rerun the script, and commit both — never hand-edit between
  the READMEs' `commands:begin/end` markers.
- **`ls --tree [<epic>]` groups the rows by epic** — one group per box (plus the
  unfiled group, `epic: null`), or just `<epic>`'s group. It answers "how does the
  matched work distribute across the boxes?", which was otherwise a full-board
  dump and a script. Every filter still applies, and the grouping is built over
  what MATCHED (so `--tree` can't show fewer tasks than the same flags without
  it); `-n` caps the number of GROUPS, not tasks. Each group carries the box's
  `active` flag, its member roll-up `progress` (`{done,total}`), and `stuck`
  (open members but none actionable — org-mode's stuck-project); each task row
  still marks **actionable** (exactly what `furrow next` would hand you; shown as
  ★). `--json` is an array of `{epic, active, progress, stuck, tasks}` groups;
  `--ndjson` streams one whole group per line. A task's box is set at `add -e`
  (a bare `add` INHERITS the scope's single active epic when there is exactly
  one — disclosed on stderr; `-e ''` stays unfiled on purpose, and zero or
  several actives inherit nothing), or later with `set <id> -e <epic>` —
  membership is the `epic` field, 0..1 per
  task, and `lint` errors `epic-required` on an open task filed under no box
  once the board has any.
- **An epic can WAIT ON other epics (schema v7): `furrow epic dep <id>
  <dep-id>...` / `--rm` / `--list`** — the task `dep` contract carried to boxes
  (variadic, all-or-nothing, acyclic on the write path, `--list` = both
  directions resolved to id+title+state). The edge is the order boxes OPEN in,
  and it is INFORMATION, not enforcement — deliberately weaker than a task dep:
  `epic activate` on a box with an open dep WARNS and proceeds (stderr note +
  `open_deps` in the `--json` envelope), a dep on a CLOSED epic is simply
  satisfied, and parallel branches are just two epics sharing a dep. `epic
  ls`/`epic show`/`brief` surface the waiting state (`open_deps` / a `deps:`
  line / ⏳ waits); `revisit` and `sync` raise **`epic_dep_done`** when every
  dep of a PARKED box is closed — that box's turn to open (furrow still never
  chooses: activating it stays the human's call). `lint` backstops what merges
  let through: `epic-dep-cycle` (error), `epic-dep-missing` (error),
  `epic-dep-open` (warn — an ACTIVE box still waiting on an open one).
- **`standing` / `pinned` are the v7 permanent-channel declarations (`epic set
  --standing / --pinned`; `--standing=false` / `--pinned=false` clears).** A
  STANDING box is one whose goal is to sit there (a mandate inbox, a parking
  lot): it is exempt from the finish-shaped revisit nags — `epic_all_done`
  (open 0 is its healthy state) and `epic_dep_done` (an always-on box has no
  "turn to open") — while `epic_stuck` still fires, and it is the box the
  REVIEW CADENCE exists for (v9): `furrow review <epic-ref>` stamps its
  `reviewed`, and `epic_review_due` fires when that review outlives the
  `[review]` staleness threshold — the same clock as the repo nudge, quiet
  until the first review opts the box in. A PINNED box's actionable
  tasks LEAD `next`/`brief` regardless of the active scope (shown even when
  nothing is active), without holding a repo slot — the read's own repo filter
  scopes the tasks. Orthogonal to each other and to activate: mandate =
  standing+pinned, parking lot = standing only, and a box can be pinned AND
  active (it shows once, in the pinned band). furrow never binds NAMES to
  these flags — which boxes deserve them is the operator's convention
  (projects' reserved-epics.md).
- **An epic is its OWN entity, not a task type (schema v6 replaced the v5
  `type`/`parent` world — `Task.Type`, `Task.Parent`, `[types]`, `furrow
  parent`, `next --containers`, and the `parent-cycle`/`parent-done`/
  `unknown-type`/`dep-mirrors-children` lint codes are all GONE).** A box lives
  in `.furrow/epics/<e-id>.json` (id prefix `[ids].epic_prefix`, default `e-`)
  with title / `goal` (the one-line closing condition, optional) / `active` /
  labels / repos / `meta` (a flat string map furrow never interprets) /
  open-closed state — no lanes, no value/effort/priority. Its prose shares the
  task `bodies/` directory (`note`/`edit --help` explain the routing). A box is not work:
  `furrow next` hands out only tasks, an epic's "progress" is the member
  roll-up, and furrow never auto-closes a box (`revisit`/`sync` raise
  `epic_all_done` instead). `epic done`/`deactivate` SUGGEST the previous
  active box (open, currently inactive, newest activation record — computed
  fresh from the body's activation log, no stored pointer; one `previous:`
  stdout line + a `previous` key in `--json`, null = unknown) and never
  activate it — choosing stays the human's. Epic refs resolve as exact id, unique id prefix, or
  unique title substring (a miss/ambiguity is exit 2 with `candidates` — kinds
  `epic-not-found`/`epic-ambiguous`).
- **A task can be PROMISED for an instant: the `due` stamp (schema v8).**
  `add --due` / `set --due` bind it; `set --clear-due` drops it. Four spellings —
  `2026-08-04` (the WHOLE day: it binds 23:59:59 in the operator's zone, so the
  promised day never starts out overdue), `2026-08-04T10:30` (that wall clock),
  an RFC3339 instant, and a signed offset `+1d`/`+2h` (the SNOOZE, measured from
  now, so pushing an overdue task always lands ahead). Stored UTC whole-second
  like every other timestamp; omitted from the shard when unset, so a dateless
  board rewrites nothing on upgrade.
  **Where it surfaces is the point**: `brief` LEADS with `{overdue, today}`
  (the session-start read is the one place a date cannot be missed) and `lint`
  is the twin — **`due-overdue` is an ERROR**, `due-today` a warn — evaluated
  BOARD-WIDE, with no epic scope and no `next` lane filter, because the work
  that carries dates is usually parked outside the active focus. `next` only
  notes the count on stderr (its rows stay what is actionable). The lanes that
  go quiet are the done lane (always) plus `[due].ignore_lanes` (default
  `["icebox"]`); terminal lanes are deliberately NOT exempt as a class — a
  `waiting` task whose last step is "look at this tomorrow" is the case the
  field exists for. Queryable as `-q due:<cmp>` / `has:`/`no:due` /
  `is:overdue`; a bare day in `-q` is a UTC day for the machine stamps
  (created/updated/closed/reviewed) but the operator's LOCAL day for `due` —
  the one date a human authors in wall clock, so `-q due:2026-08-04` finds
  exactly what `--due 2026-08-04` wrote (a UTC day there would put the two up
  to 9h apart on a +09:00 machine).
- **Repos are the scope; labels are pure tags.** A task's repositories live in
  the first-class `repos` field (`owner/repo`, 0..N; `[]` = a **draft**, the
  issue-draft analogue). `-r` is the scope control on reads: a full
  `owner/repo` or a short name resolving uniquely against the board's repos;
  an explicit `-r` overrides the board scope, `-r ''` = the whole board. `-l`
  filters by tag and ANDs with the scope. Within a single `-s` or `-l`, a comma
  is OR (`-s inbox,backlog`); flags still AND across fields. Both `-s` and `-l`
  also union when **repeated** (`-s inbox -s backlog` == `-s inbox,backlog`,
  `-l bug -l urgent` == `-l bug,urgent`), so a repeated filter no longer silently
  keeps only the last value. `-s` and `-l` diverge
  on an unknown token: a lane is a closed vocabulary, so an unknown `-s` lane is
  **exit 2 with the configured lanes in `candidates`** (symmetric with move/add — a
  typo never returns a silent `[]`), while an unknown `-l` tag just matches
  nothing — unless it uniquely names a repo with tasks, which is exit 2 +
  `candidates` steering you to `-r` (the did-you-mean guard, on EVERY filtering
  read: ls/next/revisit/search/stats). An unknown `-r` short name is exit 2
  with the board's repo universe in `candidates` (symmetric with `-s`).
  **A read never narrows or truncates silently**: a repo scope that hides
  drafts (ls/next/search) or boxes (`epic ls`) says so in one stderr hint
  line, and a `-n` cap that bites prints `note: showing N of M (-n)` on
  stderr (ls flat + `--tree` groups, next, revisit, search, epic ls) — the
  JSON stays a bare array (`brief`'s `next_total` remains the machine-readable
  uncapped count). `stats.drafts` spans the repo dimension exactly like
  `brief`'s (no scope can own a draft; `-s`/`-l`/`-q` still bind it), so the
  two counts agree on a bare read. `furrow board [--json]` prints the store
  path, discovery source, repo
  scope, and lane vocabulary (lanes/next/default/done/terminal) — read it to
  learn the lanes and active scope without provoking an error. On a board, `add`
  unions the scope repo into `repos` (`--draft` suppresses exactly that), and
  `epic add` FALLS BACK to it when no -r is given (an explicit -r stands
  alone — a cross-repo box is a normal shape; shed a wrong fallback with
  `epic set --rm-repo`); `ls
  --drafts` lists the repo-less tasks; `furrow repo <id> --add|--rm`
  attaches/detaches later. `epic ls` obeys the same board scope (its
  population is what `brief`'s epic header draws from; `-r ''` escapes,
  `-l` is the same comma-OR tag filter).
- `--json` and `--ndjson` are honored **wherever furrow emits JSON** — reads,
  mutations, and reports alike (not just the list commands); **JSON goes to
  stdout only** (logs and errors go to stderr). `--ndjson` is the same payload
  compact, one value per line: a list command streams one record per line, a
  single-object command (a single-target mutation's `{before,after,changed}`,
  `board`, `attach`, `init`, `version`, the `apply` report) prints one compact
  line — the batch mutators (`done`/`move`/`set`) are list-shaped — and
  `lint` streams one problem per line — so a line-oriented reader never gets a
  silent human-prose degrade. Filter reads with `--status/-s`, `--label/-l`,
  `--repo/-r`, `--limit/-n`, and the typed query `--query/-q` on every
  filtering read (`ls`, `next`, `revisit`, `stats`, `search`; `brief` is
  excluded) — a flat AND-list of `field:value` terms where a comma is OR, a
  leading `-` is NOT, plus `has:`/`no:` presence, `is:` computed flags,
  ordinal and date comparisons/ranges, and direct graph edges. It ANDs with
  the other filters and never widens a scoped board. So you rarely need jq. Each `lint` problem carries
  a stable kebab-case `code` (`dangling-link`, `dep-cycle`, `epic-required`,
  `epic-no-active`, `orphan-asset`, `due-overdue`, `due-today`,
  `conflict-marker`, `unknown-shard-key`, …) — branch on it, not the message, since the `id` field
  is contextual (a task id, an asset name, an `owner/repo`, `meta`, or `config`).
  Mutations (`done|move|note|set|reorder|retitle|value|effort|check|dep|epic|label|repo`)
  with `--json` emit
  `{before, after, changed}`; an out-of-range `value`/`effort` clamps to 1..5
  and is signaled — via `value`/`effort`/`set`, a `clamped` envelope key nested
  by field (`clamped.value.{requested, stored}` / `clamped.effort.{…}`) plus a
  stderr note; via `add`, the stderr note only (its `--json` prints the created
  task, no envelope) — so an explicit arg is never silently rounded. A relative
  `reorder --before/--after` that had to respace its lane adds a `renumbered`
  array (`[{id, from, to}]`) beside the envelope.
  **A mutation that changes nothing leaves `updated` alone** — re-adding a label
  the task already carries, moving it into the lane it is already in, re-setting
  a score to the value it holds, `done` on an already-closed task: `changed` is
  `[]`, the shard is not rewritten, and the staleness clocks (`is:stale`,
  `revisit`'s stale, `lint`'s `reconcile-gap`, `ls --since`) keep their reading,
  so an idempotent retry costs the shared board nothing. The write paths that
  touch PROSE (`note`, `done --note`, `edit --body`) always advance it — the body
  is the entity's content but lives outside the shard, so the comparison cannot
  see it. Boxes obey the same rule.
  **`--expect-updated <rfc3339>` is the stale-read guard on every task mutator**
  (and the epic side of the body writers, `note` / `edit --body`): pass the
  `updated` stamp your read emitted, and if a
  co-writer got in between, the write still lands but the envelope gains
  `stale_read {expected, actual}` plus a stderr note — a warning, never a
  refusal (re-read and reconcile; don't lose the second edit too). One stamp =
  one read of ONE task: the batch mutators refuse it beside several ids, and a
  malformed stamp is exit 2.
  `add --stdin` bulk-creates one task per stdin line;
  `next --json` attaches a `reason` (`in_next_lane`, `deps_satisfied`) and
  `revisit --json` a `revisit` array (`no_repo`, `value_unset`, `effort_unset`,
  `stale`, `dep_done`) per task; the five box-level ones
  (`epic_all_done` / `epic_stuck` / `epic_stale` / `epic_dep_done` — that one
  says every epic this box waited on is closed, its turn to open — and
  `epic_review_due`, a STANDING box whose last `furrow review <epic-ref>` is
  past the board's `[review]` staleness threshold; never-reviewed boxes stay
  quiet) are about epics, not tasks, and ride `sync`/`brief`'s revisit
  summary keyed by epic id instead.
- **Batch reads by id: `show <id>... --no-body`** — any id set in one process,
  metadata only (no `body_text`), input order. `--json` = ALWAYS an array, one
  element per found id (a single id is a one-element array, a total miss
  prints `[]`), `--ndjson` = one line per task at any arity. A partial miss still emits the found tasks and exits 1 with
  `details.missing` — branch on that array. If a missing id is **archived**, the
  error also carries `details.archived` (the subset retrievable with
  `--archived`) and hints it in the message; `show <id> --archived` /
  `ls --archived` read the sibling `.furrow/archive/` store (same output shapes),
  so a retired task never falls off the read API.
- **A multi-machine board converges with `furrow sync`** (auto-commit scoped
  to `.furrow/` → `fetch` + `rebase --autostash @{u}` → `push`): run it before
  reading and after writing a shared board. Within `.furrow/`, machine-written
  files (an allowlist of what furrow itself writes: the `tasks/`/`epics/`/
  `repos/` shards, `meta.json`, `config.toml`, `bodies/assets/`, the board git
  dotfiles, and the `archive/` store's copies) and brand-new (untracked) bodies
  always commit — a file furrow does not own (an editor swap, a backup `~`, a
  stray `.tmp-*`) is NEVER committed and is disclosed in `foreign_files` plus a
  stderr note — but a **merely-modified `bodies/<id>.md` is committed only when
  named with `-b/--body <id>`, swept with `--all-bodies`, or written by furrow
  ITSELF** — `note` / `edit --body` / `done --note` / `apply` journal the id
  per-checkout (inside `.git/`, never synced) and a plain sync publishes those
  bodies unnamed, so the progress record `furrow note` keeps now travels
  without `-b`. On a shared
  checkout a plain sync must not commit a co-located operator's in-progress
  prose under the wrong author. A skipped body is listed in the JSON
  `pending_bodies` field (its twin `committed_bodies` lists what was committed)
  and in a stderr note, while sync still exits 0 and pushes everything else —
  so after hand-editing a body, run `furrow sync -b <id>` (or check
  `pending_bodies`); a plain `furrow sync` would leave that HAND edit local
  (furrow-written bodies ride unnamed via the journal — see above). Because
  `pushed: true` at exit 0 can still hide such a leftover, the progress object
  also carries **`complete`** (false whenever `pending_bodies` **or**
  `pending_stash` is non-empty) and the stdout summary line names the count —
  branch on `.complete` for "the board is fully published", never on `pushed`
  alone. It
  rebases onto the tracking ref, not `FETCH_HEAD`, so a co-writer's concurrent
  fetch can't race it into `Cannot rebase onto multiple branches`. On a true
  conflict it aborts the rebase itself and exits 3 with kind `sync-conflict` + the
  conflicted shard paths in `details`. A concurrent writer's transient race is
  waited out with a bounded backoff, handled by cause: a foreign rebase caught
  by the pre-flight, if still stuck past the budget, exits 3 with kind `sync-busy`
  — marked `retryable` in the envelope (re-run), NOT the do-not-retry `exit 2`; a
  fetch/ref-lock race during the pull is retried and, if a lock still blocks
  past the budget (a likely-stale `.git/*.lock`), fails terminally
  (`sync-lock-stale`) naming the lock to remove, NOT `sync-busy`. A co-writer
  that keeps winning the
  **push** race is the third retryable kind, `sync-push-rejected` (exit 3): the
  board is untouched and the local sync commit intact, so re-running is the whole
  fix. It is deliberately its own kind — a caller that must retry a race
  but stop on a conflict could otherwise only tell them apart by matching the
  message, which this file tells you never to do. `sync-task-status.yml` retries
  whatever the envelope marks `retryable`; every other kind is terminal there.
  A SIGINT/SIGTERM cancels the
  in-flight git and exits **128+signal (130 for SIGINT, 143 for SIGTERM)** with
  kind `sync-interrupted` — retryable, just re-run (a genuine conflict is never masked
  by the signal: it still surfaces as `sync-conflict` with its `details.paths`,
  keeping its exit 3). Branch on the `kind` (and the `retryable` flag), not the
  exit code, to tell these apart.
  **A sync can lose your WORK without losing the BOARD, and git's exit code only
  ever talks about the board.** `--autostash` stashes your other dirty files for
  the rebase; when the re-apply conflicts with what was pulled, git keeps them **in
  the stash**, warns on stderr, and **exits 0** — the edits are just gone from the
  working tree, and if one was a half-written body, that is furrow's progress
  record hanging in mid-air. So sync probes the stash: the run that strands one
  fails with id **`sync-stash-stranded`** (exit 3, nothing pushed) carrying
  `details.pending_stash` (`[{ref, commit, paths}]`), and ANY leftover autostash is
  re-reported by **every** subsequent sync in the `pending_stash` progress key until
  it is popped or dropped (your own `git stash` entries are never reported). The
  index that failure leaves behind (unmerged, no operation in progress) is explained
  by a pre-flight — id **`sync-unmerged`** (exit 2), naming the paths AND the stash
  still holding the other half — instead of relaying git's opaque `notes.md:
  unmerged (…)`. The
  Bodies themselves no longer conflict on concurrent APPENDS: `furrow init`
  scaffolds `.furrow/.gitattributes` with `bodies/*.md merge=union`, so the
  task-status marker × local note race folds both paragraphs instead of
  aborting the sync (a pre-scaffold board adds that line by hand — `furrow
  doctor` warns `no-body-union-merge` until it does; shards stay real
  conflicts, union on JSON would corrupt them). The
  wreckage such a failed re-apply leaves in the file — conflict markers — is refused
  at the door: a body carrying `<<<<<<<`/`=======`/`>>>>>>>` is **never**
  auto-committed (id **`body-conflict-marker`**, exit 2, `details.bodies`
  `[{id, path, lines}]`, nothing committed), because a commit cannot be
  un-published; `furrow lint` flags any that got in already (`conflict-marker`,
  **error**). A marker inside a fenced code block is documentation, not corruption,
  and is not flagged.
  A successful sync also gains a `revisit` key
  (`{dep_done:[ids], stale:[ids], epic_all_done:[ids], epic_stuck:[ids],
  epic_stale:[ids], epic_dep_done:[ids], epic_review_due:[ids],
  unreviewed:[{repo,days}]}` — each sub-key omitted when empty, repo-scoped, the
  whole key omitted when the board is clean) — the loop-visible staleness nudge;
  run `furrow revisit` for task detail, `furrow review <repo>` to reset a repo's
  `unreviewed` clock.
- **A shard key this binary does not know is PRESERVED, not dropped.** The
  version gate only fires when someone BUMPS the version; a field added without a bump
  would be silently destroyed by the next ordinary write (`encoding/json`'s
  lenient unmarshal drops it, the marshaller writes the loss back — one `retitle`,
  one dead field, no error). So `core.Unmarshal*` now parks every unknown
  **top-level** key and `core.Marshal*` re-emits it, sorted, after the known ones —
  in all **three** machine-written files: a task shard, a `repos/` review shard,
  and `meta.json`.
  Four things it does NOT mean, all load-bearing for an agent: (a) it is **not
  retroactive** — every furrow ≤ v0.9.0 still destroys those keys on write, so a
  shared board is safe only once EVERY writer has passthrough, including every
  pinned `sync-task-status.yml@vX.Y.Z` CI caller; (b) **top-level only** — an
  unknown key inside a known nested object (`checklist[]`) is still dropped;
  (c) **preserved ≠ honoured** — an older binary carries a future `"blocked": true`
  faithfully and still hands you that task in `furrow next` and lets you close it,
  so `furrow lint` warns **`unknown-shard-key`** (SevWarn, naming the keys and
  blaming the task id / the `owner/repo` / `meta`) to make "carried but IGNORED —
  update furrow" visible; (d) the `--json`
  views project the keys THIS binary knows — an unknown key lives on disk, not in
  the view (preserving beats displaying). Corollary for hand-edits: a typo in a
  hand-edited shard (`"lables"`) is now **permanent** — nothing removes it, because
  auto-deleting a key we don't understand IS the bug being fixed. That is also why
  `lint` must cover all three files and the published schemas all declare
  `additionalProperties: true`: the flip made the schema stop rejecting a typo, so
  `lint` is the only detector left. One more reason
  the shards are furrow's to write, not yours.
- furrow is **CLI-only and non-interactive**; there is no in-repo TUI. A TUI/GUI
  is a **separate front-end** that drives furrow through its CLI/JSON contract —
  planned: **ridge** (github.com/akira-toriyama/ridge, a charm-v2 TUI, a CLI/JSON
  client) and **loom** (github.com/akira-toriyama/loom, a from-scratch TUI
  framework, future/gated). Destructive ops guard themselves: `furrow archive`
  previews unless `--yes`.

## What this is

furrow — an alternative to GitHub Projects/Issues: a clonable, git-native,
plain-text task tracker. One central board can back many repos (tasks carry
their repositories in the first-class `repos` field) or a store can live
repo-local. Structured metadata lives in
one JSON shard per task, `.furrow/tasks/<id>.json` (deterministic,
machine-written), with the board-wide layout version in `.furrow/meta.json`
(`{"schema_version": 9}`); long-form prose lives in
`.furrow/bodies/<id>.md` (hand/agent-editable); human config is
`.furrow/config.toml`. A cobra CLI drives it (CLI-only — any TUI/GUI is a
separate out-of-repo front-end that speaks the CLI/JSON contract). Go,
cross-platform, brew/nix packaged.

## Build / run

```sh
go build ./...                          # compile (use GOTOOLCHAIN=local on Go 1.25+)
go test ./...                           # all packages
./run.sh ls --json                      # build + run a subcommand
```

## Verify (how to confirm a change works — runnable headless)

```sh
sh scripts/check.sh   # the one command: marshaller + schema-write guards +
                      # build/vet/test + golangci + schema/config/docs drift + a
                      # CLI smoke + (if goreleaser & syft are installed) a
                      # release dry-run. Green == green build/govulncheck CI; the
                      # only CI-side extras are the TOML/workflow/commit-message
                      # lints (taplo, zizmor, glyph). Run it before finishing.
```

Everything is verifiable without a terminal:
- **CLI**: directly runnable headless (`init/add/ls --json/next/done/migrate/lint`).
  Tests cover core + store + app + cli + migrate.
- **Determinism / drift**: the golden round-trip test,
  `scripts/check-marshal-singlepath.sh` (encoders **and** decoders — a raw
  `json.Unmarshal` would drop a shard's unknown keys),
  `scripts/check-schema-write-guard.sh` (no ordinary write may name
  `core.SchemaVersion`), `TestShardFieldsGolden` (the shard's on-disk shape is
  frozen; changing it demands a deliberate `-update-fields` + a version-bump
  decision), **`TestFrozenBoardRoundTripsByteIdentical`** (a real board's BYTES,
  committed under `internal/store/fsstore/testdata/frozen-board/`, that Load→Save
  must reproduce exactly — the one fixture the code under test did not write), and
  the schema/config drift diffs (in `check.sh`) guard the load-bearing invariants.
- **The release pipeline**: it used to run only on a tag, so a defect in
  `.goreleaser.yaml`/`release.yml` surfaced *after* GoReleaser had published the
  draft and pushed the cask (v0.8.0 shipped broken twice). `build.yml` now runs a
  real `--snapshot` build (with syft, so the `sboms:` pipe actually runs) on every
  PR and asserts the artifact shape with
  **`scripts/check-release-artifacts.sh`** — every path the attest steps feed to
  `actions/attest` resolves to a real file (`sbom-path` is NOT glob-expanded), each
  SBOM is SPDX-2.3 (the predicate type the READMEs document is derived from it),
  and `checksums.txt` names each archive exactly once as a whole field (a
  substring match also hits the SBOM line). `release.yml` runs the SAME script to
  derive its version, so the paths asserted on the PR are the paths it attests.
  Note `goreleaser check` does NOT cover any of this — it only validates the
  config's schema.

## Source-of-truth references

Consult these before adding behavior, and keep terms consistent with them:
[docs/architecture.md](docs/architecture.md) (layers), [docs/glossary.md](docs/glossary.md)
(ubiquitous language), [docs/non-goals.md](docs/non-goals.md) (what furrow won't do).

## Non-obvious constraints — read before editing

### Layer rules (the spine)
`internal/core` is **pure** (stdlib only — no cobra, os, or filepath).
Ports (`Store`, `Clock`) are interfaces **defined in core**;
`internal/store/fsstore` is the **only** package that touches the filesystem;
`internal/store/memstore` is its in-memory twin for tests. `internal/cli` is the
only presentation layer and mutates **only** through `internal/app.App` (the
single mutation funnel); any TUI/GUI front-end (e.g. ridge/loom) lives out-of-repo
and drives the CLI, not these packages. Crossing a layer means a port is missing —
add the interface, don't add the import.

### The single marshaller path — DO NOT regress this
`core.Marshal` is the **only** function that serializes the in-memory index;
the store persists per-shard via `core.MarshalTask` (one `tasks/<id>.json`) and
the layout version via `core.MarshalMeta` (`meta.json`). All three live in
`internal/core/marshal.go`. Never call `json.Marshal`/`json.NewEncoder` on an
`*Index`, `*Task`, or `*Meta` anywhere else. Recipe (same per shard):
struct-field key order, 2-space indent, `SetEscapeHTML(false)`, `[]` not null,
sorted+deduped label/dep sets, UTC whole-second timestamps, trailing newline.
This is what makes app-writes equal hand-edits byte-for-byte, and Save writes
only the shards whose bytes changed (zero git churn on a no-op save). A golden
round-trip test and `scripts/check-marshal-singlepath.sh` guard all three.

**Unknown-key passthrough — the other half of the version gate.**
`internal/core/passthrough.go`: `core.UnmarshalTask`/`UnmarshalRepo`/`UnmarshalMeta`
park every **top-level** key the binary does not know in an **unexported** `extras`
field, and the matching `Marshal*` re-emit them, **sorted, after the known keys**.
The gate (below) stops a *bumped* layout from being misread; the passthrough stops
an *unbumped* one from being destroyed — because a field added without a bump
leaves `meta.json` still saying v5, so no gate fires anywhere and an old binary's
lenient unmarshal drops the key and writes the loss back on the next ordinary
write. Two rules make it safe, and both are load-bearing:

- **"Is this key known?" must be answered with `encoding/json`'s OWN matcher, not
  an approximation of it.** json matches struct fields case-**IN**sensitively (a
  shard key `"BODY"` populates `Task.Body`), so a case-SENSITIVE set would park
  `BODY`, re-emit it, and leave a shard carrying both `body` and `BODY`. But the
  obvious fix — a `strings.ToLower` set — is **also wrong**, and worse, because
  json matches by Unicode simple case-**FOLDING**, and lowercasing is a different
  function. They disagree in both directions, and each direction is a corruption
  bug that shipped-and-was-caught in review:
  - json folds it, `ToLower` doesn't — `"statuſ"` (U+017F) is fed to `Task.Status`
    by json *and* parked as unknown. Extras are re-emitted LAST, so the stale copy
    wins on the next read: `furrow move` never takes and the task wedges forever.
  - `ToLower` folds it, json doesn't — `"İd"` (U+0130) lowercases to `id` but has
    an empty fold orbit, so json never matches it. A `ToLower` set calls it known
    and DROPS it while `Task.ID` stays empty: the key and the task's identity,
    destroyed. That is the very loss this file exists to prevent.
  `core.isKnown` therefore uses **`strings.EqualFold`** — json's own relation — so
  a key is parked **iff** json ignored it. `TestKnownKeysFoldExactlyLikeEncodingJSON`
  pins both directions and fails if the stdlib's matcher ever moves.
- **`Task` must NEVER grow a `MarshalJSON` method.** The `extras` carrier is
  unexported *structurally*, not stylistically: `encoding/json` cannot see it, so
  it can never surface as a literal `"extras"` key and can never leak into
  `internal/cli`'s `--json` views. Those views **embed** `core.Task` to put
  `body_text` / `reason` / `revisit` / `snippet` / `mentioned_by` beside it — a
  `MarshalJSON` on `Task` would be **promoted** to those outer structs, Go would
  call it for the whole view, and every sibling field would silently vanish **with
  no compile error**. (The first implementation did exactly that and emptied 10 CLI
  tests.) The splice therefore happens on the store's write path, in
  `core.MarshalTask`, where the data actually lives.

The byte recipe is untouched: the object is composed **compactly** and indented
once as a finished document, so the 2-space / no-HTML-escape / trailing-newline
rules still live in exactly one place. A shard with **no** extras marshals
byte-identically to what v0.9.0 wrote, so no existing board sees a single
rewritten shard. `fsstore.SetBoardVersion` **reads** `meta.json` and raises its
version rather than building a fresh `core.Meta` — otherwise `furrow upgrade`, the
one command whose whole job is to move a board FORWARD, would itself eat
`meta.json`'s forward-compatible keys.

`scripts/check-marshal-singlepath.sh` now guards **decoders too**
(`json.Unmarshal` / `json.NewDecoder`), not just encoders: a raw `json.Unmarshal`
into a `Task` bypasses `core.UnmarshalTask`, so the unknown keys are never parked
and the next write destroys them. A decoder that skips the single path is exactly
as lossy as an encoder that does.

Its sibling guard, **`scripts/check-schema-write-guard.sh`** (also in
`scripts/check.sh` + CI), greps the *other* single path: `core.SchemaVersion` —
the layout THIS BINARY writes — may only be named in `internal/core/*`,
`fsstore.go`, `memstore.go`, `internal/app/{upgrade,board,lint}.go`,
`internal/cli/cmd_board.go`, `internal/schema/schema.go`, and tests. Anywhere
else fails the build: an ordinary write must never name it (see Schema below —
that one line is what took the shared board down on 2026-07-13, and it fails
silently, since every test on a fresh store still passes).

### Frozen, collision-free ids & sparse priority
ids (`t-k3m9p`) are **frozen**: never reused, never renumbered. They are
**random** (prefix + a random Crockford-base32 suffix, `[ids].width` chars),
generated locally with no shared counter, so concurrent `furrow add`
from separate worktrees/PRs won't collide (the app retries on the rare in-store
clash; `furrow lint` flags any duplicate as a backstop). Legacy numeric ids
(`t-0042`) stay valid and coexist. Reorder by editing the sparse integer
`priority` (10-step) — one field, not a renumber. `reorder <id>
--before/--after <ref>` computes that field for you (same lane only — a
cross-lane ref is exit 2); only when the sparse gap is exhausted does it
respace the whole lane, atomically in the same single write, reporting the
neighbors' moves in the `--json` envelope's `renumbered` array (`[{id, from,
to}]`) — the neighbors' `updated` deliberately does NOT advance (a respace is
positional bookkeeping, not progress, so staleness signals stay honest).

### Configuration
`.furrow/config.toml` reads are **clamp-don't-reject**: unknown keys and
out-of-range values fall back to defaults with a warning that
`furrow lint` surfaces. Read it through `internal/config`. **The one writer is
`furrow config set`** (`--user` for a `[[board]]` entry of the user config): a surgical,
git-config-style edit — comments and every untouched byte survive — that is
STRICT where reads are lenient (an unknown key is exit 2 with the vocabulary in
candidates; a value the reader would clamp is refused before the write). Every
other command still only reads. The shipped sections
are `[lanes]`, `[next]`, `[priority]`, `[ids]`, `[labels]`,
`[archive]`, `[lint]`, `[due]`, `[revisit]`, `[review]`, `[alias]`, and the
top-level `standalone` and `default_repo` — the repo-root `config.toml` (which `furrow init` writes
and check.sh diffs byte-for-byte) is the canonical annotated copy; read it rather
than trusting a prose list here. Two switches are genuinely OFF by default:
`[labels].required` (a label-less task errors on `add` and in `lint`) and
`[lint].archive_done` (a count that makes `lint` warn `archive-backlog` once that
many done tasks are archivable; 0 = off). `[next].lanes` is NOT off by default —
ready+in-progress always applies; setting it overrides which lanes `next` shows. A board `[alias]` table (`name = "command
string"`) lets `furrow <name> …` expand git-style before dispatch (the rest of
argv appends); a builtin always wins (a shadowing alias is inert and `lint`
warns `alias-shadow`), and it lives in the **board** config so it syncs. The
user-level central-board config
(`~/.config/furrow/config.toml`) scopes each `[[board]]` by **repo**:
`repo = "auto" | "" | "owner/repo"` ("auto" derives owner/repo from the
checkout's git origin, worktree-aware, ghq-path fallback — `internal/app`'s
job, file reads only, never a bare dir name); a board's `label` is only a
literal add-time tag, and `label = "auto"` is a reserved tombstone (warned,
ignored). **The board's own `default_repo` is the FALLBACK scope**, applied by
`app.applyBoardScope` only when discovery supplied none — i.e. the two arms that
inject no scope, `FURROW_DIR` and a local `.furrow` (cwd inside the board's own
tree, which outranks the `[[board]]` entry that would have scoped it). A
pointer's `default_repo` / a `[[board]]`'s `repo` are nearer and still win. It is
a literal `owner/repo` only — `"auto"` is refused with a clamp warning, because
`config.toml` is committed and shared, so a cwd-derived repo would differ per
checkout — and it carries no board-side `auto_filter`: declaring the scope
declares it for reads too (`-r ''` is the per-command escape).

### Schema
`internal/schema.TaskV2` / `MetaV2` / `RepoV1` / `EpicV2` are the sources of the
JSON Schemas; `furrow schema [task|meta|repo|epic]` prints them (no arg or
`task` = the shard schema; `meta` = the `meta.json` schema; `repo` = a repos/
review shard; `epic` = an epics/ shard) and CI diffs all four against
`docs/schema/furrow.task.v2.json`, `furrow.meta.v2.json`, `furrow.repo.v1.json`,
and `furrow.epic.v2.json`. Change a
struct → update the schema const, the committed file, and the golden together.
A task carries a first-class `repos` set (owner/repo identifiers, same
sorted+deduped/[]-not-null semantics as labels; `[]` = draft). Labels are pure
free-form tags — a repo is NOT a label. The three top-level objects declare
`"additionalProperties": true` — furrow now legitimately writes shards carrying
keys it does not know (see the passthrough), and leaving it `false` would make the
published schema call furrow's own output invalid. `$defs.checklistItem` **stays
`false`**: passthrough is TOP-LEVEL ONLY, an unknown key inside a checklist item
really is still dropped, and the schema must not promise what the marshaller does
not do.

**Adding a shard field? The default answer is BUMP.** Passthrough makes an old
binary **preserve** a field it does not know. It does not make it **honour** one:
an old furrow carries a future `"blocked": true` faithfully through every write —
and then still surfaces that task in `furrow next` and still lets you close it, as
if the field were not there. Preservation downgrades silent DATA LOSS to silent
SEMANTIC MISBEHAVIOR — a real improvement (loss is unrecoverable; misbehavior is
fixed by updating the binary), but only `core.SchemaVersion` can say "refuse to
operate". So the rule "bump when the shard layout changes" now has **teeth**:
**`TestShardFieldsGolden`** (`internal/core/schema_fields_test.go` +
`testdata/shard-fields.golden`) freezes every persisted type's json keys, in struct
order, plus the layout version. Change a shard's shape and it FAILS, naming the
version to bump. Skip the bump only if **no** query, sort, filter, or lane decision
reads the new field — and note that every field ever added to `Task` (value,
effort, repos, reviewed, deps, refs, checklist, parent) is read by one: the "safe
for an old binary to ignore" class has never had a member. Accept a new shape with
`go test ./internal/core -run TestShardFieldsGolden -update-fields`, in the same
change as the schema const, the committed `docs/schema/` file, and the goldens.
New shard fields go at the **END** of the struct: a field declared mid-struct is
written there by a new binary and re-emitted at the end by an old one (extras are
appended), so alternating writes churn a one-line move — churn, not loss, but
avoidable.

**The teeth have a second row: the FROZEN BOARD.**
`TestShardFieldsGolden` reads the Go structs, so both sides of it move together —
it FAILS on a shape change, but `-update-fields` makes it green again whether or
not you bumped, because the teeth are the failure *message*, not a mechanical
check. `internal/store/fsstore/testdata/frozen-board/` is a real board's **bytes**,
written by an earlier furrow and committed;
**`TestFrozenBoardRoundTripsByteIdentical`** copies it, runs Load → Save →
SaveRepo → SetBoardVersion, and requires every file to come back **byte-identical**,
with the same file set and untouched mtimes. It is the only fixture in the repo the
code under test did not write, and it is what shows the DAMAGE rather than the
diff: add a non-`omitempty` field and it prints `+ "sprint": ""` appearing in every
shard — i.e. every board in the fleet rewritten on its next ordinary write, and
silently dropped by every older binary. Rename or remove a key and the on-disk key
becomes unknown, so the passthrough parks it and re-emits it *after* the known ones
— a key-ORDER change no in-memory test can see. It also pins the only two things
with no committed coverage at all: `meta.json`'s bytes, and the extras splice as it
actually lands on disk. Regenerate with `go test ./internal/store/fsstore -run
TestFrozenBoard -update-board` — which rewrites a committed board, so the diff makes
the decision visible in review, exactly as a flag day should be.

**The version gate is two-sided, and `core.SchemaVersion` is what THIS BINARY
writes — not what the board declares.** The board's number lives in `meta.json`
and is an **input** to every write:

- `core.CheckSchemaVersion(v)` — the READ gate. A board NEWER than the binary is
  refused (kind **`schema-too-new`**, exit 3 — the fix is the binary, not the
  input), so an old binary can never MISREAD such a board: a v3-only binary would
  happily load a v4 shard and then act as if `reviewed` did not exist. (It no
  longer guards against DESTROYING the fields it doesn't know — the passthrough
  preserves those. But preserving is not understanding, which is exactly why this
  gate stays.)
- `core.CheckWritable(v)` — the WRITE gate. A binary may write only a board that
  already declares exactly its own layout. An OLDER board — or one with shards
  but no `meta.json` at all — is fully READABLE but READ-ONLY (kind
  **`schema-upgrade-required`**, exit 2: the BOARD is stale and an explicit
  command fixes it). Both kinds carry `details {board_schema, binary_schema}`; the
  exit code alone says which side is stale.
- Consequently **an ordinary write never touches `meta.json`'s
  `schema_version`.** `fsstore.Save` stamps it in exactly one case: a genuinely
  fresh, empty store (what `furrow init` hits). A garbled `meta.json` is an error
  (exit 3, kind `internal`, subject `meta`), never a fallback to "whatever version this binary is" —
  that old fallback silently DISABLED the gate.

**The only raiser is `furrow upgrade`** (preview unless `--yes`; raises
`.furrow/meta.json` and the `archive/` store's, re-serializes every shard through
`core.MarshalTask`, idempotent no-op on a current board; JSON
`{from,to,changed,applied,stores}`). It is a **flag day**: afterwards no older
furrow can write the board, including a CI pinned to an older release — and
furrow cannot see those pins, so the ORDER is the human's: (1) release a furrow
shipping the schema, (2) bump every caller's `sync-task-status.yml@vX.Y.Z` pin
**and** that workflow's `furrow-version` default, (3) only THEN `furrow upgrade
--yes` + `furrow sync`. There is no downgrade — recovery is `git revert` on the
board repo. Why all this: on 2026-07-13 `fsstore.Save` stamped `meta.json` with
the binary's version on every write, so one routine `furrow sync` from an
unreleased source build migrated the shared central board 3 → 4 and every pinned
release in the fleet lost it at once (v0.6.1 reported "task not found" for every
id; v0.7.0 exited 3). `scripts/check-schema-write-guard.sh` greps that guarantee
back into place — see the marshaller-path section.

## Conventions

- Commits: gitmoji-driven — `<:gitmoji:>[(<scope>)][!] <subject>` (the leading
  `:code:` is the type and drives release semver; legacy `<type>(scope):` tokens
  are accepted and ignored).
  Enable the hook once: `git config core.hooksPath scripts/hooks`. The hook does
  not hold a copy of the grammar — it runs `glyph lint --stdin`, the same checker
  CI runs over the range, and skips cleanly (with a note) when `glyph` is not on
  PATH. Spec:
  [CONTRIBUTING.md](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md).
- `go build ./...` and `go test ./...` must pass before finishing a turn.
- English is the only committed language — **there is no `README.ja.md`;
  translations are not stored** (a JA reader translates the EN docs on demand).
  That is the fleet
  [doc-consistency policy](https://github.com/akira-toriyama/.github/blob/main/docs/doc-consistency-policy.md)
  (English-only, code-first, reduction-first — truth lives in the code/CLI and
  docs point at it), for which this repo is the reference implementation.
  Keep [README.md](README.md) and the docs/ tier
  (architecture/glossary/non-goals/scheduling) carrying the same FACTS on any
  user-visible change, and **sweep the docs/ tier in the same change**: drift
  pools exactly where no guard looks, and the v5 bump proved it (the guarded
  README moved; docs/ kept describing a v4 world until an audit caught it).
  Load-bearing facts stay in lockstep with the code/CI that own them, and
  [`scripts/check-readme-parity.sh`](scripts/check-readme-parity.sh) enforces
  it by pure text extraction: the `sync-task-status.yml@vX.Y.Z` pin in README's
  `uses:` example must match the `furrow-version` default of
  [`.github/workflows/sync-task-status.yml`](.github/workflows/sync-task-status.yml)
  itself (so a reader never copies a stale pin — NOT
  `.github/workflows/task-status.yml`, which is a fleet-synced copy owned by the
  hub canonical: release-prep used to bump it ahead of the tag and the next
  scheduled fleet-sync reverted it, 3 of 4 releases, once for ~46h; leave it
  alone and it catches up when the hub rollout lands), and every
  `{"schema_version": N}` literal —
  in README.md (required) **and any docs/*.md that writes one** — must equal
  `const SchemaVersion` in `internal/core/task.go` (the claim used to be made
  and NOT checked — which is exactly how the README came to say "board layout
  v3" against a v4 board; a historic version is prose, "v4", never the
  JSON-literal form).
  Its generalization is
  [`scripts/check-docs-vocab.sh`](scripts/check-docs-vocab.sh): **whenever you
  add a member to a closed vocabulary — a command, a `[section]`, a revisit
  signal, a `-q` qualifier — the doc regions that enumerate it must name it, or
  CI fails.** The vocabularies come from the registries that own them via the
  hidden `furrow vocab` (never a second hand-kept list), and a **claims** table
  maps each to the prose regions that enumerate it — adding a claim is one line
  there. Each claim declares a **direction**: `complete` (the region lists the
  whole vocabulary, so every member must appear), `subset` (the region lists
  EXAMPLES, so completeness is NOT required, but every member it names must still
  EXIST — the rename/delete rot a completeness check structurally cannot see), or
  `both`. **Neither direction ever flags an unknown token**: a region legitimately
  mentions other things, and a guard that cries wolf gets deleted. `subset` needs
  a member **shape** and so is only available to shape-distinctive vocabularies —
  measured, `commands` yields `list` as a false candidate (an English word; the
  command is `ls`), so plain-word vocabularies are `complete`-only. This is the
  answer to the largest cluster the 2026-07 audit found: five closed vocabularies
  copied into prose and then left behind, every one of them in a file no guard
  was reading.
- **1 item = 1 PR** (squash); update docs in the same PR.

## References

<!-- broad → narrow; tag each (reviewed YYYY-MM-DD); re-check on a 6-month gap. -->
- clig.dev — CLI design guidelines (reviewed 2026-06-25)
- gitmoji.dev; glyph (gitmoji-driven lint/semver/notes) (reviewed 2026-07-20)
- GoReleaser brews/nix (reviewed 2026-06-25)

## Multi-session work policy

`docs/plans/` holds one file per in-flight task (delete on merge). **Never leave
unfinished work implicit** — every in-flight task is a plan file, a tracked
issue, or an explicit note; nothing important lives only in a chat transcript.

**Multi-operator (shared checkout).** This repo is sometimes worked on by several
people/agents at once. A checkout has one shared HEAD/index/working tree, so two
operators running git in the same directory corrupt each other (orphaned commits,
commits on the wrong branch). **Each operator/session works in its own `git
worktree` (`git worktree add ../furrow-<topic> -b <branch> origin/main`) or a
separate clone — never share one checkout for concurrent git.** Commit + push
often and `git pull --rebase` before pushing.
