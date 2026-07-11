# CLAUDE.md

Guidance for working in this repository (and for furrow itself as a tool).

## For Claude Code — integration contract (read first)

furrow's own tasks live on the **central board** (the private
`akira-toriyama/projects` repo) — this repo deliberately has **no local
`.furrow/`**, so `furrow` commands run here resolve to the central board via
the user-level config. When you work with any furrow store:

- furrow **OWNS the `.furrow/tasks/*.json` shards** (one per task) **and
  `.furrow/meta.json`**. **Never hand-edit them.** They are written by a
  single deterministic marshaller; manual edits will fight the next `furrow`
  write and churn git. Mutate tasks via commands, not the files.
- `.furrow/bodies/*.md` **ARE** safe to edit by hand or by you — that is the point
  of the hybrid store. One body file per task id, 1:1 with its shard.
- Canonical commands: `furrow add|ls|show|next|revisit|edit|attach|done|move|reorder|retitle|value|effort|check|dep|label|repo|sync|apply|archive|lint|config|init`.
- **Repos are the scope; labels are pure tags.** A task's repositories live in
  the first-class `repos` field (`owner/repo`, 0..N; `[]` = a **draft**, the
  issue-draft analogue). `-r` is the scope control on reads: a full
  `owner/repo` or a short name resolving uniquely against the board's repos;
  an explicit `-r` overrides the board scope, `-r ''` = the whole board. `-l`
  filters by tag and ANDs with the scope. Within a single `-s` or `-l`, a comma
  is OR (`-s inbox,backlog`); flags still AND across fields. On a board, `add` unions the scope
  repo into `repos` (`--draft` suppresses exactly that); `ls --drafts` lists
  the repo-less tasks; `furrow repo <id> --add|--rm` attaches/detaches later.
- `--json` is available on read commands; **JSON goes to stdout only** (logs and
  errors go to stderr). Use `--ndjson` for one task per line and
  `--status/-s`, `--label/-l`, `--repo/-r`, `--limit/-n` to filter — so you
  rarely need jq.
  Mutations (`done|move|reorder|value|effort|check|dep|label|repo`) with
  `--json` emit
  `{before, after, changed}`; `add --stdin` bulk-creates one task per stdin line;
  `next --json` attaches a `reason` (`in_next_lane`, `deps_satisfied`) and
  `revisit --json` a `revisit` array (`no_repo`, `value_unset`, `effort_unset`,
  `stale`, `dep_done`) per task.
- **Batch reads by id: `show <id>... --no-body`** — any id set in one process,
  metadata only (no `body_text`), input order. `--json` = array for ≥2 ids (a
  single id keeps the classic object), `--ndjson` = one line per task at any
  arity. A partial miss still emits the found tasks and exits 1 with
  `details.missing` — branch on that array.
- **A multi-machine board converges with `furrow sync`** (auto-commit scoped
  to `.furrow/` → `fetch` + `rebase --autostash @{u}` → `push`): run it before
  reading and after writing a shared board. Within `.furrow/`, machine-written
  files (`tasks/`, `meta.json`, `config.toml`) and brand-new (untracked) bodies
  always commit, but a **merely-modified `bodies/<id>.md` is committed only when
  named with `-b/--body <id>` or swept with `--all-bodies`** — on a shared
  checkout a plain sync must not commit a co-located operator's in-progress
  prose under the wrong author. A skipped body is listed in the JSON
  `pending_bodies` field (its twin `committed_bodies` lists what was committed)
  and in a stderr note, while sync still exits 0 and pushes everything else —
  so after hand-editing a body, run `furrow sync -b <id>` (or check
  `pending_bodies`); a plain `furrow sync` would leave that edit local. It
  rebases onto the tracking ref, not `FETCH_HEAD`, so a co-writer's concurrent
  fetch can't race it into `Cannot rebase onto multiple branches`. On a true
  conflict it aborts the rebase itself and exits 3 with id `sync-conflict` + the
  conflicted shard paths in `details`. A concurrent writer's transient race is
  waited out with a bounded backoff, handled by cause: a foreign rebase caught
  by the pre-flight, if still stuck past the budget, exits 3 with id `sync-busy`
  — a **retryable** condition (re-run), NOT the do-not-retry `exit 2`; a
  fetch/ref-lock race during the pull is retried and, if a lock still blocks
  past the budget (a likely-stale `.git/*.lock`), fails terminally (id `sync`)
  naming the lock to remove, NOT `sync-busy`. A SIGINT/SIGTERM cancels the
  in-flight git and exits 3 with id `sync-interrupted` — retryable, just re-run
  (a genuine conflict is never masked by the signal: it still surfaces as
  `sync-conflict` with its `details.paths`). Branch on the `id`, not the exit
  code, to tell these apart. A successful sync also gains a `revisit` key
  (`{dep_done:[ids], stale:[ids]}`, repo-scoped, omitted when empty) — the
  loop-visible staleness nudge; run `furrow revisit` for detail.
