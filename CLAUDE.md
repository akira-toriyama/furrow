# CLAUDE.md

Guidance for working in this repository (and for furrow itself as a tool).

## For Claude Code — integration contract (read first)

furrow's own tasks live on the **shared central board** (the private
`akira-toriyama/projects` repo). This repo deliberately has **no local
`.furrow/`**, so `furrow` commands run here resolve to that board via the
user-level config.

- Canonical commands: `furrow add|ls|show|next|brief|revisit|search|stats|board|boards|doctor|edit|note|attach|done|move|set|reorder|retitle|value|effort|check|dep|epic|label|repo|ref|review|sync|apply|archive|unarchive|rm|tidy|upgrade|lint|config|init|migrate|schema|version|vocab`.
  **`furrow brief [--json]`** is the session-start read (`next -r` →
  `show <id>` in ONE process; it never touches git): orient a shared board
  with `furrow sync && furrow brief`, and run `furrow sync` before reading
  and after writing.
- **`furrow --help` is the agent contract** (`newRootCmd`'s `Long` in
  `internal/cli/root.go`): owned shards vs. free bodies, the error envelope
  (branch on `kind` and `retryable`, never on the message or exit code),
  JSON cardinality, `session-busy`, destructive previews, the schema gates
  and the `upgrade` flag day. Each command's own contract is
  `furrow <cmd> --help`. This file holds only the rules for CHANGING furrow.
- furrow is **non-interactive by default**; the front-ends (ridge, a TUI;
  vista, a GUI) are separate repos driving its CLI/JSON contract.

## What this is

furrow — an alternative to GitHub Projects/Issues: a clonable, git-native,
plain-text task tracker. The store either sits **inside** the repo it serves (a
**repo-local board**) or outside it, reached by configuration (a **central
board**, which can therefore back many repos — tasks carry their repositories
in the first-class `repos` field). That axis is the **layout**, and `furrow
board` reports it as `central` or `repo-local`. Orthogonal to it is the
**mode**: a board with a git remote and other writers is a **shared board** (the
default, declared by omission), while a board on one machine with no remote sets
`mode = "standalone"`. Structured metadata lives in
one JSON shard per task, `.furrow/tasks/<id>.json` (likewise `epics/` and
`repos/`; deterministic,
machine-written), with the board-wide layout version in `.furrow/meta.json`
(`{"schema_version": 11}`); long-form prose lives in
`.furrow/bodies/<id>.md` (hand/agent-editable); human config is
`.furrow/config.toml`. A cobra CLI drives it. Go,
cross-platform, brew/nix packaged.

## Build / run

```sh
go build ./...                          # compile (use GOTOOLCHAIN=local on Go 1.26+)
go test ./...                           # all packages
sh scripts/repin-vendorhash.sh            # after go.mod/go.sum change: re-derive flake.nix vendorHash (needs nix)
./run.sh ls --json                      # build + run a subcommand
```

## Verify (how to confirm a change works — runnable headless)

```sh
sh scripts/check.sh   # the one command; its header says what it runs and
                      # what only CI runs (green here is NOT green CI).
                      # Run it before finishing.
```

- **CLI**: directly runnable headless.
- **Determinism / drift** is guarded, not promised: docs/architecture.md's
  "How the invariant is guarded" lists the guards (golden round-trip,
  `TestShardFieldsGolden`, `TestFrozenBoardRoundTripsByteIdentical` — the one
  fixture the code under test did not write — and the single-path and
  schema-write greps); `check.sh` adds the schema/config/docs drift diffs.
- **The release pipeline** runs for real on every PR (`build.yml`'s
  `--snapshot` build with syft); `scripts/check-release-artifacts.sh`'s header
  says what it asserts — `goreleaser check` validates the config's schema only.

## Source-of-truth references

