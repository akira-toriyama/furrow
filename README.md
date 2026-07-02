# furrow

> A repo-local, plain-text task tracker you and your coding agent can both edit cleanly.

**furrow** keeps your tasks *inside the repo* as plain text: structured metadata in one deterministic JSON shard per task, long-form prose in per-task Markdown files. It replaces the friction of GitHub Projects / a single `Task.md` with a small CLI whose writes are byte-stable, so `git diff` only ever shows what actually changed.

Written in Go (module `github.com/akira-toriyama/furrow`, Go 1.23). No database, no daemon, no cloud.

> **Status:** core, CLI, the bubbletea TUI (`furrow ui`), and `migrate` all work
> (`go test ./...` + golangci green). Packaging (brew/nix release) is configured
> but not yet published — see [Status](#status).

[日本語版 README →](README.ja.md)

---

## Install

> furrow is pre-release. The packaging targets below are wired toward release; some channels may be placeholders until the first tagged build.

```sh
# Homebrew (tap)
brew install akira-toriyama/tap/furrow

# Go toolchain (from source)
go install github.com/akira-toriyama/furrow/cmd/furrow@latest

# Nix
nix run github:akira-toriyama/furrow
```

A from-source build reports its version as `dev` (the release version is injected at link time).

---

## Quickstart

```sh
# create a .furrow store in the current repo
furrow init

# add a task (id is assigned automatically, frozen, never reused)
furrow add "Wire up the config loader" --label core --label config

# list tasks in canonical lane -> priority -> id order
furrow ls

# show what's actionable right now (non-terminal lane + all deps done)
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
├── meta.json            # board-wide layout version {"schema_version": 2} — written ONLY by furrow
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
  "schema_version": 2
}
```

Notes on the fields: `id` is frozen and is the stem of both the shard file (`tasks/t-0001.json`) and the body file (`bodies/t-0001.md`); `priority` is a sparse 10-step integer so reordering edits one field instead of renumbering; `status` is a lane defined in `config.toml`; `closed` is `null` while open and stamped when a task enters the done lane; empty collections serialize as `[]`, never `null`. `value` and `effort` are an optional coarse 1..5 estimate (importance and cost) — both omitted while unset, so dropping an idea into the inbox stays friction-free — and out-of-range scores clamp to 1..5. The JSON Schema for a shard lives at [`docs/schema/furrow.task.v2.json`](docs/schema/furrow.task.v2.json) and for `meta.json` at [`docs/schema/furrow.meta.v1.json`](docs/schema/furrow.meta.v1.json); both are emitted by `furrow schema` (`task` by default, `meta` for the board version).

`value` and `effort` exist so an agent (or you) can pick the next task from recorded data instead of re-guessing each time. **ROI = value / effort is derived, never stored** (so editing either estimate always yields a current ROI, with no stale number to reconcile), and `next` is deliberately unchanged — sorting by ROI is the caller's choice:

```sh
# highest value-per-effort first, among tasks that carry both estimates
furrow ls --json | jq 'map(select(.value and .effort)) | sort_by(-(.value / .effort))'
```

`furrow revisit` is the agent-facing companion: a **read-only** query that surfaces the open tasks whose metadata may be out of date — missing `value`/`effort`, gone stale (no update within `[revisit].stale_days`), or carrying a dependency that is already done. Each task comes back with a `revisit` array of `{code, detail}` so the agent knows exactly what to fix with the existing setters (`value`/`effort`/`dep`); it never mutates anything itself.

```sh
# tasks in this repo that still need estimates, with the reasons
furrow revisit -l furrow --json | jq '.[] | {id, revisit: [.revisit[].code]}'
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

---

## Command reference

All commands below are implemented and working today, including the `ui` TUI and `migrate`. (Packaging and a future web viewer are the remaining work — see [Status](#status).)

| Command | What it does | Key flags / args |
|---|---|---|
| `init` | Create a `.furrow/` store (config + `meta.json` + empty `tasks/` + `bodies/`) in the current directory | — |
| `add <title>...` | Add a task (or many from stdin with `--stdin`); assigns frozen ids and seeds `bodies/<id>.md` | `--stdin`, `-s/--status`, `-p/--priority`, `--value`, `--effort`, `-l/--label`, `--parent`, `--dep`, `--ref`, `--body` |
| `ls` (alias `list`) | List tasks in canonical `lane -> priority -> id` order | `-s/--status`, `-l/--label`, `-n/--limit` |
| `show <id>` | Show one task plus its Markdown body | — |
| `next` | Show actionable tasks (non-terminal lane, all deps done); `--json`/`--ndjson` attach a `reason` (`in_next_lane`, `deps_satisfied`) | `-n/--limit` (use `-n1` for just the top) |
| `revisit` | Read-only; list open tasks needing re-evaluation. `--json`/`--ndjson` attach a `revisit` array of `{code, detail}` (`value_unset`, `effort_unset`, `stale`, `dep_done`) so an agent knows what to fix. Empty result exits 0 | `-l/--label`, `-n/--limit`, `--stale-days <n>` (0 disables stale) |
| `edit <id>` | Open `bodies/<id>.md` in `$EDITOR`; prints the path when non-interactive | — |
| `done <id>` | Move a task into the done lane (stamps `closed`) | — |
| `move <id> <lane>` | Move a task to a lane (clears `closed` when leaving done) | — |
| `reorder <id> <priority>` | Set a task's priority (sparse integer; lower sorts higher) | — |
| `value <id> <1-5>` | Set a task's coarse value (importance) estimate; out-of-range scores clamp to 1..5; `--clear` unsets | `--clear` |
| `effort <id> <1-5>` | Set a task's coarse effort (cost) estimate; clamps to 1..5; `--clear` unsets | `--clear` |
| `check <id> [index]` | Toggle a checklist item by zero-based index, or append one | `--add <text>`, `--off` |
| `dep <id> <dep-id>` | Add a dependency (id waits on dep-id), or remove it with `--rm`; acyclic & idempotent | `--rm` |
| `label <id>` | Add and/or remove labels on a task (both repeatable, combinable); idempotent | `--add <label>`, `--remove <label>` |
| `apply` | Apply `SetStatus-task: <body-link> [<lane>]` directives parsed from PR/commit text (stdin or `--body-file`) — the CI hook for auto status updates. `--on open` nudges to in-progress; `--on merge` applies the lane. Validation is non-blocking | `--on open\|merge`, `--ref`, `--body-file`, `--open-lane` |
| `sync` | The multi-machine board ritual as one command: auto-commit limited to `.furrow/`, `pull --rebase` (autostash), `push` (one pull→push retry on non-fast-forward). On conflict it aborts the rebase automatically (`sync-conflict` error carries the paths); progress `{committed, pulled, pushed, conflict}` goes to stdout even on failure | `-m/--message` |
| `archive` | Move aged done tasks to `.furrow/archive/` (preview unless `--yes`) | `--older-than <days>`, `--yes` |
| `lint` | Check shard↔body 1:1, id shape, lanes, deps/parent refs, config clamp warnings (incl. a half-written user-level config) | — |
| `config init` | Write the user-level `~/.config/furrow/config.toml` (central-board template); fills the board path/scopes from the nearest `.furrow` when run inside a board, else a placeholder. Never overwrites an existing file | `--path`, `--scope` (repeatable) |
| `config path` | Print the resolved user-level config path; a half-written config's clamp warnings go to stderr (stdout stays the bare path) | — |
| `schema [task\|meta]` | Print the JSON Schema for a task shard (no arg or `task`) or for `meta.json` (`meta`); matches the committed copy | — |
| `version` | Print the furrow version | — |
| `ui` | Launch the interactive TUI (list + detail panes): navigate, filter, done, move lane, reorder (`K`/`J`), toggle checklist, edit body | — |
| `migrate <file>` | Import an existing `Task.md` etc. (dry-run by default; unmapped headings & `[[wikilink]]`s reported, never dropped) | `--write`, `-l/--label` |

Global flags (read/list commands): `--json` and `--ndjson`. Mutations (`done`, `move`, `reorder`, `value`, `effort`, `check`, `dep`, `label`) also accept `--json`, emitting `{before, after, changed}` so a caller sees the effect without a follow-up `show`. `apply --json` emits a per-directive report (`{on, ref, outcomes}`). `edit` prefers `$FURROW_EDITOR`, then `$VISUAL`, then `$EDITOR`, falling back to `vi`.

---

## Claude Code / agent integration

furrow needs no MCP server and no plugin — for a repo-local tool that is overkill. The integration is just a small `CLAUDE.md` block plus the `--json` flag. The rules:

- **Never hand-edit `tasks/<id>.json` (or `meta.json`).** A single deterministic marshaller owns those files; a manual edit will churn the diff (and likely lose the canonical ordering). Mutate tasks through the commands above.
- **`bodies/*.md` are yours to edit.** Prose lives there and is plain Markdown — edit it directly, or via `furrow edit <id>` (which prints the absolute path in a non-interactive context).
- **Use `--json` for machine reads.** JSON is written to **stdout only**; logs, confirmations, and errors go to **stderr**, so piping stdout into `jq` is always clean. `--ndjson` emits one task per line for streaming. Filters: `--status/-s`, `--label/-l`, `--limit/-n`.

furrow is **non-interactive by default** — it never prompts. Destructive operations are guarded: `archive` only previews unless you pass `--yes`.

**Exit codes:**

| Code | Meaning |
|---|---|
| `0` | OK |
| `1` | not found / empty result |
| `2` | bad usage / validation |
| `3+` | internal / I/O error |

On a non-zero exit, furrow prints a structured error object to stderr:

```json
{"error":{"code":2,"id":"t-0001","message":"unknown lane \"backlogg\""}}
```

### CI: auto-update a tracker from PRs

`furrow apply` turns a PR into a status update — the `Closes #N` idea, for a
furrow tracker. Add a footer to the PR body pointing at a task's body file:

```
SetStatus-task: https://github.com/<owner>/<tracker>/blob/main/.furrow/bodies/<id>.md done
```

On PR **open** (incl. draft) the task is nudged to in-progress; on **merge** the
named lane is applied (omit the lane to only annotate the body). `apply` reads the
text from `--body-file` or stdin and is CI/VCS-agnostic, so a thin CI job — see
this repo's [`.github/workflows/task-status.yml`](.github/workflows/task-status.yml),
which calls a shared reusable workflow — is all the wiring it needs. Validation is
non-blocking: an unknown id or lane is reported, never a merge blocker.

---

## Central board

Many repos can share one central board (e.g. a private cross-repo tracker), each
auto-scoped to its own label. Wire it up once for whole trees of repos
(user-level config), or per repo (a pointer file).

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
label       = "auto"                                  # "auto" = nearest git repo's dir name | "" = none | a literal label
auto_filter = true                                    # scope ls/next/revisit to the label (default true; false = whole board)
```

A board activates **only when the current directory is under one of its
`scopes`**; everywhere else furrow behaves exactly as without it. Repeat the
`[[board]]` table to send different trees to different boards — when several
scopes enclose the cwd, the **most specific (longest) one wins** (ties go to the
first in the file). A board with no `scopes` is ignored rather than guessed, so a
half-written entry never breaks furrow elsewhere — and because that makes it
silent, `furrow lint` and `furrow config path` report whatever was clamped.

`label = "auto"` derives the scope label from the nearest enclosing git repo's
directory name (a local `.git` walk — no `git` subprocess, no `GHQ_ROOT`);
outside any git repo the board still opens but with no auto label (a note goes to
stderr; pass `-l` to scope). `FURROW_BOARD=<path>` overrides everything with a
single board for one-offs and tests (its scope is the board repo's parent).

### Per-repo pointer

A single repo can instead redirect with a `.furrow-pointer.toml` at its root
(this **wins over** the user-level central boards):

```toml
board = "../projects/.furrow"   # the central .furrow (relative to this file, ~, or absolute)
default_label = "chord"         # optional: scope this repo to one label
```

### Discovery precedence

`FURROW_DIR` (explicit, no label injection) → the nearest ancestor directory
holding a `.furrow` (a real local store wins) → a `.furrow-pointer.toml`
redirecting to a board → a **user-level central board** (when the cwd is under
one of its `scopes`; most specific scope wins) → `furrow init`.

With a board in effect (pointer or user-level):

- `furrow add "…"` unions the scope label into the task's labels (and satisfies
  `[labels].required`); an explicit `-l x` adds to it rather than replacing.
- `furrow ls|next|revisit` filter to the scope label — **silently** (no banner).
  A user-level board can opt out with `auto_filter = false` to show the whole
  board while `add` still tags with the label; a pointer always filters. Pass
  `-l ''` to see the whole board for one command, or `-l other` for another label.

---

### Multi-machine: `furrow sync`

A central board cloned on several machines needs only one ritual: pull before
you read, push after you write. `furrow sync` is that ritual as one
non-interactive command — a thin git wrapper, not a sync daemon or server
(see [docs/non-goals.md](docs/non-goals.md)):

1. auto-commit, **pathspec-limited to `.furrow/`** — other dirty files in the
   board repo (notes, drafts) are never swept in. Default message
   `:card_file_box: chore(board): sync via furrow`; override with `-m`.
2. `git -c rebase.autoStash=true pull --rebase`
3. `git push` (one pull→push retry on non-fast-forward)

Per-task shards make true conflicts rare — two machines *adding* tasks touch
disjoint files; only both sides editing the *same* task conflicts. When that
happens sync **aborts the rebase automatically** (the board is never left with
conflict markers; your local sync commit survives) and exits 3 with an error
envelope carrying `"id": "sync-conflict"` and `"details": {"paths": [...]}` so
an agent knows exactly which shards to reconcile. The progress object
`{committed, pulled, pushed, conflict}` is printed to stdout on success and
failure alike.

## Configuration

`.furrow/config.toml` is the one human-edited file in the store. furrow only **reads** it (it never rewrites it) and applies a **clamp-don't-reject** policy: unknown keys are ignored and out-of-range values fall back to a safe default with a warning surfaced by `furrow lint` — so a typo can never break the tool.

```toml
[lanes]
# The status enum AND the top->bottom sort rank.
order   = ["inbox", "backlog", "ready", "in-progress", "waiting", "done", "icebox"]
default = "inbox"                 # lane `furrow add` uses when --status is omitted
done    = "done"                  # lane `furrow done` moves into (where `closed` is stamped)
terminal = ["done", "icebox", "waiting"]  # lanes NOT actionable for `furrow next`

[priority]
step    = 10                      # sparse step so reordering edits one field
default = 100

[ids]
prefix = "t-"                     # frozen id: prefix + random base32 suffix (collision-free)
width  = 5                        # number of random suffix chars, e.g. t-k3m9p

[archive]
older_than_days = 30              # default window for `furrow archive --older-than`

[revisit]
stale_days = 30                   # `furrow revisit` flags a task with no update in this many days (0 disables)

[ui]
theme = "auto"                    # auto | dark | light (NO_COLOR is always respected)
```

`done` stamps `closed`; moving a task *out* of the done lane clears it. Other terminal lanes (e.g. `icebox` — parked, not finished; `waiting` — the GTD *Waiting-For* lane for work delegated or blocked on someone external) do **not** stamp `closed`, which is why parked tasks are never archived.

---

## Determinism

furrow's write path is byte-stable on purpose. Every shard write goes through one marshaller (`core.MarshalTask`) with a fixed contract: struct-field key order, 2-space indent, `SetEscapeHTML(false)` (so CJK and `< > &` survive verbatim), empty collections as `[]` not `null`, sorted-and-deduped label/dep sets, whole-second UTC RFC3339 timestamps, and a trailing newline. The result is that the bytes furrow writes are identical to what a human or an agent would hand-edit, and a `Save` rewrites only the shards whose bytes actually changed — so re-saving an untouched store produces **zero git churn** — diffs show only the field you actually changed.

---

## Status

- **Working:** the core domain (`internal/core`), config loader, filesystem store,
  app coordinator, the full CLI, the bubbletea **TUI** (`furrow ui`), and
  **`migrate`** (importing a legacy `Task.md`). `go test ./...` + golangci clean;
  `sh scripts/check.sh` runs the full verification (incl. a teatest TUI e2e).
- **Configured, not yet published:** the brew/nix release — GoReleaser
  config validated, but `v0.1.0` isn't tagged yet. nix `flake.nix` carries a
  placeholder `vendorHash`.
- **Future (low priority):** a read-only web viewer / React UI over the task shards.

Design notes: architecture in [`docs/architecture.md`](docs/architecture.md),
terms in [`docs/glossary.md`](docs/glossary.md), and what furrow deliberately
doesn't do (with rationale) in [`docs/non-goals.md`](docs/non-goals.md).

---

## License

MIT © akira-toriyama
