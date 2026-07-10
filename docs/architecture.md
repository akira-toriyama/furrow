# furrow — architecture

furrow is a clonable, git-native, plain-text task tracker — an alternative to
GitHub Projects/Issues — written in Go (module
`github.com/akira-toriyama/furrow`, Go 1.25+). One board can back many repos (a
central board, each task carrying its repos as first-class `owner/repo`
identifiers) or live repo-local in a single repo's `.furrow/`. This document
describes how the
code is organized, why the layers are shaped the way they are, and which
invariants hold the design together. It is the canonical reference for the
package layout; the storage *rationale* lives in [`non-goals.md`](non-goals.md).

For terms, see [`glossary.md`](glossary.md); for explicit non-goals, see
[`non-goals.md`](non-goals.md).

---

## Dependency layers

furrow follows a hexagonal (ports-and-adapters) layout. Dependencies point
**inward**: presentation depends on the coordinator, which depends on adapters,
which depend on the pure domain. The domain depends on nothing but the standard
library.

```
                         cmd/furrow/main.go
                     os.Exit(cli.Execute())
                                 |
              +------------------+------------------+
              |                                     |
              v                                     v
     internal/cli (cobra)                  internal/tui (bubbletea v1)
     command/flag parsing,                 interactive UI (furrow ui)
     human/JSON rendering
              |                                     |
              +------------------+------------------+
                                 |  (every mutation & query)
                                 v
                          internal/app
                  the ONE mutation funnel
              (Store + Config + Clock coordinator)
                                 |
              +------------------+------------------+----------------------+
              |                  |                  |                      |
              v                  v                  v                      v
     internal/config   internal/store/fsstore  internal/store/memstore  internal/gitrepo
     read config.toml  the ONLY FS package      in-memory fake           git subprocess
     (clamp, no write) (atomic write,           (tests, dry-runs)        adapter (sync;
                         lazy body load,                                 implements no
                         random ids)                                     core port)
              |                  |                  |
              +------------------+------------------+
                                 |  (implement core ports)
                                 v
                          internal/core
                  PURE domain: Index/Task structs,
                  the core.MarshalTask/MarshalMeta paths,
                  ports (Store, Clock), validate, index ops
                  imports: stdlib only

   leaves (imported where needed, depend on nothing internal of note):
     internal/schema   JSON Schema source ( `furrow schema [task|meta]` )
     internal/version  build version string (ldflags-injected)
     internal/migrate  pure Task.md parser behind `furrow migrate`
```

A dependency arrow means "imports". Note what is **absent**: `internal/core`
imports no other furrow package and no third-party library; `internal/cli` and
`internal/tui` never import a store adapter or `internal/core`'s siblings
directly for mutation — they go through `internal/app`.

### Package responsibilities

