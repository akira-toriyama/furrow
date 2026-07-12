# furrow

> An alternative to GitHub Projects / Issues — a clonable, git-native, plain-text task tracker you and your coding agent can both edit cleanly.

**furrow** keeps your tasks as plain text *in a git repo*: structured metadata in one deterministic JSON shard per task, long-form prose in per-task Markdown files. The case against Issues is simple. An issue can't be cloned — plain text can, so the tracker works offline and greps with your code. An agent can *read and write* it with ordinary file and CLI operations, no API client. And because the tracker lives in git next to the work, status never drifts from reality — the same push that changes the code can change the task. Writes are byte-stable, so `git diff` only ever shows what actually changed.

**When to reach for which.** GitHub Issues are the right tool for *intake from anyone* — a public inbox where a stranger can file a bug without write access to your repo. furrow is the opposite tool for the opposite job: *private, in-group* tasks for you and your agent. Its "you must be able to push to create a task" is **access control, not a defect** — the same permission boundary that guards your code guards your backlog.

**Local and instant, not a round-trip.** Much of this GitHub *can* do — but through the API: online-only, rate-limited, a network round-trip per call. furrow does it against plain files on disk: milliseconds, offline, no quota. Backlinks are the concrete example — `show --backlinks` answers "which tasks mention this one?" (the `[[id]]` links in their bodies) by scanning local files, where the GitHub equivalent is an online "mentioned in" panel behind an API call.

**Multi-person, honestly.** furrow is single-operator-first today, and that is the polished path. Several people *can* work one board — it is a git repo, so they clone, push, and `furrow sync` (per-task shards make concurrent edits a clean union). But per-person niceties — an `@mention` and a task **assignee** — are **not built yet**; they are on the roadmap, not a permanent non-goal.

Written in Go (module `github.com/akira-toriyama/furrow`, Go 1.25+). No database, no daemon, no cloud.

