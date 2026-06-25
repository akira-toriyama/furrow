# furrow — Glossary

> furrow's *ubiquitous language*: the canonical word for each concept, and what
> it precisely means. Use these terms in code, commits, docs, and `--json`
> field names so the same thing is never called two things. Borrowed from a
> field of soil — parallel **furrows** are the status **lanes**, and you work
> the backlog by driving one lane forward at a time.
>
> Field names below match `internal/core/task.go` exactly (the struct field
> order *is* the JSON key order — see `internal/core/marshal.go`).

## Terms

| Term | Meaning |
|---|---|
| **task** | One tracked item — furrow's unit of work. The `core.Task` struct (metadata only): `id`, `title`, `status`, `priority`, `labels`, `parent`, `deps`, `refs`, `checklist`, `created`, `updated`, `closed`, `body`. Its long-form prose is *not* a field — it lives in a separate body file. |
| **index** | The whole of `.furrow/index.json`: a `schema_version` (currently `1`) plus the `tasks` array. Structured metadata only. Machine-written through one deterministic path (`core.Marshal`); **never hand-edit it** (a manual edit churns the git diff and can break the byte-for-byte contract). Query it with `furrow ls`/`show`/`next --json`. |
| **body** | The long-form prose for one task, stored as `.furrow/bodies/<id>.md` and addressed by the task's `body` field as a *relative path* (e.g. `bodies/t-0042.md`), never inline content. Plain markdown, no escaping — **bodies are hand- and agent-editable**. The split (metadata in the index, prose in the body) is the whole point of the hybrid store. |
| **lane** (== status) | A workflow column. A task's `status` field holds one lane value; the set of lanes and their top-to-bottom order is defined by `[lanes].order` in `config.toml` (default: `inbox`, `backlog`, `ready`, `in-progress`, `waiting`, `done`, `icebox`). "Lane" and "status" are the same thing — the field is `status`, the concept is a lane. |
| **waiting** (lane) | The GTD *Waiting-For* lane: a task delegated or blocked on **someone/something external** (distinct from a `dep`, which is an internal blocking task). It is **terminal** (not in `next`) and parked — it does **not** stamp `closed` and is never archived. Move a task there with `furrow move <id> waiting`; move it back when unblocked. |
| **priority** | A *sparse* integer that orders tasks within a lane (lower sorts higher). New tasks are spaced by `[priority].step` (default 10), starting at `[priority].default` (100): 100, 110, 120… The gaps mean **reorder edits one field** instead of renumbering neighbors. Set it with `furrow reorder <id> <priority>`. |
| **id** | A task's permanent handle, e.g. `t-0042` — the `id` field and the stem of `bodies/<id>.md`. **Frozen**: assigned once from `.furrow/seq` (prefix + zero-padded counter, `[ids].prefix`/`[ids].width`), **never reused or renumbered**. Identity is stable across moves, reorders, and renames. |
| **label** | A free-form tag in a task's `labels` array (sorted + deduped on write, so a set is a set). Categorize and filter with `furrow ls -l <label>`. When `[labels].required = true` in config, every task must carry at least one label — `furrow add` rejects a label-less task (exit 2) and `furrow lint` flags one. Used e.g. by a central cross-repo tracker where the label is the owning repo. Default is not required. |
| **checklist item** | A `core.ChecklistItem` (`text` + `done` bool) inside a task's `checklist` array — a unit of work small enough to tick off without spawning its own task (furrow's take on a GitHub "Sub-issues progress" line). Append one with `furrow check <id> --add "..."`; toggle by zero-based index with `furrow check <id> <item-index>` (`--off` to uncheck). |
| **ref** | An entry in a task's `refs` array: a pointer *out* of furrow — a `file:line` location (e.g. `docs/x.md#L10`) or a URL. References context; does not affect ordering or `next`. Add with `furrow add --ref ...`. |
| **dep** (dependency) | An entry in a task's `deps` array: the **id** of another task this one waits on. A task is only actionable once *every* dep is in the done lane (an unknown or not-yet-done dep blocks it). This is what `furrow next` reasons over. Add with `furrow add --dep ...`. |
| **parent** | The optional `parent` field: the **id** of an enclosing task, for grouping sub-tasks under a larger one. Hierarchy only — it does not gate `next` the way a dep does. Omitted entirely when absent (`omitempty`). |
| **terminal lane** | A lane whose tasks are *not* actionable for `furrow next` — work that is finished or parked. The set is `[lanes].terminal` (default: `done`, `icebox`, `waiting`). The done lane is terminal *and* stamps `closed`; parked lanes like `icebox` and `waiting` are terminal but do **not** stamp `closed`. |
| **actionable** | The base property `core.Index.Actionable` computes: a task in a **non-terminal lane** with **all of its deps in the done lane**. `furrow next` shows the actionable tasks whose status is *also* in `[next].lanes` (default `ready`, `in-progress`) — so intake/planning lanes don't clutter "the work ready to pick up." Set `[next].lanes` to all non-terminal lanes to widen it. |
| **archive** | Cold storage at `.furrow/archive/` for aged done tasks, keeping the hot index small. `furrow archive` moves done-lane tasks whose `closed` is older than `[archive].older_than_days` (default 30; override with `--older-than`). It **previews by default** and only moves when you pass `--yes` (the destructive-op guard). |
| **the store** | The `.furrow/` directory as a whole: `index.json` (metadata), `bodies/<id>.md` (prose), `config.toml` (human config, read-only from furrow), `seq` (the id counter), and `archive/` (aged done tasks). It is repo-local — created by `furrow init`, committed alongside your code, designed for clean git diffs. |

