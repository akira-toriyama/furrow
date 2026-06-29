# furrow — architecture

furrow is a repo-local, plain-text task tracker written in Go (module
`github.com/akira-toriyama/furrow`, Go 1.23). This document describes how the
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
              +------------------+------------------+
              |                  |                  |
              v                  v                  v
     internal/config   internal/store/fsstore  internal/store/memstore
     read config.toml  the ONLY FS package      in-memory fake
     (clamp, no write) (atomic write,           (tests, dry-runs)
                         lazy body load,
                         random ids)
              |                  |                  |
              +------------------+------------------+
                                 |  (implement core ports)
                                 v
                          internal/core
                  PURE domain: Index/Task structs,
                  the single core.Marshal path,
                  ports (Store, Clock), validate, index ops
                  imports: stdlib only

   leaves (imported where needed, depend on nothing internal of note):
     internal/schema   JSON Schema source ( `furrow schema` )
     internal/version  build version string (ldflags-injected)
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
| `internal/core` | Pure domain: `Index`/`Task`/`ChecklistItem` structs, the single `Marshal` serializer, the `Store`/`Clock` ports, `Validate`, and in-memory index ops. |
| `internal/schema` | The JSON Schema for `index.json` as a Go constant; emitted by `furrow schema`. |
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

- **`Store`** — persists the index and per-task bodies. It owns *all* path
  construction (callers never assemble `".furrow/bodies/<id>.md"` by hand) and
  *all* atomicity. Methods: `Load`, `Save`, `LoadBody`, `SaveBody`,
  `BodyExists`, `ListBodyIDs`, `NextID`.
- **`Clock`** — supplies `Now()`. Injected so tests get deterministic timestamps
  and the marshaller's UTC/whole-second contract is trivial to honor.
  `core.SystemClock()` is the production implementation.

These interfaces are implemented by adapters: `internal/store/fsstore` (the real
filesystem) and `internal/store/memstore` (an in-memory fake). Both carry a
compile-time assertion `var _ core.Store = (*Store)(nil)`. The `app`, `cli`, and
`tui` layers depend on the *interface*, never on a concrete adapter — that is
what keeps the core testable without touching disk.

`internal/app` widens the port slightly with its own `app.Store` interface
(`core.Store` plus `DeleteBody`, `BumpSeqTo`, and `BodyFile` for `$EDITOR`
shell-out); both adapters satisfy it.

### "Crossing a layer means a missing port"

The design heuristic: if a layer finds itself wanting to reach across to
something it should not import, the answer is **not** to add the import — it is
to add (or widen) a port. The core never grows an `os` import to "just read a
file"; it grows a `Store` method instead, implemented by the adapter.

---

## The single-marshaller invariant

`core.Marshal(*Index, laneOrder []string) ([]byte, error)` in
[`internal/core/marshal.go`](../internal/core/marshal.go) is the **one and only**
path that serializes an `Index` to bytes. Every writer — `fsstore.Save`, and
`migrate` — goes through it. No other code calls `json.Marshal` on an `Index`.

Why one path: the byte layout of `index.json` is a contract, not an
implementation detail. If two code paths could serialize the index, they could
drift, and a re-save would churn the git diff. One path means **bytes written by
`furrow` equal bytes a human or Claude would hand-edit**, so re-saving an
untouched index produces zero git churn.

### The determinism contract

