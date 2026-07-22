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
     internal/schema   JSON Schema source ( `furrow schema [task|meta|repo]` )
     internal/version  build version string (ldflags-injected)
     internal/migrate  pure Task.md parser behind `furrow migrate`
     internal/query    pure `-q` typed-query parser (lexer+parser -> AST);
                       app.compileQuery binds the AST to a task predicate
```

A dependency arrow means "imports". Note what is **absent**: `internal/core`
imports no other furrow package and no third-party library; `internal/cli` never
imports a store adapter or `internal/core`'s siblings directly for mutation — it
goes through `internal/app`. furrow is **CLI-only**: the interactive TUI once in
`internal/tui` has been removed, and any TUI/GUI now lives in a separate front-end
repo (**ridge** — `github.com/akira-toriyama/ridge`, a charm-v2 TUI that is a
CLI/JSON client of furrow; and **loom** — `github.com/akira-toriyama/loom`, a
from-scratch TUI framework, future/gated) that consumes the same `--json`/`--ndjson`
contract an agent does.

### Package responsibilities

| Package | Role |
|---|---|
| `cmd/furrow/main.go` | Entry point. Just `os.Exit(cli.Execute())` — no logic. |
| `internal/cli` | cobra adapter and the **only** in-repo presentation layer: parse flags, call `app`, render (human table or `--json`/`--ndjson`), map errors to exit codes. Holds no task logic. A TUI/GUI is an out-of-repo front-end (ridge / loom) driving this same CLI/JSON contract. |
| `internal/app` | Coordinator. Wires a `Store` + `Config` + `Clock`; exposes every mutation/query as a method. The **only** place that mutates state. |
| `internal/config` | Loads `.furrow/config.toml` (read-only, clamp-don't-reject). Produces an effective `Config`. |
| `internal/store/fsstore` | The **only** package that touches the filesystem for the store: atomic writes, lazy body load, random id generation. |
| `internal/store/memstore` | In-memory `core.Store` for tests and `migrate --dry-run`. A normal non-test package. |
| `internal/gitrepo` | git subprocess adapter behind `furrow sync` and `furrow doctor`'s freshness probe (command assembly + error classification). Driven only through `internal/app`; the store files themselves stay fsstore-owned. |
| `internal/core` | Pure domain: `Index`/`Task`/`ChecklistItem` structs, the `MarshalTask`/`MarshalMeta` serializers and their `Unmarshal*` inverses (incl. the unknown-key passthrough), the in-memory `Marshal`, the `Store`/`Clock` ports, `Validate`, the two-sided version gate, and in-memory index ops. |
| `internal/schema` | The JSON Schemas for a task shard, `meta.json`, and a repo review shard as Go constants; emitted by `furrow schema [task|meta|repo]`. |
| `internal/migrate` | Pure parser (stdlib only) behind `furrow migrate`: hand-maintained `Task.md` in, tasks + LOUD warnings for anything unmappable out. The CLI wires it to the store; dry-run by default. |
| `internal/query` | Pure parser (stdlib only) for the `-q` typed-query DSL: a flat AND-list of `field:value` terms (comma=OR, `-`=NOT, `has:`/`no:`, `is:`) → an AST. It knows the GRAMMAR, not furrow's fields; `internal/app`'s `compileQuery` binds each term to a task predicate (validating fields/lanes, exit 2 + candidates on a miss). |
| `internal/gittest` | Test-only helper: `Isolate()` neutralizes global/system git config at the process-env level (called from `TestMain`) so real-git tests — especially `App.Sync`'s subprocess — don't flake on a developer's `commit.gpgsign`/`core.hooksPath`. Imported only by `_test.go` files. |
| `internal/version` | Build version, default `"dev"`, overridden via `-ldflags`. |

---

## The purity rule

`internal/core` is the spine, and it is **pure**: it imports only the Go
standard library (`encoding/json`, `sort`, `time`, `fmt`, `errors`, `regexp`).
It must **not** import:

- `cobra` (a presentation concern), or
- `os` or `path/filepath` (filesystem access is an adapter concern).

Filesystem access lives in `internal/store/fsstore`. Presentation lives in
`internal/cli` (the only in-repo presentation layer; a TUI/GUI is an out-of-repo
front-end). The domain reaches the outside world only through interfaces it
declares itself.

> The doc comment at the top of `internal/core/task.go` states this rule
> in-code, so it travels with the source.

### Ports live IN core

The seams between the pure core and the outside world are interfaces declared in
[`internal/core/ports.go`](../internal/core/ports.go):

- **`Store`** — persists the per-task metadata shards and per-task bodies. It owns
  *all* path construction (callers never assemble `".furrow/bodies/<id>.md"` by
  hand) and *all* atomicity. Methods: `Load`, `Save`, `BoardVersion`, `LoadMeta`,
  `Writable`, `SetBoardVersion`, `LoadBody`, `SaveBody`,
  `BodyExists`, `ListBodyIDs`, `ListTaskIDs`, `SaveAsset`, `ListAssets`,
  `NextID`. `BoardVersion` reads the layout version the board *declares*
  (ungated, so `furrow board` can diagnose a board nothing else can open), and is
  derived from **`LoadMeta`**, which returns `meta.json` *whole* — the version plus
  the unknown top-level keys the passthrough parked. That distinction is the point:
  `BoardVersion` projects the one field it wants and discards the rest, so it can
  never see a typo; `lint` reads `LoadMeta` to warn `unknown-shard-key` on
  `meta.json` itself;
  **`Writable`** is the side-effect-free predicate behind the write gate — it
  answers "may this binary write this board?" (`nil` = yes, else the refusal every
  mutation would raise), so the callers that only need to *report* the state
  (`furrow board`, `furrow lint`, and `archive`, which must gate the hot board
  before it touches the sibling archive store) ask it instead of re-deriving the
  rule; a second copy of the rule is how the reporting and the enforcement drift
  apart. `SetBoardVersion` is the **one deliberate raiser**, called by
  `furrow upgrade` and nothing else; it **reads** the existing `meta.json` and
  raises its number rather than writing a fresh `core.Meta`, so the upgrade cannot
  eat the forward-compatible keys the passthrough exists to carry.
  The two asset methods are the store half of `furrow attach` /
  `furrow lint`'s asset checks: `SaveAsset` copies media into the task's asset
  area `bodies/assets/<id>-<name>` (sanitized, collision-free, atomic) and
  returns the final basename; `ListAssets` enumerates `bodies/assets/` as
  name+size, a missing dir yielding nil, not an error.
- **`Clock`** — supplies `Now()`. Injected so tests get deterministic timestamps
  and the marshaller's UTC/whole-second contract is trivial to honor.
  `core.SystemClock()` is the production implementation.

These interfaces are implemented by adapters: `internal/store/fsstore` (the real
filesystem) and `internal/store/memstore` (an in-memory fake). Both carry a
compile-time assertion `var _ core.Store = (*Store)(nil)`. The `app` and `cli`
layers depend on the *interface*, never on a concrete adapter — that is what
keeps the core testable without touching disk.

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
`core.MarshalMeta(...) ([]byte, error)` writes `meta.json` — the latter only from
`Store.SetBoardVersion` (i.e. `furrow upgrade`) and the fresh-store stamp, never
on the ordinary write path (see the version gate). Every writer —
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

### Unknown-key passthrough (the other half of the version gate)

[`internal/core/passthrough.go`](../internal/core/passthrough.go) makes the
round-trip **lossless**. `core.UnmarshalTask` / `UnmarshalRepo` / `UnmarshalMeta`
park every **top-level** key the binary does not know in an unexported `extras`
field, and the matching `Marshal*` re-emit them **sorted, after the known keys** —
so an old binary hands a future field back exactly as it found it.

Why it exists: the version gate below only fires when someone **bumps**
`core.SchemaVersion`. If a future furrow adds a shard field and does not bump —
because the change looks "additive" — `meta.json` still says v5, no gate fires
anywhere, and an older binary reads the shard, drops the key it doesn't know
(`encoding/json`'s lenient unmarshal), and writes the loss back on the next save.
**One ordinary write, one destroyed field, no error.** The 2026-07-13 outage was
only *visible* because someone did the right thing and bumped; this is its silent
twin. Stated as a pair: **the gate stops a bumped layout from being misread; the
passthrough stops an unbumped one from being destroyed.**

Three details are load-bearing:

- **"Known?" is answered with `encoding/json`'s own matcher.** json matches struct
  fields case-**IN**sensitively — a shard key `"BODY"` populates `Task.Body` — so a
  case-sensitive set would park `BODY`, re-emit it, and leave a shard carrying both
  `body` and `BODY`, self-replicating on every read. But a `strings.ToLower` set is
  wrong too, and more dangerously: json matches by Unicode simple **case-folding**,
  which is *not* lowercasing. The two disagree in both directions, and each
  direction is its own corruption (both were reproduced end-to-end before the fix):
  a key json folds but `ToLower` does not (`"statuſ"`, U+017F) is consumed into
  `Task.Status` **and** parked — and since extras are re-emitted last, the stale
  copy wins on the next read, so `furrow move` never takes and the task wedges in a
  lane forever; a key `ToLower` folds but json does not (`"İd"`, U+0130 — it
  lowercases to `id` but has an empty fold orbit) is deleted as "known" while
  `Task.ID` stays empty, destroying the key and the task's identity. `core.isKnown`
  therefore uses **`strings.EqualFold`**, json's own relation, so a key is parked
  **iff** json ignored it. `TestKnownKeysFoldExactlyLikeEncodingJSON` pins both
  directions and fails if the stdlib's matcher ever moves.
- **The carrier is unexported, and `Task` must never grow a `MarshalJSON`
  method.** `encoding/json` cannot see an unexported field, so `extras` can never
  surface as a literal `"extras"` key nor leak into `internal/cli`'s `--json`
  views. That is also the constraint: those views **embed** `core.Task` to place
  `body_text` / `reason` / `revisit` / `snippet` / `mentioned_by` beside it, so a
  `MarshalJSON` on `Task` would be **promoted** to the outer struct — Go would call
  it for the whole view and silently drop every sibling field, with no compile
  error. The splice therefore lives on the store's write path (`core.MarshalTask`),
  not on the type.
- **The byte recipe is untouched.** The object is composed *compactly* (known
  fields in struct order, then the unknown ones sorted) and indented **once** as a
  finished document, so the 2-space / `SetEscapeHTML(false)` / trailing-newline
  rules still live in exactly one place. A shard with **no** extras — the
  overwhelmingly common case — marshals byte-identically to what furrow has always
  written, so no existing board sees a single rewritten shard.

`fsstore.SetBoardVersion` **reads** `meta.json` and raises its number rather than
building a fresh `core.Meta`: a fresh one carries no extras, so `furrow upgrade` —
the one command whose whole job is to move a board *forward* — would itself have
eaten `meta.json`'s forward-compatible keys.

The honest limits, none of them papered over:

1. **Not retroactive.** Every furrow release up to `v0.9.0` destroys unknown
   keys on write (passthrough ships in `v0.10.0`). A shared board is safe only
   once **every** writer has passthrough — including every repo's pinned
   `sync-task-status.yml@vX.Y.Z` CI caller. The hole closes on the day the last
   pin is bumped past `v0.9.0`, not the day the code merges. Until then, keep
   bumping `SchemaVersion` on every field addition.
2. **Top-level only.** A key inside a known nested object (`checklist[].note`) is
   still dropped — which is why the JSON Schemas flip the three top-level objects
   to `"additionalProperties": true` while `$defs.checklistItem` stays `false`: the
   schema must not promise what the marshaller does not do.
3. **Preserved is not honoured.** An old binary carries a future `"blocked": true`
   and still hands you that task in `furrow next`. Passthrough downgrades silent
   *data loss* to silent *semantic misbehavior* — a real improvement (loss is
   unrecoverable, misbehavior is fixed by updating the binary), but only the version
   gate can say "refuse to operate". `furrow lint` warns **`unknown-shard-key`**
   (`SevWarn`, naming the keys and blaming the task id / the `owner/repo` / `meta`)
   so the carried-but-ignored case is
   visible — and so is its other cause, a typo in a hand-edited shard (`"lables"`),
   which is now **permanent**: nothing removes it, because auto-deleting a key we do
   not understand IS the bug being fixed. The warning covers **all three** written
   file kinds, and that is not tidiness: flipping their schemas to
   `additionalProperties: true` removed the only thing that ever rejected a typo in
   `meta.json` or a `repos/` shard, so a task-only lint would have shipped a
   detection regression inside a data-preservation fix. `Store.LoadMeta` exists for
   exactly this — `BoardVersion` projects the one field it wants and throws the rest
   away, so it cannot see a key nobody knows.
4. **Position churn across vintages.** A future binary declaring its field
   mid-struct writes the key there; an older one re-emits it at the end. That is a
   one-line-move diff on alternating writes — churn, not loss — and the convention
   that avoids it is: **new shard fields go at the END of the struct.**

The passthrough itself shipped **without** a bump — `core.SchemaVersion` stayed
4 in that release. The layout was unchanged and a no-extras shard byte-identical
to the previous release's output; bumping would have taken every board read-only
and bricked every pinned CI caller in order to advertise a feature to exactly
the binaries that do not have it. The bump to **5** came separately, with the
first-class `type` field (`v0.10.0`) — a field `next`'s container skip and `ls
--type` actually read, i.e. exactly the class the golden test below says must
bump.

### How the invariant is guarded

- **Golden round-trip test.** `internal/core/core_test.go` asserts that marshalling
  the fixture index produces `testdata/index.golden.json` byte-for-byte (write →
  read → write stays identical).
- **Schema drift test.** `furrow schema [task|meta|repo]` prints
  `internal/schema.TaskV2` / `internal/schema.MetaV2` (JSON Schema draft 2020-12);
  `docs/schema/furrow.task.v2.json` and `docs/schema/furrow.meta.v2.json` are
  committed copies of the same bytes, and CI diffs both so they cannot drift.
- **Struct-fingerprint golden.** `TestShardFieldsGolden`
  (`internal/core/schema_fields_test.go`, frozen in
  `testdata/shard-fields.golden`) records every persisted type's json keys **in
  struct order**, plus the layout version they belong to. Change the shape of a
  shard and it fails, telling you to bump `core.SchemaVersion` — a flag day —
  unless no query, sort, filter, or lane decision reads the new field. (Worth
  knowing before arguing "but my field is purely additive": every field ever added
  to `Task` — value, effort, repos, reviewed, deps, refs, checklist, parent — is
  read by one. That class has never had a member; the default answer is BUMP.)
  Accept a new shape deliberately, with
  `go test ./internal/core -run TestShardFieldsGolden -update-fields`. This is the
  teeth on a rule that otherwise fails **silently**: add a field, forget the bump,
  and every test on a fresh store still passes.
- **Frozen board.** `TestFrozenBoardRoundTripsByteIdentical`
  (`internal/store/fsstore/frozen_board_test.go`, fixture in
  `internal/store/fsstore/testdata/frozen-board/`) is the byte-level twin of the
  above, and the only fixture in the repo **the code under test did not write**.
  Every other determinism test builds its board with the current marshaller, so
  both sides move together; these bytes were written by an earlier furrow and are
  committed, so they cannot. Copy → `Load` → `Save` + `SaveRepo` +
  `SetBoardVersion` → every file must come back byte-identical, with the same file
  set and **untouched mtimes** (a no-op save that rewrites is git churn on every
  board in the fleet). It shows the *damage*, not just the diff: a new
  non-`omitempty` field prints as `+ "sprint": ""` appearing in **every** shard — a
  fleet-wide rewrite on the next ordinary write, silently dropped by every older
  binary. A renamed/removed key becomes unknown, so the passthrough parks it and
  re-emits it *after* the known keys — a key-ORDER change no in-memory test can
  see. It also pins the two things nothing else covers: `meta.json`'s bytes, and
  where the extras splice actually lands on disk. Regenerate with `-update-board`,
  which rewrites a committed board and so puts the flag-day decision in the diff.
- **Single-path grep guard.** `scripts/check-marshal-singlepath.sh` greps for
  stray `encoding/json` calls on a `Task`/`Index`/meta/repo outside `core`'s
  serializers (`internal/core/marshal.go` + its passthrough half) and fails CI if
  any appear; it runs as part of `scripts/check.sh`. It guards **decoders**
  (`json.Unmarshal`, `json.NewDecoder`) as well as encoders — not for symmetry's
  sake: a raw `json.Unmarshal` into a `Task` bypasses `core.UnmarshalTask`, so the
  shard's unknown keys are never parked and the next write destroys them. A decoder
  that skips the single path is exactly as lossy as an encoder that does.
- **Schema write guard.** Its sibling `scripts/check-schema-write-guard.sh` greps
  the *other* single path: `core.SchemaVersion` — the layout **this binary**
  writes — may only be named in `internal/core/*`, `fsstore.go`, `memstore.go`,
  `internal/app/{upgrade,board,lint}.go`, `internal/cli/cmd_board.go`,
  `internal/schema/schema.go`, and tests. Any other reference fails the build: an
  ordinary write must never name it. The regression it guards is one line long
  and fails **silently** (every test on a fresh store still passes) — see the
  version gate below for the outage that proved it. Also in `scripts/check.sh`
  and `.github/workflows/build.yml`.

---

## The `repos` field and the two-sided version gate

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
motivated the **version gate**. The gate's governing idea:
`core.SchemaVersion` is the layout **this binary writes**; `meta.json`'s
`schema_version` is what **the board declares**. They are two different numbers,
and the board's is an **INPUT to every write, never an output**.

- **`core.CheckSchemaVersion(v)` — the READ gate.** A board declaring a layout
  *newer* than the binary knows is refused: error id **`schema-too-new`**, exit 3
  (internal — the fix is updating the binary, not the input), carrying
  `details {board_schema, binary_schema}`. It guards against **misreading** such a
  board: a v3-only binary would happily load a v4 shard and then act as if
  `reviewed` did not exist — sorting, filtering, and closing that task as though
  the field were not there. (It no longer guards against *destroying* the fields it
  doesn't know: the unknown-key passthrough now preserves those. But preserving is
  not understanding, which is exactly why this gate stays.)
- **`core.CheckWritable(v)` — the WRITE gate.** A binary may write only a board
  that already declares *exactly* its own layout. An *older* board — or one with
  shards but no `meta.json` at all (`v == 0`) — is fully **readable** (lenient
  forward-compat is the store's normal read) but **read-only**: error id
  **`schema-upgrade-required`**, exit 2 (validation — the *board* is stale and an
  explicit command fixes it), same `details` payload. Exit code alone therefore
  says which side is stale: 3 = the binary, 2 = the board.

Both store adapters (`fsstore`, `memstore`) enforce the read gate on `Load` and
the write gate on `Save`. **No ordinary write raises `meta.json`'s version**;
`Save` stamps it in exactly one case, a genuinely fresh, empty store (what
`furrow init` hits) — there is no prior layout to misrepresent. A garbled
`meta.json` is an **error** (exit 3, id `meta`), never a fall back to "whatever
version this binary is": that fallback quietly *disabled* the gate, making any
binary believe the board was exactly as new as itself.

This is the fix for a real outage. `fsstore.Save` used to stamp `meta.json` with
`core.SchemaVersion` on every write, so on 2026-07-13 one routine `furrow sync`
from an unreleased source build migrated the **shared** central board 3 → 4 as a
side effect. Every released furrow then lost it at once: v0.6.1 — the version the
whole fleet's `task-status` CI pinned — reported "task not found" for every id,
and v0.7.0 exited 3. `scripts/check-schema-write-guard.sh` (below) makes the
regression un-writable rather than merely unlikely.

But the whole gate is keyed on someone **bumping** the number. A field added
without a bump fires nothing, and an old binary would drop it and write the loss
back — the same outage, silent. That half is closed by the **unknown-key
passthrough** (above): *the gate stops a bumped layout from being misread; the
passthrough stops an unbumped one from being destroyed.* They are not
substitutes — passthrough preserves a field it cannot honour, so the rule "bump
`core.SchemaVersion` when the shard layout changes" still stands, now enforced by
`TestShardFieldsGolden`.

### `furrow upgrade` — the one raiser, and a flag day

`App.Upgrade` ([`internal/app/upgrade.go`](../internal/app/upgrade.go)) is the
only code that may move a board's version, via the `SetBoardVersion` port method
that nothing else calls. It:

- **previews unless `--yes`** (the `furrow archive` guard), printing the flag-day
  checklist;
- raises `.furrow/meta.json` **and `.furrow/archive/meta.json` when the archive
  store exists** — a board is two stores on disk, and raising only the hot one
  would leave the next `furrow archive` meeting the write gate on a store nobody
  remembers exists;
- **re-serializes every shard** through `core.MarshalTask`, so the on-disk bytes
  become canonical for the new layout in one deliberate commit;
- is **idempotent**: a current board is a clean no-op (`changed:false`, exit 0,
  zero bytes written);
- **refuses a board newer than the binary** (`schema-too-new`, exit 3) — there is
  **no downgrade path**, since inventing one would strip the very fields the gate
  exists to protect; recovery is `git revert` on the board repo;
- emits `{from, to, changed, applied, stores:[{path, from, to, tasks}]}` under
  `--json`/`--ndjson` (`changed` = anything is behind; `applied` = `--yes` was
  passed and the write happened, so "nothing to do" and "I would do this" are
  distinguishable without parsing prose).

It is a **flag day**: once it lands, no older furrow can write that board —
including a CI pinned to an older release. furrow cannot see the fleet's pins, so
the **ordering is the human's**: (1) release a furrow shipping the layout, (2)
bump every caller's `sync-task-status.yml@vX.Y.Z` pin *and* that workflow's
`furrow-version` default, (3) only **then** `furrow upgrade --yes` + `furrow
sync`. The preview prints exactly this checklist, and
`.github/workflows/sync-task-status.yml` pre-flights `furrow board --json`,
failing with one annotated error (`::error title=furrow schema mismatch::`) when
`.writable != true` rather than letting a pinned binary emit N "task not found"s.

### `board` reports; it never fails

`App.Board` appends the schema triple to its snapshot — `schema_version` (what
the board declares; `0` = absent or unreadable), `binary_schema_version`, and a
stable kebab-case `schema_state` (`current` | `outdated` | `too-new` |
`unreadable`) plus `writable` (== `schema_state == "current"`). It reads the
version **ungated** (`Store.BoardVersion`) and **reports** a mismatch instead of
raising it. That is load-bearing, not a nicety: `board` is the last command that
still works when board and binary disagree, which is what makes it usable as the
CI pre-flight and the human's first diagnosis (`schema:   v5 (board) / v5
(binary) — writable`). `furrow lint` complements it with a `schema-outdated`
**warning** (`SevWarn`, id `meta`) — warn, not error, because a read-only board
is the legitimate middle of a flag day and must not red every repo's CI.

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
  meta.json            board-wide layout version {"schema_version": 5} — MarshalMeta,
                         stamped only on a fresh store (`init`) or by `furrow upgrade`;
                         an ordinary Save READS it (the write gate) and leaves it alone
  repos/               one review shard per repo (repos/<owner>__<repo>.json) — MarshalRepo
  config.toml          human config (read-only from furrow's side)
  archive/             a sibling sharded store: aged done tasks moved out of the hot store
```

### Load and Save (shard fold / split)

`fsstore.Load` globs `tasks/*.json`, unmarshals each shard, and folds every one
into a single in-memory `core.Index`; the board's `schema_version` is read from
`meta.json` via `Store.BoardVersion` (0 = no `meta.json`; unreadable = an error)
and checked against the read gate. `fsstore.Save` is the inverse: it splits the
`Index` back into per-task shards and writes **only the shards whose bytes
changed** (a byte-compare against what is already on disk), and **deletes** the
shards of any ids no longer present. So a no-op save touches no files and
produces zero git churn.

`Save` does **not** write `meta.json` on this path — it *reads* it, as the write
gate's input (`core.CheckWritable`), and stamps it only when the store is
genuinely empty (no shards, no meta — `furrow init`). The `Index.SchemaVersion`
field is informational: it is whatever `Load` saw, and `Save` deliberately
ignores it, because an in-memory field defaults to the binary's version at
`Canonicalize` time and trusting it is precisely how a routine write once
migrated a shared board behind its owner's back.

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

`internal/app` is the **only mutation funnel**. The CLI (and any out-of-repo
front-end, through the same CLI/JSON contract) calls `App` methods — `Add`, `Move`, `Done`, `Reorder`, `SetTitle`, `SetValue`,
`SetEffort`, `Check`, `AddCheck`, `AddDep`/`RemoveDep`, `Relabel`, `Rerepo`,
`Attach`, `ApplyDirectives`, `Sync`, `Archive`, `Upgrade`, `Lint`, `EditPath`, plus the read methods
`Get`, `List`, `Next`, `Revisit`, `Board`. Keeping every edit in one place is what keeps
the invariants (frozen
ids, canonical order, closed-timestamp rules, body↔index pairing) from being
re-implemented — and from being reinvented by an out-of-repo front-end, which
reaches the same funnel through the CLI/JSON contract rather than the Go API.
`App.load()` canonicalizes on
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
  excluded), with every named dependency already in the done lane, **and not a
  container type**. Lane semantics live in config, not core — `Index.Actionable`
  takes the terminal set and the done-id set as arguments, the `[next].lanes` gate
  is applied in `app` via `Config.IsNextLane`, and the container gate via
  `Config.IsContainerType` (a `[types].containers` type, e.g. `epic`, is a box, not
  work). `App.workable` is the type-blind readiness test (lane + deps); the composed
  predicate `App.actionable` = `workable && !container`, shared with `Tree` so the
  two never drift on "you could pick this up now". `furrow next --containers`
  relaxes `Next` to `workable` so a ready box surfaces on request; the tree ★ never
  does (a box is never actionable).
- **`Tree`** (`ls --tree`) builds the **parent** forest — furrow stores two
  relations between tasks and this is the one that nests. `parent` is the SKELETON
  (one parent, many children: a real tree); `deps` are the GATE (a DAG *across* the
  tree — a task in one branch can wait on one in another), so they can't nest and
  ride along as each node's `blocked_by`. Every node carries the derived facts
  the drawing exists to convey: `Actionable` (the `App.actionable` predicate above)
  and `BlockedBy` (deps not yet done); a **container** node additionally carries
  `Container`, a `Progress {done,total}` roll-up (direct children, or the whole
  subtree with `--progress-recursive`), and `Stuck` (open work under it but no
  actionable descendant — always a subtree walk, recursing through sub-epics, so a
  box with a ready task under a child epic is not stuck; a zero-child epic is never
  stuck). These are DERIVED, never stored. Three deliberate properties: the forest is
  built over the FILTERED set, and a task whose parent was filtered out becomes a
  root rather than disappearing (a `--tree` that shows fewer tasks than the same
  flags without it would be lying); `Limit` caps ROOTS, not tasks (truncating
  mid-hierarchy would amputate children from the trees it did show); and a **parent
  cycle** — which only a git merge of two half-edges can create — leaves every task
  in it parented-but-unreachable, i.e. invisible, so those are surfaced as roots and
  the descent carries a visited set. A corrupt hierarchy renders, truncated at the
  loop; it never vanishes and never hangs.
- **`Archive`** selects done-lane tasks whose `Closed` is older than the cutoff
  and moves them (shard + body) into the sibling `.furrow/archive/` store (its own
  `tasks/`, `meta.json`, and `bodies/`).
- **`Upgrade`** raises the board's declared layout — both stores, hot and
  `archive/` — to `core.SchemaVersion` and re-serializes every shard. It is the
  only caller of `Store.SetBoardVersion`, previews unless `--yes`, and is a flag
  day (see the version gate).

### CLI commands

Registered in [`internal/cli/root.go`](../internal/cli/root.go), all built today
except where noted:

`init`, `add`, `ls` (alias `list`), `show`, `next`, `revisit`, `search`, `stats`, `board`, `edit`,
`note`, `attach`, `done`, `move`, `set`, `reorder`, `retitle`, `value`, `effort`, `check`, `dep`,
`parent`, `label`, `repo`, `review`, `apply`, `sync`, `archive`, `upgrade`, `lint`,
`config` (`init`/`path`), `schema`, `version`, `migrate`.

- **`set`** applies the routine triage quartet — lane, value, effort, labels — in
  one write (the combined-edit funnel `App.Set`), so triage isn't move+value+
  effort+label as four commands. It reuses `applyLane`/`labelDelta`, the helpers
  shared with `Move`/`Relabel`, so the invariants can't diverge.
- **`dep`** adds or removes dependency edges on an existing task (`--rm`); it is
  variadic (`dep a b c`), applying/removing several in one all-or-nothing write.
  Adding is acyclic (rejects self- and cycle-creating edges) and idempotent.
- **`parent`** is the same command shape for the OTHER edge furrow stores — the
  hierarchy: `parent <id> <parent-id>` files a task under another, `--rm` detaches
  it to top-level, and `--list` reads both directions (the parent, `null` when
  top-level; the children, `[]` when none) resolved to id+title+lane, exactly like
  `dep --list`. It exists because `parent` was the one persisted field with no
  command: settable at `add --parent` and never again, so the only way to re-file a
  task was to hand-edit a machine-written shard. Re-parenting is acyclic (`core.
  Index.HasAncestor` is the predicate: setting id's parent to p closes a loop
  exactly when id is already p's ancestor), and it deliberately ALLOWS a done
  parent — refusing would make "this leftover belongs to the epic that shipped"
  unrepresentable. The two states that follow are lint's: `parent-cycle` (error;
  `core.ParentCycleProblems`, which shares the SCC engine with `dep-cycle`) and
  `parent-done` (warn; `core.ParentDoneProblems`, the hierarchy twin of the
  reconcile gap). Every hierarchy walker (`core.Index.Ancestors`) carries a visited
  set, because a git merge of two half-edges can close a cycle behind the app.
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
  failure — carries `{committed, pulled, pushed, conflict, complete,
  committed_bodies, pending_bodies, pending_stash}` (the lists omitted when
  empty). `complete` is `false` whenever a body or stash is left pending (the
  stdout summary line names that count too), so a pushed-but-incomplete sync is
  never mistaken for a fully-published one. Failure modes, branch
  on the error `id`: `sync-conflict` (exit 3, definitive — the rebase is
  aborted automatically, conflicted paths in `details`), `sync-busy` (exit 3,
  retryable — a foreign in-progress rebase outlived the bounded backoff),
  `sync` (terminal — a likely-stale `.git/*.lock`, named in the message),
  `sync-interrupted` (exit 130/143 = 128+signal, retryable — SIGINT/SIGTERM
  cancelled the in-flight git; a genuine conflict is never masked by the signal,
  keeping its exit 3), `sync-stash-stranded` (exit 3 — see below), `sync-unmerged`
  (exit 2 — a pre-flight: unmerged paths with no operation in progress, the state a
  stranded autostash leaves behind), and
  `body-conflict-marker` (exit 2 — a body carrying conflict markers is refused
  BEFORE the commit; `details.bodies` names them with line numbers).

  **The autostash is the one way a sync can lose WORK without losing the BOARD,
  and it is silent by construction.** `git rebase --autostash` re-applies the
  stash at the end; when that apply conflicts with what was just pulled, git
  stores the entry back (`git stash store -m autostash`), warns on **stderr**,
  and **exits 0**. There is no failing exit code and no in-progress rebase — the
  only witness is the stash itself, which is why `app.Sync` probes it around
  every pull attempt (`strandedStash`, comparing the autostash entry set before
  and after, since git localizes its warning prose but not the `autostash`
  reflog subject). A newly stranded entry fails the sync (`sync-stash-stranded`,
  nothing pushed); a pre-existing one is re-reported in `pending_stash` on every
  sync until it is popped — an operator's own `git stash` ("WIP on …") is never
  reported. `DirtyChanges` passes `-uall` for the same reason: git's default
  collapses a wholly-untracked directory into one `?? .furrow/bodies/` entry,
  which would hide every body of a fresh board behind a path that classifies as
  neither body nor shard — committed, but counted as nothing and checked by nothing.
- **`apply`** parses `SetStatus-task:` directives out of PR/commit text (stdin
  or `--body-file`) and reflects them onto the board — the CI hook behind the
  task-status workflow. Validation is non-blocking by design.
- **`upgrade`** raises the board's on-disk layout to this binary's — the only
  command that may, previewing unless `--yes`, idempotent, no downgrade. A flag
  day; see the version gate.
- **`revisit`** is the read-only, agent-facing counterpart to `next`: it lists
  open tasks needing re-evaluation (`no_repo` — a draft, surfaced regardless
  of scope — plus unset value/effort, stale, or a done dependency), attaching a
  `revisit` reason array in `--json` so an agent fixes them via the setters
  (`value`/`effort`/`dep`/`repo --add`). An empty result exits 0 (nothing to revisit is healthy).
- **`migrate`** parses a hand-maintained `Task.md` into furrow tasks (dry-run by
  default; `--write` to apply; `--label` to stamp imported tasks).

### Output, errors, and exit codes

- `--json` (persistent flag) emits JSON to **stdout only**; logs and errors go to
  stderr (so a caller piping stdout to `jq` is unaffected). Both `--json` and
  `--ndjson` are honored **wherever furrow emits JSON**, not just the read/list
  commands: `jsonMode()` (`internal/cli/output.go`) is the single predicate every
  command gates on, and `emitObject` prints one value either indented (`--json`)
  or compact-one-line (`--ndjson`). A list command streams one record per line
  under `--ndjson`; a single-object command (a mutation's
  `{before,after,changed}`, `board`, `attach`, `init`, `version`, the `apply`
  report) prints one compact line; `lint` streams one problem per line. This
  closes the old gap where a non-read command silently degraded to human prose at
  exit 0 under `--ndjson`. CLI JSON uses the same `SetEscapeHTML(false)` /
  2-space (indented) encoding as the shards.
- Read filters: `--status`/`-s`, `--label`/`-l`, `--repo`/`-r`, `--limit`/`-n`,
  `--drafts` on `ls`; `-l`/`-r`/`-n` on `next` and `revisit` (plus
  `--stale-days` on `revisit`). `-r` is the scope control (an
  explicit `-r` overrides the board scope; `-r ''` shows the whole board);
  `-l` is a pure tag filter that ANDs with the scope. Within a single `-s` or
  `-l`, a comma is OR (`-s inbox,backlog`, `-l bug,urgent`; tokens are trimmed,
  empties dropped) — the flags still AND across fields. Both `-s` and `-l` also
  union when **repeated** (`-s inbox -s backlog` == `-s inbox,backlog`,
  `-l bug -l urgent` == `-l bug,urgent`): the repeats are comma-joined
  (`joinOrFilter`) into that same OR-set, so a repeated filter no longer silently
  last-wins (they are `StringArray`, not `StringSlice`, so a comma inside one
  value is not double-split; a multi-`-l` join like `bug,urgent` also never
  misfires the single-token `-l`→`-r` did-you-mean guard). `-s` and `-l` differ on
  an *unknown* token, because a lane is a closed vocabulary and a label is not:
  an unknown `-s` lane **fails fast (exit 2)** carrying the configured lanes in
  `candidates` — the read-side symmetry with `move`/`add`, so a typo like
  `-s in_progress` never masquerades as a healthy empty result — while an
  unknown `-l` tag just matches nothing (clamp-don't-reject, an open vocabulary).
  Comma is the reserved separator, so a lane/label whose name contains one can't
  be selected this way (lane/label names with commas are not a supported shape).
  `ls --drafts` lists only the repo-less tasks. When an input *almost* resolved —
  an ambiguous repo short name, a label that uniquely names a repo (the
  did-you-mean guard), or an unknown lane — the error envelope carries a
  `candidates` array; when a repo scope — explicit `-r` or the board's auto
  scope — hides drafts, a one-line stderr hint points at `--drafts` (stdout
  stays pure data). `furrow board [--json]` prints the resolved store path,
  discovery source (`env|local|pointer|user-config`), repo scope, the full
  lane vocabulary (lanes / next-lanes / default / done / terminal), and the
  schema triple (`schema_version` / `binary_schema_version` / `schema_state` /
  `writable`) — the introspection call that answers "what lanes exist, what scope
  is active, and can I write here" without provoking an error.
- **Non-interactive by default.** furrow is CLI-only: no prompts, and no
  interactive UI ships in this repo (a TUI/GUI is an out-of-repo front-end —
  ridge / loom — over the CLI/JSON contract).
  `furrow edit` on a non-TTY prints the absolute body path instead of launching
  an editor, so an agent can edit the file directly. `NO_COLOR` and non-TTY
  suppress color.
- **Destructive-op guard.** `furrow archive` previews ("would archive …") unless
  `--yes` is passed; `furrow upgrade` previews the same way (and prints the
  flag-day checklist), because it is irreversible for every older binary.
- **Exit-code contract** (`internal/core/errors.go`): `0` ok — **including an
  empty query result** (a match of nothing still succeeded, so `ls`/`next`/
  `revisit` all exit 0 when empty) / `1` a **specifically requested id** was not
  found (e.g. `show <id>`), never an empty list / `2` bad-usage or validation /
  `3+` internal or IO — with `130`/`143` (128+signal) carved out for a run a
  SIGINT/SIGTERM interrupted (`sync-interrupted`; see the `sync` failure modes
  above). The exit-code contract also lives in the binary's own
  `--help` (the root command's long help) and each affected command's help, not
  just here. On a non-zero exit the CLI prints `{"error":{"code","id","message"}}`
  to stderr, plus optional machine-actionable fields: `candidates` (a near-miss
  that almost resolved — an ambiguous repo short name, an unknown lane, or a
  parent command's unknown subcommand) and `details` (e.g. `sync-conflict`
  carries the conflicted paths; the version gate's `schema-too-new` /
  `schema-upgrade-required` carry `{board_schema, binary_schema}`). A parent command like `config` treats an unknown
  subcommand as exit 2 with `candidates`, not the exit-0 help prose cobra
  defaults to. `cmd/furrow/main.go` is literally `os.Exit(cli.Execute())`.

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
| `[ui]` | `theme` | `auto` (one of `auto`/`dark`/`light`) — a front-end display preference (the CLI itself does not render a themed UI; an out-of-repo TUI/GUI reads it) |
| `[alias]` | `<name> = "<command string>"` | none (a `name -> command` map) |
| (top-level) | `standalone` | `false` (a local single-machine board: no remote / `furrow sync` / CI) |

`status` is just a lane from `[lanes].order`; that list is simultaneously the
status enum and the top-to-bottom sort rank.

`standalone` is **presentation-only**: it changes no behavior, no schema gate,
and no on-disk byte — the CLI reads it (`cmd_upgrade.go`) to drop the
shared-board flag-day / `furrow sync` wording that only misdirects a
single-machine operator. It lives in `config.toml` (not `meta.json`), so it is
clamp-don't-reject and needs **no** `SchemaVersion` bump. `internal/core` stays
CI-agnostic: the `schema-upgrade-required` / `schema-too-new` messages name no
workflow; any pinned-CI guidance is added above core.

`[alias]` names frequent command strings. `cli.expandAlias` runs before cobra
dispatch: when the first argv token is not a flag and not a builtin command, it
looks it up in the board's `[alias]` table (via `app.DiscoverAliases`, a
config-only read — no store load) and, on a hit, replaces the token with the
alias's whitespace-split tokens and appends the rest of argv (git-style). A
builtin always wins (the lookup is builtin-first), so a shadowing alias is inert
and `cli.aliasShadowProblems` surfaces it as a `lint` warning (`alias-shadow`);
the `internal/config` parse drops a blank-valued alias with a clamp warning.
Command knowledge stays in `internal/cli` (the layer that owns the command set),
so this needs no new port.

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
- **No interactive prompting from the CLI, and no in-repo UI.** furrow is
  CLI-only; an interactive TUI/GUI is an out-of-repo front-end (ridge / loom)
  that drives the CLI/JSON contract rather than importing furrow's packages.
- **Web / React UI is out of scope for now** (parked): a future read-only viewer
  would simply read the `tasks/*.json` shards, which is exactly why clean JSON
  shards matter.

---

*(reviewed 2026-07-16)*