- `furrow edit <id>` with no TTY **prints the body file path** instead of opening
  an editor — read/edit that file directly.
- Exit codes: `0` ok / `1` not-found|empty / `2` bad-usage|validation / `3+`
  internal|IO. On non-zero, an `{"error":{"code","id","message"}}` object is on
  stderr — plus an optional machine-actionable `details` (see `sync` above)
  and an optional `candidates` array when an input almost resolved (an
  ambiguous repo short name, or `-l <x>` matching nothing while `x` uniquely
  names a repo — the did-you-mean guard). Branch on the array, never regex
  the message.
- furrow is **non-interactive by default**; the TUI is `furrow ui` only.
  Destructive ops guard themselves: `furrow archive` previews unless `--yes`.

## What this is

furrow — an alternative to GitHub Projects/Issues: a clonable, git-native,
plain-text task tracker. One central board can back many repos (tasks carry
their repositories in the first-class `repos` field) or a store can live
repo-local. Structured metadata lives in
one JSON shard per task, `.furrow/tasks/<id>.json` (deterministic,
machine-written), with the board-wide layout version in `.furrow/meta.json`
(`{"schema_version": 3}`); long-form prose lives in
`.furrow/bodies/<id>.md` (hand/agent-editable); human config is
`.furrow/config.toml`. A cobra CLI and a bubbletea TUI drive it. Go,
cross-platform, brew/nix packaged.

## Build / run

```sh
go build ./...                          # compile (use GOTOOLCHAIN=local on Go 1.25+)
go test ./...                           # all packages (see Verify for the TUI)
./run.sh ls --json                      # build + run a subcommand
./run.sh ui                             # build + launch the TUI (needs a TTY)
```

## Verify (how to confirm a change works — runnable headless)

```sh
sh scripts/check.sh   # the one command: marshaller guard + build/vet/test +
                      # golangci + schema/config drift + a CLI smoke. Green here
                      # == green CI. Run this before finishing a turn.
```

Everything is verifiable without a terminal, including the interactive UI:
- **CLI**: directly runnable headless (`init/add/ls --json/next/done/migrate/lint`).
- **TUI**: do NOT need a real terminal to verify it. `internal/tui` has model-level
  tests (send key messages, assert state) AND a **teatest** end-to-end test that
  boots the real `tea.Program` in a simulated terminal, sends keys, and asserts
  both the rendered frame and the persisted mutation. A raw PTY is flaky on macOS —
  use teatest. Only the visual *aesthetics* need a human eye (`./run.sh ui`).
- **Determinism / drift**: the golden round-trip test, `scripts/check-marshal-singlepath.sh`,
  and the schema/config drift diffs (in `check.sh`) guard the load-bearing invariants.

## Source-of-truth references

Consult these before adding behavior, and keep terms consistent with them:
[docs/architecture.md](docs/architecture.md) (layers), [docs/glossary.md](docs/glossary.md)
(ubiquitous language), [docs/non-goals.md](docs/non-goals.md) (what furrow won't do).

## Non-obvious constraints — read before editing

### Layer rules (the spine)
`internal/core` is **pure** (stdlib only — no cobra, bubbletea, os, or filepath).
Ports (`Store`, `Clock`) are interfaces **defined in core**;
`internal/store/fsstore` is the **only** package that touches the filesystem;
`internal/store/memstore` is its in-memory twin for tests. `internal/cli` and
`internal/tui` are presentation and mutate **only** through `internal/app.App`
(the single mutation funnel). Crossing a layer means a port is missing — add the
interface, don't add the import.

### The single marshaller path — DO NOT regress this
`core.Marshal` is the **only** function that serializes the in-memory index;
the store persists per-shard via `core.MarshalTask` (one `tasks/<id>.json`) and
the layout version via `core.MarshalMeta` (`meta.json`). All three live in
`internal/core/marshal.go`. Never call `json.Marshal`/`json.NewEncoder` on an
`*Index`, `*Task`, or `*Meta` anywhere else. Recipe (same per shard):
struct-field key order, 2-space indent, `SetEscapeHTML(false)`, `[]` not null,
sorted+deduped label/dep sets, UTC whole-second timestamps, trailing newline.
This is what makes app-writes equal hand-edits byte-for-byte, and Save writes
only the shards whose bytes changed (zero git churn on a no-op save). A golden
round-trip test and `scripts/check-marshal-singlepath.sh` guard all three.

### Frozen, collision-free ids & sparse priority
ids (`t-k3m9p`) are **frozen**: never reused, never renumbered. They are
**random** (prefix + a random Crockford-base32 suffix, `[ids].width` chars),
generated locally with no shared counter, so concurrent `furrow add`
from separate worktrees/PRs won't collide (the app retries on the rare in-store
clash; `furrow lint` flags any duplicate as a backstop). Legacy numeric ids
(`t-0042`) stay valid and coexist. Reorder by editing the sparse integer
`priority` (10-step) — one field, not a renumber.