> **Status:** core (first-class `repos`, schema v2 + version gate), CLI (incl.
> `repo`, drafts, `-r` scoping, `sync`, `apply`), the bubbletea TUI
> (`furrow ui`), and `migrate` all work (`go test ./...` + golangci green).
> Releases are published — see the [Releases page](https://github.com/akira-toriyama/furrow/releases) and [Status](#status).

[日本語版 README →](README.ja.md)

---

## Install

> Releases are cut with GoReleaser and distributed via the Homebrew tap and the nix flake (which carries a real, pinned `vendorHash`); see the [Releases page](https://github.com/akira-toriyama/furrow/releases). Install with any of Homebrew, `go install`, or `nix run`. The release pipeline attaches a GitHub build-provenance attestation to each release artifact — verify a download with `gh attestation verify <file> --repo akira-toriyama/furrow`.

```sh
# Homebrew (tap)
brew install akira-toriyama/tap/furrow

# Go toolchain (from source)
go install github.com/akira-toriyama/furrow/cmd/furrow@latest

# Nix
nix run github:akira-toriyama/furrow
```

A from-source build reports its version as `dev`, with the build commit/date filled in from the Go VCS stamp; the release version is injected at link time (`furrow version --json` shows all of it).

---

## Two ways to run it

- **A central board** — one clonable tracker repo backs *all* your repos: each
  task carries the repos it relates to (the first-class `repos` field,
  `owner/repo`), each checkout is auto-scoped to its own repo, and
  `furrow sync` keeps clones on several machines converged. This is the
  GitHub-Projects-alternative mode — see [Central board](#central-board).
- **Repo-local** — another way to run it: a single repo carries its own
  `.furrow/` next to the code (`furrow init` and go). Fully supported; the
  Quickstart below runs this way, and everything except the board scoping
  works identically on a central board.

---

## Quickstart

```sh
# create a .furrow store in the current repo
furrow init

# add a task (id is assigned automatically, frozen, never reused)
furrow add "Wire up the config loader" --label core --label config

# list tasks in canonical lane -> priority -> id order
furrow ls

# move it out of intake once it's ready to pick up (add defaults to inbox)
furrow move t-0001 ready

# show what's ready to work (lane in [next].lanes — default ready + in-progress — and all deps done)
furrow next

# open the task's Markdown body in $EDITOR (prints the path when non-interactive)
furrow edit t-0001

# inspect a single task with its body
furrow show t-0001

# mark it done (stamps the closed timestamp)
furrow done t-0001
```

`add` defaults the lane to `lanes.default` (`inbox`) and appends within the lane using the sparse priority step. Pass `--status/-s`, `--priority/-p`, `--label/-l`, `--parent`, `--dep`, `--ref`, or `--body` to set fields up front.

---

## The store

furrow uses a **hybrid** layout: one machine-written JSON shard per task for structured metadata, and one hand-editable Markdown file per task for prose. A pure JSON or JSONL store would collapse long bodies into one escaped line — every prose edit would churn the whole file and an agent could easily corrupt the escaping. Splitting prose into `bodies/<id>.md` keeps both halves diffable. Sharding the metadata one file per task means two operators adding or editing tasks on separate worktrees/PRs touch distinct files, so a git merge is a conflict-free union instead of a fight over one sorted array.

```text
.furrow/
├── config.toml          # human config (furrow only READS this; never rewrites it)
├── meta.json            # board-wide layout version {"schema_version": 3} — written ONLY by furrow
├── tasks/
│   ├── t-0001.json      # one metadata shard per task — written ONLY by the single core.MarshalTask path
│   └── t-0002.json
├── bodies/
│   ├── t-0001.md        # long-form prose for t-0001 (hand/agent editable)
│   └── t-0002.md
└── archive/             # aged done tasks (its own tasks/, meta.json + bodies/)
    ├── meta.json
    ├── tasks/
    └── bodies/
```

A minimal `tasks/t-0001.json` shard:

```json
{
  "id": "t-0001",
  "title": "Wire up the config loader",
  "status": "in-progress",
  "priority": 100,
  "labels": [
    "config",
    "core"
  ],
  "repos": [
    "akira-toriyama/furrow"
  ],
  "deps": [],
  "refs": [],
  "checklist": [],
  "created": "2026-06-25T00:00:00Z",
  "updated": "2026-06-25T00:00:00Z",
  "closed": null,
  "body": "bodies/t-0001.md"
}
```

The board-wide layout version lives on its own in `meta.json` (never inside a shard, so a version bump touches one file and no shard becomes a merge point):

```json
{
  "schema_version": 3
}
```

Notes on the fields: `id` is frozen and is the stem of both the shard file (`tasks/t-0001.json`) and the body file (`bodies/t-0001.md`); `priority` is a sparse 10-step integer so reordering edits one field instead of renumbering; `status` is a lane defined in `config.toml`; `repos` is the first-class set of repositories the task relates to (`owner/repo` identifiers, 0..N — an empty set means a **draft**, the GitHub-Issues-draft analogue; labels are pure tags, a repo is *not* a label); `closed` is `null` while open and stamped when a task enters the done lane; empty collections serialize as `[]`, never `null`. `value` and `effort` are an optional coarse 1..5 estimate (importance and cost) — both omitted while unset, so dropping an idea into the inbox stays friction-free — and out-of-range scores clamp to 1..5. The JSON Schema for a shard lives at [`docs/schema/furrow.task.v2.json`](docs/schema/furrow.task.v2.json) and for `meta.json` at [`docs/schema/furrow.meta.v2.json`](docs/schema/furrow.meta.v2.json); both are emitted by `furrow schema` (`task` by default, `meta` for the board version).

`value` and `effort` exist so an agent (or you) can pick the next task from recorded data instead of re-guessing each time. **ROI = value / effort is derived, never stored** (so editing either estimate always yields a current ROI, with no stale number to reconcile), and `next` is deliberately unchanged — sorting by ROI is the caller's choice:

```sh
# highest value-per-effort first, among tasks that carry both estimates
furrow ls --json | jq 'map(select(.value and .effort)) | sort_by(-(.value / .effort))'
```

`furrow revisit` is the agent-facing companion: a **read-only** query that surfaces the open tasks whose metadata may be out of date — missing `value`/`effort`, gone stale (no update within `[revisit].stale_days`), or carrying a dependency that is already done. Each task comes back with a `revisit` array of `{code, detail}` so the agent knows exactly what to fix with the existing setters (`value`/`effort`/`dep`); it never mutates anything itself.

```sh
# tasks in this repo that still need estimates, with the reasons
furrow revisit -r furrow --json | jq '.[] | {id, revisit: [.revisit[].code]}'
```

### Attaching images and media

A task body is plain Markdown, so you can attach a screenshot or diagram by committing the file alongside the bodies and linking it with a **relative path**:

```markdown
![repro](assets/t-0001-bug.png)
```

It renders wherever Markdown does (GitHub, Obsidian, an editor preview) — but **not in the terminal** (`furrow ui`/`show` print the text, not the picture). furrow itself does nothing special with these files; they are just part of your repo. A few practical notes:

- Keep screenshots small and scrub anything secret — git history is permanent.
- On a **private** repo, committing the image in-repo and linking it relatively is the reliable option; external/raw image URLs typically need auth and expire. On a public repo you can also link an external host.
- For large media such as videos, track them with **Git LFS** (a `.gitattributes` rule) *before* committing the first one, so they never bloat the plain history (adding LFS afterwards only helps new files; cleaning existing blobs needs a history rewrite).
- `furrow lint` backs these habits up: it warns on a body that references a missing asset, an asset that no body references, and any asset ≥5 MiB — a nudge to LFS-track or shrink it *before* the blob lands in history (once committed it can't be un-committed).

---

## Command reference

All commands below are implemented and working today, including the `ui` TUI and `migrate`. (A read-only web viewer is the only remaining future work — see [Status](#status).)

| Command | What it does | Key flags / args |
|---|---|---|
| `init` | Create a `.furrow/` store (config + `meta.json` + empty `tasks/` + `bodies/`) in the current directory | — |
| `add <title>...` | Add a task (or many from stdin with `--stdin`); assigns frozen ids and seeds `bodies/<id>.md`. `--check` (repeatable) seeds checklist items (the body prose alone never populates the shard checklist); an out-of-range `--value`/`--effort` clamps with a stderr note. A title starting with `-` needs a `--` separator (the error says so) | `--stdin`, `-s/--status`, `-p/--priority`, `--value`, `--effort`, `-l/--label`, `-r/--repo`, `--draft`, `--parent`, `--dep`, `--ref`, `--body`, `--check` |
| `ls` (alias `list`) | List tasks in canonical `lane -> priority -> id` order; `--drafts` lists only the tasks with no repo (bypasses the board scope). `--since`/`--until` window by the `updated` timestamp (a bare `YYYY-MM-DD`, or a full RFC3339 instant; a bare `--until` includes the whole day). `--sort updated\|created\|value\|effort` reorders (newest/highest first; `--reverse` flips it, an unset `value`/`effort` stays last either way); with `--sort`, `-n` takes the top N of the sorted set. An unknown `--sort` field or an invalid date is exit 2. `--archived` lists from the archive store (`.furrow/archive/`) instead of the hot board (the same filters/sort apply) | `-s/--status`, `-l/--label`, `-r/--repo`, `-n/--limit`, `--drafts`, `--since`, `--until`, `--sort`, `--reverse`, `--archived` |
| `show <id>...` | Show one or more tasks plus their Markdown bodies in a single read, in input order (several ids emit a `--json` array or `---`-separated text; one id keeps the classic single-object shape; `--ndjson` is one task per line at any arity). `--no-body` omits the body (`body_text`) — the lean metadata-only read. A partial miss still prints the found tasks and exits 1 with `details.missing`; if a missing id is actually **archived**, the error names it in `details.archived` and the message says to retry with `--archived`. `--archived` reads from the archive store (`.furrow/archive/`) so a retired task (and its `[[id]]`/`SetStatus-task` links) stays reachable. `--backlinks` also lists the tasks whose body mentions each one via `[[id]]` (a "Mentioned in" section, or a `mentioned_by` array under `--json`); it can't combine with `--archived` | `--no-body`, `--backlinks`, `--archived` |
| `next` | Show actionable tasks (lane in the configured `[next].lanes` — default `ready` + `in-progress`, so intake lanes stay out — and all deps done); `--json`/`--ndjson` attach a `reason` (`in_next_lane`, `deps_satisfied`) | `-l/--label`, `-r/--repo`, `-n/--limit` (use `-n1` for just the top) |
| `revisit` | Read-only; list open tasks needing re-evaluation. `--json`/`--ndjson` attach a `revisit` array of `{code, detail}` (`no_repo`, `value_unset`, `effort_unset`, `stale`, `dep_done`) so an agent knows what to fix. Drafts surface regardless of scope. Empty result exits 0 | `-l/--label`, `-r/--repo`, `-n/--limit`, `--stale-days <n>` (0 disables stale) |
| `search <term>` | Full-text search over every task's title **and** Markdown body (case-insensitive substring), in canonical order — so finding a term is one command, not a `grep .furrow/bodies` detour. Honors the same `-s/-l/-r/-n` scope as `ls` (a bare `search` stays within this repo's board; `-r ''` searches everything). Each hit reports `matched_field` (`title`\|`body`) and a one-line `snippet` with the term in context; a title match never reads the body. Several words are one literal phrase. Empty result exits 0 | `-s/--status`, `-l/--label`, `-r/--repo`, `-n/--limit` |
| `stats` | Summarize the board within scope: `total`, `drafts`, and counts `by_lane` (a complete histogram in configured lane order — 0-count lanes included), `by_repo`, and `by_label` (the used vocabulary, most-used first). A bare `stats` describes this repo's slice; `stats -r ''` describes the whole board — the call that learns the label/repo vocabulary before guessing a `-l`/`-r`. `--json`/`--ndjson` emit one object; an all-zero board exits 0 | `-s/--status`, `-l/--label`, `-r/--repo` |
| `board` | Print the active board's introspection snapshot: store path, discovery `source` (`env`/`local`/`pointer`/`user-config`), repo scope, and the lane vocabulary (`lanes`/`next_lanes`/`default_lane`/`done_lane`/`terminal`) plus the stale/archive windows. The way to learn the lanes without provoking an error; `--json` (or `--ndjson`) emits the object | `--json`, `--ndjson` |
| `edit <id>` | Open `bodies/<id>.md` in `$EDITOR`; prints the path when non-interactive | — |
| `attach <id> <file>` | Copy an image/video into `bodies/assets/<id>-*` and append a relative markdown reference to the body — images embed (`![…]`), other media link (`[…]`); a collision-free name (`…-2`, `…-3`) never overwrites an existing asset. Because the body is committed markdown, the whole attach lands in git from the terminal alone (no web upload). LFS-independent. `--json` emits `{id, asset, ref, line}` | — |
| `done <id>` | Move a task into the done lane (stamps `closed`) | — |
| `move <id> <lane>` | Move a task to a lane (clears `closed` when leaving done) | — |
| `reorder <id> <priority>` | Set a task's priority (sparse integer; lower sorts higher) | — |
| `retitle <id> <title...>` | Rename a task, updating the shard title **and** the body's leading `# ` heading so they never drift (trailing args are joined, so the title need not be quoted) | — |
| `value <id> <1-5>` | Set a task's coarse value (importance) estimate; an out-of-range score clamps to 1..5 **and is signaled** — a `clamped {requested, stored}` key in the `--json` mutation envelope plus a stderr note (so an explicit arg is never silently rounded); `--clear` unsets | `--clear` |
| `effort <id> <1-5>` | Set a task's coarse effort (cost) estimate; clamps to 1..5 with the same `clamped` signal as `value`; `--clear` unsets | `--clear` |
| `set <id>` | Apply the routine triage edits — lane, value, effort, labels — in **one** write, instead of `move` + `value` + `effort` + `label` as four commands. At least one change required; an unknown lane is exit 2 with `candidates` (like `move`); under `[labels].required` a set that would strip the last label is refused | `-s/--status`, `--value`, `--effort`, `--clear-value`, `--clear-effort`, `--add-label`, `--rm-label` |
| `check <id> [index]` | Edit a task's checklist: mark the item at a zero-based index done (idempotent set, not a toggle; `--off` unchecks), append items (`--add`, repeatable, verbatim), delete the item at an index (`--rm`), or replace its text (`--reword <text>`). The mode flags are mutually exclusive; an out-of-range index is exit 2 | `--add <text>`, `--off`, `--rm`, `--reword <text>` |
| `dep <id> [<dep-id>...]` | Add one or more dependencies (id waits on them) in a single write, or remove them with `--rm`; acyclic, idempotent, all-or-nothing (a bad dep-id aborts without a partial change). `--list` instead reads (never mutates) `<id>`'s dependency neighborhood **both ways** — `depends_on` (what it waits on) and `blocks` (the reverse edge: what waits on it, the "what unblocks if I finish this" view) — each resolved to id+title+lane; `--json`/`--ndjson` emit one object with both arrays (`[]` when empty). A dangling dep resolves to its id alone (lint flags it). `--list` takes just the id and can't combine with `--rm` | `--rm`, `--list` |
| `label <id>` | Add and/or remove labels on a task (both repeatable, combinable); idempotent | `--add <label>`, `--remove <label>` |
| `repo <id>` | Attach and/or detach repos (`owner/repo`) on a task; each value must be a full `owner/repo` or a short name uniquely resolving against the board's repos (else exit 2 with `candidates`); idempotent. A task with no repos is a draft | `--add <repo>`, `--rm <repo>` |
| `apply` | Apply `SetStatus-task: <body-link> [<lane>]` directives parsed from PR/commit text (stdin or `--body-file`) — the CI hook for auto status updates. `--on open` nudges to in-progress; `--on merge` applies the lane. Validation is non-blocking | `--on open\|merge`, `--ref`, `--body-file`, `--open-lane` |
| `sync` | The multi-machine board ritual as one command: auto-commit scoped to `.furrow/` (machine-written shards always; a hand-edited `bodies/<id>.md` only when new or named with `-b`, else left for its author in `pending_bodies` — so a shared checkout never sweeps a co-located operator's WIP; `--all-bodies` restores the old sweep), `fetch` + `rebase --autostash @{u}` (onto the tracking ref, not `FETCH_HEAD`, so a co-writer's fetch can't race it), `push` (one pull→push retry on non-fast-forward). On conflict it aborts the rebase automatically (`sync-conflict` error carries the paths); a foreign rebase caught by the pre-flight is waited out, else retryable `sync-busy` (exit 3); a fetch/lock race during the pull is retried, and if it persists (a likely-stale `.git/*.lock`) fails terminally naming the lock to remove. Progress `{committed, pulled, pushed, conflict, committed_bodies, pending_bodies}` goes to stdout even on failure. A successful sync also adds a repo-scoped `revisit` summary (`dep_done`/`stale` id lists; omitted when empty) | `-m/--message`, `-b/--body`, `--all-bodies` |
| `archive [<id>...]` | Retire done tasks to `.furrow/archive/` (preview unless `--yes`). With `<id>`s it retires exactly those (each must be in the done lane, else exit 2 — no stranding live work); with no id it sweeps aged done. The sweep is board-wide by default; `-r/--repo` (repeatable) scopes it to one repo's aged done, ANDed with the age guard. `--older-than`/`-r` apply to the sweep only (combining them with an id list is exit 2). A task's `attach`ed media (`bodies/assets/<id>-*`) travels with it into `.furrow/archive/`, never orphaned in the hot store | `--older-than <days>`, `-r/--repo <repo>` (repeatable), `--yes` |
| `lint` | Check shard↔body 1:1, id shape, lanes, deps/parent refs, dependency cycles (error), a done-lane task with no `closed` timestamp (error — a `furrow done` backfills it), dangling `[[id]]` body links (warn; archived ids are not dangling), reconcile gaps (an open task whose done dependency closed after its last update; warn), asset hygiene — a body referencing a missing asset, an asset no body references, or an asset ≥5 MiB (all warn; a raw blob can't be un-committed, so it's flagged before it lands), config clamp warnings (incl. a half-written user-level config), and — when `[lint].archive_done` is set — an `archive-backlog` nudge once that many done tasks are old enough to archive. Every finding carries a stable kebab-case `code` (`dangling-link`, `dep-cycle`, `orphan-asset`, `archive-backlog`, …) so `--json`/`--ndjson` triage branches on the code, not the message prose — the `id` field is contextual (a task id, an asset name, or `config`) | — |
| `config init` | Write the user-level `~/.config/furrow/config.toml` (central-board template); fills the board path/scopes from the nearest `.furrow` when run inside a board, else a placeholder. Never overwrites an existing file | `--path`, `--scope` (repeatable) |
| `config path` | Print the resolved user-level config path; a half-written config's clamp warnings go to stderr (stdout stays the bare path) | — |
| `schema [task\|meta]` | Print the JSON Schema for a task shard (no arg or `task`) or for `meta.json` (`meta`); matches the committed copy | — |
| `version` | Print the furrow version (plus the build commit/date when stamped); `--version` on the root command prints the same line, and `--json` emits `{version, commit, date, modified}` for scripts/agents | `--json`, or `furrow --version` |
| `ui` | Launch the interactive TUI (list + detail panes): navigate, filter, done, move lane, reorder (`K`/`J`), toggle checklist, edit body | — |
| `migrate <file>` | Import an existing `Task.md` etc. (dry-run by default; unmapped headings & `[[wikilink]]`s reported, never dropped) | `--write`, `-l/--label` |

On the read commands, `-r/--repo` filters by the first-class `repos` field and is the scope control: a short name resolves case-insensitively at a `/` boundary (`-r furrow` → `akira-toriyama/furrow`; ambiguity is exit 2 with `candidates`), an explicit `-r` overrides the board scope, and `-r ''` shows the whole board. `-l/--label` is a pure tag filter that ANDs with the scope. Within a single `-s` or `-l`, a comma is OR (`-s inbox,backlog`, `-l bug,urgent`); the flags still AND across fields. `-s` and `-l` part ways on an *unknown* token: a lane is a closed vocabulary, so an unknown `-s` lane **exits 2 with the configured lanes in `candidates`** (symmetric with `move`/`add` — a typo like `-s in_progress` never silently returns `[]`), whereas an unknown `-l` tag just matches nothing (labels are open). When a label filter matches nothing but the name uniquely resolves to a repo that has tasks, furrow exits 2 pointing you at `-r` (the did-you-mean guard). Run `furrow board` to see the lanes and the active scope without provoking an error. When an explicit `-r` hides drafts on `ls`/`next`, one stderr hint line (`N draft(s) hidden — furrow ls --drafts`) points at them; stdout stays pure data.

Global flags: `--json` and `--ndjson` are honored **wherever furrow emits JSON**, not just the read/list commands — `--ndjson` is the same payload as `--json`, compact, one value per line (a list command streams one record per line; a single-object command like a mutation or `board` prints one compact line). Mutations (`done`, `move`, `reorder`, `retitle`, `value`, `effort`, `check`, `dep`, `label`, `repo`) emit `{before, after, changed}` so a caller sees the effect without a follow-up `show`; `apply` emits a per-directive report (`{on, ref, outcomes}`); `add`/`attach`/`init`/`lint`/`archive`/`migrate`/`version` all honor both flags too. `edit` prefers `$FURROW_EDITOR`, then `$VISUAL`, then `$EDITOR`, falling back to `vi`.

---

## Claude Code / agent integration

furrow needs no MCP server and no plugin — the plain CLI **is** the agent interface: `--json`/`--ndjson` on every read, machine-actionable error envelopes, and a clonable plain-text store the agent can read (and, for bodies, write) directly. A daemon or a second protocol would add operational surface without adding a capability (see [docs/non-goals.md](docs/non-goals.md)). The integration is just a small `CLAUDE.md` block plus the `--json` flag. The rules:

- **Never hand-edit `tasks/<id>.json` (or `meta.json`).** A single deterministic marshaller owns those files; a manual edit will churn the diff (and likely lose the canonical ordering). Mutate tasks through the commands above.
- **`bodies/*.md` are yours to edit.** Prose lives there and is plain Markdown — edit it directly, or via `furrow edit <id>` (which prints the absolute path in a non-interactive context).
- **Use `--json` for machine reads (and writes).** JSON is written to **stdout only**; logs, confirmations, and errors go to **stderr**, so piping stdout into `jq` is always clean. `--ndjson` is the compact one-value-per-line form and is honored on every command that emits JSON (mutations and reports included), so a line-oriented agent never gets a silent human-prose degrade. Filters: `--status/-s`, `--label/-l`, `--repo/-r`, `--limit/-n` (a comma within `-s`/`-l` is OR within that field).
- **Batch by id with `show <id>... --no-body`.** Cross-checking a specific id set (audit sweeps, dependency checks) is one process, metadata only — no `body_text` bloating the output. Add `--ndjson` for an arity-independent one-task-per-line shape; a partial miss still emits the found tasks and reports the rest in `details.missing`.

furrow is **non-interactive by default** — it never prompts. Destructive operations are guarded: `archive` only previews unless you pass `--yes`.

**Exit codes:**

| Code | Meaning |
|---|---|
| `0` | OK — **including an empty query result** (`ls`/`next`/`revisit` matching nothing still succeeded) |
| `1` | a **specifically requested id** was not found (e.g. `show <id>`) — never an empty list |
| `2` | bad usage / validation |
| `3+` | internal / I/O error |

The same contract is printed by `furrow --help` (and each affected command's help), so it is discoverable from the binary, not just here.

On a non-zero exit, furrow prints a structured error object to stderr:

```json
{"error":{"code":2,"id":"t-0001","message":"unknown lane \"backlogg\""}}
```

When an input almost resolved — an ambiguous repo short name, an unknown lane, a
parent command's unknown subcommand (`config show`), or a label that uniquely
names a repo (the did-you-mean guard) — the envelope also carries
`"candidates": [ … ]`, so a script picks an alternative from the array instead
of parsing the message prose. Likewise a partial `show` batch
(some ids unknown) still prints the found tasks and exits 1 with
`"details": {"missing": ["t-…", …]}` — branch on the array, never the message.

### CI: auto-update a tracker from PRs

`furrow apply` turns a PR into a status update — the `Closes #N` idea, for a
furrow tracker. Add a footer to the PR body pointing at a task's body file:

```
SetStatus-task: https://github.com/<owner>/<tracker>/blob/main/.furrow/bodies/<id>.md done
```

On PR **open** (incl. draft) the task is nudged to in-progress; on **merge** the
named lane is applied (omit the lane to only annotate the body). `apply` reads the
text from `--body-file` or stdin and is CI/VCS-agnostic.

The GitHub wiring **ships with furrow** as a reusable workflow,
[`.github/workflows/sync-task-status.yml`](.github/workflows/sync-task-status.yml).
A code repo needs only a ~10-line caller, pinned to a **concrete furrow release
tag** (never a moving ref):

```yaml
# .github/workflows/task-status.yml
name: task-status
on:
  pull_request:
    types: [opened, reopened, ready_for_review, closed]
permissions:
  contents: read
  pull-requests: write
jobs:
  sync:
    uses: akira-toriyama/furrow/.github/workflows/sync-task-status.yml@v0.7.0
    secrets:
      PROJECTS_WRITE_PAT: ${{ secrets.PROJECTS_WRITE_PAT }}
```

The workflow downloads the furrow **release binary matching its own tag**
(checksum-verified) — the workflow revision and the binary revision cannot
diverge, and CI upgrades only when you bump the pin. Auth is one fine-grained
PAT (`PROJECTS_WRITE_PAT`: Contents Read & write on the tracker repo only);
until it exists the job skips cleanly. Validation is non-blocking: an unknown
id or lane is reported, never a merge blocker.

---

## Central board

This is the GitHub-Projects-alternative mode: many repos share one central
board (e.g. a private cross-repo tracker repo — clonable, greppable, diffable),
each auto-scoped to its own repo (`owner/repo`, the first-class `repos` field).
Wire it up once for whole trees of repos (user-level config), or per repo (a
pointer file).

### User-level config (no per-repo file)

Point furrow at one or more central boards covering whole trees of repos, with
**zero per-repo setup** — new repos are covered automatically. Scaffold it with
`furrow config init` (run inside the central board's repo, it fills the board
path and scope in for you; elsewhere it writes a commented placeholder to edit),
or write `~/.config/furrow/config.toml` (or `$XDG_CONFIG_HOME/furrow/config.toml`)
by hand; `furrow config path` prints where it lives.

```toml
[[board]]
path        = "~/src/github.com/me/projects/.furrow"  # the central .furrow (~, relative to this file, or absolute)
scopes      = ["~/src/github.com/me"]                 # activate only under these dirs (at least one is required)
repo        = "auto"                                  # "auto" = derive owner/repo from the checkout | "" = none | a literal "owner/repo"
label       = ""                                      # optional literal tag `add` applies (never filters reads)
auto_filter = true                                    # scope ls/next/revisit to the board repo (default true; false = whole board)
```

A board activates **only when the current directory is under one of its
`scopes`**; everywhere else furrow behaves exactly as without it. Repeat the
`[[board]]` table to send different trees to different boards — when several
scopes enclose the cwd, the **most specific (longest) one wins** (ties go to the
first in the file). A board with no `scopes` is ignored rather than guessed, so a
half-written entry never breaks furrow elsewhere — and because that makes it
silent, `furrow lint` and `furrow config path` report whatever was clamped.

`repo = "auto"` derives the scope repo from the nearest enclosing git checkout —
file reads only, no `git` subprocess: it parses the FIRST `url` of
`[remote "origin"]` in `.git/config` (scp-like `git@host:o/r.git`, `ssh://`,
`git+ssh://`, and `http(s)://` forms, with or without `.git`; `pushurl`, second
`url` lines, and other remotes never count). A worktree's `.git` FILE is
followed through `gitdir` and `commondir` to the shared config, so a worktree
named `chord-fix-y` still derives `owner/chord`. With no usable origin it falls
back to a ghq-style `…/github.com/<owner>/<repo>` path; failing that the board
opens **unscoped** with a stderr note and `add` creates drafts — a bare
directory name is never written into `repos`. Outside any git repo the board
still opens, with the same note. `FURROW_BOARD=<path>` is the env form of the
central board: it replaces the user-level config file's `[[board]]` entries with
one synthetic board for one-offs and tests (its scope is the board repo's
parent). It does **not** override a nearer store — `FURROW_DIR`, a local
`.furrow`, and a `.furrow-pointer.toml` all still win over it (see Discovery
precedence). The retired `label = "auto"` mode is ignored with a warning pointing
at `repo = "auto"`.

### Per-repo pointer

A single repo can instead redirect with a `.furrow-pointer.toml` at its root
(this **wins over** the user-level central boards):

```toml
board = "../projects/.furrow"   # the central .furrow (relative to this file, ~, or absolute)
default_repo = "me/chord"       # optional: scope to one owner/repo ("auto" derives it; "" = redirect only)
```

### Discovery precedence

`FURROW_DIR` (explicit, no scope injection) → the nearest ancestor directory
holding a `.furrow` (a real local store wins) → a `.furrow-pointer.toml`
redirecting to a board → a **central board**: `FURROW_BOARD` (env override —
one synthetic board) if set, otherwise the user-level config file's `[[board]]`
entries (when the cwd is under one of their `scopes`; most specific scope wins)
→ `furrow init`. So `FURROW_BOARD` only outranks the config-file boards, never a
nearer `FURROW_DIR` / local `.furrow` / pointer.

With a board in effect (pointer or user-level):

- `furrow add "…"` unions the scope repo into the task's `repos` (an explicit
  `-r x` adds to it rather than replacing); `add --draft` suppresses exactly
  that union. The board's literal `label` (if any) still unions into labels.
- `furrow ls|next|revisit` filter to the scope repo — **silently** (no banner).
  A user-level board can opt out with `auto_filter = false` to show the whole
  board while `add` still attaches the repo; a pointer always filters. Scope
  control is `-r`: pass `-r ''` to see the whole board for one command, or
  `-r <repo>` for another repo. An explicit `-l tag` filters *within* the scope
  (it ANDs; it does not clear it). When the scope hides drafts, one stderr hint
  line points at `furrow ls --drafts`.

---

### Multi-machine: `furrow sync`

A central board cloned on several machines needs only one ritual: pull before
you read, push after you write. `furrow sync` is that ritual as one
non-interactive command — a thin git wrapper, not a sync daemon or server
(see [docs/non-goals.md](docs/non-goals.md)):

1. auto-commit, **scoped to `.furrow/`** — other dirty files in the board repo
   (notes, drafts) are never swept in. Within `.furrow/`, machine-written shards
   (`tasks/`, `meta.json`) are always committed, but a hand-edited
   `bodies/<id>.md` is committed **only when it is new or named with `-b/--body`**
   — a merely-modified body is left for its author (surfaced in `pending_bodies`)
   so a shared checkout never commits a co-located operator's in-progress prose
   under the wrong author. `--all-bodies` restores the old sweep for a checkout
   you know is yours alone. Default message
   `:card_file_box: chore(board): sync via furrow`; override with `-m`.
2. `git fetch`, then `git rebase --autostash @{u}` — rebasing onto the upstream
   **tracking ref**, never `FETCH_HEAD`, so a co-writer's concurrent fetch in a
   shared checkout can't make it `fatal: Cannot rebase onto multiple branches`
3. `git push` (one pull→push retry on non-fast-forward)

Per-task shards make true conflicts rare — two machines *adding* tasks touch
disjoint files; only both sides editing the *same* task conflicts. When that
happens sync **aborts the rebase automatically** (the board is never left with
conflict markers; your local sync commit survives) and exits 3 with an error
envelope carrying `"id": "sync-conflict"` and `"details": {"paths": [...]}` so
an agent knows exactly which shards to reconcile. The progress object
`{committed, pulled, pushed, conflict, committed_bodies, pending_bodies}` is
printed to stdout on success and failure alike (the two body lists are omitted
when empty).

On a **successful** sync it also prints a repo-scoped `revisit` summary: open
tasks with a done dependency (`dep_done`) or gone stale (`stale`) — a nudge to
run `furrow revisit` for detail. Human output adds one line,
`revisit: <n> dep_done, <n> stale (<scope>) — furrow revisit` (`<scope>` is
the current repo's short name, or `board` when there is no auto repo);
`--json`/`--ndjson` gain a `revisit` key (`{dep_done:[ids], stale:[ids]}`)
with the id lists. Both are omitted entirely when the board is clean.

Because a bot or a second operator can be pushing at any moment, a shared
checkout races two ways, and sync handles each by its likely cause. (1) The
pre-flight can catch *their* rebase mid-flight — sync **waits it out** with a
bounded backoff (~5s); if it is still going it exits 3 with `"id": "sync-busy"`,
a **retryable** class (not the `exit 2` "fix the args" class) signalling that
re-running usually clears it (they will have finished, or a rebase here is
genuinely stuck and needs a manual `git rebase --abort`). (2) *Their* `git fetch`
can briefly contend a ref/index lock while ours runs — sync retries the pull
through the same backoff. A live race clears in well under a second, so if a lock
still blocks after the budget it is almost certainly a **stale** lock (a crashed
git left a `.git/*.lock`); sync then fails **terminally** telling you which lock
to remove, rather than looping an agent forever on a `sync-busy` that will never
clear.

### Board git hooks (optional)

The design lens: **remote automation is GitHub Actions; local automation is git
hooks.** furrow ships three POSIX-sh hooks in
[`scripts/board-hooks/`](scripts/board-hooks/) that put `furrow lint` at git's
extension points, so a board that goes inconsistent (a dep pointing at a task
someone archived, an orphaned body, a duplicate shard from a merge) is caught the
moment it happens — and never reaches the remote.

| hook | fires after | action | blocking |
|---|---|---|---|
| `post-merge`   | `git merge` / plain `git pull` | `furrow lint` | no (nudge) |
| `post-rewrite` | `git rebase` / `--amend` / `git pull --rebase` | `furrow lint` | no (nudge) |
| `pre-push`     | before a push | `furrow lint` | **yes, on errors** |

Only `pre-push` blocks, and only on lint **errors** (`furrow lint` exits 2);
warnings flow through and are surfaced non-blockingly after a merge or rebase. A
`git pull --rebase` fires `post-rewrite` (not `post-merge`), so a board wants
both — and since `furrow sync` pulls with `--rebase` internally, sync trips these
hooks too (which is why sync carries no lint of its own).

Enable them once per machine — git does not turn on hooks at clone time, by
design — with the same one line furrow's own repo uses:

```sh
git config core.hooksPath scripts/hooks   # after placing the hooks there
```

`core.hooksPath` **replaces** `.git/hooks` rather than augmenting it — git then
consults only this directory — so move any hook you already keep in the default
`.git/hooks/` into the hooks dir too, or it silently stops running. Once both
live there, a same-name hook (say a `pre-push` that protects `main`) is a
collision to **compose** — keep the existing body and add the furrow-lint block —
not to replace. Each hook also **skips cleanly** when `furrow` is absent from
`PATH` or the repo has no `.furrow/`, so it never wedges a checkout.

## Configuration

`.furrow/config.toml` is the one human-edited file in the store. furrow only **reads** it (it never rewrites it) and applies a **clamp-don't-reject** policy: unknown keys are ignored and out-of-range values fall back to a safe default with a warning surfaced by `furrow lint` — so a typo can never break the tool.

```toml
[lanes]
# The status enum AND the top->bottom sort rank.
order   = ["inbox", "backlog", "ready", "in-progress", "waiting", "done", "icebox"]
default = "inbox"                 # lane `furrow add` uses when --status is omitted
done    = "done"                  # lane `furrow done` moves into (where `closed` is stamped)
terminal = ["done", "icebox", "waiting"]  # lanes never actionable (done/parked); what `next` shows is [next].lanes below

[next]
lanes = ["ready", "in-progress"]  # lanes `furrow next` considers "ready to work" (besides the deps-done check);
                                  # intake/planning lanes are excluded — set to all non-terminal lanes to show everything actionable

[priority]
step    = 10                      # sparse step so reordering edits one field
default = 100

[ids]
prefix = "t-"                     # frozen id: prefix + random base32 suffix (collision-free)
width  = 5                        # number of random suffix chars, e.g. t-k3m9p

[archive]
older_than_days = 30              # default window for `furrow archive --older-than`

[lint]
# archive_done = 0                # `furrow lint` warns once this many done tasks are old enough to archive (0 = off)

[revisit]
stale_days = 30                   # `furrow revisit` flags a task with no update in this many days (0 disables)

[ui]
theme = "auto"                    # auto | dark | light (NO_COLOR is always respected)

[alias]                           # name your frequent filters; `furrow <name> …` expands git-style
triage = "ls -s inbox,backlog"    #   `furrow triage -r app` -> `furrow ls -s inbox,backlog -r app`
wip    = "ls -s in-progress"       #   the remaining args append, so all existing flags/scope compose
```

A board `[alias]` names a frequent command string; `furrow <name> <extra args>` expands it git-style (the alias tokens replace the name, the rest of the argv is appended), so every flag, board scope, and auto-filter composes for free. It lives in the **board** config (not the user-level one), so it syncs with the board and every machine/agent shares it. A real command always wins — an alias that shadows a builtin (`ls`, `next`, …) is inert and `furrow lint` flags it (`alias-shadow`); a blank alias value is dropped with a clamp warning. Put global flags *after* the alias (`furrow triage --json`), as with git.

`done` stamps `closed`; moving a task *out* of the done lane clears it. Other terminal lanes (e.g. `icebox` — parked, not finished; `waiting` — the GTD *Waiting-For* lane for work delegated or blocked on someone external) do **not** stamp `closed`, which is why parked tasks are never archived.

---

## Determinism

furrow's write path is byte-stable on purpose. Every shard write goes through one marshaller (`core.MarshalTask`) with a fixed contract: struct-field key order, 2-space indent, `SetEscapeHTML(false)` (so CJK and `< > &` survive verbatim), empty collections as `[]` not `null`, sorted-and-deduped label/dep sets, whole-second UTC RFC3339 timestamps, and a trailing newline. The result is that the bytes furrow writes are identical to what a human or an agent would hand-edit, and a `Save` rewrites only the shards whose bytes actually changed — so re-saving an untouched store produces **zero git churn** — diffs show only the field you actually changed.

---

## Status

- **Working:** the core domain (`internal/core`) with the first-class `repos`
  field (board layout v3 + the version gate), config loader, filesystem store, app
  coordinator, the full CLI (incl. `repo`, drafts, `-r` scoping, `apply`, and
  `sync`), the bubbletea **TUI** (`furrow ui`), and **`migrate`** (importing a
  legacy `Task.md`). `go test ./...` + golangci clean; `sh scripts/check.sh`
  runs the full verification (incl. a teatest TUI e2e).
- **Released:** tags are cut with GoReleaser → the Homebrew tap (see the
  [Releases page](https://github.com/akira-toriyama/furrow/releases); the
  bundled task-status Action ships since `v0.5.0`, board layout v3 since
  `v0.6.0`). The nix `flake.nix` carries a real, pinned `vendorHash` with a
  committed `flake.lock` (since `v0.4.0`).
- **Future (low priority):** a read-only web viewer / React UI over the task shards.

Design notes: architecture in [`docs/architecture.md`](docs/architecture.md),
terms in [`docs/glossary.md`](docs/glossary.md), and what furrow deliberately
doesn't do (with rationale) in [`docs/non-goals.md`](docs/non-goals.md).

---

## License

MIT © akira-toriyama