| Package | Role |
|---|---|
| `cmd/furrow/main.go` | Entry point. Just `os.Exit(cli.Execute())` — no logic. |
| `internal/cli` | cobra adapter: parse flags, call `app`, render (human table or `--json`/`--ndjson`), map errors to exit codes. Holds no task logic. |
| `internal/tui` | bubbletea v1 interactive UI (`furrow ui`): list + glamour detail, navigate / filter / done / move / reorder (`K`/`J`) / checklist toggle / edit body. |
| `internal/app` | Coordinator. Wires a `Store` + `Config` + `Clock`; exposes every mutation/query as a method. The **only** place that mutates state. |
| `internal/config` | Loads `.furrow/config.toml` (read-only, clamp-don't-reject). Produces an effective `Config`. |
| `internal/store/fsstore` | The **only** package that touches the filesystem for the store: atomic writes, lazy body load, random id generation. |
| `internal/store/memstore` | In-memory `core.Store` for tests and `migrate --dry-run`. A normal non-test package. |
| `internal/gitrepo` | git subprocess adapter behind `furrow sync` (command assembly + error classification). Driven only through `internal/app`; the store files themselves stay fsstore-owned. |
| `internal/core` | Pure domain: `Index`/`Task`/`ChecklistItem` structs, the `MarshalTask`/`MarshalMeta` serializers (and the in-memory `Marshal`), the `Store`/`Clock` ports, `Validate`, and in-memory index ops. |
| `internal/schema` | The JSON Schemas for a task shard and `meta.json` as Go constants; emitted by `furrow schema [task|meta]`. |
| `internal/migrate` | Pure parser (stdlib only) behind `furrow migrate`: hand-maintained `Task.md` in, tasks + LOUD warnings for anything unmappable out. The CLI wires it to the store; dry-run by default. |
| `internal/gittest` | Test-only helper: `Isolate()` neutralizes global/system git config at the process-env level (called from `TestMain`) so real-git tests — especially `App.Sync`'s subprocess — don't flake on a developer's `commit.gpgsign`/`core.hooksPath`. Imported only by `_test.go` files. |
| `internal/version` | Build version, default `"dev"`, overridden via `-ldflags`. |

---

## The purity rule

`internal/core` is the spine, and it is **pure**: it imports only the Go
standard library (`encoding/json`, `sort`, `time`, `fmt`, `errors`, `regexp`).
It must **not** import:

- `cobra` or `bubbletea` (those are presentation concerns), or
- `os` or `path/filepath` (filesystem access is an adapter concern).

Filesystem access lives in `internal/store/fsstore`. Presentation lives in
`internal/cli` and `internal/tui`. The domain reaches the outside world only
through interfaces it declares itself.

> The doc comment at the top of `internal/core/task.go` states this rule
> in-code, so it travels with the source.

### Ports live IN core

The seams between the pure core and the outside world are interfaces declared in
[`internal/core/ports.go`](../internal/core/ports.go):

- **`Store`** — persists the per-task metadata shards and per-task bodies. It owns
  *all* path construction (callers never assemble `".furrow/bodies/<id>.md"` by
  hand) and *all* atomicity. Methods: `Load`, `Save`, `LoadBody`, `SaveBody`,
  `BodyExists`, `ListBodyIDs`, `ListTaskIDs`, `SaveAsset`, `ListAssets`,
  `NextID`. The two asset methods are the store half of `furrow attach` /
  `furrow lint`'s asset checks: `SaveAsset` copies media into the task's asset
  area `bodies/assets/<id>-<name>` (sanitized, collision-free, atomic) and
  returns the final basename; `ListAssets` enumerates `bodies/assets/` as
  name+size, a missing dir yielding nil, not an error.
- **`Clock`** — supplies `Now()`. Injected so tests get deterministic timestamps
  and the marshaller's UTC/whole-second contract is trivial to honor.
  `core.SystemClock()` is the production implementation.

These interfaces are implemented by adapters: `internal/store/fsstore` (the real
filesystem) and `internal/store/memstore` (an in-memory fake). Both carry a
compile-time assertion `var _ core.Store = (*Store)(nil)`. The `app`, `cli`, and
`tui` layers depend on the *interface*, never on a concrete adapter — that is
what keeps the core testable without touching disk.

`internal/app` widens the port slightly with its own `app.Store` interface
(`core.Store` plus `DeleteBody` and `BodyFile` for `$EDITOR` shell-out); both
adapters satisfy it.

### "Crossing a layer means a missing port"

The design heuristic: if a layer finds itself wanting to reach across to
something it should not import, the answer is **not** to add the import — it is
to add (or widen) a port. The core never grows an `os` import to "just read a
file"; it grows a `Store` method instead, implemented by the adapter.

---

## The single-marshaller invariant

The serializers in
[`internal/core/marshal.go`](../internal/core/marshal.go) are the **one and only**
paths that serialize task metadata to bytes. Persistence goes per shard:
`core.MarshalTask(Task) ([]byte, error)` writes one `tasks/<id>.json`, and
`core.MarshalMeta(...) ([]byte, error)` writes `meta.json`. Every writer —
`fsstore.Save`, and `migrate` — goes through them. `core.Marshal(*Index, laneOrder
[]string) ([]byte, error)` still exists, but it is now the **in-memory canonical
form** (used by the determinism golden and by inspection), *not* a persistence
path: the store never writes those bytes to disk, because doing so would resurrect
the abolished `index.json`. No other code calls `json.Marshal` on a `Task`,
`Index`, or the meta object.

Why one path per file: the byte layout of each shard is a contract, not an
implementation detail. If two code paths could serialize a task, they could drift,
and a re-save would churn the git diff. One path means **bytes written by `furrow`
equal bytes a human or Claude would hand-edit**, so re-saving an untouched task
produces zero git churn.

### The determinism contract

Each serializer calls `Canonicalize` and then encodes — the recipe is identical
for `MarshalTask`, `MarshalMeta`, and the in-memory `Marshal`. The contract
(documented in the `Marshal` doc comment and exercised by
[`internal/core/testdata/index.golden.json`](../internal/core/testdata/index.golden.json)):

- **Key order = struct field order.** `encoding/json` emits struct fields in
  declaration order, so the field order of `core.Index` / `core.Task` *is* the
  JSON key order. Reordering fields changes every diff — do not reorder without a
  schema bump and a golden update.
- **2-space indent** (`SetIndent("", "  ")`).
- **`SetEscapeHTML(false)`** so CJK and `< > &` survive verbatim. The golden file
  proves it: `"畝を一本進める"` and `"done item <b>&amp;</b> 完了"` round-trip
  unescaped.
- **`[]`, never `null`.** `Canonicalize` replaces nil slices (`Labels`, `Deps`,
  `Refs`, `Checklist`) with empty ones.
- **Stable sort: lane-rank → priority → id.** Lane rank comes from the configured
  `[lanes].order`; unknown lanes sort last (and are flagged by lint). `Labels`
  and `Deps` are treated as sets and sorted; `Refs` and `Checklist` keep user
  order.
- **UTC, whole-second RFC3339 timestamps.** `normTime` does
  `t.UTC().Truncate(time.Second)`, so timestamps render as `...Z` with no
  fractional component. `Closed` is a `*time.Time` — `null` while a task is open.
- **Trailing newline.** `json.Encoder.Encode` appends it.

`Unmarshal` is the inverse, and a parse failure is reported as a *validation*
error (the file is malformed input), not an internal fault.

### How the invariant is guarded

- **Golden round-trip test.** `internal/core/core_test.go` asserts that marshalling
  the fixture index produces `testdata/index.golden.json` byte-for-byte (write →
  read → write stays identical).
- **Schema drift test.** `furrow schema [task|meta]` prints
  `internal/schema.TaskV2` / `internal/schema.MetaV2` (JSON Schema draft 2020-12);
  `docs/schema/furrow.task.v2.json` and `docs/schema/furrow.meta.v2.json` are
  committed copies of the same bytes, and CI diffs both so they cannot drift.
- **Single-path grep guard.** `scripts/check-marshal-singlepath.sh` greps for
  stray `json.Marshal` calls on a `Task`/`Index`/meta outside `core`'s serializers
  (all in `internal/core/marshal.go`) and fails CI if any appear; it runs as part
  of `scripts/check.sh`.

---

## The `repos` field and the version gate

A task carries a **first-class `repos` set**: the repositories it relates to,
as `owner/repo` identifiers, 0..N per task, with the same set semantics as
labels (sorted + deduped on write, `[]` never `null`). Labels stay pure
free-form tags — a repo is **not** a label. An empty `repos` set is a
**draft** (the GitHub-Issues-draft analogue), a first-class state that `ls
--drafts` lists and `revisit` flags with the `no_repo` signal.
`core.IsRepoShaped` is the one shape predicate (exactly `owner/repo`);
`furrow lint` warns on entries that don't match.

Promoting `repos` to a schema field is what let the schema *document* bump to
**v2** (`internal/schema.TaskV2`, `docs/schema/furrow.task.v2.json`) — and it
motivated the **version gate**: `core.CheckSchemaVersion` rejects a board
whose `meta.json` declares a `schema_version` **newer than the binary knows**
(`core.SchemaVersion`) — surfaced as exit 3 (internal: the fix is updating
the binary, not the input). Both
store adapters enforce it on `Load` *and* `Save` (`fsstore`, `memstore`).
Without the gate, an old binary's lenient unmarshal would silently drop the
fields it doesn't know — a re-save would strip every task's `repos` and git
would dutifully commit the damage. Older versions load fine (the store's
normal lenient read is the forward-compat path); only *newer* boards are
refused.

---

## The store

`internal/store/fsstore` is the only package that touches the filesystem for the
store. It is constructed with the few config-derived values it needs (lane order
for the marshaller's sort, id prefix/width for `NextID`) so it never imports
`internal/config`.

A `.furrow/` store directory contains:

```
.furrow/
  tasks/               structured metadata, one JSON shard per task
    t-k3m9p.json         written ONLY via core.MarshalTask
    t-9qw2z.json
  bodies/<id>.md       long-form prose, one file per task (hand/agent editable)
  bodies/assets/       attached media, one file per attachment: <id>-<sanitized-name>
    t-k3m9p-shot.png     written ONLY via Store.SaveAsset (atomic, collision-free
                         basename); linked from the body by `furrow attach`; scanned
                         by `furrow lint` (dangling / orphan / oversized warnings)
  meta.json            board-wide layout version {"schema_version": 3} — MarshalMeta
  config.toml          human config (read-only from furrow's side)
  archive/             a sibling sharded store: aged done tasks moved out of the hot store
```

### Load and Save (shard fold / split)

`fsstore.Load` globs `tasks/*.json`, unmarshals each shard, and folds every one
into a single in-memory `core.Index`; the `schema_version` is read from
`meta.json`. `fsstore.Save` is the inverse: it splits the `Index` back into
per-task shards and writes **only the shards whose bytes changed** (a byte-compare
against what is already on disk) plus `meta.json`, and **deletes** the shards of
any ids no longer present. So a no-op save touches no files and produces zero git
churn.

### Atomic writes (tmp + rename)

Every write — each `tasks/<id>.json`, `meta.json`, and each `bodies/<id>.md` —
goes through `atomicWrite`: create a temp file (`.tmp-*`) in the **destination
directory**, write, `fsync`, `close`, then `os.Rename` over the target. Rename is
atomic on a single filesystem, so a crash never leaves a half-written shard. The
temp file is removed on any error path. A single-task change is one shard and thus
fully atomic; a bulk change is atomic **per shard** — each shard is independently
valid, so an interrupted bulk save leaves a coherent store and is safely
re-runnable.

### Lazy body load

The `Index` holds only metadata; `Task.Body` is a *relative path*
(`bodies/t-0042.md`), never the prose itself. Body text is read on demand via
`LoadBody` (returning `""` when the file is absent — a task may legitimately have
no body yet) and written via `SaveBody`. This split is the whole point of the
hybrid store: metadata diffs per field, prose diffs per task, and long
markdown never collapses into a one-line escaped JSON string.

`core.BodyPath(id)` is the single source of the `bodies/<id>.md` path; both the
store and the marshaller use it so the `Body` field is never hand-assembled.

### Frozen, collision-free random ids

`NextID` returns a **random** id: `prefix` + a random Crockford-base32 suffix
(lowercase `0-9a-z` minus the ambiguous `i,l,o,u` = 32 symbols, masked from
`crypto/rand` low-5 bits, `[ids].width` chars, default 5 → e.g. `t-k3m9p`).
There is **no shared counter** — nothing on disk to coordinate — so two
operators running `furrow add` in separate worktrees/PRs won't mint the same id.
The app draws ids until one is not already in the index (a retry loop; the first
draw almost always wins at 32^5 ≈ 33.5M), and `core.Validate`/`furrow lint`
flags any duplicate as a cross-branch backstop. Ids are still **frozen** (never
reused or renumbered); legacy zero-padded numeric ids (`t-0042`) remain valid
and coexist with new random ones.

`Load` on a missing `tasks/` directory returns an empty, well-formed `Index`
(`schema_version` set, `tasks: []`) rather than an error, so `furrow add` works
on day one before `init` has written anything.

### memstore

`internal/store/memstore` is a parallel `core.Store` kept entirely in memory. It
is a **normal package, not a test helper**, so both unit tests and runtime
dry-run code can use it. Its `BodyFile` returns `""` because an in-memory store is
not file-backed — so `$EDITOR` shell-out is unsupported against it, which the
`app` layer detects and reports.

---

## The coordinator and the CLI contract

`internal/app` is the **only mutation funnel**. The CLI (and, later, the TUI)
call `App` methods — `Add`, `Move`, `Done`, `Reorder`, `SetTitle`, `SetValue`,
`SetEffort`, `Check`, `AddCheck`, `AddDep`/`RemoveDep`, `Relabel`, `Rerepo`,
`Attach`, `ApplyDirectives`, `Sync`, `Archive`, `Lint`, `EditPath`, plus the read methods
`Get`, `List`, `Next`, `Revisit`. Keeping every edit in one place is what keeps
the invariants (frozen
ids, canonical order, closed-timestamp rules, body↔index pairing) from being
re-implemented across two presentation layers. `App.load()` canonicalizes on
every read, so reads see the same lane→priority→id order regardless of any
hand-edit.

A few app-level rules worth stating, all verified against the code:

- **`Add`** assigns the next frozen id, picks a sparse priority (explicit
  `--priority`, else `max(priority in lane) + step`), writes a body file seeded
  with `# <title>`, then saves. With a board scope in effect, it **unions the
  scope repo** into the task's `repos` (an explicit `-r` adds rather than
  replaces; `--draft` suppresses exactly that union).
- **`Rerepo`** (the `furrow repo` command) attaches/detaches `owner/repo`
  values on a task, resolving short names against the board's known repos
  (`ResolveRepo`); an ambiguous or unknown name is a validation error carrying
  a `candidates` array — never a silent new repo.
- **`Move` / `Done`** set the lane. Moving **into** the done lane stamps
  `Closed`; moving **out** of it clears `Closed`. Other terminal lanes (e.g.
  `icebox`) leave `Closed` alone — *parked is not closed*.
- **`Next`** returns actionable tasks: in one of the configured `[next].lanes`
  (default `ready` + `in-progress` — intake lanes like `inbox` are deliberately
  excluded) and with every named dependency already in the done lane. Lane
  semantics live in config, not core — `Index.Actionable` takes the terminal
  set and the done-id set as arguments, and the `[next].lanes` gate is applied
  in `app` via `Config.IsNextLane`.
- **`Archive`** selects done-lane tasks whose `Closed` is older than the cutoff
  and moves them (shard + body) into the sibling `.furrow/archive/` store (its own
  `tasks/`, `meta.json`, and `bodies/`).

### CLI commands

Registered in [`internal/cli/root.go`](../internal/cli/root.go), all built today
except where noted:

`init`, `add`, `ls` (alias `list`), `show`, `next`, `revisit`, `edit`, `attach`,
`done`, `move`, `reorder`, `retitle`, `value`, `effort`, `check`, `dep`, `label`, `repo`, `apply`,
`sync`, `archive`, `lint`, `config` (`init`/`path`), `schema`, `version`, `ui`,
`migrate`.

- **`dep`** adds or removes a dependency edge on an existing task (`--rm`).
  Adding is acyclic (rejects self- and cycle-creating edges) and idempotent.
- **`repo`** attaches/detaches `owner/repo` values on a task (`--add`/`--rm`,
  both repeatable); short names resolve against the board's known repos or
  fail with `candidates`. A task with no repos is a **draft** (`ls --drafts`).
- **`attach <id> <file>`** copies a media file into the task's asset area
  (`.furrow/bodies/assets/<id>-<name>`) and appends a relative markdown
  reference to the body (images embed with `![...]`, other media link). The id
  is validated before anything is written, so a bad id fails cleanly with no
  stray asset. LFS-independent: a plain file copy plus a body edit — a
  `.gitattributes` rule makes git-lfs take the blob transparently. `--json`
  emits `{id, asset, ref, line}`.
- **`sync`** runs the multi-machine ritual against the git repo enclosing the
  board: auto-commit scoped to `.furrow/` — machine-written paths (`tasks/`,
  `meta.json`, `config.toml`) and brand-new (untracked) bodies always commit,
  while a merely-modified `bodies/<id>.md` commits only when named with
  `-b/--body <id>` or under `--all-bodies`, and is otherwise left for its
  author and reported in `pending_bodies` plus a stderr note — then `fetch` +
  autostash `rebase @{u}` (onto the tracking ref, not `FETCH_HEAD`, so a
  co-writer's fetch can't race it), `push` (one retry on non-fast-forward), via
  the `internal/gitrepo` adapter. The progress object — stdout on success AND
  failure — carries `{committed, pulled, pushed, conflict, committed_bodies,
  pending_bodies}` (the body lists omitted when empty). Failure modes, branch
  on the error `id`: `sync-conflict` (exit 3, definitive — the rebase is
  aborted automatically, conflicted paths in `details`), `sync-busy` (exit 3,
  retryable — a foreign in-progress rebase outlived the bounded backoff),
  `sync` (terminal — a likely-stale `.git/*.lock`, named in the message), and
  `sync-interrupted` (exit 3, retryable — SIGINT/SIGTERM cancelled the
  in-flight git; a genuine conflict is never masked by the signal).
- **`apply`** parses `SetStatus-task:` directives out of PR/commit text (stdin
  or `--body-file`) and reflects them onto the board — the CI hook behind the
  task-status workflow. Validation is non-blocking by design.
- **`revisit`** is the read-only, agent-facing counterpart to `next`: it lists
  open tasks needing re-evaluation (`no_repo` — a draft, surfaced regardless
  of scope — plus unset value/effort, stale, or a done dependency), attaching a
  `revisit` reason array in `--json` so an agent fixes them via the setters
  (`value`/`effort`/`dep`/`repo --add`). An empty result exits 0 (nothing to revisit is healthy).
- **`ui`** launches the bubbletea TUI (`internal/tui`): list + glamour detail,
  navigate / filter / done / move lane / reorder (`K`/`J`) / checklist toggle /
  edit body.
- **`migrate`** parses a hand-maintained `Task.md` into furrow tasks (dry-run by
  default; `--write` to apply; `--label` to stamp imported tasks).

### Output, errors, and exit codes

- `--json` (persistent flag) emits JSON to **stdout only**; logs and errors go to
  stderr (so a caller piping stdout to `jq` is unaffected). `--ndjson` emits one
  compact task object per line. CLI JSON uses the same `SetEscapeHTML(false)` /
  2-space encoding as the shards.
- Read filters: `--status`/`-s`, `--label`/`-l`, `--repo`/`-r`, `--limit`/`-n`,
  `--drafts` on `ls`; `-l`/`-r`/`-n` on `next` and `revisit` (plus
  `--stale-days` on `revisit`). `-r` is the scope control (an
  explicit `-r` overrides the board scope; `-r ''` shows the whole board);
  `-l` is a pure tag filter that ANDs with the scope. `ls --drafts` lists only
  the repo-less tasks. When an input *almost* resolved — an ambiguous repo
  short name, or a label that uniquely names a repo (the did-you-mean guard) —
  the error envelope carries a `candidates` array; when a repo scope — explicit `-r`
  or the board's auto scope — hides drafts, a one-line stderr hint points at
  `--drafts` (stdout stays pure data).
- **Non-interactive by default.** No prompts; the TUI is `furrow ui` only.
  `furrow edit` on a non-TTY prints the absolute body path instead of launching
  an editor, so an agent can edit the file directly. `NO_COLOR` and non-TTY
  suppress color.
- **Destructive-op guard.** `furrow archive` previews ("would archive …") unless
  `--yes` is passed.
- **Exit-code contract** (`internal/core/errors.go`): `0` ok / `1`
  not-found or empty result / `2` bad-usage or validation / `3+` internal or IO.
  On a non-zero exit the CLI prints `{"error":{"code","id","message"}}` to
  stderr, plus optional machine-actionable fields: `candidates` (a near-miss
  that almost resolved) and `details` (e.g. `sync-conflict` carries the
  conflicted paths). `cmd/furrow/main.go` is literally `os.Exit(cli.Execute())`.

---

## Command design: sugar over raw git

furrow never hides git from someone who knows it. Every state change is a plain
commit to a plain-text store, and an operator fluent in git can always drop to
`git add` / `commit` / `fetch` / `rebase @{u}` / `push` and get exactly what
furrow would have done. The CLI's job is not to wall git off — it is to offer **sugar**
for the common multi-step rituals, so a GUI-leaning user (or an agent) gets one
verb where an expert would type three. The principle: *never obstruct the
expert; bundle the ceremony for everyone else.*

`furrow sync` is exemplar #1. It bundles the exact dance a git
expert runs by hand — **auto-commit (pathspec-limited to `.furrow/`, and within
it gated to machine-written files plus new/opted-in bodies) →
`fetch` + `rebase --autostash @{u}` → `push`** — behind one command, adding a
machine-readable progress object and conflict classification on top. The sugar
is a convenience, not a cage: the underlying store is still just files in a git
repo you fully own, so nothing stops you from running those three git commands
yourself.

The `[[id]]` **link** notation follows the same "one source, many readers"
discipline: [`internal/core/links.go`](../internal/core/links.go)
(`LinkPattern` + `ExtractLinks`) is the **single** definition of what a `[[id]]`
body link is — a bare id is not a link, and a `[[id]]` inside code is an inert
example — and both `App.Backlinks` (`show --backlinks`) and `furrow lint`'s
dangling-link check read it, so the two features can never drift.

---

## Configuration

`internal/config` reads `.furrow/config.toml` and produces an effective
`Config`. furrow **only reads** this file — it never writes or regenerates it.
The policy is **clamp-don't-reject**: unknown keys are ignored (go-toml/v2
default), out-of-range values fall back to a safe default, and each correction is
collected as a warning that `furrow lint` surfaces. A *missing* file yields the
built-in defaults with no warnings; only *malformed TOML* is an error.

