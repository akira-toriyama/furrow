# furrow — architecture

furrow is a clonable, git-native, plain-text task tracker — an alternative to
GitHub Projects/Issues — written in Go (module
`github.com/akira-toriyama/furrow`, Go 1.25+). One board can live outside the
repos it backs and be reached by configuration (a central board — each task
carries its repos as first-class `owner/repo` identifiers, so one board can back
many) or sit repo-local in a single repo's `.furrow/`. This document describes
how the code is organized, why the layers are shaped the way they are, and which
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
                                 v
     internal/cli (cobra) — the ONLY presentation layer in-repo
     command/flag parsing, human/JSON rendering
     (a TUI/GUI is a SEPARATE front-end repo — ridge / loom —
      that drives furrow through its CLI/JSON contract, not its Go packages)
                                 |
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
     (clamp, no write) (atomic write,           (tests, dry-runs)        adapter (git ops;
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

   adapter beside gitrepo: internal/claudecode (Claude Code's private session
     registry -> core.SessionRegistry, the session write guard's eyes)
   leaves: internal/schema (JSON Schema source), internal/version (ldflags),
     internal/migrate (Task.md parser), internal/query (`-q` parser -> AST),
     internal/recur (`--repeat` -> RRULE; a stored rule -> next occurrence)
```

A dependency arrow means "imports". Note what is **absent**: `internal/core`
imports no other furrow package and no third-party library; `internal/cli` never
imports a store adapter for mutation — it goes through `internal/app`. furrow is
**CLI-only**: any TUI/GUI (ridge, loom) is a separate front-end consuming the
same `--json`/`--ndjson` contract an agent does.

### Package responsibilities

| Package | Role |
|---|---|
| `cmd/furrow/main.go` | Entry point. Just `os.Exit(cli.Execute())` — no logic. |
| `internal/cli` | cobra adapter and the **only** in-repo presentation layer: parse flags, call `app`, render (human table or `--json`/`--ndjson`), map errors to exit codes. Holds no task logic. A TUI/GUI is an out-of-repo front-end (ridge / loom) driving this same CLI/JSON contract. |
| `internal/app` | Coordinator. Wires a `Store` + `Config` + `Clock`; exposes every mutation/query as a method. The **only** place that mutates state. |
| `internal/config` | Loads `.furrow/config.toml` (clamp-don't-reject). Produces an effective `Config`. Also owns the surgical single-key editor behind `furrow config set`, the one config writer. |
| `internal/store/fsstore` | The **only** package that touches the filesystem for the store: atomic writes, lazy body load, random id generation. |
| `internal/store/memstore` | In-memory `core.Store` twin for tests (a normal package, not a test helper). |
| `internal/gitrepo` | git subprocess adapter behind `sync`, `doctor`'s freshness probe and autocommit (command assembly + error classification). Driven only through `internal/app`. |
| `internal/claudecode` | Adapter over Claude Code's **private** session registry (`~/.claude/sessions/<pid>.json`; the transcript's mtime as activity, its last message record as the turn state): implements the `core.SessionRegistry` port for the session write guard. The ONE place that knows the format, and best-effort by contract (a dead pid is dropped, an unparsable entry skipped and named for `doctor`). |
| `internal/core` | Pure domain: the entity structs, the `Marshal*`/`Unmarshal*` single path (incl. the unknown-key passthrough), the `Store`/`Clock`/`SessionRegistry` ports, `Validate`, the two-sided version gate, and in-memory index ops. |
| `internal/schema` | The four JSON Schemas as Go constants; emitted by `furrow schema [task\|meta\|repo\|epic]`. |
| `internal/migrate` | Pure `Task.md` parser behind `furrow migrate` (dry-run by default; LOUD warnings for the unmappable). |
| `internal/recur` | Recurrence, a leaf beside `query`: the short `--repeat` spelling to one RFC 5545 RRULE line, a stored rule to its next occurrence. The only importer of the RRULE library and the only package that knows the grammar; `internal/app` owns WHEN a rule advances (a close). The five library traps it contains (an exhausted rule is a zero time with no error, day-of-month skips per RFC 5545 §3.3.10, DTSTART dropped and rebuilt, an off-lattice anchor never emitted, the local-midnight day grid) are its package doc. |
| `internal/query` | Pure parser (stdlib only) for the `-q` typed-query DSL, producing an AST. It knows the GRAMMAR, not furrow's fields; `internal/app`'s `compileQuery` binds each term to a task predicate against the index, the `Clock` and the store's bodies (loaded on demand). One compiled predicate serves every filtering read. |
| `internal/gittest` | Test-only: `Isolate()` neutralizes global/system git config (and background maintenance) at the process-env level from `TestMain`, plus the shared `GitOrSkip`/`RunGit`. Imported only by `_test.go` files. |
| `internal/version` | Build version, default `"dev"`, overridden via `-ldflags`. |

---

## The purity rule

`internal/core` is the spine, and it is **pure**: it imports only the Go
standard library (`encoding/json`, `sort`, `time`, `fmt`, `errors`, `regexp`,
`strings`, `reflect` — the passthrough asks json's own field matcher).
It must **not** import:

- `cobra` (a presentation concern), or
- `os` or `path/filepath` (filesystem access is an adapter concern).

Filesystem access lives in `internal/store/fsstore`, presentation in
`internal/cli`; the domain reaches the outside world only through interfaces it
declares itself (the doc comment atop `internal/core/task.go` says so in-code).

### Ports live IN core

The seams between the pure core and the outside world are interfaces declared in
[`internal/core/ports.go`](../internal/core/ports.go):

- **`Store`** — persists the shards and bodies, owning *all* path construction
  and *all* atomicity. The method set is the interface in
  [`internal/core/ports.go`](../internal/core/ports.go) — read it there (a list
  here drifted to 14 of 27). Four methods carry an invariant: **`LoadMeta`**
  returns `meta.json` whole (version + the passthrough's parked keys) and
  **`BoardVersion`** projects the one field, ungated, so `furrow board` can
  diagnose a board nothing else can open; **`Writable`** is the side-effect-free
  predicate behind the write gate, so reporters (`board`, `lint`, `archive`)
  ask it instead of re-deriving the rule; **`SetBoardVersion`** is the one
  deliberate raiser (`furrow upgrade` only) and READS the existing `meta.json`
  before raising it, so an upgrade cannot eat forward-compatible keys; and
  **`Save` canonicalizes in both directions** — the caller's `*core.Index` is
  normalized in place, which is why the app hands back a just-saved task
  straight out of the index.
- **`Clock`** — supplies `Now()`; injected so tests are deterministic
  (`core.SystemClock()` in production).
- **`SessionRegistry`** — `Sessions()`, the live sessions of an AI coding
  harness on this machine (`core.Session`: pid, id, name, cwd, started, last
  active, `TurnEnded`). The session write guard's eyes; `internal/claudecode` implements it
  for Claude Code, and the decision itself (`core.SessionClashes`) stays pure —
  it takes a `repoOf(cwd)` function so core never touches git.

Both adapters carry `var _ core.Store = (*Store)(nil)`; `app` and `cli` depend
on the interface, never on an adapter. `internal/app` widens it slightly as
`app.Store` (`DeleteBody`, `BodyFile` for the `$EDITOR` shell-out).

### "Crossing a layer means a missing port"

If a layer wants to reach across to something it should not import, the answer
is **not** the import — it is a new (or wider) port: the core never grows an
`os` import to "just read a file"; it grows a `Store` method the adapter
implements.

---

## The single-marshaller invariant

The serializers in
[`internal/core/marshal.go`](../internal/core/marshal.go) are the **one and only**
paths that serialize task metadata to bytes. Persistence goes per shard:
`core.MarshalTask(Task) ([]byte, error)` writes one `tasks/<id>.json`, and
`core.MarshalMeta(...) ([]byte, error)` writes `meta.json` — the latter only from
`Store.SetBoardVersion` (i.e. `furrow upgrade`) and the fresh-store stamp, never
on the ordinary write path (see the version gate). Every writer —
`fsstore.Save`, and `migrate` — goes through them; the index has no serialized
form (its normal form is what `core.Canonicalize` enforces in memory). No other
code calls `json.Marshal` on a `Task`, `Index`, or the meta object.

Why one path per file: the byte layout of each shard is a contract. One path
means **bytes written by `furrow` equal bytes a human or Claude would
hand-edit**, so re-saving an untouched task produces zero git churn.

### The determinism contract

Each serializer normalizes (`canonicalizeTask` and friends) and then encodes
via the one encoder, `encodeCanonicalWithExtras` — the recipe is identical for
`MarshalTask`, `MarshalEpic`, `MarshalRepo`, and `MarshalMeta`. The contract
(documented in the `MarshalTask` doc comment and exercised by the per-shard
goldens under [`internal/core/testdata/`](../internal/core/testdata/)):

- **Key order = struct field order** (reordering fields changes every diff: a
  schema bump and a golden update, never casually).
- **2-space indent**, **`SetEscapeHTML(false)`** (CJK and `< > &` survive; the
  task golden proves it), **trailing newline**.
- **`[]`, never `null`** for the slice fields; `Labels`/`Deps`/`Repos` are
  sorted sets, `Refs`/`Checklist` keep user order.
- **Stable sort: lane-rank → priority → id**, lane rank from `[lanes].order`,
  unknown lanes last (and flagged by lint).
- **UTC, whole-second RFC3339 timestamps**; `Closed` is `null` while open.

`Unmarshal` is the inverse; a parse failure is a *validation* error, not an
internal fault.

### Unknown-key passthrough (the other half of the version gate)

[`internal/core/passthrough.go`](../internal/core/passthrough.go) makes the
round-trip **lossless**: `core.UnmarshalTask` / `UnmarshalRepo` / `UnmarshalMeta`
park every **top-level** key the binary does not know in an unexported `extras`
field, and the matching `Marshal*` re-emit them sorted, after the known keys.
Why: the version gate below fires only when someone **bumps**; a field added
without a bump leaves `meta.json` unchanged, so an older binary drops the key
(json's lenient unmarshal) and writes the loss back on its next save — one
ordinary write, one destroyed field, no error. **The gate stops a bumped layout
from being misread; the passthrough stops an unbumped one from being
destroyed.** They are not substitutes: preserved is not honoured (an old binary
carries a future `"blocked": true` and still hands the task out in `next`), so
`lint` warns `unknown-shard-key` over all four written file kinds, and the rule
"bump on a shape change" still stands (`TestShardFieldsGolden`, below).

The two rules that make it safe, with the reproduced corruptions behind them,
are the file's doc comment and stay there: "known?" is answered with
`strings.EqualFold` — json's own matcher; a case-sensitive set re-emits a
`BODY` twin, a `ToLower` set both wedges a task (U+017F) and destroys its id
(U+0130) — and **`Task` must never grow a `MarshalJSON` method** (the CLI's
views embed it; a promoted method would silently drop every sibling field).
The byte recipe is untouched: a shard with no extras marshals byte-identically
to what furrow always wrote. Limits, none papered over: top-level only
(`$defs.checklistItem` stays `additionalProperties: false`); not retroactive
(a shared board is safe only once EVERY writer, pinned CI callers included, has
passthrough); position churn across vintages (new shard fields go at the END of
the struct).

### How the invariant is guarded

- **Golden round-trip tests.** `internal/core`'s shard goldens freeze each
  marshaller's bytes, and write → read → write must stay byte-identical.
- **Schema drift test.** `furrow schema [task|meta|repo|epic]` prints
  `internal/schema.TaskV2` / `MetaV2` / `RepoV1` / `EpicV2` (JSON Schema draft
  2020-12); `docs/schema/furrow.task.v2.json`, `furrow.meta.v2.json`,
  `furrow.repo.v1.json`, and `furrow.epic.v2.json` are
  committed copies of the same bytes, and CI diffs all four so they cannot drift.
- **Struct-fingerprint golden.** `TestShardFieldsGolden`
  (`internal/core/schema_fields_test.go`, frozen in
  `testdata/shard-fields.golden`) records every persisted type's json keys **in
  struct order**, plus the layout version they belong to. Change the shape of a
  shard and it fails, telling you to bump `core.SchemaVersion` — a flag day —
  unless no query, sort, filter, or lane decision reads the new field — a class
  that has never had a member, so the default answer is BUMP. Accept a new
  shape deliberately, with
  `go test ./internal/core -run TestShardFieldsGolden -update-fields`. This is the
  teeth on a rule that otherwise fails **silently**: add a field, forget the bump,
  and every test on a fresh store still passes.
- **Frozen board.** `TestFrozenBoardRoundTripsByteIdentical`
  (`internal/store/fsstore/testdata/frozen-board/`) is the byte-level twin and
  the only fixture **the code under test did not write**: Copy → `Load` →
  `Save` + `SaveRepo` + `SetBoardVersion` → every file byte-identical, same file
  set, **untouched mtimes**. It shows the *damage*, not the diff — a new
  non-`omitempty` field as `+ "sprint": ""` in **every** shard, a renamed key
  re-emitted after the known ones by the passthrough — and pins `meta.json`'s
  bytes and where the extras splice lands. `-update-board` rewrites a committed
  board, putting the flag-day decision in the diff.
- **Single-path grep guard.** `scripts/check-marshal-singlepath.sh` fails on a
  stray `encoding/json` call on a persisted type outside `core`'s serializers —
  **decoders too**: a raw `json.Unmarshal` into a `Task` skips the passthrough,
  so the next write destroys the shard's unknown keys.
- **Schema write guard.** `scripts/check-schema-write-guard.sh` greps the other
  single path: `core.SchemaVersion` may be named only in `internal/core/*`, the
  two stores, `internal/app/{upgrade,board,lint}.go`, `cmd_board.go`,
  `schema.go` and tests. The regression it guards is one line and fails
  **silently** (every test on a fresh store still passes) — see the outage below.

---

## The `repos` field and the two-sided version gate

A task carries a **first-class `repos` set** (`owner/repo`, 0..N, the same set
semantics as labels; a repo is **not** a label; an empty set is a **draft**).
Promoting it to a schema field is what motivated the **version gate**, whose
governing idea is: `core.SchemaVersion` is the layout **this binary writes**;
`meta.json`'s `schema_version` is what **the board declares**. They are two
different numbers, and the board's is an **INPUT to every write, never an
output**.

- **`core.CheckSchemaVersion(v)` — the READ gate.** A board declaring a layout
  *newer* than the binary is refused (kind **`schema-too-new`**, exit 3 — the
  fix is the binary; `details {board_schema, binary_schema}`): a v3-only binary
  would load a v4 shard and act as if `reviewed` did not exist. Preserving
  (the passthrough) is not understanding, which is why this gate stays.
- **`core.CheckWritable(v)` — the WRITE gate.** A binary writes only a board
  declaring *exactly* its own layout. An *older* board — or shards with no
  `meta.json` (`v == 0`) — is fully **readable** but **read-only** (kind
  **`schema-upgrade-required`**, exit 2 — the board is stale). The exit code
  alone says which side is stale: 3 = the binary, 2 = the board; the orient and
  listing reads print one stderr "READ-ONLY for this binary" note
  (`warnReadOnly`) so a session learns at `furrow brief`, not at its first
  refused write (`board` and `doctor` stay quiet — reporting the mismatch is
  their output).

Both adapters enforce the read gate on `Load` and the write gate on `Save`.
**No ordinary write raises `meta.json`'s version**; `Save` stamps it only on a
genuinely fresh, empty store (`furrow init`). A garbled `meta.json` is an
**error** (exit 3, kind `internal`, subject `meta`), never a fallback to the
binary's version — that fallback once quietly *disabled* the gate.

This is the fix for a real outage: `fsstore.Save` used to stamp `meta.json` on
every write, so on 2026-07-13 one routine `furrow sync` from an unreleased
source build migrated the **shared** central board 3 → 4, and every pinned
release in the fleet lost it at once (v0.6.1 reported "task not found" for every
id; v0.7.0 exited 3). `scripts/check-schema-write-guard.sh` makes the regression
un-writable. The half the gate cannot see — a field added WITHOUT a bump — is
the passthrough's (above).

### `furrow upgrade` — the one raiser, and a flag day

`App.Upgrade` ([`internal/app/upgrade.go`](../internal/app/upgrade.go)) is the
only caller of `SetBoardVersion`. It previews unless `--yes`, raises
`.furrow/meta.json` **and the `archive/` store's** (a board is two stores on
disk), re-serializes every shard through `core.MarshalTask`, is idempotent on a
current board, and refuses a board newer than the binary — there is **no
downgrade path** (recovery is `git revert` on the board repo). Its report is
`{from, to, changed, applied, stores}` under `--json`.

It is a **flag day**: once it lands, no older furrow can write that board,
pinned CI included, and furrow cannot see the fleet's pins — so the **ordering
is the human's**: (1) release a furrow shipping the layout, (2) bump every
caller's `sync-task-status.yml@vX.Y.Z` pin *and* that workflow's
`furrow-version` default, (3) only **then** `furrow upgrade --yes` + `furrow
sync`. `sync-task-status.yml` pre-flights `furrow board --json` and fails with
one annotated error when `.writable != true`.

### `board` reports; it never fails

`App.Board` reads the version **ungated** (`Store.BoardVersion`) and **reports**
the schema triple — `schema_version`, `binary_schema_version`, `schema_state`
(`current` | `outdated` | `too-new` | `unreadable`) and `writable` — instead of
raising: `board` is the last command that works when board and binary
disagree, which is what makes it the CI pre-flight. `furrow lint` complements
it with `schema-outdated` as a **warning**, because a read-only board is the
legitimate middle of a flag day and must not red every repo's CI.

The same contract makes the `git` key beside it a **state, never an error**:
`App.boardGit` probes the enclosing repo (HEAD, whether `.furrow/` is dirty,
ahead/behind from local knowledge only) and folds every failure into `doctor`'s
closed vocabulary (`ok` / `not-a-repo` / `no-upstream` / `unavailable`), each
probe independent. Dirty is scoped to `.furrow/`, so a co-located operator's
source edit never reads as a dirty BOARD.

---

## The store

`internal/store/fsstore` is the only package that touches the filesystem for the
store; it takes the few config values it needs (lane order, id prefix/width) as
arguments so it never imports `internal/config`.

A `.furrow/` store directory contains:

```
.furrow/
  tasks/<id>.json      one shard per task — written ONLY via core.MarshalTask
  epics/<id>.json      one shard per epic (e- ids) — core.MarshalEpic
  bodies/<id>.md       prose, one file per task OR epic (hand/agent editable;
                         ids are prefix-disjoint, so one directory is unambiguous)
  bodies/assets/       <id>-<sanitized-name>, written ONLY via Store.SaveAsset
  meta.json            {"schema_version": 10} — MarshalMeta; stamped only on a
                         fresh store (`init`) or by `furrow upgrade`; an ordinary
                         Save READS it (the write gate) and leaves it alone
  repos/<owner>__<repo>.json   one review shard per repo — MarshalRepo
  config.toml          human config (written only by `furrow config set`)
  archive/             a sibling sharded store for aged done tasks
```

### Load and Save (shard fold / split)

`fsstore.Load` folds every `tasks/*.json` shard into one in-memory
`core.Index`, checks `meta.json`'s version against the read gate, and records
the bytes of every shard it read (`Index.MarkSeen`). `fsstore.Save` is the
inverse and touches **only what this index changed** — a task is rewritten when
its bytes moved from what `Load` read (an identical file keeps its mtime), a
shard is deleted only when `Load` met it and the index dropped it. So a no-op
save touches no files — and a shard this index **never met** is left exactly as
found. That
last clause is load-bearing: the store has no lock, so two processes routinely
sit between each other's `Load` and `Save`; the old sweep deleted every shard
the other process had just added (ten concurrent `furrow add` left ZERO shards,
t-msqv). What remains unguarded is a same-task race — the later `Save` wins,
which is what `--expect-updated` reports. `memstore.Save` answers the same two
questions per entry.

`Save` *reads* `meta.json` as the write gate's input and stamps it only when
the store is genuinely empty (`furrow init`); the index carries no version
field a write could trust.

### Atomic writes (tmp + rename)

Every write goes through `atomicWrite`: a temp file (`.tmp-*`) in the
**destination directory**, write, `fsync`, `close`, `os.Rename` over the
target — a crash never leaves a half-written shard; a bulk change is atomic
**per shard**, so an interrupted one leaves a coherent, re-runnable store.

**A batch of bodies lands in two phases** (`SaveBodies`): every body is staged
to its temp file first — the half that can fail — and only then are they all
renamed over their targets, so a staging failure changes no body and leaves no
temp. A note is not idempotent: a per-id loop that failed on the third body had
annotated the first two while the index write never happened, and the retry
appended the note again (t-5n2x). The app composes every body before writing
any, so the store's one call is the last step that can refuse before the index
save.

### Lazy body load

The `Index` holds only metadata; `Task.Body` is a *relative path*, never the
prose, which `LoadBody` reads on demand (`""` when absent). Metadata diffs per
field, prose per task, and long markdown never collapses into an escaped JSON
string. `core.BodyPath(id)` is the single source of the path.

### Frozen, collision-free random ids

`NextID` returns a **random** id: `prefix` + a Crockford-base32 suffix from
`crypto/rand` (`[ids].width` chars, default 5, e.g. `t-k3m9p`). There is **no
shared counter**, so two operators in separate worktrees never mint the same
id; the app redraws on the rare in-index clash, `lint` flags a duplicate as the
cross-branch backstop, and both stores **refuse to `Save`** an index carrying
one (`core.CheckUniqueIDs`) — shards are keyed by id, so a save would keep one
file and silently delete the other's. Reads stay open on such a board for
diagnosis; a misnamed shard with a unique id is repaired, not refused. Ids are
**frozen** (never reused or renumbered); legacy numeric ids coexist.

`Load` on a missing `tasks/` returns an empty `Index`, so `add` works on day one.

### memstore

`internal/store/memstore` is the in-memory `core.Store` (a normal package, not
a test helper); its `BodyFile` returns `""`, so `$EDITOR` shell-out is
unsupported against it, which `app` reports.

**It may never promise LESS than fsstore.** fsstore serializes on write and
parses fresh bytes on read, so canonicalization and isolation come for free; the
twin does both explicitly (`Save`/`SaveRepo`/`SaveEpic` round-trip through the
single `core.Marshal*`/`Unmarshal*` path; every read deep-copies out), because
every divergence lands as a test that is green against a shape no real board
can hold. `internal/store/memstore/parity_test.go` pins the two divergences
that shipped, against a real `fsstore`.

---

## The coordinator and the CLI contract

`internal/app` is the **only mutation funnel**: the CLI (and any front-end,
through the CLI/JSON contract) calls `App` methods, one per verb; the
authoritative list is the exported method set of [`internal/app`](../internal/app)
itself (a hand copy here rotted to 25 of 88). One place for every edit is what
keeps the invariants from being re-implemented. `App.load()` canonicalizes on
every read, so reads see lane→priority→id order regardless of any hand-edit.

**Every verb reads the board once** — writes through `App.mutate`/`mutateIn`
(a verb that validates first applies against the index it already holds), and
the reads through their own single snapshot (`listMatched`, `epicDetailIn`,
`epicMemberStats`). A second load is a second snapshot, and the store has no
lock: `check` once range-checked on one and indexed on another, a panic when a
co-writer shortened the list.

**A write that changes nothing leaves `updated` alone.** Every write path
re-marshals the shard after the edit and compares bytes (`App.stampIfChanged`;
`shardChanged` for the batch paths): equal bytes, no stamp — the `Save` still
runs, since `Store.Save` writes only changed shards and a path that moved a
NEIGHBOR (reorder's respace) must persist it. `updated` is the clock `is:stale`,
`revisit`, `lint`'s reconcile-gap and `ls --since` read, so an idempotent retry
must not reset it. Boxes obey the same rule (`mutateEpic`); PROSE is the
exception — `note`, `done --note`, a body replacement stamp unconditionally,
because the body lives outside the shard where the comparison cannot see it.

The invariants the funnel keeps, beyond what each verb's `--help` promises:

- **`Move` / `Done`**: moving INTO the done lane stamps `Closed`, moving OUT
  clears it; other terminal lanes (`icebox`, `waiting`) leave it alone — *parked
  is not closed*.
- **Actionable has one definition** (`App.actionable`: in a `[next].lanes` lane,
  every dep done — deliberately NOT epic-aware), shared by `next`, `ls
  --actionable`, `is:actionable` and `ls --tree`'s ★; `next` narrows it further
  with the active/pinned epic scope (`App.NextScope`), so ★ is a strict superset
  of what `next` hands out. Lane semantics live in config, never in core.
- **Epic roll-ups are derived, never stored**, and computed for every box in
  ONE pass over the full index (`epicMemberStats`) so a read filter cannot
  under-count a box; `epic` is the GROUPING and `deps` the GATE (a DAG across
  boxes), so they never nest.
- **Assets move by one rule** (`asset_hold.go`, shared by archive / unarchive /
  rm): a store loses a file only when nothing remaining there holds it — no
  remaining body shows it, no remaining entity owns it. Planned before any
  write, applied after both indexes are durable, so an interrupted run
  converges on retry.
- **`[[id]]` links have one definition** (`internal/core/links.go`), read by
  both `show --backlinks` and `lint`'s dangling-link check, so the two can never
  drift; a bare id is not a link and a `[[id]]` inside code is inert.

### The session write guard

`internal/app/session_guard.go` mechanizes a rule judgment kept breaking (on
2026-09-10 one Claude Code session ran `furrow add` into a repo where another
was working autonomously). The guard runs in the funnel, once per write path,
on the entity's repos BEFORE ∪ AFTER the edit (an add: after the board-scope
union), and a refusal leaves the store untouched — it runs before `Save`,
before the asset write in `Attach`, before `fn` in the epic funnel, and over
the whole batch before `moveMany`'s per-id loop
(`TestSessionGuardRefusalLeavesEveryFileUntouched`).

The decision is pure (`core.SessionClashes`): self is the registry entry with
this process's `CLAUDE_PID`; an occupant is any other live session that started
strictly EARLIER and whose cwd derives to a repo the write touches (the
`repo = "auto"` derivation, worktree-aware). A clash is *idle* when the
occupant's turn is known to have ENDED (`TurnEnded`: the transcript's last
record is an assistant `end_turn` — however fresh, the closing message IS the
last write) or when it has been silent past `[session].busy_seconds`;
otherwise *busy*. The window is the ceiling on a mid-turn reading, not the
signal, so a long tool call goes idle after it rather than refusing forever;
`busy_seconds = 0` makes every clash idle (refusals off, guard on). Busy
refuses (exit 2, `session-busy`, `details.clashes`, `details.hint` = `--draft`
on an add); idle warns on stderr and puts `session_warn {clashes}` in the
`--json` envelope. First come, first served keeps the autonomous session safe:
its own writes never clash with a later watcher.

Four stand-downs, each one stderr `note: session guard: …` line and never a
refusal: no `CLAUDECODE` env (a human shell, CI), an unreadable registry
(`doctor`: `session-registry-unreadable`), a self not in it, and a self whose
own transcript cannot be found (a refusal on a guess would be a new failure,
worse than the one prevented). The registry format is read in exactly one
package (`internal/claudecode`); the board-level `[session]` section holds the
one knob because what "still working" means is a policy of the shared board.

### CLI commands

Registered in [`internal/cli/root.go`](../internal/cli/root.go); each command's
contract — flags, `--json` shape, exit codes — is its `--help` and README's
command notes (the README table is generated from this tree), never a copy here:

`init`, `add`, `ls` (alias `list`), `show`, `next`, `brief`, `revisit`, `search`, `stats`,
`board`, `boards`, `doctor`, `edit`, `note`, `attach`, `done`, `move`, `reorder`,
`retitle`, `set`, `value`, `effort`, `check`, `dep`, `epic`, `label`, `repo`, `ref`, `review`,
`apply`, `sync`, `archive`, `unarchive`, `rm`, `tidy`, `migrate`, `upgrade`, `lint`, `config`, `schema`, `version`
(the hidden `commands`/`vocab` are the generators' own).

### Output, errors, and exit codes

- `--json` emits JSON to **stdout only**; logs and errors go to stderr. `--json`
  and `--ndjson` are honored **wherever furrow emits JSON**: `jsonMode()`
  (`internal/cli/output.go`) is the single predicate, `emitObject`/`emitList`
  the single emitters (indented, or compact one-per-line). A command whose `Use`
  says `<id>...` (`show`, `done`, `move`, `set`) is ALWAYS an array, whatever
  the argv length; a one-id mutation is an object; the report-shaped commands
  (`rm`, `archive`, `tidy`, `upgrade`, `apply`) are one object; `lint` streams
  one problem per line. CLI JSON uses the shards' encoding.
- **Read filters resolve through one path.** Every filtering read (`ls`, `next`,
  `revisit`, `stats`, `search`) takes the same `-s`/`-l`/`-r`/`-n`/`-q`, and the
  batch mutators (`set`/`done`/`move`) take `-q`/`-l`/`-r` as a write-side
  selector resolved through that very path, so `ls <flags>` previews exactly
  what the write would touch. `-r` is the scope control, `-l` a pure tag filter;
  a comma is OR within a flag and repeats union (`StringArray`, never
  double-split). An unknown `-s` lane fails fast (exit 2 + `candidates`, a
  closed vocabulary); an unknown `-l` tag matches nothing (open) — unless it
  uniquely names a repo, the did-you-mean guard. `TestRepeatableFlagNotationFrozen`
  freezes which repeatable flags split on comma (identifiers) and which take
  values verbatim (free text, paths, URLs). `furrow board [--json]` is the
  introspection read: store path, discovery source, mode/layout, scope, the
  lane vocabulary and the schema triple, without provoking an error.
- **Non-interactive by default**: no prompts; `furrow edit` on a non-TTY prints
  the body path instead of launching an editor; `NO_COLOR`/non-TTY suppress
  color. **Destructive ops preview unless `--yes`** (`archive`, `rm`, `tidy`,
  `upgrade`).
- **Exit-code contract** (`internal/core/errors.go`): `0` ok — **including an
  empty query result** / `1` a **specifically requested id** was not found,
  never an empty list / `2` bad-usage or validation / `3+` internal or IO, with
  `130`/`143` (128+signal) for an interrupted run (`sync-interrupted`). On a
  non-zero exit the CLI prints `{"error":{"kind","subject","retryable","exit",
  "message"}}` to stderr — `kind` a closed vocabulary (`furrow vocab
  error-kinds`), `retryable` whether re-running is the documented recovery —
  plus `candidates` (a near-miss: an ambiguous repo short name, an unknown lane
  or (sub)command) and `details` (`sync-conflict`'s paths; the version gate's
  `{board_schema, binary_schema}`). The contract also lives in the root
  command's `--help`. `cmd/furrow/main.go` is literally `os.Exit(cli.Execute())`.

---

## Configuration

`internal/config` reads `.furrow/config.toml` into an effective `Config` for
every command; **the one writer is `furrow config set`** (a surgical,
git-config-style edit; `furrow init` writes the template once), landed with the
store's tmp+rename. The policy is **clamp-don't-reject**: unknown keys are
ignored, out-of-range values fall back with a warning `furrow lint` surfaces, a
missing file is the defaults; only malformed TOML is an error — a table
defined twice included, since blanking the header would hand its keys to the
table above.

Sections and their defaults:
`[lanes]`, `[next]`, `[priority]`, `[ids]`, `[labels]`, `[archive]`, `[lint]`,
`[due]`, `[revisit]`, `[review]`, `[session]`, `[alias]`, and the top-level
`mode` and `default_repo`. The keys and per-key reasoning live in the repo-root
[`config.toml`](../config.toml), the exact file `furrow init` writes and
check.sh diffs byte-for-byte; `scripts/check-docs-vocab.sh` holds this list to
`config.TopLevelKeys()`.

`status` is just a lane from `[lanes].order` — the status enum and the sort
rank at once. `mode` is presentation-only (no schema gate, no byte), so it lives
in `config.toml`, not `meta.json`. `[alias]` expands before cobra dispatch,
builtin-first (a shadowing alias is inert; `lint` warns `alias-shadow`), and
command knowledge stays in `internal/cli`, so it needs no port.

### User-level config: central boards

The user-level `${XDG_CONFIG_HOME:-~/.config}/furrow/config.toml` declares
central boards as `[[board]]` entries (`path`, `scopes`, `repo`, `label`,
`auto_filter`, `autocommit`); its shape and setup are README's ([Central
board](../README.md#user-level-config-no-per-repo-file)). What lives here is
the resolution, split to honour the purity rule:

- **`internal/config`** parses and **clamps per entry** (no `path`, or no
  `scopes` after blanks are pruned: dropped with a warning; all dropped: "no
  user-level board"). It never touches cwd or the filesystem.
- **`internal/app`** (the only fs/cwd-aware layer) is the last arm of `discover`
  after `FURROW_DIR`, a local `.furrow`, and `.furrow-pointer.toml`: it resolves
  paths, canonicalizes cwd and scopes (symlinks), and picks the board whose
  scope is the **longest canonical prefix** of cwd; only the winner is
  `stat`-ed, so a broken path in another scope never breaks this directory.
  `FURROW_BOARD=<path>` short-circuits the file.

**Scope vs store.** A `[[board]]` injects a scope repo like a pointer does
(`repo = "auto"` derives it from the checkout); `auto_filter` decides whether
reads filter by it (writes attach regardless). The board's own **`default_repo`**
is the FALLBACK, applied by `app.applyBoardScope` only when discovery ran an arm
that declares no scope at all (`FURROW_DIR`, a local `.furrow`) — the gate is the
ARM, not an empty repo, or a committed shared file would override a nearer
per-machine one. `"auto"` is refused there (a cwd-derived repo differs per
checkout), and `furrow doctor` runs the same function, so its `scope_repo` can
never disagree with the real commands.

**`autocommit`** is per-machine on purpose (the board config syncs to every
clone and CI): it reuses `partitionSync` — exactly what `furrow sync` commits
by — plus the ids this command wrote, is best-effort (a commit failure never
turns a landed mutation into a non-zero exit, which would make an agent
double-apply), never fetches or pushes, and refuses when the enclosing git repo
is not the board's own.

**Repo derivation (`repo = "auto"`)**, in [`internal/app/gitorigin.go`](../internal/app/gitorigin.go),
is **file reads only — no git subprocess**: find the checkout (a `.git` file's
`gitdir:` and `commondir` are followed, so a worktree named `chord-fix-y` still
derives `owner/chord`), take the **first `url` line of `[remote "origin"]`** and
nothing else (never `pushurl`, a second line, or another remote — a foreign
URL once sat there), fall back to a ghq-style path, and failing both open
UNSCOPED with a stderr note so `add` creates drafts. The invariant: **a bare
directory name is never written into `repos`**. `label = "auto"` is a reserved
tombstone (warned, ignored). `furrow config init` / `config set --user` are the
file's two writers, through `internal/app`; discovery stays silent on its inert
path, so `lint` and `config path` are what report a half-written file.

---

## What's NOT in scope

This document covers the *built* architecture. What furrow deliberately does
**not** do — no MCP server or plugin, no GitHub Issues coupling, no binary
store, no sync daemon, no in-repo UI — is catalogued with its rationale in
[`non-goals.md`](non-goals.md). Two facts those choices rest on live above: the
CLI *is* the agent interface, and furrow is non-interactive by default.

---


