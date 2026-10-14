# furrow

> An alternative to GitHub Projects / Issues — a clonable, git-native, plain-text task tracker you and your coding agent can both edit cleanly.

**furrow** keeps your tasks as plain text *in a git repo*: structured metadata in one deterministic JSON shard per task, long-form prose in per-task Markdown files. The case against Issues is simple. An issue can't be cloned — plain text can, so the tracker works offline and greps with your code. An agent can *read and write* it with ordinary file and CLI operations, no API client. And because the tracker lives in git next to the work, status never drifts from reality — the same push that changes the code can change the task. Writes are byte-stable, so `git diff` only ever shows what actually changed.

**When to reach for which.** GitHub Issues are the right tool for *intake from anyone* — a public inbox where a stranger can file a bug without write access to your repo. furrow is the opposite tool for the opposite job: *private, in-group* tasks for you and your agent. Its "you must be able to push to create a task" is **access control, not a defect** — the same permission boundary that guards your code guards your backlog.

**Local and instant, not a round-trip.** Much of this GitHub *can* do — but through the API: online-only, rate-limited, a network round-trip per call. furrow does it against plain files on disk: milliseconds, offline, no quota. Backlinks are the concrete example — `show --backlinks` answers "which tasks mention this one?" (the `[[id]]` links in their bodies) by scanning local files, where the GitHub equivalent is an online "mentioned in" panel behind an API call.