Consult before adding behavior; keep terms consistent with them: [docs/architecture.md](docs/architecture.md)
(layers), [docs/glossary.md](docs/glossary.md) (language), [docs/non-goals.md](docs/non-goals.md) (what furrow won't do).

## Non-obvious constraints — read before editing

### Layer rules (the spine)
`internal/core` is **pure** (stdlib only — no cobra, os, or filepath).
Ports (`Store`, `Clock`, `SessionRegistry`) are interfaces **defined in core**;
`internal/store/fsstore` is the **only** package that loads the store or writes its files;
`internal/store/memstore` is its in-memory twin for tests. `internal/cli` is the
only presentation layer and mutates **only** through `internal/app.App` (the
single mutation funnel); it never touches the store port directly. Any
TUI/GUI front-end lives out-of-repo and drives the CLI, not these packages.
Crossing a layer means a port is missing — add the interface, don't add the
import.

### The single marshaller path — DO NOT regress this
`core.MarshalTask` / `MarshalEpic` / `MarshalRepo` / `MarshalMeta` are the
**only** encoders of persisted values, and `core.UnmarshalTask` / `UnmarshalEpic` / `UnmarshalRepo`
/ `UnmarshalMeta` the only decoders (`internal/core/marshal.go`,
`passthrough.go`). Never call `json.Marshal`/`json.Unmarshal` on a persisted
type anywhere else: the byte recipe is what makes an app write equal a
hand-edit byte-for-byte, and the decoders are what park a shard's unknown
top-level keys so the next write re-emits instead of destroying them
(**preserved ≠ honoured**: `lint` warns `unknown-shard-key`). Two rules,
reasoned in the code's doc comments and both shipped-and-caught: "is this key
known?" is `strings.EqualFold` (json's own matcher, not a lowercase set), and
**`Task` must never grow a `MarshalJSON` method** (the CLI's views embed it; a
promoted method would silently empty every sibling field).

### Frozen, collision-free ids & sparse priority
ids (`t-k3m9p`) are **frozen**: never reused, never renumbered. They are
**random** (prefix + a random Crockford-base32 suffix, `[ids].width` chars),
generated locally with no shared counter, so concurrent `furrow add` from
separate worktrees won't collide (the app retries on the rare in-store clash;
`lint` flags any duplicate; both stores REFUSE to Save an index carrying one).
Legacy numeric ids (`t-0042`) stay valid. Order within a lane is the sparse
integer `priority` (10-step); only an exhausted gap respaces the lane, reported
in the envelope's `renumbered` without advancing the neighbours' `updated`.

### Configuration
`.furrow/config.toml` reads are **clamp-don't-reject**: unknown keys and
out-of-range values fall back to defaults with a warning that
`furrow lint` surfaces. Read it through `internal/config`. **The one writer is
`furrow config set`** — surgical, and STRICT where reads are lenient
(`furrow config set --help`); every other command only reads, bar `init` and
`config init` writing a fresh template. The shipped sections
are `[lanes]`, `[next]`, `[priority]`, `[ids]`, `[labels]`,
`[archive]`, `[lint]`, `[due]`, `[revisit]`, `[review]`, `[alias]`,
and the top-level `mode` and `default_repo` — the repo-root `config.toml` (which `furrow init` writes
and check.sh diffs byte-for-byte) is the canonical annotated copy; read it rather
than trusting a prose list here. The session write guard's `[session]`
is the USER-LEVEL config's, per machine (repo-root `config.global.toml` is its
annotated copy). **The board's own `default_repo` is the FALLBACK scope**
(docs/architecture.md's "Scope vs store"); deriving a repo from the checkout is
`internal/app`'s job.

### Schema
`internal/schema.TaskV2` / `MetaV2` / `RepoV1` / `EpicV2` are the sources of the
JSON Schemas (`furrow schema [task|meta|repo|epic]`; CI diffs them against
`docs/schema/`): change a struct → schema const, committed file and golden
together. Top-level objects declare `additionalProperties: true` (the
passthrough writes unknown keys); `$defs.checklistItem` stays `false`.

**Adding a shard field? The default answer is BUMP.** Passthrough makes an old
binary **preserve** a field it does not know, not **honour** it (a future
`"blocked": true` is carried faithfully and the task still comes out of
`next`); only `core.SchemaVersion` can say "refuse to operate".
**`TestShardFieldsGolden`** FAILS on any shape change, naming the version to
bump; skip the bump only if **no** query, sort, filter, or lane decision reads
the field — a class that has never had a member. Accept the shape with
`-update-fields` and `-update-board` (the frozen board) in the same change as
the schema const and `docs/schema/`; new fields go at the **END** of the struct
(an old binary re-emits unknown keys last).

**The version gate is two-sided, and `core.SchemaVersion` is what THIS BINARY
writes — not what the board declares.** `core.CheckSchemaVersion` is the READ
gate (a NEWER board: `schema-too-new`), `core.CheckWritable` the WRITE gate (an
OLDER board is READ-ONLY: `schema-upgrade-required`) — docs/architecture.md's
"two-sided version gate". So **an ordinary write never touches `meta.json`'s
`schema_version`** (`fsstore.Save` stamps only a fresh, empty store);
`scripts/check-schema-write-guard.sh` greps that back into place (the
2026-07-13 outage it answers is recorded in docs/non-goals.md). **The only raiser is `furrow
upgrade`**, a **flag day** whose order (release, bump every pin, THEN upgrade)
is docs/architecture.md's "`furrow upgrade`" section — run it on the `projects`
board AND on `akira-toriyama/furrow-test` (README links it as the worked
example; a stale one is read-only for the reader who follows the link).

## Conventions

- Commits: gitmoji-driven — the grammar is `glyph.toml`'s `template` (its
  sigil, not the gitmoji, drives release semver). Enable the hook once with
  `git config core.hooksPath scripts/hooks`; `scripts/hooks/commit-msg` is
  glyph's generated script (regenerate, never hand-edit) — its comments say why
  only a convention violation stops the commit: CI is the authority.
  Spec: [CONTRIBUTING.md](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md).
