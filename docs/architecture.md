# furrow — architecture

furrow is a clonable, git-native, plain-text task tracker — an alternative to
GitHub Projects/Issues — written in Go (module
`github.com/akira-toriyama/furrow/v9`, Go 1.26+). One board can live outside the
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
     read config.toml  the store's FS adapter   in-memory fake           git subprocess
     (clamp, no write) (atomic write,           (tests, dry-runs)        adapter (git ops;
                         lazy body load,                                 implements no
                         random ids)                                     core port)
                                 |                  |
                                 +------------------+
                                 |  (implement core ports)
                                 v
                          internal/core
                  PURE domain: Index/Task structs,
                  the core.MarshalTask/MarshalMeta paths,
                  ports (Store, Clock, SessionRegistry), validate, index ops
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
**CLI-only**: the front-ends (ridge, a TUI; vista, a GUI) are separate repos consuming the
same `--json`/`--ndjson` contract an agent does.

### Package responsibilities

| Package | Role |
|---|---|
| `cmd/furrow/main.go` | Entry point. Just `os.Exit(cli.Execute())` — no logic. |
| `internal/cli` | cobra adapter and the **only** in-repo presentation layer: parse flags, call `app`, render (human table or `--json`/`--ndjson`), map errors to exit codes. Holds no task logic. |
| `internal/app` | Coordinator. Wires a `Store` + `Config` + `Clock`; exposes every mutation/query as a method. The **only** place that mutates state. |
| `internal/config` | Loads `.furrow/config.toml` (clamp-don't-reject). Produces an effective `Config`. Also owns the surgical single-key editor behind `furrow config set`, the one config writer. |
| `internal/store/fsstore` | The filesystem adapter behind `core.Store`: atomic writes, lazy body load, random id generation. |
| `internal/store/memstore` | In-memory `core.Store` twin for tests (a normal package, not a test helper). |
| `internal/gitrepo` | git subprocess adapter behind `sync`, `doctor`'s freshness probe and autocommit (command assembly + error classification). Driven only through `internal/app`. |
| `internal/claudecode` | Adapter over Claude Code's **private** session registry, implementing `core.SessionRegistry` for the session write guard. The ONE place that knows the format, best-effort by contract; its package doc records both. |
| `internal/core` | Pure domain: the entity structs, the `Marshal*`/`Unmarshal*` single path (incl. the unknown-key passthrough), the `Store`/`Clock`/`SessionRegistry` ports, `Validate`, the two-sided version gate, and in-memory index ops. |
| `internal/schema` | The four JSON Schemas as Go constants; emitted by `furrow schema [task\|meta\|repo\|epic]`. |
| `internal/migrate` | Pure `Task.md` parser behind `furrow migrate` (dry-run by default; LOUD warnings for the unmappable). |
| `internal/recur` | Recurrence, a leaf beside `query`: the short `--repeat` spelling to one RFC 5545 RRULE line, a stored rule to its next occurrence. The only importer of the RRULE library and the only package that knows the grammar; `internal/app` owns WHEN a rule advances (a close). The library traps it contains are the doc comments on `Next`, `Skips`, `refuseDtstart`, `OffLattice` and `naked`. |
| `internal/query` | Pure parser (stdlib only) for the `-q` typed-query DSL, producing an AST. It knows the GRAMMAR, not furrow's fields; `internal/app`'s `compileQuery` binds it to the one task predicate every filtering read uses. |
| `internal/gittest` | Test-only: isolates real-git tests from ambient git config at the process-env level, from `TestMain`. Imported only by `_test.go` files. |
| `internal/testutil` | Test-only, like `gittest`: the user-level config fixture under an isolated config home. Imports nothing of furrow's own, so any package's `_test.go` files may use it. |
| `internal/version` | Build version, default `"dev"`, overridden via `-ldflags`. |

---

## The purity rule

`internal/core` is the spine, and it is **pure**: it imports only the Go
standard library. It must **not** import:

- `cobra` (a presentation concern), or
- `os` or `path/filepath` (filesystem access is an adapter concern).

The domain reaches the outside world only through interfaces it declares itself;
the doc comment atop `internal/core/task.go` states the rule in-code.

### Ports live IN core

The seams between the pure core and the outside world are interfaces declared in
[`internal/core/ports.go`](../internal/core/ports.go):

- **`Store`** — persists the shards and bodies, owning *all* path construction
  and *all* atomicity. The method set and each method's invariant (`Save`
  canonicalizing the caller's index in place, the ungated `BoardVersion`, the
  one raiser `SetBoardVersion`) are the doc comments in
  [`internal/core/ports.go`](../internal/core/ports.go) — read them there.