Sections and their defaults:

| Section | Keys | Default |
|---|---|---|
| `[lanes]` | `order`, `default`, `done`, `terminal` | `inbox, backlog, ready, in-progress, waiting, done, icebox`; default `inbox`; done `done`; terminal `done, icebox, waiting` |
| `[next]` | `lanes` | `ready, in-progress` (falls back to all non-terminal lanes when neither exists) |
| `[priority]` | `step`, `default` | `10`, `100` |
| `[ids]` | `prefix`, `width` | `t-`, `5` |
| `[labels]` | `required` | `false` |
| `[archive]` | `older_than_days` | `30` |
| `[revisit]` | `stale_days` | `30` (`0` disables the stale signal) |
| `[ui]` | `theme` | `auto` (one of `auto`/`dark`/`light`) |

`status` is just a lane from `[lanes].order`; that list is simultaneously the
status enum and the top-to-bottom sort rank.

### User-level config: central boards

There is a **second**, machine-specific config — the user-level
`${XDG_CONFIG_HOME:-~/.config}/furrow/config.toml` — that declares one or more
**central boards**: a single `.furrow` that backs many repos *without* a per-repo
`.furrow-pointer.toml`. It is to the board-local `config.toml` what `~/.gitconfig`
is to a repo's `.git/config`: ambient and personal, never committed. Each board
is a `[[board]]` table (an array, so several can coexist):

