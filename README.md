# furrow

> A repo-local, plain-text task tracker you and your coding agent can both edit cleanly.

**furrow** keeps your tasks *inside the repo* as plain text: structured metadata in a deterministic JSON index, long-form prose in per-task Markdown files. It replaces the friction of GitHub Projects / a single `Task.md` — public issues you can't keep private notes in, one giant file that mixes tasks + design + process, manual priority renumbering, manual done-archiving — with a small CLI whose writes are byte-stable, so `git diff` only ever shows what actually changed.

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

furrow uses a **hybrid** layout: a single machine-written JSON index for structured metadata, and one hand-editable Markdown file per task for prose. A pure JSON or JSONL store would collapse long bodies into one escaped line — every prose edit would churn the whole file and an agent could easily corrupt the escaping. Splitting prose into `bodies/<id>.md` keeps both halves diffable.

```text
.furrow/
├── config.toml          # human config (furrow only READS this; never rewrites it)
├── index.json           # structured metadata — written ONLY by the single core.Marshal path
├── bodies/
│   ├── t-0001.md        # long-form prose for t-0001 (hand/agent editable)
│   └── t-0002.md
└── archive/             # aged done tasks (its own index.json + bodies/)
    ├── index.json
    └── bodies/
```

A minimal `index.json`:

```json
{
  "schema_version": 1,
  "tasks": [
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
  ]
}
```

Notes on the fields: `id` is frozen and is the stem of the body file (`bodies/t-0001.md`); `priority` is a sparse 10-step integer so reordering edits one field instead of renumbering; `status` is a lane defined in `config.toml`; `closed` is `null` while open and stamped when a task enters the done lane; empty collections serialize as `[]`, never `null`. The JSON Schema for the index lives at [`docs/schema/furrow.index.v1.json`](docs/schema/furrow.index.v1.json) and is emitted by `furrow schema`.

---

## Command reference

All commands below are implemented and working today, including the `ui` TUI and `migrate`. (Packaging and a future web viewer are the remaining work — see [Status](#status).)

| Command | What it does | Key flags / args |
|---|---|---|
| `init` | Create a `.furrow/` store (config + empty index + `bodies/`) in the current directory | — |
| `add <title>...` | Add a task (or many from stdin with `--stdin`); assigns frozen ids and seeds `bodies/<id>.md` | `--stdin`, `-s/--status`, `-p/--priority`, `-l/--label`, `--parent`, `--dep`, `--ref`, `--body` |
| `ls` (alias `list`) | List tasks in canonical `lane -> priority -> id` order | `-s/--status`, `-l/--label`, `-n/--limit` |
| `show <id>` | Show one task plus its Markdown body | — |
| `next` | Show actionable tasks (non-terminal lane, all deps done); `--json`/`--ndjson` attach a `reason` (`in_next_lane`, `deps_satisfied`) | `-n/--limit` (use `-n1` for just the top) |
| `edit <id>` | Open `bodies/<id>.md` in `$EDITOR`; prints the path when non-interactive | — |
| `done <id>` | Move a task into the done lane (stamps `closed`) | — |
| `move <id> <lane>` | Move a task to a lane (clears `closed` when leaving done) | — |
| `reorder <id> <priority>` | Set a task's priority (sparse integer; lower sorts higher) | — |
| `check <id> [index]` | Toggle a checklist item by zero-based index, or append one | `--add <text>`, `--off` |
| `dep <id> <dep-id>` | Add a dependency (id waits on dep-id), or remove it with `--rm`; acyclic & idempotent | `--rm` |
| `archive` | Move aged done tasks to `.furrow/archive/` (preview unless `--yes`) | `--older-than <days>`, `--yes` |
| `lint` | Check index↔body 1:1, id shape, lanes, deps/parent refs, config clamp warnings | — |
| `schema` | Print the JSON Schema for `index.json` (matches the committed copy) | — |
| `version` | Print the furrow version | — |
| `ui` | Launch the interactive TUI (list + detail panes): navigate, filter, done, move lane, reorder (`K`/`J`), toggle checklist, edit body | — |
| `migrate <file>` | Import an existing `Task.md` etc. (dry-run by default; unmapped headings & `[[wikilink]]`s reported, never dropped) | `--write`, `-l/--label` |

Global flags (read/list commands): `--json` and `--ndjson`. Mutations (`done`, `move`, `reorder`, `check`, `dep`) also accept `--json`, emitting `{before, after, changed}` so a caller sees the effect without a follow-up `show`. `edit` prefers `$FURROW_EDITOR`, then `$VISUAL`, then `$EDITOR`, falling back to `vi`.

---

## Claude Code / agent integration

furrow needs no MCP server and no plugin — for a repo-local tool that is overkill. The integration is just a small `CLAUDE.md` block plus the `--json` flag. The rules:

- **Never hand-edit `index.json`.** A single deterministic marshaller owns that file; a manual edit will churn the diff (and likely lose the canonical ordering). Mutate it through the commands above.
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

---

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

[ui]
theme = "auto"                    # auto | dark | light (NO_COLOR is always respected)
```

`done` stamps `closed`; moving a task *out* of the done lane clears it. Other terminal lanes (e.g. `icebox` — parked, not finished; `waiting` — the GTD *Waiting-For* lane for work delegated or blocked on someone external) do **not** stamp `closed`, which is why parked tasks are never archived.

---

## Determinism

furrow's write path is byte-stable on purpose. Every index write goes through one marshaller (`core.Marshal`) with a fixed contract: struct-field key order, 2-space indent, `SetEscapeHTML(false)` (so CJK and `< > &` survive verbatim), empty collections as `[]` not `null`, a stable `lane -> priority -> id` sort, whole-second UTC RFC3339 timestamps, and a trailing newline. The result is that the bytes furrow writes are identical to what a human or an agent would hand-edit, so re-saving an untouched index produces **zero git churn** — diffs show only the field you actually changed.

---

## Status

- **Working:** the core domain (`internal/core`), config loader, filesystem store,
  app coordinator, the full CLI, the bubbletea **TUI** (`furrow ui`), and
  **`migrate`** (importing a legacy `Task.md`). `go test ./...` + golangci clean;
  `sh scripts/check.sh` runs the full verification (incl. a teatest TUI e2e).
- **Configured, not yet published:** the brew/nix release — GoReleaser
  config validated, but `v0.1.0` isn't tagged yet. nix `flake.nix` carries a
  placeholder `vendorHash`.
- **Future (low priority):** a read-only web viewer / React UI over `index.json`.

Design notes: architecture in [`docs/architecture.md`](docs/architecture.md),
terms in [`docs/glossary.md`](docs/glossary.md), and what furrow deliberately
doesn't do (with rationale) in [`docs/non-goals.md`](docs/non-goals.md).

---

## License

MIT © akira-toriyama