- **`Clock`** — supplies `Now()`; injected so tests are deterministic
  (`core.SystemClock()` in production).
- **`SessionRegistry`** — `Sessions()`, the live sessions (`core.Session`) of an
  AI coding harness on this machine: the session write guard's eyes.
  `internal/claudecode` implements it, and the decision (`core.SessionClashes`)
  stays pure — it takes a `repoOf(cwd)` function so core never touches git.

Both adapters carry `var _ core.Store = (*Store)(nil)`; `cli` never imports a store
adapter; `app` also imports `fsstore` to build its stores and for `WriteFileAtomic`. `internal/app` widens it slightly as
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
paths that serialize a persisted value to bytes: `core.MarshalTask` writes one
`tasks/<id>.json` (likewise `MarshalEpic` and `MarshalRepo`), and
`core.MarshalMeta` writes `meta.json` — only from `Store.SetBoardVersion` (i.e.
`furrow upgrade`), the fresh-store stamp and `Store.PruneMetaExtras`
(`furrow tidy --unknown-keys`), never on the ordinary write path (see the
version gate). The index has no serialized form (its normal form is what
`core.Canonicalize` enforces in memory). No other code calls `json.Marshal` on a
persisted type.

Why one path: **bytes written by `furrow` equal bytes a human or Claude would
hand-edit**, so re-saving an untouched task produces zero git churn.

### The determinism contract

`MarshalTask`, `MarshalEpic` and `MarshalRepo` normalize (`canonicalizeTask` and
friends), and every `Marshal*` encodes via the one encoder,
`encodeCanonicalWithExtras`. The byte recipe is the
`MarshalTask` doc comment, the set-vs-sequence slices are `canonicalizeTask`'s,
and the lane → priority → id order is `core.TaskOrder`'s; the per-shard goldens
under [`internal/core/testdata/`](../internal/core/testdata/) exercise it.
Reordering struct fields changes every diff: a schema bump and a golden update,
never casually. A parse failure in `Unmarshal*` is a *validation* error, not an
internal fault.

### Unknown-key passthrough (the other half of the version gate)

[`internal/core/passthrough.go`](../internal/core/passthrough.go) makes the
round-trip **lossless**: the `Unmarshal*` decoders park every **top-level** key
the binary does not know in an unexported `extras` field, and the matching
`Marshal*` re-emit them sorted, after the known keys. The file's header says
why a field added without a bump needs it, and its doc comments keep the two
rules behind reproduced corruptions: "known?" is `strings.EqualFold`
(`isKnown`), and **`Task` must never grow a `MarshalJSON` method**
(`encodeCanonicalWithExtras`). **The gate stops a bumped layout from being
misread; the passthrough stops an unbumped one from being destroyed.**
Preserved is not honoured, so `lint` warns `unknown-shard-key` over all four
written file kinds, and "bump on a shape change" still stands
(`TestShardFieldsGolden`, below). Limits: top-level only
(`$defs.checklistItem` stays `additionalProperties: false`); not retroactive
(a shared board is safe only once EVERY writer, pinned CI callers included, has
passthrough); new shard fields go at the END of the struct, or key order churns
against an old binary's re-emitted extras.

### How the invariant is guarded

- **Golden round-trip tests.** `internal/core`'s shard goldens freeze each shard
  marshaller's bytes, and write → read → write must stay byte-identical.