```toml
[[board]]
path        = "~/src/github.com/me/projects/.furrow"
scopes      = ["~/src/github.com/me"]   # at least one; cwd must be under one to activate
repo        = "auto"                    # "auto" | "" | a literal "owner/repo"
label       = ""                        # optional literal tag `add` applies (never filters reads)
auto_filter = true                      # scope reads by the board repo (default true; false = whole board)
```

Resolution is split across two layers, honouring the purity rule:

- **`internal/config` (pure, read-only)** parses the `[[board]]` array and
  **clamps per entry**: an entry with no `path`, or no `scopes` after blank
  strings are pruned, is dropped with a warning; if every entry is dropped the
  result is "no central board" (`nil`). It never touches cwd, the filesystem, or
  symlinks — it only shapes what the file says. A legacy single `[board]` table
  decodes into a one-element array whose old `scope` key is ignored, so it clamps
  away to "no board" rather than erroring (the accepted rollout-window
  degradation when a v2 binary meets a v1 config).
- **`internal/app` (the only fs/cwd-aware layer)** is the last arm of `discover`
  (after `FURROW_DIR`, a local `.furrow`, and a `.furrow-pointer.toml`). It
  resolves each board/scope path (`~`, relative-to-the-config-file, absolute),
  canonicalizes both cwd and scopes (symlinks resolved, so `/var`→`/private/var`
  still matches), and selects the board whose matching scope is the **longest
  (most specific) canonical prefix** of cwd, ties broken by file order. Only the
  **winning** board is `stat`-ed for existence — a broken path in an unrelated
  scope never breaks furrow in this directory. `FURROW_BOARD=<path>` short-circuits
  the file with one synthetic board whose nil scopes are a sentinel for "derive
  the scope from the board repo's parent".