## The determinism contract (why "never hand-edit the index")

`core.Marshal` is the single path that serializes the index, and the bytes it
writes are meant to equal the bytes a human or an agent would type by hand:
struct-field key order, 2-space indent, `SetEscapeHTML(false)` (so CJK and
`< > &` survive verbatim), empty arrays as `[]` (never `null`), tasks sorted
**lane → priority → id**, UTC whole-second RFC3339 timestamps, and a trailing
newline. Because of this, re-saving an untouched index produces **zero git
churn** — and a stray manual edit to `index.json` is what breaks it. Edit task
metadata through commands; edit prose in the `bodies/*.md` files.

## Don't call it

Non-canonical synonyms, mapped to the term to use. Prefer the right word.

| Don't say | Say | Why |
|---|---|---|
| ticket, issue, card, todo, item, entry | **task** | One name for the unit of work. ("Issue" is especially misleading — furrow deliberately has **no** GitHub Issues integration.) |
| database, db, store file, `tasks.json` | **the index** (`.furrow/index.json`) | It's a deterministic JSON file, not a database; the file is named `index.json`. "The store" is the whole `.furrow/` directory, not this one file. |
| description, notes, content, details, the markdown | **body** (`bodies/<id>.md`) | The prose is a separate file addressed by a relative path, not a field on the task. |
| column, state, bucket, list, category | **lane** (the `status` field) | A lane is a `config.toml`-defined status. Two words for one thing is exactly what the glossary prevents. |
| rank, order, weight, position, sort key | **priority** | The orderable field is `priority` — a sparse integer, not an array position. |
| slug, key, name, number, uuid, hash | **id** | The frozen handle is the `id` (e.g. `t-0042`). It is not derived from the title and never changes. |
| tag, category, topic | **label** (the `labels` field) | furrow calls them labels (mirroring GitHub Projects "Labels"). |
| subtask, todo, checkbox, AC, acceptance criterion | **checklist item** | An in-task `checklist` entry; a *subtask* would be a separate task with a `parent`. |
| link, citation, source, attachment | **ref** | The `refs` field — `file:line` or URL pointers out of furrow. |
| blocker, requirement, prerequisite, "depends on" (as a noun) | **dep** | The `deps` field; the ids a task waits on for `next`. |
| epic, group, milestone | **parent** | The `parent` field is a single id for hierarchy, nothing heavier. |
| closed lane, final lane, done-only | **terminal lane** | "Terminal" covers both done *and* parked (e.g. icebox); only the done lane stamps `closed`. |
| ready, available, unblocked, "the next task" | **actionable** | The precise property (non-terminal + all deps done) that `furrow next` selects on. |
| delete, purge, close out, trash | **archive** | `archive` *moves* aged done tasks to `.furrow/archive/`; it doesn't delete them, and it previews unless `--yes`. |
| `.furrow` repo, the database, the project file | **the store** | The `.furrow/` directory; "repo" is your git repository, which contains the store. |

## What's built vs. planned

Every term above is backed by shipped code (`internal/core`, `internal/app`,
`internal/cli`) **except** the two interactive/migration surfaces:

- **TUI** (`furrow ui`) — ROADMAP **Phase 6**, not yet wired. The `ui` command
  exists but is an honest stub: it returns "the TUI (furrow ui) is not
  implemented yet — see ROADMAP Phase 6; use the CLI for now". `[ui].theme`
  in `config.toml` is read and validated today but has no renderer to act on it.
- **migrate** — ROADMAP **Phase 5**, not built. There is no `furrow migrate`
  command yet (the `internal/migrate` package is an empty placeholder).

---

References (reviewed 2026-06-25): [`internal/core/task.go`](../internal/core/task.go),
[`internal/core/marshal.go`](../internal/core/marshal.go),
[`internal/config/defaults.go`](../internal/config/defaults.go),
[`internal/app/app.go`](../internal/app/app.go),
[`ROADMAP.md`](../ROADMAP.md), [`MEMO.md`](../MEMO.md). See also
[`docs/architecture.md`](architecture.md) and [`docs/non-goals.md`](non-goals.md).