### Configuration
`.furrow/config.toml` is **read-only from the app** and **clamp-don't-reject**:
unknown keys and out-of-range values fall back to defaults with a warning that
`furrow lint` surfaces. Read it through `internal/config`. Two additive,
off-by-default switches: `[next].lanes` (which lanes `furrow next` shows;
default ready+in-progress) and `[labels].required` (a label-less task errors on
`add` and in `lint`; default false). The user-level central-board config
(`~/.config/furrow/config.toml`) scopes each `[[board]]` by **repo**:
`repo = "auto" | "" | "owner/repo"` ("auto" derives owner/repo from the
checkout's git origin, worktree-aware, ghq-path fallback — `internal/app`'s
job, file reads only, never a bare dir name); a board's `label` is only a
literal add-time tag, and `label = "auto"` is a reserved tombstone (warned,
ignored).

### Schema
`internal/schema.TaskV2` and `internal/schema.MetaV2` are the sources of the JSON
Schemas; `furrow schema [task|meta]` prints them (no arg or `task` = the shard
schema; `meta` = the `meta.json` schema) and CI diffs them against
`docs/schema/furrow.task.v2.json` and `docs/schema/furrow.meta.v2.json`. Change a
struct → update the schema const, the committed file, and the golden together.
A task carries a first-class `repos` set (owner/repo identifiers, same
sorted+deduped/[]-not-null semantics as labels; `[]` = draft). Labels are pure
free-form tags — a repo is NOT a label. The store refuses (exit 3) a board whose
`meta.json` version is newer than the binary (`core.CheckSchemaVersion`), so an
old binary can never lenient-parse away fields it doesn't know.

## Conventions

- Commits: gitmoji + Conventional — `<:gitmoji:> <type>(<scope>)<!>: <subject>`.
  Enable the hook once: `git config core.hooksPath scripts/hooks`. Spec:
  [CONTRIBUTING.md](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md).
- `go build ./...` and `go test ./...` must pass before finishing a turn.
- Keep [README.md](README.md) / [README.ja.md](README.ja.md) carrying the same
  FACTS, not the same STRUCTURE, on any user-visible change (bilingual is the
  house style; JA is intentionally a superset). Only shared load-bearing facts —
  the `sync-task-status.yml@vX.Y.Z` workflow-pin tag and `schema_version` — stay
  in lockstep, enforced by [`scripts/check-readme-parity.sh`](scripts/check-readme-parity.sh).
- **Don't push without explicit OK.** 1 item = 1 PR (squash); update docs
  in the same PR.

## References

<!-- broad → narrow; tag each (reviewed YYYY-MM-DD); re-check on a 6-month gap. -->
- clig.dev — CLI design guidelines (reviewed 2026-06-25)
- Conventional Commits 1.0.0; gitmoji.dev (reviewed 2026-06-25)
- charmbracelet bubbletea/bubbles/lipgloss/glamour — v1 line (reviewed 2026-06-25)
- GoReleaser brews/nix; git-cliff (reviewed 2026-06-25)

## Multi-session work policy

`docs/plans/` holds one file per in-flight task (delete on merge). **Never leave
unfinished work implicit** (未達成を暗黙にしない) — every in-flight task is a plan
file, a tracked issue, or an explicit note; nothing important lives only in a
chat transcript.

**Multi-operator (shared checkout).** This repo is sometimes worked on by several
people/agents at once. A checkout has one shared HEAD/index/working tree, so two
operators running git in the same directory corrupt each other (orphaned commits,
commits on the wrong branch). **Each operator/session works in its own `git
worktree` (`git worktree add ../furrow-<topic> -b <branch> origin/main`) or a
separate clone — never share one checkout for concurrent git.** Commit + push
often and `git pull --rebase` before pushing.