- **Schema drift test.** `furrow schema [task|meta|repo|epic]` prints
  `internal/schema`'s four schemas; [`docs/schema/`](schema/) holds committed
  copies of the same bytes, and CI diffs all four.
- **Struct-fingerprint golden.** `TestShardFieldsGolden`
  (`internal/core/schema_fields_test.go`) fails on any change to a persisted
  type's json keys or their order, naming the version to bump; its doc comment
  says why the default answer is BUMP. Accept a deliberate shape with
  `-update-fields`.
- **Frozen board.** `TestFrozenBoardRoundTripsByteIdentical`
  (`internal/store/fsstore/frozen_board_test.go`) round-trips a committed board
  **the code under test did not write** through each file kind's writer: same
  file set, every byte and mtime untouched. Its header lists what only it
  catches; `-update-board` rewrites the board, putting the flag-day decision in
  the diff.
- **Single-path grep guard.** `scripts/check-marshal-singlepath.sh` fails on a
  `json` encode or decode call outside the files its header sanctions —
  **decoders too**, since a raw `json.Unmarshal` into a `Task` skips the passthrough.
- **Schema write guard.** `scripts/check-schema-write-guard.sh` confines
  `core.SchemaVersion` to the files its header lists. The regression it guards
  fails **silently** — see the outage below.

---

## The `repos` field and the two-sided version gate

A task carries a **first-class `repos` set** (`owner/repo`, 0..N, the same set
semantics as labels; a repo is **not** a label; an empty set is a **draft**).
Promoting it to a schema field is what motivated the **version gate**, whose
governing idea is: `core.SchemaVersion` is the layout **this binary writes**;
`meta.json`'s `schema_version` is what **the board declares**. They are two
different numbers, and the board's is an **INPUT to every write, never an
output**.

- **`core.CheckSchemaVersion` — the READ gate.** A board declaring a *newer*
  layout is refused (kind **`schema-too-new`**, exit 3 — fix the binary):
  preserving (the passthrough) is not understanding.
- **`core.CheckWritable` — the WRITE gate.** A binary writes only a board
  declaring *exactly* its own layout; an *older* or unstamped one is
  **readable** but **read-only** (kind **`schema-upgrade-required`**, exit 2 —
  the board is stale). The exit code alone says which side is stale.

