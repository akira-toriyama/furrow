# furrow — Glossary

> furrow's *ubiquitous language*: the canonical word for each concept, and what
> it precisely means. Use these terms in code, commits, docs, and `--json`
> field names so the same thing is never called two things. Borrowed from a
> field of soil — parallel **furrows** are the status **lanes**, and you work
> the backlog by driving one lane forward at a time.
>
> Field names below match `internal/core/task.go` exactly (the struct field
> order *is* the JSON key order — see `internal/core/marshal.go`). Each entry
> is a definition, not a manual: the mechanism behind it is `furrow <cmd>
> --help`, README, or [`architecture.md`](architecture.md), and every closed
> vocabulary (lanes, codes, keys) is printed by the hidden `furrow vocab
> <name>` rather than copied here.

## Terms

| Term | Meaning |
|---|---|
| **task** | One tracked item — furrow's unit of work. The `core.Task` struct (metadata only): `id`, `title`, `status`, `priority`, `value`, `effort`, `labels`, `repos`, `deps`, `refs`, `checklist`, `created`, `updated`, `closed`, `reviewed`, `body`, `epic`, `due`, `repeat`, `repeat_anchor`. Its prose is *not* a field — it lives in a separate **body** file. |
| **epic** | A **box of work**: a first-class entity (`.furrow/epics/<id>.json`, `e-` ids), not a task wearing a type. It carries a one-line `goal`, free-form `meta`, an `active` flag, `deps` on other boxes (the order boxes open in — information, not enforcement), and the `standing`/`pinned` channel declarations. `next` scopes to the active box, `ls --tree` groups by box, and furrow never auto-closes or auto-picks one — choosing stays the human's. |
| **due** | The instant a task is **promised** for (`due`, optional). An INSTANT, not a day: a bare date binds the end of that day in the board's calendar; `+1d` is the snooze. `brief` leads with it and `lint` is its board-wide twin (`due-overdue` an error, `due-today` a warn); furrow never rings — a date is state a read reports ([`scheduling.md`](scheduling.md)). |
| **repeat** | The recurrence rule a task runs on (`repeat`, one RFC 5545 RRULE line compiled from a short spelling, plus the immutable `repeat_anchor` its first `due` set). Only a **close** advances it: the next occurrence is minted in the same write and the rule handed to it, so exactly one live task carries a series and a snooze moves this occurrence without re-latticing. Spellings, anchor rules and calendar edge cases: `add --help` and README; code: `internal/recur`. |
| **index** | The in-memory aggregate folded from the per-task **shards** — there is no `.furrow/index.json`. Query it with `ls`/`show`/`next --json`; never hand-edit its shards. |
| **shard** | The metadata for one task on disk: `.furrow/tasks/<id>.json`, a bare `core.Task` object, 1:1 with its `bodies/<id>.md`, machine-written by `core.MarshalTask`. One file per task is why concurrent edits across worktrees merge as a union. |
| **meta** | `.furrow/meta.json` — `{"schema_version": 10}`, the board-wide layout version in its own file. The number is the **board's**, an input to every write, never an output: an older board is read-only, a newer one refused, and only `furrow upgrade` raises it (the two-sided version gate, [`architecture.md`](architecture.md)). |
| **flag day** | The moment a board's **meta** version is raised by `furrow upgrade` — a one-way, coordinated cutover after which no older furrow (a pinned CI included) can write the board. furrow cannot see the pins, so the order is a human's: release, bump every pin, then upgrade. No downgrade; recovery is `git revert`. |
| **unknown-key passthrough** | A shard's **top-level** keys this binary does not know are **preserved, not dropped**: `core.Unmarshal*` parks them and `core.Marshal*` re-emits them. The gate stops a *bumped* layout from being misread; the passthrough stops an *unbumped* one from being destroyed — and preserving is not honouring, so `lint` warns `unknown-shard-key`. Limits and proofs: `internal/core/passthrough.go`. |
| **extras** | The unexported carrier the passthrough parks unknown keys in. Not a field on disk and never in a `--json` view (which is why `Task` must never grow a `MarshalJSON` method); `ExtraKeys()` is the only way out, reading a file the only way in. |
| **body** | The long-form prose for one task **or epic**: `.furrow/bodies/<id>.md`, addressed by the `body` field as a relative path. Plain markdown, hand- and agent-editable; `note` appends and advances `updated`, `edit --body` replaces. Which entity a ref names is store membership, never the id prefix. |
| **lane** (== status) | A workflow column; the `status` field holds one. The set and its top-to-bottom order is `[lanes].order` in `config.toml` (the shipped defaults are in the repo-root `config.toml`). |
| **waiting** (lane) | The GTD *Waiting-For* lane: blocked on someone or something external (a `dep` is the internal kind). Terminal and parked — it does not stamp `closed`. |
| **priority** | A *sparse* integer ordering tasks within a lane (lower sorts higher, `[priority].step` apart). `reorder`/`set --before|--after` compute it; only an exhausted gap respaces the lane, reported in the envelope's `renumbered`. |
| **id** | A task's permanent handle (`t-k3m9p`): `[ids].prefix` + a random Crockford-base32 suffix, generated locally with no shared counter, **frozen** — never reused or renumbered. Legacy numeric ids (`t-0042`) coexist. |
| **label** | A free-form tag in `labels` (a sorted, deduped set). Filter with `-l`; `[labels].required` makes a label-less task a validation error. A repository is **not** a label. |
| **repo** (the `repos` field) | An `owner/repo` identifier a task relates to, 0..N per task as a set. `-r` scopes reads (full name, or a short name resolved uniquely against the board's repos); an active **central board** unions its scope repo on `add`. |
| **draft** | A task whose `repos` set is empty — the GitHub-Issues-draft analogue, first-class: `add --draft` makes one, `ls --drafts` lists them, `revisit` flags `no_repo`, and a scoped read that hides them says so on stderr. |
| **checklist item** | A `text` + `done` entry in `checklist`, ticked with `furrow check` — too small for its own task. An unticked item never blocks or warns on `done` (a close asserts the body's completion condition, not the boxes); `show` prints the tally as `checklist: N/M`. |
| **ref** | An entry in `refs`: a pointer *out* of furrow (`file:line` or URL). Context only; never affects `next`. |
| **asset** | A media file attached with `furrow attach`, copied to `.furrow/bodies/assets/<id>-<name>` and shown from the body by a relative markdown line. It moves by the **hold rule** (`internal/app/asset_hold.go`): a store loses a file only when nothing remaining there shows or owns it, which every verb reports. `lint` warns on dangling, orphan and oversized assets. |
| **dep** (dependency) | An entry in `deps`: the id of another task this one waits on. A task is actionable only once every dep is in the done lane — what `next` reasons over. |
| **epic field** (on a task) | The optional `epic`: the box this task is filed under (0..1). Membership only — it never gates like a dep — and `lint` errors `epic-required` on an open, unfiled task once the board has any box. |
| **link** | A `[[<id>]]` reference in body prose, resolved by the one definition in `internal/core/links.go`: only the brackets count (a bare id is not a link; one inside code is inert). Powers `show --backlinks` and `lint`'s dangling-link warn. Human synonym: "mention". |
| **backlink** | The reverse view of a **link** — the tasks whose body links to a given one ("Mentioned in", `mentioned_by` under `--json`), computed by scanning local bodies. |
| **terminal lane** | A lane whose tasks are not actionable: finished or parked (`[lanes].terminal`). The done lane is terminal *and* stamps `closed`; parked lanes are terminal and do not. |
| **actionable** | A task in a non-terminal lane with every dep in the done lane (`core.Index.Actionable`). `next` shows the actionable tasks whose lane is also in `[next].lanes`. |
| **typed query** (`-q`) | One filter string on every filtering read and, as a write-side selector, on the batch mutators: a flat AND-list of `field:value` terms (comma = OR, leading `-` = NOT), `has:`/`no:` presence, ordinal and date comparisons, graph edges, free text, and `is:` computed flags (`furrow vocab query-qualifiers` / `query-presence` / `query-is`). Full grammar: README's "Typed query". |
| **revisit** | The read-only re-evaluation query, the counterpart to `next`: open tasks whose metadata may be out of date, each with **revisit signals** (`furrow vocab revisit-codes`). It never mutates; an empty result is healthy. |
| **stale** | The revisit signal for an open task whose `updated` is older than `[revisit].stale_days` — the one keyed on elapsed time. |
| **review / reviewed / unreviewed** | `furrow review <repo>` stamps a per-repo clock (its review shard, `.furrow/repos/`); `reviewed` is also a per-task and, for a **standing** box, a per-epic stamp; **unreviewed** is the resulting repo nudge on `sync`/`brief` once the last HUMAN review is older than `[review].stale_after_days`. A never-reviewed repo or box stays quiet — the first review opts it in. |
| **board git state** | What `furrow board` reports about the board repo from local knowledge only: HEAD, whether `.furrow/` is dirty, ahead/behind, and a `state` from `doctor`'s vocabulary (`furrow vocab doctor-codes`). `ok` needs a tracking ref, so a standalone board reads `no-upstream`; read the **mode** from `mode`, not from this. |
| **archive** | Cold storage at `.furrow/archive/`, a sibling sharded store for aged done tasks (`furrow archive`, previews unless `--yes`; `--archived` on the reads). `unarchive` is the round trip back; assets follow the hold rule. Deletion is a different verb: **rm**. |
| **the store** | The `.furrow/` directory as a whole — shards, `meta.json`, bodies, assets, `config.toml`, the `repos/` review shards, `archive/` — living in a git repo next to the code or in a tracker repo of its own. |
| **central board** | A `.furrow` that lives **outside** the repos it backs, reached by configuration (a user-level `[[board]]` entry, a per-repo **pointer**, or `FURROW_BOARD`) — the **layout** opposite of a **repo-local board**. It can back many repos (the count is a consequence) and injects a scope **repo**; orthogonal to the **mode**. `furrow board`'s `layout` says how this invocation reached the board. |
| **repo-local board** | A `.furrow/` inside the repo it serves, found by walking up from cwd — the arm that outranks every configured one (a stray `furrow init` shadows the central board; `doctor` reports it). It declares no scope of its own, which is what a board's **default_repo** fallback is for. |
| **shared board** | A board with a git **remote**, written by several parties (other checkouts, a co-located session, every repo's pinned CI) and kept converged by **sync**. The default **mode**, declared by omission; it is why raising the layout is a **flag day** and why `due-overdue` ships as an error. Says nothing about layout or repo count. |
| **standalone** (`mode = "standalone"`) | A board on one machine, under its own git, with no remote — the mode opposite of a **shared board**. Wording only, never behaviour (the write gate and bytes are identical); pair it with **autocommit** for backup. Reports `no-upstream` to `board`/`doctor`. |
| **scope** | The directory subtree under which a `[[board]]` entry activates; the longest matching scope wins. Outside every scope furrow behaves as if no board were configured. |
| **pointer** | A `.furrow-pointer.toml` at a repo root redirecting to a central board, optionally with a `default_repo`. Wins over the user-level boards, loses to a real local `.furrow`. |
| **default_repo** | Two keys, one name: in a **pointer**, the per-repo scope (`"auto"` allowed); in a board's own committed `config.toml`, that board's *fallback* scope, applied only when discovery ran an arm that declares none (`FURROW_DIR`, a local `.furrow`) — a literal `owner/repo` only, and it never outranks a pointer or `[[board]]`. |
| **doctor** | The machine-wide board-setup health check: what is *wrong* and how to fix it (`boards` lists what is configured). Read-only, network-free, stable codes (`furrow vocab doctor-codes`), exit 1 = problems found. |
| **auto-filter** | The per-`[[board]]` `auto_filter` switch: whether the filtering reads scope to the board **repo** (writes attach regardless). A pointer and a board's own **default_repo** always filter. |
| **autocommit** | The per-`[[board]]`, per-machine switch that git-commits `.furrow/` after every mutating command — the standalone board's backup guarantee. Reuses **sync**'s partition rule, best-effort (a failure warns), never fetches or pushes. |
| **session write guard** | The refusal or warning raised under Claude Code when a task or epic write touches a repo an **earlier-started** session on this machine sits in: busy → exit 2 `session-busy`; idle → the write lands with a `session_warn`. First come, first served; the escape is `add --draft`; it stands down and says so rather than refuse on a guess. Code: `internal/app/session_guard.go` + `internal/claudecode`. |
| **sync** | The shared-board ritual as one command: auto-commit scoped to `.furrow/` (machine-written files and furrow-written or opted-in bodies; a co-located operator's prose is left pending and reported), then `fetch` + `rebase --autostash @{u}` + `push`. A thin git wrapper — git is the transport; no daemon. Its progress keys (`furrow vocab sync-progress-keys`; `complete` is the fully-published flag), `incoming` kinds (`furrow vocab incoming-kinds`), `revisit` summary keys (`furrow vocab revisit-summary-keys`) and failure kinds (`furrow vocab error-kinds`) are the contract README documents. |

## The determinism contract (why "never hand-edit a shard")

`core.MarshalTask` is the single path that serializes a shard, and its bytes
equal what a human would type by hand (struct-field key order, 2-space indent,
`SetEscapeHTML(false)`, `[]` not `null`, sorted sets, UTC whole-second
timestamps, trailing newline), so `Save` rewrites only the shards whose bytes
changed and an untouched store is **zero git churn**. A stray manual edit is
exactly what breaks it — and since **unknown-key passthrough**, a misspelled
key in a hand-edited shard is preserved forever (`lint` warns
`unknown-shard-key`; `tidy --unknown-keys` is the exit). Edit metadata through
commands; edit prose in `bodies/*.md`. The guards that freeze the recipe are in
[`architecture.md`](architecture.md).

## Don't call it

Non-canonical synonyms, mapped to the term to use. Prefer the right word.

| Don't say | Say | Why |
|---|---|---|
| ticket, issue, card, todo, item, entry | **task** | One name for the unit of work. ("Issue" is especially misleading — furrow is an *alternative* to GitHub Issues, not a client of them; there is deliberately no Issues integration.) |
| database, db, store file, `index.json`, `tasks.json` | **the index** (the in-memory aggregate) / a **shard** (`.furrow/tasks/<id>.json`) | It's a set of deterministic JSON files, not a database, and not one monolithic file; each task is its own shard. "The index" is the aggregate folded from them; "the store" is the whole `.furrow/` directory. |
| description, notes, content, details, the markdown | **body** (`bodies/<id>.md`) | The prose is a separate file addressed by a relative path, not a field on the task. |
| column, board/project column, state, bucket, list, category | **lane** (the `status` field) | A lane is a `config.toml`-defined status. Two words for one thing is exactly what the glossary prevents (GitHub Projects' "column" included). |
| rank, order, weight, position, sort key | **priority** | The orderable field is `priority` — a sparse integer, not an array position. |
| slug, key, name, number, uuid, hash | **id** | The frozen handle is the `id` (e.g. `t-0042`). It is not derived from the title and never changes. |
| tag, category, topic | **label** (the `labels` field) | furrow calls them labels (mirroring GitHub Projects "Labels"). A label is a *pure* tag — the repository a task belongs to is **not** a label. |
| repository, project (as task metadata), repo label | **repo** (the `repos` field) | The `owner/repo` identifier(s) a task relates to — a first-class field, not a label convention. "Project" is GitHub's word; furrow's per-task attachment is `repos` and the whole tracker is the **board/store**. |
| draft issue, repo-less task, unattached task | **draft** (`repos: []`) | The issue-draft analogue has one name: a task with an empty `repos` set. |
| subtask, todo, checkbox, AC, acceptance criterion | **checklist item** | An in-task `checklist` entry — the only in-task breakdown (a bigger sub-item is its own task, filed in the same epic). |
| citation, source, external link | **ref** | The `refs` field — a `file:line` or URL pointer *out* of furrow. A task→task `[[id]]` reference is **not** a ref — that is a **link** (next row). |
| attachment, upload, screenshot, media file | **asset** | A file attached with `furrow attach`, stored at `.furrow/bodies/assets/<id>-<name>` and referenced from the body by a relative markdown line — never an entry in `refs`. |
| mention, wikilink, see-also, cross-reference, "mentioned in" | **link** (a `[[id]]` in a body) / **backlink** (its reverse "who points at me" view) | A task→task reference written as `[[id]]` in body prose is a **link**; the reverse view that `show --backlinks` renders is a **backlink**. "Mention" / "Mentioned in" is the human-facing synonym for the same pair. Note `@mention` (a *person*-directed notation) is **not** this — that one is unbuilt and on the roadmap (see [`non-goals.md`](non-goals.md)), not shipped. |
| blocker, requirement, prerequisite, "depends on" (as a noun) | **dep** | The `deps` field; the ids a task waits on for `next`. |
| epic, initiative, project (as a box of work) | **epic** (the entity) | A first-class entity (`furrow epic add`), not a task wearing a type; tasks file under it via the `epic` field (`-e`). (v5 modelled this as `type=epic` + `parent` edges — v6 replaced both.) |
| group, sub-project, hierarchy | **epic membership** (the `epic` field) | One level of grouping: task → box. Epics do not nest, so there is no deeper hierarchy to name. |
| deadline, ETA, target date, reminder, due_date | **due** (the `due` field) | One name for "when this is promised for". Not a *deadline* in the contractual sense and not a *reminder* in the notifying sense — furrow rings nothing; `brief` and `lint` report it when you run them. |
| milestone | **(deferred)** | A time-box is a separate axis from the scope-box an epic is; furrow has no first-class milestone yet (label it, e.g. `m6`). Not an epic. A per-task **due** date is not a milestone either: it dates one task, not a set. |
| closed lane, final lane, done-only | **terminal lane** | "Terminal" covers both done *and* parked (e.g. icebox); only the done lane stamps `closed`. |
| ready, available, unblocked, "the next task" | **actionable** | The precise property (non-terminal + all deps done) that `furrow next` selects on. |
| close out, fold, retire | **archive** | `archive` *moves* aged done tasks to `.furrow/archive/`; it doesn't delete them, and it previews unless `--yes`. |
| delete, purge, trash, withdraw | **rm** (`furrow rm` / `epic rm`) | The one real deletion: shard, body, and the assets nothing else still shows gone, git history the only way back. For a filing that should not have been made — never for finished work (that is `archive`) and never for parking (that is the icebox lane). Refused while anything still references the target (kind `referenced`); `--force` severs; previews unless `--yes`. A target carrying a live **repeat** rule takes its whole series with it — disclosed, not refused. |
| migration, auto-upgrade, "furrow will convert it" | **flag day** (`furrow upgrade`) | Raising a board's layout is a deliberate, coordinated, one-way cutover a human orders — never something a write does for you in the background. Calling it a "migration" is what makes people expect it to just happen. |
| `.furrow` repo, the database, the project file | **the store** | The `.furrow/` directory; "repo" is your git repository, which contains the store. |
| hosted board, hosted-board-only, hosted-board-shaped | **shared board** | One axis, one word. In docs/ "hosted" already means the cloud-backend non-goal, so borrowing it for "has a git remote" collides with a term that is doing other work. |
| a shared tracker repo (as the thing a standalone board lacks) | **a remote you can push to** | The blocker on a work machine is hosting, not repo count — a standalone board may back several repos, and the documented recipe registers one through the same `[[board]]` mechanism a central board uses. |
| multi-machine board (as a noun) | **shared board** | Fine as an inline gloss ("a shared (multi-machine) board"); not as a second name. |
| global default board, central task tracker, 中央ボード, global 既定ボード | **central board** | One name for the store-location axis, so a grep finds every discussion of it. |
| shared checkout | **co-located checkout** | Several operators in ONE working tree on ONE machine — true of a standalone board too, and a near-homograph of **shared board**. |

## What's built

Every term above is backed by shipped code (`internal/core`, `internal/app`,
`internal/cli`, `internal/gitrepo`) — furrow is **CLI-only**; the sole
presentation layer is `internal/cli`. Any interactive TUI/GUI is a separate
front-end repo that drives furrow through its CLI/JSON contract (planned:
**ridge**, a charm-v2 TUI).

---

References: [`internal/core/task.go`](../internal/core/task.go),
[`internal/core/marshal.go`](../internal/core/marshal.go),
[`internal/config/defaults.go`](../internal/config/defaults.go),
[`internal/app/app.go`](../internal/app/app.go). See also
[`docs/architecture.md`](architecture.md) and [`docs/non-goals.md`](non-goals.md).