A central board injects a scope repo exactly like a pointer (see the coordinator
contract): `repo = "auto"` derives the owner/repo from the enclosing checkout
(git origin URL, worktree-aware, ghq-path fallback — file reads only, never a
git subprocess), which is how a cross-repo tracker attaches each task to its
owning repo; a board's `label` is only a literal add-time tag. Whether the read
commands (`ls`/`next`/`revisit`) auto-filter by that repo is a separate,
explicit knob: a board's per-entry **`auto_filter`** (default true) threads onto
`App.AutoFilter`; a pointer always filters. The repo still attaches on `add`
regardless, so `auto_filter = false` means "attach writes, show the whole
board". Because the switch is declared in config, the old scope banner is
gone — filtering is silent (stdout stays pure data).

**Repo derivation (`repo = "auto"`).** The derivation lives in
[`internal/app/gitorigin.go`](../internal/app/gitorigin.go) (the app layer is
the only fs/cwd-aware layer) and is **file reads only — no `git`
subprocess**. The chain, in order:

1. **Find the checkout.** Walk up from cwd to the nearest directory holding a
   `.git` entry.
2. **Find the shared git config.** A `.git` *directory* holds it directly. A
   `.git` **file** (worktree/submodule) is a `gitdir:` redirect: follow it,
   then follow that dir's `commondir` file back to the shared `.git` — this
   commondir chase is what makes a worktree named `chord-fix-y` still derive
   `owner/chord` (a submodule gitdir has no `commondir` and carries its own
   config, which is the right one to read).