**Multi-person, honestly.** furrow is single-operator-first today, and that is the polished path. Several people *can* work one board — it is a git repo, so they clone, push, and `furrow sync` (per-task shards make concurrent edits a clean union). An `@mention` and a task **assignee** are planned, not built yet ([honesty note](docs/non-goals.md#built-vs-planned--honesty-note)).

Written in Go (module `github.com/akira-toriyama/furrow/v9`, Go 1.26+). No database, no daemon, no cloud.

> **Status:** **CLI-only** and shipping; the front-ends ridge (a TUI) and vista
> (a GUI) are separate repos — see [Status](#status), and the
> [Releases page](https://github.com/akira-toriyama/furrow/releases) for downloads.

---

## Install

> Releases are cut with GoReleaser and distributed via the Homebrew tap and the nix flake ([Releases page](https://github.com/akira-toriyama/furrow/releases)). Each release artifact carries a GitHub build-provenance attestation — verify a download with `gh attestation verify <file> --repo akira-toriyama/furrow` — and each archive an SPDX 2.3 SBOM (`<archive>.spdx.sbom.json`) with its own attestation, verified by adding `--predicate-type https://spdx.dev/Document/v2.3`.

```sh
# Homebrew (tap)
brew install akira-toriyama/tap/furrow

# Go toolchain (from source)
go install github.com/akira-toriyama/furrow/v9/cmd/furrow@latest

# Nix
nix run github:akira-toriyama/furrow
```

A from-source build reports its version as `dev`; `furrow version --json` adds the commit and date when the build carries a Go VCS stamp.

---

## Two questions, four shapes

Setting furrow up is two *independent* choices; ask them in this order and the
four shapes name themselves.

- **Mode — does the board have a remote?** A **shared board** is the default:
  it has a git remote and more than one writer (your other checkouts, a
  co-located session, every repo's pinned CI caller), kept converged by
  [`furrow sync`](#multi-machine-furrow-sync). A **standalone** board
  (`mode = "standalone"`) lives on one machine with no remote: no sync, no CI —
  see [Standalone](#standalone-a-local-board-with-no-remote).
- **Layout — where does the store sit?** A **central board** lives *outside*
  the repos it backs and is reached by configuration, so one tracker repo backs
  them all: each task carries its repos (the `repos` field) and each checkout is
  auto-scoped to its own — see [Central board](#central-board). A
  **repo-local board** sits *inside* the repo it serves (`furrow init` and go,
  as in the Quickstart below); only the board scoping differs.

The shapes stack mode first — *shared central* (one tracker for a fleet),
*shared repo-local*, *standalone central* (the work-machine recipe below),
*standalone repo-local* — with the same schema and on-disk bytes in all four.

---

## Quickstart

```sh
# create a .furrow store in the current repo
furrow init

# add a task; its id is random (like t-k3m9p), so capture it
id=$(furrow add "Wire up the config loader" --label core --label config --json | jq -r .id)

# list tasks in canonical lane -> priority -> id order
furrow ls

# move it out of intake (add defaults to inbox)
furrow move "$id" ready

# show what's actionable now
furrow next

# open the task's Markdown body in $EDITOR
furrow edit "$id"

# inspect a single task with its body
furrow show "$id"

# mark it done
furrow done "$id"
```

On a board whose scope has exactly **one active epic**, a bare `add` files the task under it and says so on stderr (`-e ''` stays unfiled). `furrow add --help` lists every field `add` can set up front; [Command notes](#command-notes) carry the rest.

### Typed query — `-q`

Every filtering read — `ls`, `next`, `revisit`, `stats`, `search` — takes `-q "<query>"`, a GitHub-Projects-style query folded into one string (a repeated `-q` ANDs its queries; `-r` and `-e` take one value each, an OR over several being `-q 'repo:a,b'` / `-q 'epic:a,b'`) and compiled by ONE shared evaluator, so it means the same thing everywhere (`brief` is deliberately excluded — a fixed session-orient read). It is a **flat AND-list**: whitespace between terms is AND, a comma inside one value is OR, a leading `-` is NOT, and it ANDs with the other filters (`-s/-l/-r`, `--sort`, …) so a query never widens a scoped board. No cross-field OR, no grouping, no in-query sort — GitHub's own ceiling; `--json | jq` owns the long tail.

The same filters drive the WRITE side: `set`, `done` and `move` take `-q`
(plus `-l`/`-r`) as a **selector** instead of an id list — one all-or-nothing
write that **previews until `--yes`**, under the board scope exactly as in `ls`, so
`furrow ls <flags>` previews precisely what `furrow done <flags>` would close. The
preview's shape, its refusals and an empty match: `furrow done --help` and `furrow set --help`.

```sh
furrow ls -q 'is:actionable label:cli,dx -status:icebox'   # workable now, (cli OR dx), not iced
furrow next -q 'value:>=4 -label:chore'                     # ready AND worth it
furrow ls -q 'is:open updated:<-30d'                        # open but untouched for a month
furrow ls -q 'closed:2026-07-01..2026-07-15'                # closed inside a window
furrow ls -q 'depends-on:t-k3m9p is:blocked'                # what waits on t-k3m9p, still stuck
furrow stats -q is:stale                                    # the stale board's shape
furrow ls -q 'roi:>2 "typed query"'                         # ROI>2 with a text phrase
```

- **qualifiers** (`field:value`, comma = OR, repeat = AND): `status`/`lane`, `epic` and `anchor` (an epic ref, resolved as `-e` does), `label` (a `*` is a wildcard — `label:area/*`), `repo` (resolved as `-r` does), `id` (prefix), `title`, `body`, and the graph fields `depends-on`/`blocks`/`descendant-of`/`ancestor-of` (task ids the read can place — see **graph**). A bad query is **exit 2**, never a silent empty result: an unknown qualifier or `is:` flag is kind `query-unknown-field`/`query-unknown-flag` with `candidates`, an operator on a non-ordered field is `query-type` with `allowed_operators`, a ref that resolves to nothing is the sibling flag's own fault (`epic-not-found`/`epic-ambiguous` with `candidates`, `repo-unknown`/`repo-ambiguous`, a task-id miss `validation` with the misses in `details.missing`), and every fault carries the offending `term` and its byte `offset` in `details`.
- **ordinal** (`value`, `effort`, `priority`, `roi`): comparison `>`, `>=`, `<`, `<=` and range `2..4` / `*..3` / `3..*`. An unset estimate (and an undefined `roi`) never satisfies a comparison.
- **dates** (`created`, `updated`, `closed`, `reviewed`, `due`): absolute `YYYY-MM-DD` or RFC3339, plus signed **relative offsets** from now (`updated:>=-2w`; units `m/h/d/w`, `m` = minutes; an offset needs a comparison or range). Ranges are inclusive with `*` open ends; a `null` stamp satisfies no comparison (existence is `has:`/`no:`'s job). A bare day on the machine stamps is a **UTC** day, on **`due`** the **board's calendar day** (`[due].timezone`) — the one date a human types, so `-q due:2026-08-04` finds what `--due 2026-08-04` wrote.
- **graph** (refs, never silent): `epic:X` selects a box's members, X resolving exactly as `-e` does — an exact id, else a unique id prefix, else a unique case-folded title substring — so a miss or an ambiguity is exit 2 with `candidates` rather than 0 rows, and a quoted value (`epic:'会場'`) is exact only — the id, else a pointer, else the whole title, and a pointer that is also a whole title is the same ambiguity — as `title:'…'` is; `anchor:X` selects the tasks whose due FOLLOWS box X's day, X resolved the same way. `depends-on:X` and `blocks:X` are the two directions of the dep edge (the tasks waiting on X, and the tasks X waits on); `descendant-of:X` and `ancestor-of:X` are their **transitive** twins over the deps DAG (start ids excluded from their own closure, cycles terminate). A task ref is an exact id the snapshot being read holds as a task or carries as a dep; anything else is exit 2 with the misses in `details.missing`. Epics do not nest — box membership is `epic:`. The archive boundary is `furrow ls --help`'s, under `--archived`; dangling pointers, ambiguity and the miss hints are the binders' doc comments in `internal/app/query.go` (`resolveEpicRef`, `resolveTaskRefs`, `edgeRefs`).
- **presence**: `has:FIELD` / `no:FIELD` over `label`, `repo` (`no:repo` = a draft), `epic` (`no:epic` = unfiled, the `epic-required` lint state), `value`, `effort`, `deps`, `refs`, `checklist`, `closed`, `reviewed`, `due`, `repeat` (`has:repeat` = the live occurrence of a series — the rule sits on exactly one task at a time), `anchor` (`has:anchor` = a due that follows a box's day; `has:due no:anchor` = the dates the other side or the calendar fixed), `body` (non-whitespace content — note `add` seeds every body with a heading).
- **computed flags** (furrow's own, no GitHub equivalent): `is:actionable` (a next lane with every dep done — a strict SUPERSET of what `furrow next` hands you, since next also scopes to the active epic), `is:blocked`, `is:stale` (no update within `[revisit].stale_days` — revisit's own definition, a pure age test that does not imply open), `is:unfiled` (no epic), `is:overdue` (a promised instant that has passed — `lint`'s `due-overdue` as a filter, minus every lane exemption, the done lane included: `-q` returns what you asked for, and which lanes stay QUIET is a lint/brief policy, not a fact about the date), `is:open`/`is:closed`/`is:draft`.
- **free text**: a bare word or `"quoted phrase"` is a case-insensitive substring over the task's text — **title, each checklist item, each ref, body** — `furrow search`'s walk, so `ls -q foo` finds what `search foo` finds (free text never takes a pattern; that is `search --regex`). On `title:` a quoted value means whole-field equality; `body:` stays a substring match.

---

## The store

furrow uses a **hybrid** layout: one machine-written JSON shard per task for structured metadata, and one hand-editable Markdown file per task for prose, so both halves stay diffable and two operators adding or editing tasks on separate worktrees touch distinct files — a git merge is a conflict-free union. The formats this rules out, and why, are [docs/non-goals.md](docs/non-goals.md#storage-format)'s.

```text
.furrow/
├── .gitattributes       # bodies/*.md merge=union
├── config.toml          # human config (furrow only READS this, except `furrow config set`'s surgical edit)
├── meta.json            # board-wide layout version {"schema_version": 11} — written ONLY by furrow, raised ONLY by `furrow upgrade`
├── tasks/
│   ├── t-k3m9p.json     # one metadata shard per task — written ONLY by the single core.MarshalTask path
│   └── t-p4q2r.json
├── repos/               # one review shard per repo — furrow review <repo> (last_reviewed clock)
│   └── akira-toriyama__furrow.json
├── epics/               # one shard per epic (box of work) — goal/active/meta + deps (the epics it waits on)
│   └── e-k3m9p.json
├── bodies/
│   ├── t-k3m9p.md       # long-form prose for t-k3m9p (hand/agent editable)
│   ├── t-p4q2r.md
│   ├── e-k3m9p.md
│   └── assets/          # `furrow attach`'s copies
└── archive/             # aged done tasks (its own tasks/, meta.json + bodies/)
    ├── meta.json
    ├── tasks/
    └── bodies/
```

A minimal `tasks/t-k3m9p.json` shard:

```json
{
  "id": "t-k3m9p",
  "title": "Wire up the config loader",
  "status": "in-progress",
  "priority": 100,
  "labels": [
    "config",
    "core"
  ],
  "repos": [
    "akira-toriyama/furrow"
  ],
  "deps": [],
  "refs": [],
  "checklist": [],
  "created": "2026-06-25T00:00:00Z",
  "updated": "2026-06-25T00:00:00Z",
  "closed": null,
  "reviewed": null,
  "body": "bodies/t-k3m9p.md",
  "due": "2026-08-04T14:59:59Z"
}
```

The layout version lives only in `meta.json`, never inside a shard, so a bump touches one file and no shard becomes a merge point.

### The layout version gates writes (and only `furrow upgrade` raises it)

That number is the **board's**, not the binary's — an **input** to every write, never an output. The gate has two sides, and the kind says which to fix:

- **The board is newer than your furrow** → `schema-too-new`, exit 3. Update the binary (in CI: bump the `sync-task-status.yml@vX.Y.Z` pin).
- **The board is older than your furrow** → readable but **read-only**: a write fails with `schema-upgrade-required`, exit 2, and the orient reads print one stderr `READ-ONLY` note, so a session learns at `furrow brief`, not at its first failed write.

No ordinary command migrates a board. `furrow upgrade` is the one raiser, and a **flag day**: afterwards no older furrow can write the board — pinned CI included. furrow cannot see those pins, so you keep the order:

```sh
furrow board                # schema:   vN (board) / vM (binary) — READ-ONLY: run `furrow upgrade`
# 1. release a furrow that ships the new layout
# 2. bump every caller's sync-task-status.yml@vX.Y.Z pin to it
furrow upgrade              # 3. preview: which stores change, and how many shards
furrow upgrade --yes && furrow sync
```

On a [standalone board](#standalone-a-local-board-with-no-remote) `upgrade` drops the checklist and the `sync` step; the gate is unchanged. `furrow board` reports `writable`/`schema_state` and **never fails on a mismatch**, which makes it the CI pre-flight. The error details and the reasoning are `furrow --help`'s and [docs/architecture.md](docs/architecture.md#the-repos-field-and-the-two-sided-version-gate)'s; the 2026-07-13 outage the gate answers is in [docs/non-goals.md](docs/non-goals.md#no-automatic-schema-migration).

### A key furrow doesn't know is preserved, not dropped

A field added *without* a bump fires no gate, so furrow **parks every top-level key it does not know and re-emits it** on the next write: *the gate stops a bumped layout from being misread; the passthrough stops an unbumped one from being destroyed.* Preserved is not honoured (`furrow lint` warns `unknown-shard-key`; `furrow tidy --unknown-keys` prunes), and releases ≤ `v0.9.0` still drop such keys. Mechanism and limits: [docs/architecture.md](docs/architecture.md#unknown-key-passthrough-the-other-half-of-the-version-gate).

### Fields, estimates and revisit

Every term is defined in [docs/glossary.md](docs/glossary.md) and every field typed by the JSON Schemas under [`docs/schema/`](docs/schema/) (`furrow schema [task|meta|repo|epic]`). Two surprises: `repos` is a first-class set (`owner/repo`, empty = a **draft**; a repo is *not* a label), and `due` is an **instant** — `--due 2026-08-04` binds the end of that day in `[due].timezone`, so the 4th is not overdue at 00:01.

`value` and `effort` (1..5, unset by default) let an agent pick the next task from recorded data. **ROI = value / effort is derived, never stored**, and `next` ignores it — `furrow ls -q 'roi:>=2'` selects on it, and ranking by it is the caller's choice:

```sh
# highest value-per-effort first, among tasks that carry both estimates
furrow ls --json | jq 'map(select(.value and .effort)) | sort_by(-(.value / .effort))'
```

`furrow revisit` is the **read-only** companion: the open tasks whose metadata may be out of date, each row naming the signal to fix with the existing setters — see its [command note](#command-notes) and `furrow revisit --help`.

### Attaching files

A task body is plain Markdown, so a screenshot, a diagram or a received document is attached by committing the file beside the bodies and linking it by **relative path** — `furrow attach <id> <file>` does both, for any kind of file (`furrow attach --help`):

```markdown
![repro](assets/t-k3m9p-bug.png)
[quote](assets/t-k3m9p-quote.pdf)
```

It renders wherever Markdown does, not in the terminal. Keep screenshots small and scrub secrets (git history is permanent); track large files with **Git LFS** before the first commit; `furrow lint` warns on a missing, orphan or ≥5 MiB asset.

---

## Command reference

furrow is CLI-only; the front-ends (ridge, a TUI; vista, a GUI) are separate repos that drive it through the CLI/JSON contract (see [Status](#status)).

The table is **generated from the binary**: the cobra tree's `Use`/`Short`/aliases/flags are the single source of truth — `scripts/gen-command-table.sh` splices it in, and check.sh/CI fail when the block and the binary disagree — so no command or flag ships without appearing here. `furrow <cmd> --help` says the same one-liners; the [command notes](#command-notes) below carry the behavior contracts a one-liner can't.

<!-- commands:begin — generated by scripts/gen-command-table.sh from internal/cli (Use/Short/flags). Edit those, rerun the script, commit both. Hand edits inside this block are overwritten. -->
| Command | What it does | Flags |
|---|---|---|
| `init [dir]` | Create a .furrow store (at FURROW_DIR/FURROW_BOARD if set, else in dir or the current directory) | — |
| `add <title>...` | Add a task (or many with --stdin / --batch) | `--anchor`, `--batch`, `--body`, `--body-file`, `--check`, `--dep`, `--draft`, `--due`, `--effort`, `-e/--epic`, `-l/--label`, `-p/--priority`, `--ref`, `--repeat`, `-r/--repo`, `-s/--status`, `--stdin`, `--value` |
| `ls [<epic>]` (alias `list`) | List tasks (canonical lane->priority->id order), or group them by epic with --tree | `--actionable`, `--archived`, `--blocked`, `--drafts`, `-e/--epic`, `-l/--label`, `-n/--limit`, `-q/--query`, `-r/--repo`, `--reverse`, `--since`, `--sort`, `-s/--status`, `--tree`, `--until` |
| `show <id>...` | Show tasks or epics with metadata and markdown body (batch-friendly) | `--archived`, `--backlinks`, `--no-body` |
| `next` | Show actionable tasks (in the next-lanes, all deps done, in the active epic) | `--all-epics`, `-e/--epic`, `-l/--label`, `--lanes`, `-n/--limit`, `-q/--query`, `-r/--repo` |
| `brief` | One-shot session-orient read: due band, active/pinned epics, next picks with bodies, blocked, revisit, drafts | `-l/--label`, `-n/--limit`, `-r/--repo`, `--stale-days` |
| `revisit` | List open tasks needing re-evaluation (agent re-weighing signal) | `-e/--epic`, `-l/--label`, `-n/--limit`, `-q/--query`, `-r/--repo`, `--stale-days` |
| `search <term>` | Full-text search over every task's title, checklist, refs and body | `--archived`, `-e/--epic`, `-l/--label`, `-n/--limit`, `-q/--query`, `--regex`, `-r/--repo`, `-s/--status` |
| `stats` | Summarize the board: counts by lane, repo, and label | `-e/--epic`, `-l/--label`, `-q/--query`, `-r/--repo`, `--since`, `-s/--status`, `--until` |
| `board` | Print the active board: store path, mode/layout, scope, lane vocabulary, and schema state | — |
| `boards` | List the configured boards (user-level config), independent of cwd | — |
| `doctor [dir...]` | Diagnose this machine's board setup: config, boards, scopes, git freshness | — |
| `edit <id>` | Edit a task's or epic's markdown body in $EDITOR, or replace it with --body | `--body`, `--body-file`, `--expect-updated` |
| `note <id> <text>` | Append (or --prepend) a paragraph to a task's or epic's body and advance its updated time | `--expect-updated`, `--prepend` |
| `attach <id> <file>` | Attach a file of any kind to a task (copies into bodies/assets/, links it from the body) | — |
| `done [<id>...]` | Move tasks into the done lane (stamps closed) | `--expect-updated`, `-l/--label`, `--note`, `-q/--query`, `-r/--repo`, `--yes` |
| `move [<id>...] <lane>` | Move tasks to a lane | `--expect-updated`, `-l/--label`, `-q/--query`, `-r/--repo`, `--yes` |
| `reorder <id> [<priority>]` | Set a task's priority — absolute, or relative with --before/--after | `--after`, `--before`, `--expect-updated` |
| `retitle <id> <title...>` | Rename a task (updates the shard title and the body heading) | `--expect-updated` |
| `set [<id>...]` | Apply several triage edits at once (lane, priority, value, effort, labels, repos, epic, due, repeat, anchor) | `--add-label`, `--add-repo`, `--after`, `--anchor`, `--before`, `--clear-anchor`, `--clear-due`, `--clear-effort`, `--clear-repeat`, `--clear-value`, `--due`, `--due-shift`, `--effort`, `-e/--epic`, `--expect-updated`, `-l/--label`, `-p/--priority`, `-q/--query`, `--repeat`, `-r/--repo`, `--rm-label`, `--rm-repo`, `-s/--status`, `--value`, `--yes` |
| `value <id> [<1-5>]` | Set a task's value estimate (coarse 1..5), or clear it with --clear | `--clear`, `--expect-updated` |
| `effort <id> [<1-5>]` | Set a task's effort estimate (coarse 1..5), or clear it with --clear | `--clear`, `--expect-updated` |
| `check <id> [item-index]` | Toggle, add, remove, or reword a checklist item | `--add`, `--expect-updated`, `--off`, `--reword`, `--rm` |
| `dep <id> [<dep-id>...]` | Add/remove a task's dependencies, or list them both ways with --list | `--expect-updated`, `--list`, `--rm` |
| `epic add <title>` | Create an epic (never active — open it with `epic activate`) | `--anchor`, `--body`, `--body-file`, `--goal`, `-l/--label`, `--meta`, `-r/--repo` |
| `epic ls` (alias `list`) | List epics (open only by default), active first | `--all`, `-l/--label`, `-n/--limit`, `-r/--repo` |
| `epic show <epic>` | Show one epic: goal, meta, progress, member tasks, and its body | `--no-body` |
| `epic set <epic>` | Edit an epic's title, goal, meta, labels, repos, anchor day, or its standing/pinned declarations | `--add-label`, `--add-repo`, `--anchor`, `--clear-anchor`, `--goal`, `--meta`, `--pinned`, `--rm-label`, `--rm-meta`, `--rm-repo`, `--standing`, `--title`, `--yes` |
| `epic activate <epic>` | Make this the active epic for its repos (at most one each) | `--reason` |
| `epic deactivate <epic>` | Clear the active flag without closing the epic (suggests where to return) | — |
| `epic done <epic>` | Close an epic (clears active; suggests the previous active, never picks it) | — |
| `epic reopen <epic>` | Reopen a closed epic (clears closed; never re-activates) | — |
| `epic dep <epic> [<dep-epic>...]` | Add/remove an epic's deps (open after those close), or list them both ways with --list | `--list`, `--rm` |
| `epic rm <epic>` | Delete an epic outright — withdraw a box, never close one (preview unless --yes) | `--force`, `--yes` |
| `label <id>` | Add and/or remove labels on a task | `--add`, `--expect-updated`, `--rm` |
| `repo <id>` | Attach and/or detach repos (owner/repo) on a task | `--add`, `--expect-updated`, `--rm` |
| `ref <id>` | Add and/or remove refs (file:line or URL) on a task | `--add`, `--expect-updated`, `--rm` |
| `review <repo\|id\|epic>` | Record a review: stamp a task's or epic's reviewed time, or a repo's last-reviewed clock | `--by` |
| `apply --on <open\|merge> [--ref <src>] [--body-file <path>] [--dry-run]` | Apply SetStatus-task directives parsed from PR/commit text | `--body-file`, `--dry-run`, `--on`, `--open-lane`, `--ref` |
| `sync` | Commit the board, pull --rebase, push (thin git wrapper) | `--all-bodies`, `-b/--body`, `-m/--message` |
| `archive [<id>...]` | Retire done tasks to .furrow/archive/ — by id, or the aged sweep (preview unless --yes) | `--older-than`, `-r/--repo`, `--yes` |
| `unarchive <id>...` | Restore archived tasks to the hot board (archive's inverse) | — |
| `rm <id>...` | Delete tasks outright — withdraw a filing, never retire done work (preview unless --yes) | `--force`, `--yes` |
| `tidy` | Prune dead bookkeeping: satisfied done-lane dep edges, parked unknown shard keys (preview unless --yes) | `--done-deps`, `--unknown-keys`, `--yes` |
| `migrate <task-file.md>` | Import a Task.md-style tracker into furrow (preview unless --yes) | `-e/--epic`, `-l/--label`, `--yes` |
| `upgrade` | Raise the board's on-disk layout to this furrow's schema (flag day; preview unless --yes) | `--yes` |
| `lint` | Check index<->body consistency, lanes, deps, links, assets, and config | `--code`, `--exclude-code`, `--severity` |
| `config init` | Write the user-level furrow config (central-board template) | `--path`, `--scope` |
| `config path` | Print the resolved path to the user-level furrow config | — |
| `config set <key> <value>` | Set one config key — the board's config.toml, or --user for the user-level file | `--board`, `--user` |
| `schema [task\|meta\|repo\|epic]` | Print the JSON Schema for a task shard, meta.json, a repo review shard, or an epic shard | — |
| `version` | Print the furrow version (with build commit/date when stamped) | — |
| `vocab [name]` | Print a closed vocabulary (error kinds, lint codes, …), one member per line | — |
<!-- commands:end -->

On the read commands, `-r/--repo` is the scope control: a full `owner/repo` or a short name resolving to exactly one (ambiguity is exit 2 with `candidates`); an explicit `-r` overrides the board scope, `-r ''` shows the whole board. `-l/--label` is a pure tag filter that ANDs with the scope. Within `-s` or `-l` a comma is OR and repeats union; the flags AND across fields. An unknown `-s` lane **exits 2 with the configured lanes in `candidates`** (a closed vocabulary), an unknown `-l` tag matches nothing — unless it uniquely names a repo and no `-r` was typed, which is exit 2 pointing at `-r` (the did-you-mean guard, on every filtering read). **A read never narrows or truncates silently**: a scope that hides drafts or boxes prints one stderr hint, and a `-n` cap that bites prints `note: showing N of M (-n)`; the JSON stays a bare array. `furrow board` shows the lanes and the active scope without provoking an error.

Global flags: `--json` and `--ndjson` are honored **wherever furrow emits JSON** — `--ndjson` is the same payload, compact, one value per line. A command whose `Use` says `<id>...` (`show`, `done`, `move`, `set`, `unarchive`) is **always an array**; a one-id mutation emits `{before, after, changed}` (a lane respace adds `renumbered`); the report-shaped commands (`rm`, `archive`, `tidy`, `upgrade`, `apply`, `migrate`) emit one object. **A write that changes nothing leaves `updated` alone** — an idempotent retry resets no staleness clock and writes no shard; the prose writes (`note`, `done --note`, a body replacement, `attach`, `apply`'s annotation) always advance it. `edit` prefers `$FURROW_EDITOR`, then `$VISUAL`, then `$EDITOR`, falling back to `vi`.

### Command notes

The generated table is the machine-guaranteed surface; these are the behavior contracts that don't fit a one-liner. (Commands whose whole story fits their table row — `init`, `retitle`, `label`, `ref`, `schema`, `version`, `vocab` — have no entry, and `attach`, `sync`, `upgrade`, and `config` have their own sections: [attachments](#attaching-files), [multi-machine sync](#multi-machine-furrow-sync), [the layout gate](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it), [the central board](#central-board).)

- **`add`** — `--stdin` (one title per line) and `--batch` (NDJSON with per-task fields and batch-local keys, so an epic's dependency graph is one file) create every task in one write, and **`--repeat`** makes a task recur from its required `--due`: a short spelling — `daily`, `every <n> days`, `weekly`, `weekly on <days>`, `every <n> weeks on <days>`, `monthly`, `monthly on <day-of-month>`, `monthly on last`, `monthly on <nth> <weekday>`, `monthly on last <weekday>`, `every <n> months on <day-of-month>`, `yearly`, `every <n> years` — or a raw RRULE line, and only a close advances the series. The contract is `furrow add --help`.
- **`ls`** — canonical `lane -> priority -> id` order unless `--sort`; every flat row carries a state glyph (★ actionable, ✓ done, ~ parked, · open but not available), and `--tree` groups the same rows by epic without ever showing fewer tasks: `furrow ls --help`.
- **`show`** — any number of ids (tasks or epics) in one read, `--json` **always an array**; a partial miss still prints what was found, and the error names the rest in `details.missing`. The human output names what `deps` and `epic` point at, while `--json` keeps the ids plus `actionable`/`blocked_by`, so `show --no-body --json` is key-for-key an `ls` row: `furrow show --help`.
- **`next`** — actionable = lane in `[next].lanes` **and** every dep done; on a board with epics it is scoped to the pinned boxes, then the active box, then the unfiled pile (with nothing active, only the pinned band remains), and what the scope leaves out is counted on stderr: `furrow next --help`.
- **`brief`** — the session-orient read, `furrow sync && furrow brief`: **`due` leads and no automatic scope narrows it**, then the epic header, `next`'s top picks (with bodies in `--json`), `blocked`, the `revisit` summary, drafts, and `sync`'s lint ride-along. Read-only and git-free: `furrow brief --help`.
- **`revisit`** — read-only; every row ends `← code: detail` and `--json` carries the same as a `revisit` array of `{code, detail}`, so an agent knows what to fix (the task and box codes: `App.Revisit` in `internal/app/revisit.go`); the box signals ride `sync`/`brief`'s summary, not this read. The contract is `furrow revisit --help`.
- **`search`** — a case-insensitive substring, or with `--regex` an RE2 pattern, over each task's title, checklist items, refs, then body, under `ls`'s scope and `-q`; each hit names its `matched_field` and a `snippet`: `furrow search --help`.
- **`stats`** — the board's shape (`total`, the board-wide `drafts`, `by_lane`, `by_repo`, `by_label`; `-r ''` for the whole board), and with `--since`/`--until` a `window` of the ids created and closed inside the bounds, archive included — the machine side of a session's `created ≤ closed − 1` budget check; `furrow stats --help`.
- **`board`** — the introspection snapshot (store, discovery source, mode/layout, scope, lanes, schema state) that **never fails on a version mismatch — it reports one**; `furrow board --help`.
- **`boards`** — every user-level `[[board]]`, **without resolving against cwd** (exit 0, possibly empty — the diagnosis for a machine whose scopes were never configured); `furrow boards --help`.
- **`doctor`** — the machine-wide board-setup **health check**, read-only and network-free, with a stable `code` per finding; exit 1 (`doctor-unhealthy`) means problems, so `furrow doctor --json | jq -e '.healthy'` is a pre-flight; `furrow doctor --help`.
- **`edit`** — opens a task's or an epic's `bodies/<id>.md` in the editor (no TTY: prints the path); `--body`/`--body-file` **replace the whole body and advance `updated`** in one write, and `note` is how to *append*; `furrow edit --help`.
- **`note`** — appends (or `--prepend`s) a paragraph **and** advances `updated` in one write, on a task or an epic — **membership routes the id, never its prefix**; `furrow note --help`.
- **`done`** — **this is what advances a recurring task**: the next occurrence is written in the same all-or-nothing write, so exactly one live task carries a series; `--note` folds the closing word into every closed body; `furrow done --help`.
- **`--expect-updated <rfc3339>`** (every writer whose `--help` lists it, one entity at a time) — the **stale-read guard**: when someone else wrote since your read, the mutation still goes through but says so (`stale_read {expected, actual}` plus a stderr note) — a warning, not an optimistic-lock refusal; `expectUpdatedFlag` in `internal/cli/cmd_mutate.go`.
- **The session write guard** (task and epic writes, under Claude Code only) — a write touching a repo an **earlier-started** Claude Code session on this machine sits in is **refused** while that session is working (exit 2, kind `session-busy`; the escape is `add --draft`, never a force flag) and goes **through with a warning** (`session_warn`) once it is idle; the per-machine mode is `[session].guard` in the [user-level config](#user-level-config-no-per-repo-file). Contract: root `furrow --help` and [docs/architecture.md](docs/architecture.md#the-session-write-guard).
- **`move`** — stamps `closed` entering the done lane and clears it leaving; a task keeps its priority across lanes, landing at the destination's tail (with a stderr note) when that lane already holds the value; `furrow move --help`.
- **`reorder`** — an absolute priority, or `--before`/`--after <id>` beside a lane-mate; an exhausted gap respaces the lane in the same write, reported in `renumbered` without advancing the neighbours' `updated`; `furrow reorder --help`.
- **`value` / `effort`** — a coarse 1..5 estimate; an out-of-range score clamps **and is signaled**, so an explicit arg is never silently rounded, and `--clear` unsets; `furrow value --help`.
- **`set`** — the routine triage edits (lane, priority, value, effort, labels, repos, epic, due, repeat, anchor) in **one** all-or-nothing write over one id, several, or a `-q` selection that previews until `--yes`: `--due` is the snooze, `--due-shift` the reschedule of the dues tasks already carry, `--anchor <epic>` makes a due follow a box's day; `furrow set --help`.
- **`check`** — zero-based indexes, the number `show` prints beside each row; marking done is an idempotent set, not a toggle (`--off` unchecks), beside `--add`/`--rm`/`--reword`; `furrow check --help`.
- **`dep`** — variadic, acyclic, idempotent and all-or-nothing (`dep add` / `dep rm` are accepted spellings). `--list` reads the neighborhood **both ways** — `depends_on` and `blocks`, each row with its own `blocked_by` — so one read answers "what moves if I close this". Contract: `furrow dep --help`.
- **`epic`** — the box's subcommands, each with its own `--help` as its contract (`furrow epic --help` lists them). At most one active box per repo, and `epic done` never picks a successor; `epic show` prints the **waiting** state, and moving a box's day with `epic set --anchor` is the **reschedule** — every open follower's due shifts with it, previewed until `--yes` (no stored offset, no cascade: [docs/non-goals.md](docs/non-goals.md)). A task joins a box with `add -e` / `set -e` (`-e ''` unfiles); `epic add` only creates one.
- **`repo`** — each value is a full `owner/repo` or a short name resolving to exactly one known repo (else exit 2 with `candidates`); a task with no repos is a draft. Contract: `furrow repo --help`.
- **`review`** — stamps a task's or a box's `reviewed` (apart from `updated`) or a repo's review clock; `--by agent` logs a repo sweep without advancing the human clock. Contract: `furrow review --help`.
- **`apply`** — parses `SetStatus-task: <body-link> [<lane>]` directives from PR/commit text, the CI hook behind [auto status updates](#ci-auto-update-a-tracker-from-prs); validation is non-blocking, and `--dry-run` is the local footer check before `gh pr create`. Contract: `furrow apply --help`.
- **`archive`** — retires done tasks into `archive/` (named ids, or an aged sweep under the board scope) and previews unless `--yes`; a done task a hot task still depends on is held, never archived. `unarchive` is the way back. Contract: `furrow archive --help`.
- **`unarchive`** — restores archived tasks all-or-nothing, exactly as archived; reopening stays `move`'s job. Contract: `furrow unarchive --help`.
- **`rm`** — deletes tasks outright (git history is the only way back): the withdrawal of a filing, not a retirement. Previews unless `--yes`; a target something still points at is refused (kind `referenced`) unless `--force` severs the references; **`epic rm`** is the box twin. Contract: `furrow rm --help`.
- **`tidy`** — prunes dead bookkeeping, per-run opt-in: `--done-deps` and `--unknown-keys` (the operator's tool for parked unknown keys, since hand-editing a shard is off the table). Previews unless `--yes`. Contract: `furrow tidy --help`.
- **`lint`** — every finding carries a stable kebab-case `code` (`dep-cycle`, `ready-blocked`, `due-overdue`, `unknown-shard-key`, …; the whole set is `vocab`'s lint-codes); branch on the code, not the message. A write that raises an error the board did not have says so on stderr in the same command, and never refuses. `--code` / `--exclude-code` / `--severity` and the `[lint]` config narrow the run, and **the filter drives the exit code**. Contract: `furrow lint --help`.
- **`migrate`** — imports a hand-maintained Task.md, dry-run until `--yes`; unmapped headings and wikilinks are reported, never dropped. Contract: `furrow migrate --help`.

---

## Claude Code / agent integration

furrow needs no MCP server and no plugin — the plain CLI **is** the agent interface: `--json`/`--ndjson` on every read, machine-actionable error envelopes, and a clonable plain-text store the agent can read (and, for bodies, write) directly. A daemon or a second protocol would add operational surface without adding a capability (see [docs/non-goals.md](docs/non-goals.md)). The integration is a `CLAUDE.md` contract plus the `--json` flag, and furrow never prompts.

**The contract is `furrow --help`**, printed by the binary rather than kept here: `bodies/*.md` are yours to edit, every shard and `meta.json` is furrow's (never hand-edit them), the exit codes, the stderr error envelope (branch on its `kind` — a closed vocabulary `furrow vocab error-kinds` prints — on `retryable`, and on `candidates` when an input almost resolved; never on the message), the JSON shapes, the one list of destructive operations that preview unless `--yes`, and the two schema-gate kinds. Each command's `--help` carries the rest — `furrow board --json` as the write pre-flight (`writable` / `schema_state`), `edit --body` and `note` for body writes that keep `updated` honest, `show <id>... --no-body` for a lean batch read.

**Read a board at working scale before writing one: [furrow-test](https://github.com/akira-toriyama/furrow-test).** A hundred tasks in five boxes with 181 dependency edges, plus the logs of fresh sessions acting on it read-only (its `docs/drills.md`) — where a real board trips an agent, and what removed each trip.

### CI: auto-update a tracker from PRs

`furrow apply` turns a PR into a status update — the `Closes #N` idea, for a
furrow tracker. Add a footer to the PR body pointing at a task's body file:

```
SetStatus-task: https://github.com/<owner>/<tracker>/blob/main/.furrow/bodies/<id>.md done
```

A PR **open** nudges the task to in-progress and the **merge** applies the named lane (`furrow apply --help` has the rules). The CI apply is non-blocking, so check a footer locally first: `furrow apply --on merge --dry-run --body-file <pr-body>` validates every directive and writes nothing.

The GitHub wiring **ships with furrow** as a reusable workflow,
[`.github/workflows/sync-task-status.yml`](.github/workflows/sync-task-status.yml).
A code repo needs only a ~10-line caller, pinned to a **concrete furrow release
tag** (never a moving ref):

```yaml
# .github/workflows/task-status.yml
name: task-status
on:
  pull_request:
    types: [opened, edited, reopened, ready_for_review, closed]
permissions:
  contents: read
  pull-requests: write
jobs:
  sync:
    uses: akira-toriyama/furrow/.github/workflows/sync-task-status.yml@v9.0.1
    secrets:
      PROJECTS_WRITE_PAT: ${{ secrets.PROJECTS_WRITE_PAT }}
```

The workflow's header comment and `inputs` carry the rest: the tracker it writes (default `akira-toriyama/projects`), the release binary it downloads to match its own tag, the one `PROJECTS_WRITE_PAT` secret (the job skips cleanly until it exists), and the `furrow board --json` pre-flight that fails a stale pin with one error instead of "task not found" for every id. That pin is why a board upgrade keeps [the flag-day order](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it): release furrow → bump every caller's pin → *then* `furrow upgrade`.

---

## Central board

A **central board** is a `.furrow` that lives *outside* the repos it backs and
is reached by configuration rather than by sitting in the checkout, so one
tracker repo can back many repos, each checkout scoped to its own `owner/repo`.
The count is a consequence, not the definition: the
[work-machine recipe](#standalone-a-local-board-with-no-remote) below is a
central board backing exactly one repo. Wire it up for whole trees of repos
(user-level config), or per repo (a pointer file).

### User-level config (no per-repo file)

`~/.config/furrow/config.toml` (or `$XDG_CONFIG_HOME/furrow/config.toml`;
`furrow config path` prints which) points whole trees of repos at central
boards with **zero per-repo setup**. `furrow config init` scaffolds it,
`furrow config set --user` edits one key, and the repo-root
[`config.global.toml`](config.global.toml) is the annotated copy of every key.

A board activates **only when the cwd is under one of its `scopes`** (the most
specific wins); everywhere else furrow behaves exactly as without it. A
half-written entry is dropped rather than guessed, and `furrow lint` and
`furrow config path` report what was clamped (`config.LoadGlobal`'s doc
comment). `repo = "auto"` derives `owner/repo` by file reads alone and never
writes a bare directory name into `repos`; when it cannot, `add` creates drafts
([`internal/app/gitorigin.go`](internal/app/gitorigin.go)). `FURROW_BOARD=<path>`
is the one-off env form ([Discovery precedence](#discovery-precedence)); a job
with no meaningful cwd wants `FURROW_DIR` ([docs/scheduling.md](docs/scheduling.md)).

Two settings belong here rather than in the board's committed `config.toml`,
because they are this machine's choice: **`autocommit = true`** git-commits the board's
`.furrow/` after every mutating command — best-effort, never pushing, keyed by
the **store** rather than the scope (`App.AutoCommitFlush`,
`app.applyStoreAutoCommit`; `furrow board` reports `autocommit: on`) — and
**`[session]`** sets the [session write guard](docs/architecture.md#the-session-write-guard)'s
mode, whose behaviour is its bullet in [Command notes](#command-notes).

### Per-repo pointer

A single repo can instead redirect with a `.furrow-pointer.toml` at its root
(this **wins over** the user-level central boards):

```toml
board = "../projects/.furrow"   # the central .furrow (relative to this file, ~, or absolute)
default_repo = "me/chord"       # optional: scope to one owner/repo ("auto" derives it; "" = redirect only)
```

### Discovery precedence

`FURROW_DIR` (explicit, no scope injection) → the nearest ancestor directory
holding a `.furrow` or a `.furrow-pointer.toml` (at the same level the real
local store wins) → a **central board** whose scope encloses the cwd:
`FURROW_BOARD` (one synthetic board, scoped two levels above its store) if set,
otherwise the user-level config file's `[[board]]` entries → else nothing
resolves (exit 2). So `FURROW_BOARD` replaces only the config-file boards, never
a nearer `FURROW_DIR` / local `.furrow` / pointer. A configured path must name
the store itself or a store-to-be (empty, or only dot-entries other than
`.furrow`, which the first write stamps); the repo holding the `.furrow` is
exit 2 with that `.furrow` as the did-you-mean (`isStoreDir`'s doc comment).

That order picks the **store**; the **scope** is a second question. A pointer or
a `[[board]]` answers it (including with "none"); `FURROW_DIR` and a local
`.furrow` declare nothing and fall back to the board's own
[`default_repo`](#configuration). With a scope in effect, `add` unions the
scope repo into `repos` (`--draft` suppresses exactly that; an explicit `-r`
adds), `epic add` falls back to it, and
the filtering reads (`ls`/`next`/`revisit`/`search`/`stats`/`brief`, `epic ls`)
filter to it — never silently *hiding*: a hidden draft or box gets one stderr
hint. `auto_filter = false` shows the whole board while `add` still attaches;
`-r ''` is the per-command escape.

---

## Multi-machine: `furrow sync`

A **shared board** — one with a remote — needs only one ritual: pull before you
read, push after you write. `furrow sync` is that ritual as one
non-interactive command — auto-commit **scoped to `.furrow/`**, `git fetch` +
`git rebase --autostash @{u}`, `git push` — a thin git wrapper, not a sync
daemon or server (see [docs/non-goals.md](docs/non-goals.md)). **`furrow sync
--help` is its contract**: which files the commit takes, the field-wise shard
merge, every key of the progress object and the `incoming` classes. What to
rely on:

- **It never publishes someone else's prose.** Machine-written files and
  brand-new bodies always commit; a merely-modified `bodies/<id>.md` commits
  only when furrow itself wrote it or you name it (`-b`, or `--all-bodies` on a
  checkout that is yours alone) — otherwise it is left for its author and
  reported in `pending_bodies`, so a **co-located checkout** never commits
  another operator's draft under the wrong author. A file furrow does not own
  is never committed.
- **Concurrent writes merge.** Per-task shards make true conflicts rare — only
  both sides editing the *same* task can collide. `furrow init` scaffolds
  `.furrow/.gitattributes` with `bodies/*.md merge=union` (and the
  `archive/bodies/` twin), so two appends to one body fold together instead of
  stopping the sync (`furrow doctor` warns `no-body-union-merge` on a board
  that lacks the line). **Shards are merged by furrow itself**, field by field
  (union on JSON would corrupt it).
- **`complete` — not `pushed` — is the "fully published" flag.** The progress
  object prints to stdout on success and failure alike, and a failed sync
  published nothing, whatever its lists say. The task changes the pull brought
  in print as one `incoming:` line, so the CI that closed your in-progress task
  surfaces in the sync that pulled it.

## Sync failure modes

Every failure is an error envelope: branch on its `kind`, and re-run only when
`retryable` says so (`furrow vocab error-kinds` prints the vocabulary;
`furrow sync --help` gives the pre-flights' exit codes and each way out):

- **A pre-flight refuses before anything is committed** — `git-missing`,
  `no-git-repo`, `sync-op-in-progress` (the message names the exact way out),
  `sync-detached-head`, `sync-no-upstream` (`git push -u` once),
  `sync-unmerged`, `body-conflict-marker`, and `validation` for a `-b` naming
  no task.
- **A stranded autostash fails the sync.** When git's re-apply of your other
  dirty files conflicts, it keeps them in the stash and still exits 0; sync
  probes for that and fails with `sync-stash-stranded` (exit 3, nothing
  pushed — `git stash pop`, then re-run), re-reporting the leftover in
  `pending_stash` until it is popped.
- **A concurrent writer is waited out, then retried.** A foreign rebase that
  outlasts the backoff is `sync-busy` (exit 3), a push race lost again is
  `sync-push-rejected`, an interrupted run is `sync-interrupted` — all
  retryable, and `sync-task-status.yml` retries exactly those; a lock still
  held past the budget is the terminal `sync-lock-stale`, naming the lock.
- **A real conflict aborts the rebase** (`sync-conflict`, exit 3): a field
  both sides set differently, a `closed` change on a repeating task,
  `meta.json`, or a shard one side deleted. The board is never left with
  markers and your local sync commit survives; `details.paths` and
  `details.fields` name what needs a decision.

A **successful** sync also prints the repo-scoped `revisit` summary — `dep_done`
and `stale` task ids, the box signals of [`revisit`](#command-notes), and the
repos whose review clock lapsed (`unreviewed`); `--json` gains a `revisit` key,
each list omitted when empty and the whole key when clean — plus a lint error
count by code and the `epic activate` records it published: ride-alongs that
never change sync's exit code.

## Board git hooks (optional)

The design lens: **remote automation is GitHub Actions; local automation is git
hooks.** furrow ships three POSIX-sh hooks in
[`scripts/board-hooks/`](scripts/board-hooks/) that put `furrow lint` at git's
extension points, so a board that goes inconsistent (a dep on an archived task,
an orphaned body, a duplicate shard from a merge) is caught before it reaches
the remote.

| hook | fires after | action | blocking |
|---|---|---|---|
| `post-merge`   | `git merge` / plain `git pull` | `furrow lint` | no (nudge) |
| `post-rewrite` | `git rebase` / `--amend` / `git pull --rebase` | `furrow lint` | no (nudge) |
| `pre-push`     | before a push | `furrow lint` | **yes, on errors** |

Only `pre-push` blocks, only on lint **errors**, and never on `due-overdue` (a
gate fails on what THIS push did; the hook's header says why). `furrow sync`
rebases and pushes internally, so it trips these hooks too, and relays a
blocking hook's stderr verbatim (on stderr, and as `details.stderr`).

Enable them once per machine — git does not turn on hooks at clone time, by
design:

```sh
git config core.hooksPath scripts/hooks   # after placing the hooks there
```

`core.hooksPath` **replaces** `.git/hooks`, so compose any same-name hook you
already keep there rather than replacing it. Each hook skips cleanly without
`furrow` on `PATH` or without a `.furrow/`.

## Standalone: a local board with no remote

For a work machine with no remote you can push to: a board on **one machine, under its own git, never pushed** — no `furrow sync`, no CI. Everything in [The store](#the-store) works identically; two pieces of config make it seamless for you and a coding agent.

1. **Give the board its own git repo, ignored by the code repo** — a workspace dir beside the code, with its own `git init` and no remote:

   ```
   <code-repo>/                     # has its own remote (e.g. github.com/acme/app)
   ├── .git/info/exclude    →  claude_workspace/     # keep the board out of the code repo
   └── claude_workspace/            # its own `git init`, no remote, never pushed
       └── .furrow/
           ├── config.toml          # mode = "standalone", default_repo = "acme/app"
           └── meta.json, tasks/, bodies/
   ```

2. **Register it in your user-level config.** Walking up from the code finds the *code* repo's git, not a board in a subdirectory, so scope it with a [`[[board]]` entry](#user-level-config-no-per-repo-file) — this is a *standalone central board*:

   ```toml
   # ~/.config/furrow/config.toml
   [[board]]
   path       = "/abs/path/to/<code-repo>/claude_workspace/.furrow"
   scopes     = ["/abs/path/to/<code-repo>"]   # `furrow` run anywhere under here uses this board
   repo       = "auto"                          # auto-tag new tasks with the checkout's owner/repo
   autocommit = true                            # commit the board after each change — the backup habit, automated
   ```

Then set **`mode = "standalone"`** (wording only) and **`default_repo = "<owner>/<repo>"`** in the board's `config.toml` ([Configuration](#configuration)): a command run from *inside* `claude_workspace/` reaches the board by local discovery, which carries no scope, so `default_repo` is its fallback ([Discovery precedence](#discovery-precedence)). `autocommit` needs no fallback — it follows the store, not the scope — and with no remote its commits are the only undo.

A board outside the code repo (e.g. `~/furrow-boards/app/.furrow`) works the same; only `path`/`scopes` differ.

### What a standalone board can't use

- **`furrow sync`** refuses on a branch with no upstream (exit 2, kind `sync-no-upstream` — see [Sync failure modes](#sync-failure-modes)); backup here is `autocommit`, not sync.
- **PR→status automation** (`furrow apply`, the `SetStatus-task:` footer, the reusable workflow): the footer points CI at a body-file **URL** a never-pushed board does not have, so status moves are manual.

---

## Configuration

`.furrow/config.toml` is the one human-edited file in the store. Reads are **clamp-don't-reject** — unknown keys are ignored and out-of-range values fall back to a default with a warning `furrow lint` surfaces, so a typo never breaks the tool. The one writer, **`furrow config set <key> <value>`**, is a surgical edit that is strict where reads are lenient; its key grammar, `--user`/`--board` targets and refusals are in `furrow config set --help`.

The full annotated reference is the repo-root [`config.toml`](config.toml) — the
**canonical copy**, byte-for-byte what `furrow init` writes (check.sh and CI
diff the two). The sections: `[lanes]`, `[next]`, `[priority]`, `[ids]`,
`[labels]`, `[archive]`, `[lint]`, `[due]`, `[revisit]`, `[review]`, `[alias]`,
plus the top-level `mode` and `default_repo` (the session write guard's
`[session]` is the **user-level** config's — see
[User-level config](#user-level-config-no-per-repo-file)).

A board `[alias]` names a frequent command string that `furrow <name> <extra args>` expands git-style; a builtin always wins (`furrow lint` warns `alias-shadow`), and global flags go *after* the alias (`furrow triage --json`) — `expandAlias` in `internal/cli/root.go`.

`mode` (`"shared"`, the default, or `"standalone"`) changes **wording only** — see [Two questions, four shapes](#two-questions-four-shapes). `default_repo` is the board's *fallback* scope ([Discovery precedence](#discovery-precedence)). `config.toml` annotates both.

`done` stamps `closed` and leaving the done lane clears it; parked lanes (`icebox`, `waiting`) never stamp it, so they are never archived.

---

## Determinism

Every shard and `meta.json` is written by its kind's one marshaller, with a fixed byte recipe, so furrow's bytes equal a hand-edit, an untouched store is **zero git churn**, `git diff` shows only the field you changed, and an ordinary write never rewrites `meta.json` ([the layout version gate](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it)). The recipe and its guards: [docs/architecture.md](docs/architecture.md#the-single-marshaller-invariant).

---

## Status

- **Working:** furrow is **CLI-only** and everything in this README ships, at
  board layout v11 (what each layout added: `core.SchemaVersion` in
  `internal/core/task.go`). `sh scripts/check.sh` is the full local
  verification.
- **Released:** with GoReleaser, to the Homebrew tap and the nix flake — see
  [Install](#install).
- **Separate front-ends:** ridge (a TUI) and vista (a GUI) drive furrow through
  its CLI/JSON contract
  ([non-goals](docs/non-goals.md#any-ui-is-a-separate-front-end-not-part-of-this-repo),
  which also scopes a future read-only web viewer).

Design notes: [`docs/architecture.md`](docs/architecture.md),
[`docs/glossary.md`](docs/glossary.md),
[`docs/scheduling.md`](docs/scheduling.md) (due dates, launchd recipes) and
what furrow deliberately doesn't do, with rationale, in
[`docs/non-goals.md`](docs/non-goals.md).

---

## License

MIT © akira-toriyama