- `go build ./...` and `go test ./...` must pass before finishing a turn.
- English is the only committed language — **there is no `README.ja.md`;
  translations are not stored**. That is the fleet
  [doc-consistency policy](https://github.com/akira-toriyama/.github/blob/main/docs/doc-consistency-policy.md)
  (English-only, code-first, reduction-first — truth lives in the code/CLI and
  docs point at it), for which this repo is the reference implementation.
  Keep [README.md](README.md) and the docs/ tier
  (architecture/glossary/non-goals) agreeing with the code on any
  user-visible change, and **sweep the docs/ tier in the same change**: drift
  pools exactly where no guard looks. The guards that do look —
  [`scripts/check-readme-parity.sh`](scripts/check-readme-parity.sh) (the
  README's workflow pin, the layout version in literal and prose, the Go
  paths' module major),
  [`scripts/check-docs-vocab.sh`](scripts/check-docs-vocab.sh) and
  [`scripts/check-docs-budget.sh`](scripts/check-docs-budget.sh) (a word cap
  per prose doc) — say in their headers what they hold. **Add a member to a closed vocabulary (a command, a
  `[section]`, a revisit signal, a `-q` qualifier) and the doc regions its
  claims table names must enumerate it, or CI fails.**
- README's command table is **generated** by `scripts/gen-command-table.sh`
  (its header has the recipe): edit `Short`/flags in `internal/cli`, rerun it,
  commit both — never hand-edit between the `commands:begin/end` markers.
- **1 item = 1 PR** (squash); update docs in the same PR.

## References

- clig.dev — CLI design guidelines (reviewed 2026-06-25)
- gitmoji.dev; glyph (gitmoji-driven lint/semver/notes) (reviewed 2026-07-20)
- GoReleaser homebrew_casks/nix (reviewed 2026-06-25)

## Multi-session work policy

**The single source of progress is the task body on the board** (the fleet
default: no plan files, no copies in memory or on a branch). On interruption,
update the body's checkboxes and leave one line of what the next session
should do; nothing important lives only in a chat transcript.

**Multi-operator (co-located checkout).** This repo is sometimes worked on by several
people/agents at once. A checkout has one shared HEAD/index/working tree, so two
operators running git in the same directory corrupt each other (orphaned commits,
commits on the wrong branch). **Each operator/session works in its own `git
worktree` (`git worktree add ../furrow-<topic> -b <branch> origin/main`) or a
separate clone — never share one checkout for concurrent git.** Commit + push
often and `git pull --rebase` before pushing.