3. **The first-url rule.** Parse the config as section-aware INI and take the
   **first `url` line of `[remote "origin"]`** — only that line counts: never
   `pushurl`, never a second `url` line (a real config carried a foreign
   repo's URL there), never another remote. Supported URL forms: scp-like
   (`git@host:o/r.git`), `ssh://`, `git+ssh://`, `git://`, and `http(s)://`,
   each with or without `.git`. A first url that is unusable does **not** fall
   through to the next line — that misattribution is exactly what the rule
   guards against.
4. **ghq-path fallback.** With no usable origin (typically a repo not pushed
   yet), a ghq-style path — a host-like component followed by
   `<owner>/<repo>`, the match closest to the repo winning — supplies the
   identifier.
5. **Fail open, as drafts.** Failing both, the board opens **unscoped** with a
   stderr note and `add` creates drafts. The invariant this chain guards:
   every derived value is owner/repo-shaped — **a bare directory name is never
   written into `repos`**.

The retired `label = "auto"` mode is a reserved tombstone: ignored with a
warning pointing at `repo = "auto"` (a board's `label` is only a literal
add-time tag now).

**Writing and validating it.** `furrow config init` scaffolds this file — the
single exception to "config is read-only", exactly like `furrow init` writing a
board's `config.toml` (both write through `internal/app`, not a new fs path). Run
inside a board it derives the `path` (nearest enclosing `.furrow`) and `scopes`
(that board repo's parent) from context; `--path`/`--scope` override; elsewhere it
writes the commented placeholder — the `config.GlobalTemplate` const, mirrored at
the repo-root `config.global.toml` and drift-guarded by `scripts/check.sh`.
`furrow config path` prints the resolved location. Discovery stays **silent on its
inert path** (when every `[[board]]` clamps away there is no board *and* no
signal), so those clamp warnings are surfaced explicitly instead: both `furrow
lint` and `furrow config path` report a half-written user-level config rather than
spamming every command's stderr.

---

## What's NOT in scope

This document covers the *built* architecture. Several things are deliberately
**out of scope** for furrow's design; the full rationale lives in
[`non-goals.md`](non-goals.md). The headline non-goals:

- **No MCP server, no Claude Code plugin.** The plain CLI (with
  `--json`/`--ndjson` and machine-actionable error envelopes) plus a clonable
  plain-text store is already the agent interface; a daemon or a second
  protocol would add nothing but operational surface. The integration layer is
  a short `CLAUDE.md` block plus `--json` on read
  commands. The rules that block belongs to: never hand-edit `tasks/<id>.json`
  (the single marshaller owns them; manual edits churn git), `bodies/*.md` *are*
  editable, and mutate only through commands.
- **No binary storage** (no SQLite) and **no YAML.** JSON for the machine-written
  shards, TOML for human config, Markdown for prose.
- **No GitHub Issues coupling.** furrow is an *alternative* to Issues, not a
  client: a clonable plain-text store. "GitHub friendly" means "diffs cleanly",
  not "syncs to Issues" (see docs/non-goals.md for the boundary with the
  task-status Action).
- **No interactive prompting from the CLI.** Interactivity is confined to
  `furrow ui`.
- **Web / React UI is out of scope for now** (parked): a future read-only viewer
  would simply read the `tasks/*.json` shards, which is exactly why clean JSON
  shards matter.

### Built vs. planned — honest status

| Area | Status |
|---|---|
| `internal/core` (structs incl. `repos`, `Marshal`, ports, validate, index ops, version gate) | **Built** |
| `internal/config` (TOML load, clamp; user-level `[[board]]` with `repo = "auto"`) | **Built** |
| `internal/store/fsstore`, `internal/store/memstore` | **Built** |
| `internal/app` (mutation funnel, board discovery + repo derivation, archive, lint) | **Built** |
| `internal/gitrepo` (git subprocess adapter behind `furrow sync`) | **Built** |
| `internal/cli` (cobra: all commands above, including `repo`, `sync`, `apply`, `ui`, `migrate`) | **Built** |
| `internal/tui` (bubbletea v1, `furrow ui`) | **Built** |
| `internal/schema` + `docs/schema/furrow.task.v2.json` / `furrow.meta.v2.json` | **Built** |
| Golden round-trip + schema drift tests | **Built** |
| `scripts/check-marshal-singlepath.sh` | **Built** |
| Packaging (GoReleaser → Homebrew tap) | **Released** — `v0.1.0`–`v0.6.1` published (task-status Action bundled since `v0.5.0`) |
| nix flake | **Built** — real pinned `vendorHash` + committed `flake.lock` (since `v0.4.0`) |
| Read-only web / React viewer | **Future, low priority** |

---

*(reviewed 2026-07-02)*
