# CLAUDE.md

Guidance for working in this repository (and for furrow itself as a tool).

## For Claude Code — integration contract (read first)

furrow is the task store for THIS repo, living in `.furrow/`. When you work here:

- furrow **OWNS `.furrow/index.json`**. **Never hand-edit it.** It is written by a
  single deterministic marshaller; manual edits will fight the next `furrow`
  write and churn git. Mutate tasks via commands, not the file.
- `.furrow/bodies/*.md` **ARE** safe to edit by hand or by you — that is the point
  of the hybrid store. One body file per task id, 1:1 with the index.
- Canonical commands: `furrow add|ls|show|next|edit|done|move|reorder|check|dep|archive|lint|init`.
- `--json` is available on read commands; **JSON goes to stdout only** (logs and
  errors go to stderr). Use `--ndjson` for one task per line and
  `--status/-s`, `--label/-l`, `--limit/-n` to filter — so you rarely need jq.
  Mutations (`done|move|reorder|check|dep`) with `--json` emit
  `{before, after, changed}`; `add --stdin` bulk-creates one task per stdin line;
  `next --json` attaches a `reason` (`in_next_lane`, `deps_satisfied`) per task.
- `furrow edit <id>` with no TTY **prints the body file path** instead of opening
  an editor — read/edit that file directly.
- Exit codes: `0` ok / `1` not-found|empty / `2` bad-usage|validation / `3+`
  internal|IO. On non-zero, an `{"error":{"code","id","message"}}` object is on
  stderr.
- furrow is **non-interactive by default**; the TUI is `furrow ui` only.
  Destructive ops guard themselves: `furrow archive` previews unless `--yes`.

## What this is

furrow — a repo-local, plain-text task tracker. Structured metadata lives in
`.furrow/index.json` (deterministic, machine-written); long-form prose lives in
`.furrow/bodies/<id>.md` (hand/agent-editable); human config is
`.furrow/config.toml`. A cobra CLI and a bubbletea TUI drive it. Go,
cross-platform, brew/nix packaged. Background and decisions: [ROADMAP.md](ROADMAP.md);
research log: [MEMO.md](MEMO.md).

## Build / run

```sh
go build ./...                          # compile (use GOTOOLCHAIN=local on Go 1.23)
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
`core.Marshal` is the **only** function that serializes the index. Never call
`json.Marshal`/`json.NewEncoder` on an `*Index` anywhere else. Recipe:
struct-field key order, 2-space indent, `SetEscapeHTML(false)`, `[]` not null,
sort lane→priority→id, UTC whole-second timestamps, trailing newline. This is
what makes app-writes equal hand-edits byte-for-byte (zero git churn). A golden
round-trip test and `scripts/check-marshal-singlepath.sh` guard it.

### Frozen ids & sparse priority
ids (`t-0042`) are **frozen**: never reused, never renumbered (counter in
`.furrow/seq`). Reorder by editing the sparse integer `priority` (10-step) — one
field, not a renumber.

### Configuration
`.furrow/config.toml` is **read-only from the app** and **clamp-don't-reject**:
unknown keys and out-of-range values fall back to defaults with a warning that
`furrow lint` surfaces. Read it through `internal/config`. Two additive,
off-by-default switches: `[next].lanes` (which lanes `furrow next` shows;
default ready+in-progress) and `[labels].required` (a label-less task errors on
`add` and in `lint`; default false).

### Schema
`internal/schema.IndexV1` is the source of the JSON Schema; `furrow schema`
prints it and CI diffs it against `docs/schema/furrow.index.v1.json`. Change the
struct → update the schema const, the committed file, and the golden together.

## Conventions

- Commits: gitmoji + Conventional — `<:gitmoji:> <type>(<scope>)<!>: <subject>`.
  Enable the hook once: `git config core.hooksPath scripts/hooks`. Spec:
  [docs/commit-convention.md](docs/commit-convention.md).
- `go build ./...` and `go test ./...` must pass before finishing a turn.
- Keep [README.md](README.md) / [README.ja.md](README.ja.md) in sync on any
  user-visible change (bilingual is the house style).
- **Don't push without explicit OK.** 1 item = 1 PR (squash); update ROADMAP/docs
  in the same PR.

## References

<!-- broad → narrow; tag each (reviewed YYYY-MM-DD); re-check on a 6-month gap. -->
- clig.dev — CLI design guidelines (reviewed 2026-06-25)
- Conventional Commits 1.0.0; gitmoji.dev (reviewed 2026-06-25)
- charmbracelet bubbletea/bubbles/lipgloss/glamour — v1 line (reviewed 2026-06-25)
- GoReleaser brews/nix; git-cliff (reviewed 2026-06-25)

## Multi-session work policy

`docs/plans/` holds one file per in-flight task (delete on merge); `ROADMAP.md` =
design decisions + phase status; `MEMO.md` = research log. furrow's own backlog
lives in the private `akira-toriyama/projects` repo (label `furrow`) — don't add
a `.furrow/` board to this repo. **Never leave unfinished work implicit**
(未達成を暗黙にしない) — every in-flight task is a `projects` task, a plan file,
or a ROADMAP note.

**Multi-operator (shared checkout).** This repo is sometimes worked on by several
people/agents at once. A checkout has one shared HEAD/index/working tree, so two
operators running git in the same directory corrupt each other (orphaned commits,
commits on the wrong branch). **Each operator/session works in its own `git
worktree` (`git worktree add ../furrow-<topic> -b <branch> origin/main`) or a
separate clone — never share one checkout for concurrent git.** Commit + push
often and `git pull --rebase` before pushing. (Canonical statement of this rule:
the `projects` tracker's CLAUDE.md.)