Their doc comments (`internal/core/task.go`) carry the reasoning and the
`details`; the stderr "READ-ONLY" note on reads is `warnReadOnly`'s
(`internal/cli/scope.go`). Both adapters enforce the read gate on `Load` and
the write gate on `Save`. **No ordinary write raises `meta.json`'s version**:
only a genuinely fresh, empty store is stamped, and a garbled `meta.json` is an
error, never a fallback to the binary's version (`fsstore.Store.LoadMeta` /
`BoardVersion` say why). This is the fix for the 2026-07-13 outage recorded in
[`non-goals.md`](non-goals.md#no-automatic-schema-migration), and
`scripts/check-schema-write-guard.sh` makes the regression un-writable. The
half the gate cannot see — a field added WITHOUT a bump — is the
passthrough's (above).

### `furrow upgrade` — the one raiser, and a flag day

`App.Upgrade` ([`internal/app/upgrade.go`](../internal/app/upgrade.go)) is the
only caller of `SetBoardVersion`; its comments, `UpgradeReport` and
`furrow upgrade --help` carry the contract (preview unless `--yes`, both
stores, idempotent, no downgrade). It is a **flag day** whose order furrow
cannot enforce, because it cannot see the fleet's pins; README's
[layout-gate section](../README.md#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it)
states that order. `sync-task-status.yml` pre-flights
`furrow board --json` and fails with one annotated error when
`.writable != true`.

### `board` reports; it never fails

`App.Board` reads the version **ungated** (`Store.BoardVersion`) and
**reports** the schema fields (`SchemaTriple`, `internal/app/board.go`)
instead of raising: `board` is the last command that works when board and
binary disagree, which is what makes it the CI pre-flight. `furrow lint`
complements it with `schema-outdated` as a **warning**, because a read-only
board is the legitimate middle of a flag day and must not red every repo's CI.
The same contract makes its `git` key a **state, never an error** — `BoardGit`
and `boardGit` (same file) give the closed vocabulary and the probes.

---

## The store

`internal/store/fsstore` is the only package that touches the filesystem for the
store; it takes the few config values it needs (lane order, id prefixes/width) as
arguments so it never imports `internal/config`.

A `.furrow/` store directory contains:

```
.furrow/
  tasks/<id>.json      one shard per task — written ONLY via core.MarshalTask
  epics/<id>.json      one shard per epic (e- ids) — core.MarshalEpic
  bodies/<id>.md       prose, one file per task OR epic (hand/agent editable;
                         ids are prefix-disjoint, so one directory is unambiguous)
  bodies/assets/       <id>-<sanitized-name>, written ONLY via Store.SaveAsset / SaveAssetRaw
  meta.json            {"schema_version": 11} — MarshalMeta; stamped only on a
                         fresh store (`init`) or by `furrow upgrade`; an ordinary
                         Save READS it (the write gate) and leaves it alone
  repos/<owner>__<repo>.json   one review shard per repo — MarshalRepo
  config.toml          human config (written by `init`, then only by `furrow config set`)
  archive/             a sibling sharded store for aged done tasks
  .gitattributes       bodies/*.md + archive/bodies/*.md merge=union (init)
```

### Load and Save (shard fold / split)

`fsstore.Load` folds every `tasks/*.json` shard into one `core.Index` and
records the bytes it read; `fsstore.Save` is the inverse and touches **only what
this index changed**, so a no-op save touches no files and a shard this index
never met is left exactly as found — the store has no lock, and the old sweep
deleted a concurrent writer's new shards (t-msqv). `Save` *reads* `meta.json`
as the write gate's input and stamps only a genuinely empty store; the index
carries no version. The rules and their history are `fsstore.Save`'s doc
comment (`memstore.Save` answers the same two questions per entry); the
same-task race that remains — the later `Save` wins — is what
`--expect-updated` reports (`internal/cli/cmd_mutate.go`).

### Atomic writes (tmp + rename)

Every write is a temp file in the **destination directory**, `fsync`, then
rename over the target (`atomicWrite`): a crash never leaves a half-written
shard, and a bulk change is atomic **per shard**. A batch of bodies lands in
two phases — all staged, then all renamed (`SaveBodies`) — because a note is
not idempotent (t-5n2x); the why is the `SaveBodies` port doc in
`internal/core/ports.go`.

### Lazy body load

The `Index` holds only metadata; `Task.Body` is a *relative path*, never the
prose, which `LoadBody` reads on demand. Metadata diffs per field, prose per
task, and long markdown never collapses into an escaped JSON string.

### Frozen, collision-free random ids

`NextID` returns a **random** id (prefix + a Crockford-base32 suffix,
`[ids].width` chars) from **no shared counter**, so separate worktrees never
mint the same id; both stores **refuse to `Save`** an index carrying a
duplicate (`core.CheckUniqueIDs`, whose doc says why reads stay open). Ids are
**frozen** (never reused or renumbered); legacy numeric ids coexist. The
clash handling is `fsstore.NextID`'s doc comment.

### memstore

`internal/store/memstore` is the in-memory `core.Store` (a normal package, not
a test helper; its `BodyFile` returns `""`, so `$EDITOR` is unavailable).

**It may never promise LESS than fsstore**: every divergence lands as a test
that is green against a shape no real board can hold. How it canonicalizes and
isolates is `memstore.Save`'s and `memstore.Load`'s doc;
`internal/store/memstore/parity_test.go` pins the divergences that shipped,
against a real `fsstore`.

---

## The coordinator and the CLI contract

`internal/app` is the **only mutation funnel**: the CLI (and any front-end,
through the CLI/JSON contract) calls `App` methods, one per verb; the
authoritative list is the exported method set of [`internal/app`](../internal/app)
itself (a hand copy here rotted). One place for every edit is what
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
  every dep done — deliberately NOT epic-aware), shared by `ls
  --actionable`, `is:actionable` and `ls --tree`'s ★; `next` inlines it and narrows it further
  with the active/pinned epic scope (`App.NextScope`), so ★ is a strict superset
  of what a plain `next` hands out. Lane semantics live in config, never in core.
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

`internal/app/session_guard.go` mechanizes a rule judgment kept breaking: a
write from a Claude Code session that touches a repo an EARLIER-started
session on this machine sits in is refused (exit 2, `session-busy`) while that
occupant is working, and warned about (`session_warn`) while it is idle. That
file's header is the canonical description — the repos a write is judged on,
idle versus busy, what is deliberately unguarded, and the `[session]` modes;
the decision itself is pure (`core.SessionClashes`), and the registry format
is read in exactly one package (`internal/claudecode`, its package doc). What
the funnel keeps:

- **A refusal leaves every file untouched**: each write path guards before its
  first write (`TestSessionGuardRefusalLeavesEveryFileUntouched`).
- **First come, first served**: the earlier session's own writes never clash,
  so an autonomous session is never stopped by a later watcher.
- **Never a refusal on a guess**: outside Claude Code (no `CLAUDECODE`) the
  guard is off, and wherever the facts cannot be read it stands down with one
  stderr `note: session guard: …` line (`App.sessionSnapshot`; `doctor`
  reports `session-registry-unreadable`).
- **`[session]` is the USER-LEVEL config's section and names no agent**
  (`config.SessionGuard`): another agent is one more `core.SessionRegistry`
  adapter, never a config change.
- **The stderr line is news, not noise** (`App.UnheardClashes`): once per
  session per (occupant, repo), again only when the occupant has been active
  since; the envelope's `session_warn` carries every clash on every write.

### CLI commands

Registered in [`internal/cli/root.go`](../internal/cli/root.go); each command's
contract — flags, `--json` shape, exit codes — is its `--help` and README's
command notes (the README table is generated from this tree), never a copy here:

`init`, `add`, `ls` (alias `list`), `show`, `next`, `brief`, `revisit`, `search`, `stats`,
`board`, `boards`, `doctor`, `edit`, `note`, `attach`, `done`, `move`, `reorder`,
`retitle`, `set`, `value`, `effort`, `check`, `dep`, `epic`, `label`, `repo`, `ref`, `review`,
`apply`, `sync`, `archive`, `unarchive`, `rm`, `tidy`, `migrate`, `upgrade`, `lint`, `config`, `schema`, `version`, `vocab`
(the hidden `commands` is the README table generator's own).

### Output, errors, and exit codes

- `--json` emits JSON to **stdout only**; logs and errors go to stderr. `--json`
  and `--ndjson` are honored **wherever furrow emits JSON**: `jsonMode()`
  (`internal/cli/output.go`) is the single predicate, `emitObject`/`emitList`
  the single emitters (indented, or compact one-per-line). Which command emits
  an array and which one object is read off its signature — the rule is
  `furrow --help`'s. CLI JSON uses the shards' encoding.
- **Read filters resolve through one path.** Every filtering read (`ls`, `next`,
  `revisit`, `stats`, `search`) takes the same `-l`/`-r`/`-q`/`-e` (`-s` all but
  `next`/`revisit`, `-n` all but `stats`), and the
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
  lane vocabulary and the schema fields, without provoking an error.
- **Non-interactive by default**: no prompts; `furrow edit` on a non-TTY prints
  the body path instead of launching an editor. **Destructive ops preview unless
  `--yes`** — the one list is `furrow --help`'s.
- **Exit codes and the error envelope** are `core.Code` and `core.Error`
  (`internal/core/errors.go`), and `furrow --help` prints both: an empty query
  result is exit `0`, never `1`; a non-zero exit writes one `{"error":{...}}`
  envelope to stderr, and callers branch on its `kind` (`furrow vocab
  error-kinds`) and `retryable`, never on the message. `cmd/furrow/main.go` is
  literally `os.Exit(cli.Execute())`.

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
`[due]`, `[revisit]`, `[review]`, `[alias]`, and the top-level
`mode` and `default_repo`. The keys and per-key reasoning live in the repo-root
[`config.toml`](../config.toml), the exact file `furrow init` writes and
check.sh diffs byte-for-byte; `scripts/check-docs-vocab.sh` holds this list to
`config.TopLevelKeys()`.

`status` is just a lane from `[lanes].order` — the status enum and the sort
rank at once. `mode` is presentation-only (no schema gate, no byte), so it lives
in `config.toml`, not `meta.json`. `[alias]` expands before cobra dispatch,
builtin-first (a shadowing alias is inert; `lint` warns `alias-shadow`), and
command knowledge stays in `internal/cli`, so it needs no port.

### User-level config: central boards and the session guard

The user-level `${XDG_CONFIG_HOME:-~/.config}/furrow/config.toml` declares
central boards as `[[board]]` entries (`path`, `scopes`, `repo`, `label`,
`auto_filter`, `autocommit`) and the per-machine `[session]` section of the
[session write guard](#the-session-write-guard) (`guard`, `busy_seconds`;
`config.LoadGlobal` returns both as one `config.Global`, clamped per key);
its shape and setup are README's ([Central
board](../README.md#user-level-config-no-per-repo-file)), its per-key reasoning
the repo-root [`config.global.toml`](../config.global.toml). What lives here is
the resolution, split to honour the purity rule:

- **`internal/config`** parses and **clamps per entry** (the cases are
  `config.LoadGlobal`'s doc comment). It never touches cwd and reads only the
  files it is handed.
- **`internal/app`** (the fs/cwd-aware side) is the last arm of `discover`
  after `FURROW_DIR`, a local `.furrow`, and `.furrow-pointer.toml`
  (`resolveGlobalBoard`): the board whose scope is the **longest canonical
  prefix** of cwd wins, and only the winner is `stat`-ed, so a broken path in
  another scope never breaks this directory (`pickBoard`; `FURROW_BOARD` is one
  synthetic entry, `loadGlobalBoards`). Every configured path must pass
  `isStoreDir`, whose doc comment is the rule: furrow's own data makes a store
  whatever sits beside it, and a repo root holding a marked `.furrow` is
  refused with it as the did-you-mean. The walk's own `.furrow` needs no
  marker — its name is one. The user-facing order is README's [Discovery
  precedence](../README.md#discovery-precedence).

**Scope vs store.** A `[[board]]` injects a scope repo like a pointer does;
`auto_filter` decides whether reads filter by it (writes attach regardless).
The board's own **`default_repo`** is the FALLBACK, applied only when discovery
ran an arm that declares no scope at all (`FURROW_DIR`, a local `.furrow`) —
the gate is the ARM, not an empty repo; `app.applyBoardScope`'s doc comment
holds why, and why `"auto"` is refused there. `furrow doctor` runs the same
function, so its `scope_repo` can never disagree with the real commands.

**`autocommit`** is per-machine on purpose (the board config syncs to every
clone and CI) and keyed by the **store**, not the arm; it commits by `furrow
sync`'s rule, is best-effort and never fetches or pushes. The contract is
`App.AutoCommitFlush`'s doc comment, which entry applies is
`app.applyStoreAutoCommit`'s.

**Repo derivation (`repo = "auto"`)** is **file reads only — no git
subprocess**, and its invariant is that **a bare directory name is never
written into `repos`**; the chain (origin URL, ghq-style path, unscoped) and its
worktree handling are [`internal/app/gitorigin.go`](../internal/app/gitorigin.go)'s.
`furrow config init` / `config set --user` are the file's two writers, through
`internal/app`; discovery stays silent on its inert path, so `lint` and
`config path` are what report a half-written file.

---

## What's NOT in scope

This document covers the *built* architecture. What furrow deliberately does
**not** do — no MCP server or plugin, no GitHub Issues coupling, no binary
store, no sync daemon, no in-repo UI — is catalogued with its rationale in
[`non-goals.md`](non-goals.md). Two facts those choices rest on live above: the
CLI *is* the agent interface, and furrow is non-interactive by default.

---