`Marshal` calls `Canonicalize` and then encodes. The contract (documented in the
`Marshal` doc comment and exercised by
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
- **Schema drift test.** `furrow schema` prints `internal/schema.IndexV1`
  (JSON Schema draft 2020-12); `docs/schema/furrow.index.v1.json` is a committed
  copy of the same bytes, and CI diffs the two so they cannot drift.
- **Single-path grep guard.** `scripts/check-marshal-singlepath.sh` greps for
  stray `json.Marshal(Index)` calls outside `core.Marshal` and fails CI if any
  appear; it runs as part of `scripts/check.sh`.

---

## The store

`internal/store/fsstore` is the only package that touches the filesystem for the
store. It is constructed with the few config-derived values it needs (lane order
for the marshaller's sort, id prefix/width for `NextID`) so it never imports
`internal/config`.

A `.furrow/` store directory contains:

```
.furrow/
  index.json          structured metadata — written ONLY via core.Marshal
  bodies/<id>.md       long-form prose, one file per task (hand/agent editable)
  config.toml          human config (read-only from furrow's side)
  archive/             a sibling store: aged done tasks moved out of the hot index
```

### Atomic writes (tmp + rename)

Every write — `index.json` and each `bodies/<id>.md` — goes through
`atomicWrite`: create a temp file (`.tmp-*`) in the **destination directory**,
write, `fsync`, `close`, then `os.Rename` over the target. Rename is atomic on a
single filesystem, so a crash never leaves a half-written `index.json`. The temp
file is removed on any error path.

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

`Load` on a missing `index.json` returns an empty, well-formed `Index`
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
call `App` methods — `Add`, `Move`, `Done`, `Reorder`, `SetTitle`, `Check`,
`AddCheck`, `Archive`, `Lint`, `EditPath`, plus the read methods `Get`, `List`,
`Next`. Keeping every edit in one place is what keeps the invariants (frozen
ids, canonical order, closed-timestamp rules, body↔index pairing) from being
re-implemented across two presentation layers. `App.load()` canonicalizes on
every read, so reads see the same lane→priority→id order regardless of any
hand-edit.

A few app-level rules worth stating, all verified against the code:

- **`Add`** assigns the next frozen id, picks a sparse priority (explicit
  `--priority`, else `max(priority in lane) + step`), writes a body file seeded
  with `# <title>`, then saves.
- **`Move` / `Done`** set the lane. Moving **into** the done lane stamps
  `Closed`; moving **out** of it clears `Closed`. Other terminal lanes (e.g.
  `icebox`) leave `Closed` alone — *parked is not closed*.
- **`Next`** returns actionable tasks: not in a terminal lane and with every
  named dependency already in the done lane. Lane semantics live in config, not
  core — `Index.Actionable` takes the terminal set and the done-id set as
  arguments.
- **`Archive`** selects done-lane tasks whose `Closed` is older than the cutoff
  and moves them (index entry + body) into the sibling `.furrow/archive/` store.

### CLI commands

Registered in [`internal/cli/root.go`](../internal/cli/root.go), all built today
except where noted:

`init`, `add`, `ls` (alias `list`), `show`, `next`, `revisit`, `edit`, `done`,
`move`, `reorder`, `check`, `dep`, `archive`, `lint`, `schema`, `version`, `ui`,
`migrate`.

- **`dep`** adds or removes a dependency edge on an existing task (`--rm`).
  Adding is acyclic (rejects self- and cycle-creating edges) and idempotent.
- **`revisit`** is the read-only, agent-facing counterpart to `next`: it lists
  open tasks needing re-evaluation (unset value/effort, stale, or a done
  dependency), attaching a `revisit` reason array in `--json` so an agent fixes
  them via the setters. An empty result exits 0 (nothing to revisit is healthy).
- **`ui`** launches the bubbletea TUI (`internal/tui`): list + glamour detail,
  navigate / filter / done / move lane / reorder (`K`/`J`) / checklist toggle /
  edit body.
- **`migrate`** parses a hand-maintained `Task.md` into furrow tasks (dry-run by
  default; `--write` to apply; `--label` to stamp imported tasks).

### Output, errors, and exit codes

- `--json` (persistent flag) emits JSON to **stdout only**; logs and errors go to
  stderr (so a caller piping stdout to `jq` is unaffected). `--ndjson` emits one
  compact task object per line. CLI JSON uses the same `SetEscapeHTML(false)` /
  2-space encoding as the index.
- Read filters: `--status`/`-s`, `--label`/`-l`, `--limit`/`-n` on `ls`;
  `--limit`/`-n` on `next`.
- **Non-interactive by default.** No prompts; the TUI is `furrow ui` only.
  `furrow edit` on a non-TTY prints the absolute body path instead of launching
  an editor, so an agent can edit the file directly. `NO_COLOR` and non-TTY
  suppress color.
- **Destructive-op guard.** `furrow archive` previews ("would archive …") unless
  `--yes` is passed.
- **Exit-code contract** (`internal/core/errors.go`): `0` ok / `1`
  not-found or empty result / `2` bad-usage or validation / `3+` internal or IO.
  On a non-zero exit the CLI prints `{"error":{"code","id","message"}}` to
  stderr. `cmd/furrow/main.go` is literally `os.Exit(cli.Execute())`.

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
| `[lanes]` | `order`, `default`, `done`, `terminal` | `inbox, backlog, ready, in-progress, done, icebox`; default `inbox`; done `done`; terminal `done, icebox` |
| `[priority]` | `step`, `default` | `10`, `100` |
| `[ids]` | `prefix`, `width` | `t-`, `4` |
| `[archive]` | `older_than_days` | `30` |
| `[ui]` | `theme` | `auto` (one of `auto`/`dark`/`light`) |

`status` is just a lane from `[lanes].order`; that list is simultaneously the
status enum and the top-to-bottom sort rank.

---

## What's NOT in scope

This document covers the *built* architecture. Several things are deliberately
**out of scope** for furrow's design; the full rationale lives in
[`non-goals.md`](non-goals.md). The headline non-goals:

- **No MCP server, no Claude Code plugin.** For a repo-local tool these are overkill.
  The integration layer is a short `CLAUDE.md` block plus `--json` on read
  commands. The rules that block belongs to: never hand-edit `index.json` (the
  single marshaller owns it; manual edits churn git), `bodies/*.md` *are*
  editable, and mutate only through commands.
- **No binary storage** (no SQLite) and **no YAML.** JSON for the machine-written
  index, TOML for human config, Markdown for prose.
- **No GitHub Issues coupling.** furrow is repo-local plain text; "GitHub
  friendly" means "diffs cleanly", not "syncs to Issues".
- **No interactive prompting from the CLI.** Interactivity is confined to
  `furrow ui`.
- **Web / React UI is out of scope for now** (parked): a future read-only viewer
  would simply read `index.json`, which is exactly why a clean JSON index matters.

### Built vs. planned — honest status

| Area | Status |
|---|---|
| `internal/core` (structs, `Marshal`, ports, validate, index ops) | **Built** |
| `internal/config` (TOML load, clamp) | **Built** |
| `internal/store/fsstore`, `internal/store/memstore` | **Built** |
| `internal/app` (mutation funnel, archive, lint) | **Built** |
| `internal/cli` (cobra: all commands above, including `ui` and `migrate`) | **Built** |
| `internal/tui` (bubbletea v1, `furrow ui`) | **Built** |
| `internal/schema` + `docs/schema/furrow.index.v1.json` | **Built** |
| Golden round-trip + schema drift tests | **Built** |
| `scripts/check-marshal-singlepath.sh` | **Built** |
| Packaging (GoReleaser → Homebrew tap, nix) | **Configured; release not yet tagged** |
| Read-only web / React viewer | **Future, low priority** |

---

*(reviewed 2026-06-25)*
