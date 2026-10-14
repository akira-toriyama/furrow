# furrow — Glossary

> furrow's *ubiquitous language*: the canonical word for each concept, and what
> it precisely means. Use these terms in code, commits, docs, and `--json`
> field names so the same thing is never called two things. Borrowed from a
> field of soil — parallel **furrows** are the status **lanes**, and you work
> the backlog by driving one lane forward at a time.
>
> Each entry is a definition, not a manual: the mechanism is the pointer it
> ends on, and every closed vocabulary (codes, kinds, keys) is printed by
> `furrow vocab <name>` rather than copied here.

## Terms

| Term | Meaning |
|---|---|
| **task** | furrow's unit of work: a **shard** of metadata plus a **body** of prose. The fields are `core.Task` (`internal/core/task.go`; struct order is key order), the schema `furrow schema task`. |
| **epic** | A **box of work**: a first-class entity (`.furrow/epics/<id>.json`, `e-` ids), not a task wearing a type. `next` scopes to the active box; furrow never auto-closes or auto-picks one — choosing stays the human's (`furrow epic --help`). |
| **standing** | An epic declaration for a permanent box: no lifecycle nags, the review cadence instead (`furrow epic set --help`). |
| **pinned** | An epic declaration: the box's actionable tasks lead `next`/`brief` regardless of the active scope (`furrow epic set --help`). |
| **mandate** / **parking lot** | The convention's reserved boxes: a mandate is standing + pinned, a parking lot standing only; furrow stays name-independent (`furrow epic set --help`). |
| **waiting** (box) | A box's derived "parked until" state: open work done, a parked member's due still ahead. The epic reads and `brief` print `⏳ waiting until <due> (<task>)`; it silences `epic_all_done` (README's `epic` command note). |
| **due** | The instant a task is **promised** for (`due`, optional) — a bare date binds the end of that day in the board's calendar. `brief` leads with it and `lint` checks it board-wide; furrow never rings ([`scheduling.md`](scheduling.md), `furrow set --help`). |
| **snooze** | Re-dating a promise from now (`set --due +1d`); on a repeating task it moves this occurrence only. `--due-shift` moves the dues tasks already carry (`furrow set --help`). |
| **repeat** | The recurrence rule a task runs on (`repeat`, one RFC 5545 RRULE, plus the immutable `repeat_anchor`). Only a **close** advances it, minting the next occurrence in the same write (`furrow done --help`); spellings: `furrow vocab repeat-spellings`; code: `internal/recur`. |
| **anchor** | A box's own **calendar day** (`anchor` on the epic) and, on a task, the epic whose day its due **follows**. Moving the day re-dates every open follower by the calendar-day delta in one previewed write; no offset is stored (`furrow epic set --help`, [non-goals.md](non-goals.md#no-stored-offset-no-cascade)). Distinct from `repeat_anchor`. |
| **follower** | A task whose due follows a box's **anchor** day (`set --anchor <epic>`; `-q anchor:<epic>` lists them). |
| **index** | The in-memory aggregate folded from the per-task **shards** — there is no `.furrow/index.json`. Query it with `ls`/`show`/`next --json` ([`architecture.md`](architecture.md#the-store)). |
| **shard** | One task's metadata on disk, `.furrow/tasks/<id>.json`, written only by `core.MarshalTask`; `epics/` and `repos/` hold shards too. One file per task is why concurrent edits merge as a union ([`architecture.md`](architecture.md#the-store)). |
| **meta** | `.furrow/meta.json` — `{"schema_version": 11}`, the board's layout version: an input to every write, never an output. An older board is read-only, a newer one refused, and only `furrow upgrade` raises it (`furrow --help`). |
| **flag day** | The moment `furrow upgrade` raises a board's **meta** version — a one-way cutover after which no older furrow (pinned CI included) can write the board. Its order and recovery are `furrow --help`'s. |
| **unknown-key passthrough** | A shard's unknown **top-level** keys are **preserved, not dropped** — and not honoured, so `lint` warns `unknown-shard-key` and `tidy --unknown-keys` prunes them. Contract and limits: `internal/core/passthrough.go`; its place beside the version gate: [`architecture.md`](architecture.md). |
| **body** | The long-form prose for one task **or epic**: `.furrow/bodies/<id>.md`, plain markdown, hand- and agent-editable. The prose writes advance `updated`; a hand edit does not (`furrow --help`). |
| **lane** (== status) | A workflow column; the `status` field holds one. The set and its order is `[lanes].order` (defaults: the repo-root `config.toml`; live: `furrow board`). |
| **waiting** (lane) | The GTD *Waiting-For* lane: blocked on someone or something external (a `dep` is the internal kind). Terminal and parked — it does not stamp `closed`. |
| **priority** | A *sparse* integer ordering tasks within a lane (lower sorts higher), computed by `reorder`/`set --before|--after`; only an exhausted gap respaces the lane (`furrow reorder --help`). |
| **id** | A task's permanent handle (`t-k3m9p`): a prefix plus a random suffix minted with no shared counter, **frozen** — never reused or renumbered ([`architecture.md`](architecture.md#frozen-collision-free-random-ids)). |
| **label** | A free-form tag in `labels` (a sorted, deduped set). Filter with `-l`; `[labels].required` makes a label-less task a validation error. A repository is **not** a label (README, "Fields, estimates and revisit"). |
| **value** / **effort** | Coarse 1..5 estimates of worth and cost, unset by default; an out-of-range score clamps and says so (`furrow value --help`). |
| **roi** | value / effort, derived and never stored: selectable as a `-q` ordinal (`roi:>=2`); `next`'s order ignores it (README, "Fields, estimates and revisit"). |
| **repo** (the `repos` field) | An `owner/repo` identifier a task relates to, 0..N per task as a set — what a **repo scope** filters on. |
| **repo scope** | The repo filter a read applies: `-r` (`-r ''` = the whole board), else the scope repo the board was reached with (the help texts' *board scope*) — a pointer's or `[[board]]`'s, else **default_repo**. `add` unions it into `repos` (README, "Discovery precedence"). |
| **draft** | A task whose `repos` set is empty — the GitHub-Issues-draft analogue: `add --draft` makes one, `ls --drafts` lists them, and a read whose **repo scope** hides them says so on stderr. |
| **checklist item** | A `text` + `done` entry in `checklist`, too small for its own task (`furrow check --help`). An unticked item never blocks `done`: a close asserts the body's completion condition, not the boxes. |
| **ref** | An entry in `refs`: a pointer *out* of furrow (`file:line` or URL). Context only; never affects `next`. |
| **asset** | A file of any kind attached with `furrow attach` into `.furrow/bodies/assets/` and shown from the body by a relative markdown line. It moves by the **hold rule**: a store loses a file only when nothing there still shows or owns it (`internal/app/asset_hold.go`). |
| **dep** (dependency) | An entry in `deps`: the id of another task this one waits on. A task is actionable only once every dep is in the done lane; `blocked_by` names the undone ones (`furrow dep --help`). |
| **epic** (the task field) | The optional `epic`: the box a task is filed under (0..1). Membership only, never a gate; `lint` errors `epic-required` on an open, unfiled task once the board has any box (`furrow set --help`). |
| **link** | A `[[<id>]]` reference in body prose; only the brackets count (a bare id is not a link; one inside code is inert). Human synonym: "mention"; definition: `internal/core/links.go`. |
| **backlink** | The reverse view of a **link**: the tasks whose body links to a given one (`show --backlinks`, `mentioned_by` under `--json`). |
| **terminal lane** | A lane whose tasks are not actionable: finished or parked (`[lanes].terminal`). The done lane is terminal *and* stamps `closed`; parked lanes do not (`furrow board`). |
| **close** | Moving a task into the done lane, which stamps `closed`. A close asserts the completion condition is **met or gone** (the reason in `done --note`); there is no cancelled close ([non-goals.md](non-goals.md#no-cancelled-close)), and parking and `rm` are separate verbs. |
| **actionable** | A task in a non-terminal `[next].lanes` lane with every dep in the done lane (`ls --actionable`, `is:actionable`). `next` further scopes them to the active box (`furrow next --help`). |
| **typed query** (`-q`) | One filter string on every filtering read and, as a selector, on the batch mutators: an AND-list of `field:value`, presence, comparison, graph and `is:` terms. Grammar: README, "Typed query — `-q`"; vocabularies: `furrow vocab query-qualifiers` / `query-presence` / `query-is`. |
| **revisit** | The read-only re-evaluation query, the counterpart to `next`: open tasks whose metadata may be out of date, each with its signals (`furrow vocab revisit-codes`). An empty result is healthy (`furrow revisit --help`). |
| **stale** | The revisit signal for an open task whose `updated` is older than `[revisit].stale_days` — the one keyed on elapsed time (`furrow revisit --help`). |
| **review / reviewed / unreviewed** | `furrow review` stamps a review clock on a repo, a task or a **standing** box; **unreviewed** is the repo nudge `sync`/`brief` raise once the last human review is stale. Never-reviewed stays quiet (`furrow review --help`). |
| **archive** | Cold storage for aged done tasks at `.furrow/archive/`, a sibling sharded store (`furrow archive`; `unarchive` is the way back). A done task a staying task depends on is **held** (`furrow archive --help`). Deletion is a different verb: **rm**. |
| **the store** | The `.furrow/` directory as a whole, in a git repo next to the code or in a tracker repo of its own (its files: [`architecture.md`](architecture.md#the-store)). |
| **central board** | A `.furrow` **outside** the repos it backs, reached by configuration (`[[board]]`, a **pointer**, `FURROW_BOARD`, `FURROW_DIR`) — the **layout** opposite of a **repo-local board**, orthogonal to the **mode**. It can back many repos (README, "Central board"). |
| **repo-local board** | A `.furrow/` inside the repo it serves, found by walking up from cwd. It declares no **repo scope**, so a board's **default_repo** is the fallback (README, "Discovery precedence"). |
| **shared board** | A board with a git **remote** and several writers, kept converged by **sync** — the default **mode**, declared by omission, and why raising the layout is a **flag day**. Says nothing about layout. |
| **standalone** (`mode = "standalone"`) | A board on one machine, under its own git, with no remote — the mode opposite of a **shared board**. Wording only; back it up with **autocommit** (README, "Standalone: a local board with no remote"). |
| **scope** (a `[[board]]`'s `scopes`) | The directory subtree under which a `[[board]]` entry (or `FURROW_BOARD`) activates; the longest match wins, and outside every scope no central board resolves (README, "User-level config (no per-repo file)"). |
| **store-to-be** | A configured directory with no store in it yet — empty, or only dot-entries other than `.furrow`. The first write stamps it; any other non-store directory is refused (README, "Discovery precedence"). |
| **pointer** | A `.furrow-pointer.toml` at a repo root redirecting to a central board, optionally with a `default_repo`. It wins over the user-level boards and loses to a nearer or same-level `.furrow` (README, "Per-repo pointer"). |
| **default_repo** | Two keys, one name: a **pointer**'s **repo scope** (`"auto"` allowed), and a board's own fallback repo scope in `config.toml` — a literal `owner/repo` that never outranks a pointer or `[[board]]` (README, "Discovery precedence"). |
| **doctor** | The machine-wide board-setup health check: what is *wrong* and how to fix it (`boards` lists what is configured). Read-only, network-free, stable codes (`furrow vocab doctor-codes`; `furrow doctor --help`). |
| **auto-filter** | The per-`[[board]]` `auto_filter` switch: whether the filtering reads apply the board's **repo scope** (writes attach regardless). A pointer and a board's own **default_repo** always filter (README, "User-level config (no per-repo file)"). |
| **autocommit** | The per-machine `[[board]]` switch that git-commits `.furrow/` after every mutating command but `config set`, `migrate` and `edit` in `$EDITOR` — the standalone board's backup. Keyed by the store, best-effort, never fetches or pushes (README, "User-level config (no per-repo file)"). |
| **session write guard** | Under Claude Code, a task or epic write touching a repo an **earlier-started** session on this machine sits in is refused while that session works (`session-busy`) and warned while it idles; the escape is `add --draft` ([`architecture.md`](architecture.md#the-session-write-guard)). |
| **sync** | The shared-board ritual as one command: a scoped auto-commit of `.furrow/`, then `fetch` + `rebase --autostash @{u}` + `push` — a thin git wrapper, no daemon. Shards both sides wrote in *different* fields merge field by field (`furrow sync --help`; `furrow vocab sync-progress-keys` / `incoming-kinds`). |
| **envelope** | The JSON object around a command's result: a mutation's `{before, after, changed}` on stdout, or `{"error": {…}}` on stderr (`furrow --help`). |
| **kind** | The error envelope's stable kebab-case failure class — branch on it, never on the message or exit code (`furrow vocab error-kinds`). |
| **retryable** | The error envelope's flag: `true` means re-running the same command is the documented recovery; anything else is terminal (`furrow --help`). |
| **candidates** | The array an error envelope adds when an input *almost* resolved, so a caller picks an alternative instead of parsing prose (README, "Claude Code / agent integration"). |
| **preview** | What a destructive operation does without `--yes`: report what would change, write nothing. The one list is `furrow --help`'s. |
| **alias** | A board `[alias]` entry naming a frequent command string, expanded git-style; a real command always wins (README, "Configuration"). |

## The determinism contract (why "never hand-edit a shard")

`core.MarshalTask` is the single path that serializes a shard, and its bytes
equal a hand-edit's, so `Save` rewrites only changed shards and an untouched
store is **zero git churn**. A stray manual edit breaks that, and a misspelled
key in it survives (the **unknown-key passthrough**) until `tidy --unknown-keys`.
Edit metadata through commands and prose in `bodies/*.md`; the byte recipe is
`MarshalTask`'s doc comment, its guards [`architecture.md`](architecture.md#how-the-invariant-is-guarded).

## Don't call it

Non-canonical synonyms, mapped to the term to use. A ban stands only for a
word the shipped `--help` and README avoid for that concept.

| Don't say | Say | Why |
|---|---|---|
| ticket, issue, card, todo, item, entry | **task** | One name for the unit of work — and furrow is an *alternative* to GitHub Issues, not a client of them. |
| database, db, store file, `index.json`, `tasks.json` | **the index** / a **shard** | Deterministic per-task JSON files, not a database or one file (**index**, **shard**, **the store** above). |
| description, notes, content, details, the markdown | **body** (`bodies/<id>.md`) | The prose is its own file, not a field on the task. |
| state, bucket, list, category | **lane** (the `status` field) | Two words for one thing is what this table prevents. |
| rank, order, weight, sort key | **priority** | The field is `priority`, a sparse integer, not an array index. |
| slug, key, name, number, uuid, hash | **id** | The frozen handle, never derived from the title. |
| category, topic | **label** (the `labels` field) | GitHub Projects' word for a pure tag; a task's repository is **not** a label. |
| repository, project (as task metadata), repo label | **repo** (the `repos` field) | A first-class `owner/repo` set, not a label convention; "project" is GitHub's word. |
| draft issue, unattached task | **draft** (`repos: []`) | One name for a task with an empty `repos` set. |
| subtask, todo, checkbox, AC, acceptance criterion | **checklist item** | The only in-task breakdown; anything bigger is its own task in the same epic. |
| citation, source, external link | **ref** | A `refs` pointer *out* of furrow; a task→task `[[id]]` is a **link**. |
| upload, screenshot, media file | **asset** | A file from `furrow attach`, shown from the body — never a `refs` entry. |
| see-also, cross-reference | **link** (a `[[id]]` in a body) / **backlink** (its reverse view) | "Mention" is fine; the person-directed `@mention` is unbuilt ([`non-goals.md`](non-goals.md#built-vs-planned--honesty-note)). |
| blocker, requirement, prerequisite, "depends on" (as a noun) | **dep** | The `deps` field; what `next` waits on. |
| initiative, project (as a box of work) | **epic** (the entity) | A first-class entity, not a task wearing a type. |
| sub-project, hierarchy | **epic membership** (the `epic` field) | One level, task → box; epics do not nest. |
| deadline, ETA, target date, reminder, due_date | **due** (the `due` field) | When a task is promised for; furrow reports it, never rings. |
| milestone | **anchor** (an epic's `anchor` field) | A box's reference day its followers' dues count from; nothing is due on it unless a task says so. |
| closed lane, final lane, done-only | **terminal lane** | Done *and* parked; only done stamps `closed`. |
| close out | **archive** | Moves aged done tasks to cold storage; it never deletes them. |
| purge, trash | **rm** (`furrow rm` / `epic rm`) | The one real deletion, for a filing that should not have been made (`furrow rm --help`). |
| migration, auto-upgrade, "furrow will convert it" | **flag day** (`furrow upgrade`) | A human-ordered one-way cutover, never a write's side effect ([`non-goals.md`](non-goals.md#no-automatic-schema-migration)). |
| `.furrow` repo, the database, the project file | **the store** | The `.furrow/` directory; "repo" is the git repository holding it. |
| hosted board, hosted-board-only, hosted-board-shaped | **shared board** | "Hosted" already names the cloud-backend non-goal. |
| a shared tracker repo (as the thing a standalone board lacks) | **a remote you can push to** | A standalone board lacks hosting, not repos — it may back several. |
| multi-machine board (as a noun) | **shared board** | Fine as an inline gloss ("a shared (multi-machine) board"); not as a second name. |
| global default board, central task tracker, 中央ボード, global 既定ボード | **central board** | One name per axis, so a grep finds every use. |
| shared checkout | **co-located checkout** | Several operators in ONE working tree, in any mode — a near-homograph of **shared board**. |

## What's built

Every term above names shipped code: the packages are
[`architecture.md` § Package responsibilities](architecture.md#package-responsibilities);
furrow is CLI-only, and the TUI (**ridge**) and GUI (**vista**) are separate
front-end repos ([`non-goals.md`](non-goals.md#any-ui-is-a-separate-front-end-not-part-of-this-repo)).

---

References: [`internal/core/task.go`](../internal/core/task.go),
[`internal/core/marshal.go`](../internal/core/marshal.go),
[`internal/config/defaults.go`](../internal/config/defaults.go),
[`internal/app/app.go`](../internal/app/app.go). See also
[`docs/architecture.md`](architecture.md) and [`docs/non-goals.md`](non-goals.md).
