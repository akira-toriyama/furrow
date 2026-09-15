# CLAUDE.md

Guidance for working in this repository (and for furrow itself as a tool).

## For Claude Code — integration contract (read first)

furrow's own tasks live on the **shared central board** (the private
`akira-toriyama/projects` repo): **central** because the store sits outside the
repos it backs and is reached by configuration, **shared** because it has a git
remote and more than one writer. This repo deliberately has **no local
`.furrow/`**, so `furrow` commands run here resolve to that board via the
user-level config. The essentials, in the order a session meets them:

- Canonical commands: `furrow add|ls|show|next|brief|revisit|search|stats|board|boards|doctor|edit|note|attach|done|move|set|reorder|retitle|value|effort|check|dep|epic|label|repo|ref|review|sync|apply|archive|unarchive|rm|tidy|upgrade|lint|config|init|migrate|schema|version`.
  **`furrow brief [--json]`** is the session-start read (sync → `next -r` →
  `show <id>` in ONE process; read-only): orient a shared board with
  `furrow sync && furrow brief`. Run `furrow sync` before reading and after
  writing a shared board.
- **Every command's contract is in `furrow <cmd> --help` and README's command
  notes** — lanes, flags, `--json` shapes, exit codes, the `-q` grammar, the
  batch selectors, `due`/`repeat`, `sync`'s progress keys and failure kinds,
  the session write guard. Read those; this file holds only what they cannot:
  the rules for CHANGING furrow.
- **Shards are furrow's to write, bodies are yours.** Never hand-edit
  `.furrow/tasks/*.json` (one marshaller path, byte-identical to hand-edits by
  construction — see below); `.furrow/bodies/<id>.md` is the prose record and
  may be edited freely.
- **Branch on `kind` (and `retryable`), never on the message or exit code.**
  Every error is a JSON envelope on stderr; a retryable sync kind is re-run,
  everything else is terminal.
- **`session-busy` (exit 2) means another Claude Code session on this machine
  holds that repo**: wait, hand the write to that session, or `add --draft`.
  Never look for a force flag; there is none.
- **Array cardinality is read from the signature, with one exception.** A
  command whose `Use` says `<id>...` (`show`, `done`, `move`, `set`) emits a
  JSON array at every arity; a one-id command (`note`, `retitle`, …) emits an
  object; the report-shaped commands (`rm`, `archive`, `tidy`, `upgrade`,
  `apply`) emit ONE report object regardless of how many ids they took.
- **A read never narrows silently.** A repo scope that hid drafts or boxes, a
  `-n` cap that bit, a board this binary cannot write: each is one stderr
  line; stdout stays pure data. JSON goes to stdout ONLY.
- **A mutation that changes nothing leaves `updated` alone** (idempotent
  retries cost the shared board nothing); the prose writers (`note`,
  `done --note`, `edit --body`) always advance it.
- **`config set` is the one write that works on a board this binary cannot
  otherwise write** (an older layout): it edits `config.toml` surgically and
  never touches the store, so a flag day can be prepared from any binary.
- **Destructive ops preview unless `--yes`** (`archive`, `rm`/`epic rm`,
  `tidy`), and `rm` refuses a still-referenced target (kind `referenced`)
  unless `--force`.
- **`upgrade` is a flag day.** Release the furrow that ships the schema, bump
  every pinned `sync-task-status.yml@vX.Y.Z` caller and that workflow's
  `furrow-version` default, and only THEN `furrow upgrade --yes` — see Schema.
- furrow is **CLI-only and non-interactive**; a TUI/GUI (ridge, loom) is a
  separate front-end driving this CLI/JSON contract.

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
one JSON shard per task, `.furrow/tasks/<id>.json` (deterministic,
machine-written), with the board-wide layout version in `.furrow/meta.json`
(`{"schema_version": 10}`); long-form prose lives in
`.furrow/bodies/<id>.md` (hand/agent-editable); human config is
`.furrow/config.toml`. A cobra CLI drives it (CLI-only — any TUI/GUI is a
separate out-of-repo front-end that speaks the CLI/JSON contract). Go,
cross-platform, brew/nix packaged.

## Build / run

```sh
go build ./...                          # compile (use GOTOOLCHAIN=local on Go 1.25+)
go test ./...                           # all packages
./run.sh ls --json                      # build + run a subcommand
```

## Verify (how to confirm a change works — runnable headless)

```sh
sh scripts/check.sh   # the one command: marshaller + schema-write guards +
                      # build/vet/test + golangci (warns when its version is
                      # not build.yml's pin) + govulncheck of source AND binary +
                      # schema/config/docs drift + a CLI smoke + (if goreleaser &
                      # syft are installed) a release dry-run + (if installed)
                      # actionlint / taplo / zizmor. Green here is NOT green CI:
                      # go-bite, the commit/PR-title lint (glyph) and repo-policy
                      # run only in CI, and an uninstalled tool is skipped with a
                      # note, not failed. Run it before finishing.
```

- **CLI**: directly runnable headless; tests cover core + store + app + cli + migrate.
- **Determinism / drift** is guarded, not promised: the golden round-trip test,
  `scripts/check-marshal-singlepath.sh` (encoders AND decoders),
  `scripts/check-schema-write-guard.sh` (no ordinary write may name
  `core.SchemaVersion`), `TestShardFieldsGolden` (the shard's on-disk shape is
  frozen), `TestFrozenBoardRoundTripsByteIdentical` (a real board's BYTES under
  `internal/store/fsstore/testdata/frozen-board/` that Load→Save must reproduce
  exactly — the one fixture the code under test did not write), and the
  schema/config/docs drift diffs in `check.sh`.
- **The release pipeline** runs for real on every PR (`build.yml`'s
  `--snapshot` build with syft) and `scripts/check-release-artifacts.sh`
  asserts the artifact shape against `.goreleaser.yaml`'s `goos × goarch` (the
  ONE platform list) and `release.yml`'s attest steps — its header says what;
  `goreleaser check` validates the config's schema only.

## Source-of-truth references

Consult before adding behavior; keep terms consistent with them: [docs/architecture.md](docs/architecture.md)
(layers), [docs/glossary.md](docs/glossary.md) (language), [docs/non-goals.md](docs/non-goals.md) (what furrow won't do).

## Non-obvious constraints — read before editing

### Layer rules (the spine)
`internal/core` is **pure** (stdlib only — no cobra, os, or filepath).
Ports (`Store`, `Clock`) are interfaces **defined in core**;
`internal/store/fsstore` is the **only** package that touches the filesystem;
`internal/store/memstore` is its in-memory twin for tests. `internal/cli` is the
only presentation layer and mutates **only** through `internal/app.App` (the
single mutation funnel); it never touches the store port directly. Any
TUI/GUI front-end lives out-of-repo and drives the CLI, not these packages.
Crossing a layer means a port is missing — add the interface, don't add the
import.

### The single marshaller path — DO NOT regress this
`core.MarshalTask` / `MarshalEpic` / `MarshalRepo` / `MarshalMeta` are the
**only** encoders of persisted values, and `core.UnmarshalTask` / `UnmarshalRepo`
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
`furrow config set`** (`--user` for a `[[board]]` entry of the user config): a surgical,
git-config-style edit — comments and every untouched byte survive — that is
STRICT where reads are lenient (an unknown key is exit 2 with the vocabulary in
candidates; a value the reader would clamp is refused before the write). Every
other command still only reads. The shipped sections
are `[lanes]`, `[next]`, `[priority]`, `[ids]`, `[labels]`,
`[archive]`, `[lint]`, `[due]`, `[revisit]`, `[review]`, `[session]`, `[alias]`,
and the top-level `mode` and `default_repo` — the repo-root `config.toml` (which `furrow init` writes
and check.sh diffs byte-for-byte) is the canonical annotated copy; read it rather
than trusting a prose list here. **The board's own `default_repo` is the
FALLBACK scope**, applied only when discovery (the user-level
`~/.config/furrow/config.toml` `[[board]]` entries, scoped by repo) supplied
none; it is a literal `owner/repo` only, because `config.toml` is committed and
shared, and deriving a repo from the checkout is `internal/app`'s job.

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
gate (a NEWER board: `schema-too-new`, exit 3 — fix the binary);
`core.CheckWritable` the WRITE gate (an OLDER board is readable but READ-ONLY:
`schema-upgrade-required`, exit 2 — the board is stale, and the orient reads say
so on stderr). So **an ordinary write never touches `meta.json`'s
`schema_version`** (`fsstore.Save` stamps only a fresh, empty store);
`scripts/check-schema-write-guard.sh` greps that back into place (the
2026-07-13 outage it answers is recorded in docs/non-goals.md). **The only raiser is `furrow
upgrade`** (preview unless `--yes`; the `archive/` store too; idempotent on a
current board) — a **flag day** with no downgrade (`git revert` on the board
repo), whose ORDER is the human's: (1) release the furrow shipping the schema,
(2) bump every caller's `sync-task-status.yml@vX.Y.Z` pin and that workflow's
`furrow-version` default, (3) only THEN `furrow upgrade --yes` + `furrow sync`.

## Conventions

- Commits: gitmoji-driven — `<:gitmoji:>[(<scope>)][!] <subject>` (the leading
  `:code:` is the type and drives release semver). Enable the hook once with
  `git config core.hooksPath scripts/hooks`; it runs `glyph lint --stdin` (the
  checker CI runs over the range) and skips with a note when `glyph` is absent.
  Spec: [CONTRIBUTING.md](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md).
- `go build ./...` and `go test ./...` must pass before finishing a turn.
- English is the only committed language — **there is no `README.ja.md`;
  translations are not stored**. That is the fleet
  [doc-consistency policy](https://github.com/akira-toriyama/.github/blob/main/docs/doc-consistency-policy.md)
  (English-only, code-first, reduction-first — truth lives in the code/CLI and
  docs point at it), for which this repo is the reference implementation.
  Keep [README.md](README.md) and the docs/ tier
  (architecture/glossary/non-goals) carrying the same FACTS on any
  user-visible change, and **sweep the docs/ tier in the same change**: drift
  pools exactly where no guard looks. The guards that do look:
  [`scripts/check-readme-parity.sh`](scripts/check-readme-parity.sh) (README's
  `sync-task-status.yml@vX.Y.Z` pin must match that workflow's `furrow-version`
  default — leave the fleet-synced `task-status.yml` alone — and every
  `{"schema_version": N}` literal in README.md, CLAUDE.md and docs/*.md must
  equal `core.SchemaVersion`) and
  [`scripts/check-docs-vocab.sh`](scripts/check-docs-vocab.sh): **add a member
  to a closed vocabulary (a command, a `[section]`, a revisit signal, a `-q`
  qualifier) and the doc regions its claims table names must enumerate it, or
  CI fails** — the vocabularies come from the registries via the hidden
  `furrow vocab`, never a second hand-kept list.
- The READMEs' command table is **generated** from the cobra tree (hidden
  `furrow commands`, spliced by `scripts/gen-command-table.sh`, drift-checked by
  check.sh/CI): edit `Short`/flag definitions in `internal/cli`, rerun the
  script, commit both — never hand-edit between the `commands:begin/end` markers.
- **1 item = 1 PR** (squash); update docs in the same PR.

## References

- clig.dev — CLI design guidelines (reviewed 2026-06-25)
- gitmoji.dev; glyph (gitmoji-driven lint/semver/notes) (reviewed 2026-07-20)
- GoReleaser brews/nix (reviewed 2026-06-25)

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
