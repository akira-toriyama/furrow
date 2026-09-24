# furrow

> An alternative to GitHub Projects / Issues — a clonable, git-native, plain-text task tracker you and your coding agent can both edit cleanly.

**furrow** keeps your tasks as plain text *in a git repo*: structured metadata in one deterministic JSON shard per task, long-form prose in per-task Markdown files. The case against Issues is simple. An issue can't be cloned — plain text can, so the tracker works offline and greps with your code. An agent can *read and write* it with ordinary file and CLI operations, no API client. And because the tracker lives in git next to the work, status never drifts from reality — the same push that changes the code can change the task. Writes are byte-stable, so `git diff` only ever shows what actually changed.

**When to reach for which.** GitHub Issues are the right tool for *intake from anyone* — a public inbox where a stranger can file a bug without write access to your repo. furrow is the opposite tool for the opposite job: *private, in-group* tasks for you and your agent. Its "you must be able to push to create a task" is **access control, not a defect** — the same permission boundary that guards your code guards your backlog.

**Local and instant, not a round-trip.** Much of this GitHub *can* do — but through the API: online-only, rate-limited, a network round-trip per call. furrow does it against plain files on disk: milliseconds, offline, no quota. Backlinks are the concrete example — `show --backlinks` answers "which tasks mention this one?" (the `[[id]]` links in their bodies) by scanning local files, where the GitHub equivalent is an online "mentioned in" panel behind an API call.

**Multi-person, honestly.** furrow is single-operator-first today, and that is the polished path. Several people *can* work one board — it is a git repo, so they clone, push, and `furrow sync` (per-task shards make concurrent edits a clean union). But per-person niceties — an `@mention` and a task **assignee** — are **not built yet**; they are on the roadmap, not a permanent non-goal.

Written in Go (module `github.com/akira-toriyama/furrow`, Go 1.25+). No database, no daemon, no cloud.

> **Status:** furrow is **CLI-only** and shipping — the core domain, the full
> CLI, and `migrate` all work. A TUI/GUI is a separate, planned front-end over
> the CLI/JSON contract, not part of this binary. Feature and release detail in
> [Status](#status); downloads on the [Releases page](https://github.com/akira-toriyama/furrow/releases).

---

## Install

> Releases are cut with GoReleaser and distributed via the Homebrew tap and the nix flake (which carries a real, pinned `vendorHash`); see the [Releases page](https://github.com/akira-toriyama/furrow/releases). Install with any of Homebrew, `go install`, or `nix run`. The release pipeline attaches a GitHub build-provenance attestation to each release artifact — verify a download with `gh attestation verify <file> --repo akira-toriyama/furrow`. Each archive also ships an SPDX SBOM (`<archive>.spdx.sbom.json`, listed in the release assets and `checksums.txt`) with its own signed attestation — verify it with `gh attestation verify <archive> --repo akira-toriyama/furrow --predicate-type https://spdx.dev/Document/v2.3` (the predicate type is derived from the SPDX version, which the release pins to 2.3).

```sh
# Homebrew (tap)
brew install akira-toriyama/tap/furrow

# Go toolchain (from source)
go install github.com/akira-toriyama/furrow/cmd/furrow@latest

# Nix
nix run github:akira-toriyama/furrow
```

A from-source build reports its version as `dev`, with the build commit/date filled in from the Go VCS stamp; the release version is injected at link time (`furrow version --json` shows all of it).

---

## Two questions, four shapes

Setting furrow up is two *independent* choices, not one menu of three. Ask them
in this order and the four shapes name themselves.

- **Mode — does the board have a remote?** A **shared board** is the default:
  it has a git remote, so it is cloned on several machines and written by more
  than one party (your other checkouts, a co-located session, every repo's
  pinned CI caller), kept converged by
  [`furrow sync`](#multi-machine-furrow-sync). A **standalone** board
  (`mode = "standalone"`) lives on one machine, under its own git, with no remote
  you can push to: no sync, no CI — see
  [Standalone](#standalone-a-local-board-with-no-remote).
- **Layout — where does the store sit?** A **central board** lives *outside*
  the repos it backs and is reached by configuration, so one clonable tracker
  repo can back all of them: each task carries the repos it relates to (the
  first-class `repos` field, `owner/repo`) and each checkout is auto-scoped to
  its own — the GitHub-Projects-alternative shape, see
  [Central board](#central-board). A **repo-local board** sits *inside* the
  repo it serves, committed next to the code (`furrow init` and go); the
  Quickstart below runs this way, and everything except the board scoping works
  identically either way.

The two are orthogonal, so the shapes stack mode first: a *shared central
board* (one tracker for a whole fleet), a *shared repo-local board*, a
*standalone central board* (the work-machine recipe below), a *standalone
repo-local board*. The schema and the on-disk bytes are identical in all four.

---

## Quickstart

```sh
# create a .furrow store in the current repo
furrow init

# add a task (id is assigned automatically, frozen, never reused — and random,
# like t-k3m9p, so capture it instead of retyping it; --json prints the created task)
id=$(furrow add "Wire up the config loader" --label core --label config --json | jq -r .id)

# list tasks in canonical lane -> priority -> id order
furrow ls

# move it out of intake once it's ready to pick up (add defaults to inbox)
furrow move "$id" ready

# show what's ready to work (lane in [next].lanes — default ready + in-progress — and all deps done)
furrow next

# open the task's Markdown body in $EDITOR (prints the path when non-interactive)
furrow edit "$id"

# inspect a single task with its body
furrow show "$id"

# mark it done (stamps the closed timestamp)
furrow done "$id"
```

`add` defaults the lane to `lanes.default` (`inbox`) and appends within the lane using the sparse priority step. Pass `--status/-s`, `--priority/-p`, `--label/-l`, `--epic/-e`, `--dep`, `--ref`, or `--body` to set fields up front. On a board whose scope has exactly **one active epic**, a bare `add` (and `add --stdin`) files the capture under it — disclosed in a stderr note, reversed with `set <id> -e ''` — the epic mirror of the board-repo union; zero or several actives inherit nothing (furrow never guesses between focuses), and an explicit `-e ''` stays unfiled on purpose.

### Typed query — `-q`

Every filtering read — `ls`, `next`, `revisit`, `stats`, `search` — takes `-q "<query>"`, a GitHub-Projects-style query folded into one string and compiled by ONE shared evaluator, so it means the same thing everywhere (`brief` is deliberately excluded — a fixed session-orient read). It is a **flat AND-list**: whitespace between terms is AND, a comma inside one value is OR, a leading `-` is NOT, and it ANDs with the other filters (`-s/-l/-r`, `--sort`, …) so a query never widens a scoped board. No cross-field OR, no grouping, no in-query sort — GitHub's own ceiling; `--json | jq` owns the long tail.

The same filters drive the WRITE side: `set`, `done` and `move` take `-q`
(plus `-l`/`-r`) as a **selector** instead of an id list — one all-or-nothing
write, no `ls --json | jq | xargs`. A selection refuses to combine with ids,
**previews until `--yes`** (`{dry_run: true, tasks}`), and matches-nothing is
exit 0 with a stderr note; the board scope applies exactly as in `ls`, so
`furrow ls <flags>` previews precisely what `furrow done <flags>` would close.

```sh
furrow ls -q 'is:actionable label:cli,dx -status:icebox'   # workable now, (cli OR dx), not iced
furrow next -q 'value:>=4 -label:chore'                     # ready AND worth it
furrow ls -q 'is:open updated:<-30d'                        # open but untouched for a month
furrow ls -q 'closed:2026-07-01..2026-07-15'                # closed inside a window
furrow ls -q 'depends-on:t-k3m9p is:blocked'                # what waits on t-k3m9p, still stuck
furrow stats -q is:stale                                    # the stale board's shape
furrow ls -q 'roi:>2 "typed query"'                         # ROI>2 with a text phrase
```

- **qualifiers** (`field:value`, comma = OR, repeat = AND): `status`/`lane`, `epic`, `label` (a `*` is a wildcard — `label:area/*`), `repo` (resolved as `-r` does), `id` (prefix), `title`, `body`, and the graph fields `depends-on`/`blocks`/`descendant-of`/`ancestor-of`. A bad query is **exit 2**, never a silent empty result: an unknown qualifier or `is:` flag is kind `query-unknown-field`/`query-unknown-flag` with `candidates`, an operator on a non-ordered field is `query-type` with `allowed_operators`, and every fault carries the offending `term` and its byte `offset` in `details`.
- **ordinal** (`value`, `effort`, `priority`, `roi`): comparison `>`, `>=`, `<`, `<=` and range `2..4` / `*..3` / `3..*`. An unset estimate (and an undefined `roi`) never satisfies a comparison.
- **dates** (`created`, `updated`, `closed`, `reviewed`, `due`): absolute `YYYY-MM-DD` or RFC3339, plus signed **relative offsets** from now (`updated:>=-2w`; units `m/h/d/w`, `m` = minutes; an offset needs a comparison or range). Ranges are inclusive with `*` open ends; a `null` stamp satisfies no comparison (existence is `has:`/`no:`'s job). A bare day on the machine stamps is a **UTC** day, on **`due`** the **board's calendar day** (`[due].timezone`) — the one date a human types, so `-q due:2026-08-04` finds what `--due 2026-08-04` wrote.
- **graph** (exact ids; an unknown id just matches nothing): `epic:X` selects a box's members (lenient on an unknown id — a box that does not exist simply has no members, and `lint` owns reporting the dangling reference; the STRICT spelling is the `-e` flag, which resolves and fails with candidates); `depends-on:X` and `blocks:X` are the two directions of the dep edge (the tasks waiting on X, and the tasks X waits on); `descendant-of:X` and `ancestor-of:X` are their **transitive** twins over the deps DAG — everything that transitively waits on X, and everything X transitively waits on (start ids excluded from their own closure, cycles terminate). Epics do not nest, so there is no hierarchy walk to spell — box membership is `epic:`.
- **presence**: `has:FIELD` / `no:FIELD` over `label`, `repo` (`no:repo` = a draft), `epic` (`no:epic` = unfiled, the `epic-required` lint state), `value`, `effort`, `deps`, `refs`, `checklist`, `closed`, `reviewed`, `due`, `repeat` (`has:repeat` = the live occurrence of a series — the rule sits on exactly one task at a time), `body` (non-whitespace content — note `add` seeds every body with a heading).
- **computed flags** (furrow's own, no GitHub equivalent): `is:actionable` (a next lane with every dep done — a strict SUPERSET of what `furrow next` hands you, since next also scopes to the active epic), `is:blocked`, `is:stale` (no update within `[revisit].stale_days` — revisit's own definition, a pure age test that does not imply open), `is:unfiled` (no epic), `is:overdue` (a promised instant that has passed — `lint`'s `due-overdue` as a filter, minus every lane exemption, the done lane included: `-q` returns what you asked for, and which lanes stay QUIET is a lint/brief policy, not a fact about the date), `is:open`/`is:closed`/`is:draft`.
- **free text**: a bare word or `"quoted phrase"` is a case-insensitive substring over **title + body** — `furrow search`'s matcher, so `ls -q foo` finds what `search foo` finds; bodies are loaded only by the terms that read them. On `title:` a quoted value means whole-field equality; `body:` stays a substring match.

Deliberately out, permanently: cross-field OR, grouping, and in-query sort — GitHub's own ceiling, and the long tail is `--json | jq`'s job.

---

## The store

furrow uses a **hybrid** layout: one machine-written JSON shard per task for structured metadata, and one hand-editable Markdown file per task for prose. A pure JSON or JSONL store would collapse long bodies into one escaped line — every prose edit would churn the whole file and an agent could easily corrupt the escaping. Splitting prose into `bodies/<id>.md` keeps both halves diffable. Sharding the metadata one file per task means two operators adding or editing tasks on separate worktrees/PRs touch distinct files, so a git merge is a conflict-free union instead of a fight over one sorted array.

```text
.furrow/
├── config.toml          # human config (furrow only READS this; never rewrites it)
├── meta.json            # board-wide layout version {"schema_version": 10} — written ONLY by furrow, raised ONLY by `furrow upgrade`
├── tasks/
│   ├── t-0001.json      # one metadata shard per task — written ONLY by the single core.MarshalTask path
│   └── t-0002.json
├── repos/               # one review shard per repo — furrow review <repo> (last_reviewed clock)
│   └── akira-toriyama__furrow.json
├── epics/               # one shard per epic (box of work) — goal/active/meta + deps (the epics it waits on)
│   └── e-k3m9.json
├── bodies/
│   ├── t-0001.md        # long-form prose for t-0001 (hand/agent editable)
│   └── t-0002.md
└── archive/             # aged done tasks (its own tasks/, meta.json + bodies/)
    ├── meta.json
    ├── tasks/
    └── bodies/
```

A minimal `tasks/t-0001.json` shard:

```json
{
  "id": "t-0001",
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
  "body": "bodies/t-0001.md",
  "due": "2026-08-04T14:59:59Z"
}
```

The board-wide layout version lives on its own in `meta.json` (never inside a shard, so a version bump touches one file and no shard becomes a merge point):

```json
{
  "schema_version": 10
}
```

### The layout version gates writes (and only `furrow upgrade` raises it)

That number is the **board's** — not the binary's — and it is an **input** to every write, never an output. The gate has two sides, and the exit code alone says which to fix:

- **The board is newer than your furrow** → refused: `schema-too-new`, exit 3. Update the binary (in CI: bump the `sync-task-status.yml@vX.Y.Z` pin).
- **The board is older than your furrow** → fully **readable** but **read-only**: a write fails with `schema-upgrade-required`, exit 2. The board is the stale side, and an explicit command fixes it. The state is not silent until then: the orient and listing reads (`brief`, `sync`, `ls`, `show`, `next`, `revisit`, `stats`, `search`) each print one stderr note — `note: this board is READ-ONLY for this binary (board layout vN, binary vM) …` — so a session learns at `furrow brief`, not at its first failed write (`board`/`doctor` stay quiet there: reporting the mismatch is their output).

Both carry `"details": {"board_schema": N, "binary_schema": M}`. An ordinary command **never** migrates a board as a side effect — `meta.json` is stamped only when a genuinely empty store is created (`furrow init`). `furrow upgrade` is the one deliberate raiser, and it is a **flag day**: once it lands, no older furrow can write that board — including any CI pinned to an older release. furrow cannot see those pins, so you keep the order:

```sh
furrow board                # schema:   vN (board) / vM (binary) — READ-ONLY: run `furrow upgrade`
# 1. release a furrow that ships the new layout
# 2. bump every caller's sync-task-status.yml@vX.Y.Z pin to it
furrow upgrade              # 3. preview: which stores change, and how many shards
furrow upgrade --yes && furrow sync
```

On a **standalone board** (`mode = "standalone"`, see [Standalone](#standalone-a-local-board-with-no-remote)) there is no fleet to coordinate, so `furrow upgrade` skips the flag-day checklist and the `furrow sync` step. The gate itself is unchanged; only the guidance differs.

`furrow board` reports the schema triple (`schema_version`, `binary_schema_version`, `schema_state`, `writable`) plus the board repo's local `git` state and **never fails on a mismatch** — it is the command that still answers when board and binary disagree, which is why the task-status workflow pre-flights it. `furrow lint` warns `schema-outdated` without erroring. The 2026-07-13 outage the gate answers is recorded in [docs/non-goals.md](docs/non-goals.md).

### A key furrow doesn't know is preserved, not dropped

The gate above only fires when someone **bumps** the version. A field added *without* a bump would leave `meta.json` still saying vN, so no gate fires and an older binary's lenient parse would drop the unknown key and write the loss back — one ordinary write, one destroyed field, no error. So furrow **parks every top-level key it does not recognise and re-emits it** (sorted, after the known ones) in all three machine-written files. Stated as a pair: *the gate stops a bumped layout from being misread; the passthrough stops an unbumped one from being destroyed.*

Its limits are part of the rule: **not retroactive** (releases ≤ `v0.9.0` still destroy unknown keys), **top-level only** (a key inside a `checklist` item is still dropped), and **preserved is not honoured** (an old binary carries a future `"blocked": true` and still hands you that task in `next` — `furrow lint` warns **`unknown-shard-key`**). A hand-edit typo therefore stays until the operator prunes it with `furrow tidy --unknown-keys`. The mechanism is in [docs/architecture.md](docs/architecture.md).

Notes on the fields: `id` is frozen and is the stem of both files; `priority` is a sparse 10-step integer so a reorder edits one field; `status` is a lane from `config.toml`; `repos` is the first-class set of repositories (`owner/repo`, 0..N — empty = a **draft**; a repo is *not* a label); `closed` is `null` while open; empty collections are `[]`, never `null`; `value`/`effort` are optional 1..5 estimates (clamped, omitted while unset); `due` is an **instant**, not a day (`--due 2026-08-04` binds the end of that day in the board's `[due].timezone`, so the 4th is not overdue at 00:01). The JSON Schemas are under [`docs/schema/`](docs/schema/), emitted by `furrow schema [task|meta|repo|epic]`; every term is defined in [docs/glossary.md](docs/glossary.md).

`value` and `effort` exist so an agent (or you) can pick the next task from recorded data instead of re-guessing each time. **ROI = value / effort is derived, never stored** (so editing either estimate always yields a current ROI, with no stale number to reconcile), and `next` is deliberately unchanged — sorting by ROI is the caller's choice:

```sh
# highest value-per-effort first, among tasks that carry both estimates
furrow ls --json | jq 'map(select(.value and .effort)) | sort_by(-(.value / .effort))'
```

`furrow revisit` is the agent-facing companion: a **read-only** query that surfaces the open tasks whose metadata may be out of date — missing `value`/`effort`, gone stale (no update within `[revisit].stale_days`), or carrying a dependency that is already done. Each task comes back with a `revisit` array of `{code, detail}` so the agent knows exactly what to fix with the existing setters (`value`/`effort`/`dep`); it never mutates anything itself.

```sh
# tasks in this repo that still need estimates, with the reasons
furrow revisit -r furrow --json | jq '.[] | {id, revisit: [.revisit[].code]}'
```

### Attaching images and media

A task body is plain Markdown, so you can attach a screenshot or diagram by committing the file alongside the bodies and linking it with a **relative path**:

```markdown
![repro](assets/t-0001-bug.png)
```

`furrow attach <id> <file>` does exactly that (copies into `bodies/assets/` as `<id>-<name>` and links it). It renders wherever Markdown does, not in the terminal. Keep screenshots small and scrub secrets (git history is permanent); track large media with **Git LFS** before the first commit; `furrow lint` warns on a missing, orphan or ≥5 MiB asset.

---

## Command reference

All commands below are implemented and working today. furrow is CLI-only; a TUI/GUI is a separate, planned front-end that drives it through the CLI/JSON contract (see [Status](#status)).

The table is **generated from the binary**: the cobra tree's `Use`/`Short`/aliases/flags are the single source of truth — `scripts/gen-command-table.sh` splices it in, and check.sh/CI fail when the block and the binary disagree — so a command or flag can no longer ship without appearing here (hand-kept lists kept losing commands; the audit found four missing). `furrow <cmd> --help` says the same one-liners; the [command notes](#command-notes) below carry the behavior contracts a one-liner can't.

<!-- commands:begin — generated by scripts/gen-command-table.sh from internal/cli (Use/Short/flags). Edit those, rerun the script, commit both. Hand edits inside this block are overwritten. -->
| Command | What it does | Flags |
|---|---|---|
| `init [dir]` | Create a .furrow store (at FURROW_DIR/FURROW_BOARD if set, else in dir or the current directory) | — |
| `add <title>...` | Add a task (or many with --stdin / --batch) | `--batch`, `--body`, `--check`, `--dep`, `--draft`, `--due`, `--effort`, `-e/--epic`, `-l/--label`, `-p/--priority`, `--ref`, `--repeat`, `-r/--repo`, `-s/--status`, `--stdin`, `--value` |
| `ls [<epic>]` (alias `list`) | List tasks (canonical lane->priority->id order), or group them by epic with --tree | `--actionable`, `--archived`, `--blocked`, `--drafts`, `-e/--epic`, `-l/--label`, `-n/--limit`, `-q/--query`, `-r/--repo`, `--reverse`, `--since`, `--sort`, `-s/--status`, `--tree`, `--until` |
| `show <id>...` | Show tasks or epics with metadata and markdown body (batch-friendly) | `--archived`, `--backlinks`, `--no-body` |
| `next` | Show actionable tasks (in the next-lanes, all deps done, in the active epic) | `--all-epics`, `-e/--epic`, `-l/--label`, `--lanes`, `-n/--limit`, `-q/--query`, `-r/--repo` |
| `brief` | One-shot session-orient read: due band, active/pinned epics, next picks with bodies, blocked, revisit, drafts | `-l/--label`, `-n/--limit`, `-r/--repo`, `--stale-days` |
| `revisit` | List open tasks needing re-evaluation (agent re-weighing signal) | `-e/--epic`, `-l/--label`, `-n/--limit`, `-q/--query`, `-r/--repo`, `--stale-days` |
| `search <term>` | Full-text search over task titles and bodies | `--archived`, `-e/--epic`, `-l/--label`, `-n/--limit`, `-q/--query`, `-r/--repo`, `-s/--status` |
| `stats` | Summarize the board: counts by lane, repo, and label | `-e/--epic`, `-l/--label`, `-q/--query`, `-r/--repo`, `--since`, `-s/--status`, `--until` |
| `board` | Print the active board: store path, mode/layout, scope, lane vocabulary, and schema state | — |
| `boards` | List the configured boards (user-level config), independent of cwd | — |
| `doctor [dir...]` | Diagnose this machine's board setup: config, boards, scopes, git freshness | — |
| `edit <id>` | Edit a task's or epic's markdown body in $EDITOR, or replace it with --body | `--body`, `--expect-updated` |
| `note <id> <text>` | Append a paragraph to a task's or epic's body and advance its updated time | `--expect-updated` |
| `attach <id> <file>` | Attach a media file to a task (copies into bodies/assets/, links it from the body) | — |
| `done [<id>...]` | Move tasks into the done lane (stamps closed) | `--expect-updated`, `-l/--label`, `--note`, `-q/--query`, `-r/--repo`, `--yes` |
| `move [<id>...] <lane>` | Move tasks to a lane | `--expect-updated`, `-l/--label`, `-q/--query`, `-r/--repo`, `--yes` |
| `reorder <id> [<priority>]` | Set a task's priority — absolute, or relative with --before/--after | `--after`, `--before`, `--expect-updated` |
| `retitle <id> <title...>` | Rename a task (updates the shard title and the body heading) | `--expect-updated` |
| `set [<id>...]` | Apply several triage edits at once (lane, priority, value, effort, labels, repos, epic, due, repeat) | `--add-label`, `--add-repo`, `--after`, `--before`, `--clear-due`, `--clear-effort`, `--clear-repeat`, `--clear-value`, `--due`, `--effort`, `-e/--epic`, `--expect-updated`, `-l/--label`, `-p/--priority`, `-q/--query`, `--repeat`, `-r/--repo`, `--rm-label`, `--rm-repo`, `-s/--status`, `--value`, `--yes` |
| `value <id> [<1-5>]` | Set a task's value estimate (coarse 1..5), or clear it with --clear | `--clear`, `--expect-updated` |
| `effort <id> [<1-5>]` | Set a task's effort estimate (coarse 1..5), or clear it with --clear | `--clear`, `--expect-updated` |
| `check <id> [item-index]` | Toggle, add, remove, or reword a checklist item | `--add`, `--expect-updated`, `--off`, `--reword`, `--rm` |
| `dep <id> [<dep-id>...]` | Add/remove a task's dependencies, or list them both ways with --list | `--expect-updated`, `--list`, `--rm` |
| `epic add <title>` | Create an epic (never active — open it with `epic activate`) | `--body`, `--goal`, `-l/--label`, `--meta`, `-r/--repo` |
| `epic ls` (alias `list`) | List epics (open only by default), active first | `--all`, `-l/--label`, `-n/--limit`, `-r/--repo` |
| `epic show <epic>` | Show one epic: goal, meta, progress, member tasks, and its body | `--no-body` |
| `epic set <epic>` | Edit an epic's title, goal, meta, labels, repos, or its standing/pinned declarations | `--add-label`, `--add-repo`, `--goal`, `--meta`, `--pinned`, `--rm-label`, `--rm-meta`, `--rm-repo`, `--standing`, `--title` |
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
| `config set <key> <value>` | Set one config key — the board's config.toml, or --user for a [[board]] entry | `--board`, `--user` |
| `schema [task\|meta\|repo\|epic]` | Print the JSON Schema for a task shard, meta.json, a repo review shard, or an epic shard | — |
| `version` | Print the furrow version (with build commit/date when stamped) | — |
<!-- commands:end -->

On the read commands, `-r/--repo` is the scope control: a full `owner/repo` or a short name resolving to exactly one (ambiguity is exit 2 with `candidates`); an explicit `-r` overrides the board scope, `-r ''` shows the whole board. `-l/--label` is a pure tag filter that ANDs with the scope. Within `-s` or `-l` a comma is OR and repeats union; the flags AND across fields. An unknown `-s` lane **exits 2 with the configured lanes in `candidates`** (a closed vocabulary), an unknown `-l` tag matches nothing — unless it uniquely names a repo, which is exit 2 pointing at `-r` (the did-you-mean guard, on every filtering read). **A read never narrows or truncates silently**: a scope that hides drafts or boxes prints one stderr hint, and a `-n` cap that bites prints `note: showing N of M (-n)`; the JSON stays a bare array. `furrow board` shows the lanes and the active scope without provoking an error.

Global flags: `--json` and `--ndjson` are honored **wherever furrow emits JSON** — `--ndjson` is the same payload, compact, one value per line. A command whose `Use` says `<id>...` (`show`, `done`, `move`, `set`) is **always an array**; a one-id mutation emits `{before, after, changed}` (a relative `reorder` adds `renumbered`); the report-shaped commands (`rm`, `archive`, `tidy`, `upgrade`, `apply`) emit one object. **A write that changes nothing leaves `updated` alone** — an idempotent retry resets no staleness clock and writes no shard; the prose writes (`note`, `done --note`, a body replacement) always advance it. `edit` prefers `$FURROW_EDITOR`, then `$VISUAL`, then `$EDITOR`, falling back to `vi`.

### Command notes

The generated table is the machine-guaranteed surface; these are the behavior contracts that don't fit a one-liner. (Commands whose whole story fits their table row — `init`, `retitle`, `label`, `schema`, `version` — have no entry, and `attach`, `sync`, `upgrade`, and `config` have their own sections: [attachments](#attaching-images-and-media), [multi-machine sync](#multi-machine-furrow-sync), [the layout gate](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it), [the central board](#central-board).)

- **`add`** — one task per stdin line with `--stdin`; **`--batch <file|->`** reads NDJSON with per-task fields and a batch-local `key`, so a dep may cite another line's key and a `[[key]]` in a title, body, or checklist item becomes `[[id]]` once ids are minted — an epic with a dependency graph is one file, written in any order, and `--json` echoes each task's key beside it (shards never carry keys; an unknown field, a bad reference, or an in-batch cycle is exit 2 and writes nothing); `--check` seeds checklist items; an out-of-range `--value`/`--effort` clamps to 1..5 with a note; a title starting with `-` needs `--`; `-e/--epic` files it under a box (id, unique prefix or unique title substring — a miss is exit 2 with `candidates`; unfiled is legal at add time and a `lint` error while open). `--due` promises the task for a date (`2026-08-04` = that whole day, `2026-08-04T10:30`, an RFC3339 instant, or `+1d`). **`--repeat` makes it recur** (board layout v10): a short spelling — `daily`, `every <n> days`, `weekly`, `weekly on <days>`, `every <n> weeks on <days>`, `monthly`, `monthly on <day-of-month>`, `monthly on last`, `monthly on <nth> <weekday>`, `monthly on last <weekday>`, `every <n> months on <day-of-month>`, `yearly`, `every <n> years` — optionally ending in `until <date>` or `for <n> times`, or a raw RRULE line (never with a `DTSTART`: the series starts at the `--due` it requires, and that first date is the immovable anchor). An anchor off the rule's lattice is one live occurrence outside the series, a day past 28 skips the months that lack it and February 29 the years that lack one; each says so at bind time. `furrow add --help` is the contract; only a **close** advances a series (`done`).
- **`ls`** — canonical `lane -> priority -> id` order; `--drafts` shows only repo-less tasks (bypasses the board scope); `--since`/`--until` window by `updated`; `--sort updated|created|value|effort` (`--reverse` flips; with a sort, `-n` is the top N); `--archived` reads the archive store. Every flat row carries a state glyph — ★ actionable, ✓ done, ~ parked, · open but not available — and `--json` adds `actionable`/`blocked_by`; `--actionable`/`--blocked` filter on it, `-e <epic>` on box membership (strict; the unfiled pile is `-q no:epic`). **`--tree`** groups the same rows by epic (active first, open by id, closed, then unfiled — or one box with an `<epic>` argument), built over what matched so it never shows fewer tasks; `-n` caps groups; each group carries `progress` (over the full board) and `stuck`. A dated row carries `due …`/`overdue …` in its title cell and a repeating one a bare `repeats` beside it — on every human view that renders a task as a task, because closing that row writes another task (`search`'s snippet column is the one exception).
- **`show`** — any number of ids in one read, in input order: `--json` is **always an array** (a single id is a one-element array, a total miss `[]`); `--ndjson` is one entity per line. `--no-body` omits `body_text`. A partial miss still prints the found ones and exits 1 with `details.missing` (`details.archived` when the id is retired). `--backlinks` adds the tasks whose body mentions each one (`mentioned_by`). An id may name an **epic**: store membership routes it, never the prefix, and the box renders as `epic show` does; entries dedupe by the resolved entity.
- **`next`** — actionable = lane in `[next].lanes` (default `ready` + `in-progress`) **and** every dep done. On a board with epics the result is also scoped: a **pinned** box's actionable tasks lead, then the active box's, then the unfiled pile; with nothing active the result is deliberately empty apart from the pinned band (exit 0, a stderr hint). The scope never narrows silently: actionable tasks it leaves out (other boxes' members) are counted on one stderr line naming `--all-epics` as the escape. `-e <epic>` reads one box, `--all-epics` ignores the scope, `--lanes <csv>` widens the lanes for this call. `--json` attaches a `reason` per task. An arrived due date is a stderr note, never a row; a repeating row is tagged `repeats`.
- **`brief`** — the session-orient read. It **leads with `due`** (`{overdue, today}`, longest-overdue first): a date is the only thing on the board that expires, so **no automatic scope narrows it** — not the epic focus, not the board's cwd-derived repo scope (that one once hid a task due today while `lint` counted it); a typed `-r`/`-l` still applies, and on a board with boxes the human header says `due (N, every epic):` because the next band below is scoped. Then the **epic header** (`active`, `pinned` channels with an open member, a `pinned_quiet` count, `epics_declared`), `next`'s top `-n` picks (default 3) **with bodies** plus `next_total` and `next_hidden` (what the cap dropped, per lane — canonical order lists `in-progress` after `ready`, so the cap drops the work already in flight first, and the lane name is what stops a session from opening a second task beside it), `blocked` (next-lane tasks with an unsatisfied dep and their `blocked_by`) plus `blocked_total`, the same count over every open lane so the band's lane filter never reads as an unblocked board, the `revisit` summary, the `drafts` count, and `sync`'s lint error ride-along (omitted when clean). Human mode is a compact dashboard without bodies. Read-only and git-free: `furrow sync && furrow brief`.
- **`revisit`** — read-only; `--json` attaches a `revisit` array of `{code, detail}` (`no_repo`, `value_unset`, `effort_unset`, `stale`, `dep_done`) so an agent knows what to fix. The box-level signals — `epic_all_done` / `epic_stuck` / `epic_stale` / `epic_dep_done` / `epic_review_due` — ride `sync`/`brief`'s summary keyed by epic id. Drafts surface regardless of scope; `--stale-days 0` disables the stale signal.
- **`search`** — case-insensitive substring over every title **and** body, in canonical order (several words are one phrase); the same `-s/-l/-r/-n` scope and `-q` as `ls`. `--archived` searches the archive store instead, reading its own bodies. Each hit reports `matched_field` and a `snippet`.
- **`stats`** — `total`, `drafts` (board-wide, like `brief`'s), and `by_lane` (every configured lane), `by_repo`, `by_label`. `stats -r ''` describes the whole board. `--since`/`--until` window by `updated` like `ls` **and** add a `window` section with the ids `created` and `closed` inside the bounds (archive store unioned) — the machine side of a session's `created ≤ closed − 1` budget check.
- **`board`** — the introspection snapshot: store path, discovery `source`, mode/layout, repo scope, lane vocabulary, and the schema triple (`schema_version`, `binary_schema_version`, `schema_state`, `writable`). It **never fails on a version mismatch — it reports one**.
- **`boards`** — every `[[board]]` in the user-level config, in file order, **without resolving against cwd** (exit 0, possibly empty — the diagnosis for a machine whose scopes were never configured). `{config, boards: []}`, each entry with its resolved `store`/`scopes`, declared `repo`/`label`, `exists`, and `board`'s vocabulary/schema keys.
- **`doctor`** — the machine-wide board-setup **health check**, read-only and network-free: the user config parses and a usable `[[board]]` exists (`no-boards`), every board is on disk, readable and on this binary's schema, every scope exists, a git-backed board's freshness as of the last fetch (`board-behind`/`board-ahead`), where a nearer `.furrow`/pointer **shadows** a board inside its own scopes (`scope-shadowed`, info), and whether the session-guard registry still parses (`session-registry-unreadable`). Discovery is simulated at cwd and asserted at each argument dir (`dir-unresolved`). Stable `code`s; exit 1 = problems found (`doctor-unhealthy`): `furrow doctor --json | jq -e '.healthy'`.
- **`edit`** — opens `bodies/<id>.md` in the editor; with no TTY it prints the path. `--body "<markdown>"` (or `-` for stdin) **replaces the whole body and advances `updated`** in one write — an empty replacement is exit 2, and `--json` carries `replaced_bytes`. `<id>` may name a task or an epic. Prefer `note` to *append*.
- **`note`** — appends the text as a new paragraph **and** advances `updated` in one write (`-` reads stdin); `--json` adds `appended`. `<id>` may name a task or an epic — the two share `bodies/`, and **membership routes it, never the id's prefix**; an unknown `e-` id is exit 2 with `candidates`, an unknown task id exit 1.
- **`done`** — **this is what advances a recurring task**: the next occurrence is written in the same all-or-nothing write and the rule handed to it, so exactly one live task carries a series and re-closing mints nothing (`lint` errors `repeat-on-closed` on the hand-edited shape). The successor lands in the default lane with body and checklist copied under a `previous: [[id]]` line, inheriting everything but what the close settled, the computed `due`, this run's deps and its position — the `epic` included, even a closed one (`epic done` discloses that; `set <live-id> -e <open-epic>` re-files the series). Its due is the first occurrence after what the close **settles** (the whole board-calendar day for a bare-date series, the instant for a timed one), so a late close jumps the lapsed cycles and reports them: `t-k3m9p  repeat: next due … (t-p4q2r) — 2 occurrence(s) skipped`, or `series complete`; `--json` carries `repeat` `{created, due, skipped, completed}`. The calendar is `[due].timezone` — declare it on a shared board (`lint`: `repeat-no-timezone`). `furrow done --help` is the contract. `--note "<text>"` folds the closing word into every closed task's body (`note`'s contract; an empty note is exit 2).
- **`--expect-updated <rfc3339>`** (every task mutator, and `note`'s epic side) — the **stale-read guard**: pass the `updated` stamp your last read emitted; when someone else wrote in between, the mutation still goes through but says so (a stderr note plus `stale_read {expected, actual}` in the envelope) — a warning, not an optimistic-lock refusal, since the co-writer usually acted on newer knowledge. One stamp describes one read of one task, so the batch mutators refuse it beside several ids.
- **The session write guard** (every task and epic write, under Claude Code only) — a write that touches a repo an **earlier-started** Claude Code session on this machine sits in is **refused** while that session is working (exit 2, kind `session-busy`, every clash in `details.clashes`) and goes **through with a warning** once it is idle (one stderr line plus `session_warn {clashes}` in the envelope). Idle is read off the occupant's transcript: its turn has ended, or it has been silent past `[session].busy_seconds` (default 300; `0` = warn only). **First come, first served** — the earlier session's own writes never clash. The escape is `add --draft` (`details.hint` names it), never a force flag; a human shell and CI pass untouched. Best-effort: when the registry cannot be read or this session is not in it, the guard stands down and says so (`furrow doctor`: `session-registry-unreadable`). Reads and board maintenance (`archive`, `tidy`, `upgrade`, `review`, `sync`) are not guarded. Mechanism: [docs/architecture.md](docs/architecture.md).
- **`move`** — clears `closed` when a task leaves the done lane (and `done` stamps it).
- **`reorder`** — the absolute form sets the sparse integer directly; `--before`/`--after <id>` compute it instead, slotting the task immediately next to a lane-mate (both tasks must share a lane — relative order across lanes is meaningless, so a cross-lane target is exit 2). When the sparse gap next to the target is exhausted, the whole lane is respaced **in the same single write** (all-or-nothing): `--json` adds a `renumbered` array of the neighbors' `{id, from, to}` moves and a stderr note names the count. The respaced neighbors' `updated` stamps deliberately do **not** advance — a respace is positional bookkeeping, not progress, so staleness signals stay honest.
- **`value` / `effort`** — an out-of-range score clamps to 1..5 **and is signaled**: a `clamped` key nested by field (`clamped.value.{requested, stored}` / `clamped.effort.{…}`) in the `--json` envelope plus a stderr note, so an explicit arg is never silently rounded. Via `add`, the clamp is stderr-only (`add --json` prints the created task, no envelope). `--clear` unsets.
- **`set`** — the routine triage edits (lane, priority, value, effort, labels, repos, epic, due, repeat) in **one** write. `--due` sets or re-dates the promise and **is the snooze** (`--due +1d`, from now); on a repeating task it moves this occurrence only — the anchor never moves — and `--clear-due` there is exit 2; `--repeat`/`--clear-repeat` bind or drop the rule. An empty `--due ''` is exit 2, never a silent clear. Several ids apply the same edits in one all-or-nothing write (`--json` is always an envelope array); the position flags (`--priority`/`--before`/`--after`, mutually exclusive) place ONE task, `-s <lane> --before <id>` being the cross-column drop with `reorder`'s respace contract. An unknown lane or `-e` is exit 2 with `candidates`; under `[labels].required` stripping the last label is refused.
- **`check`** — indexes are zero-based; marking done is an idempotent set, not a toggle (`--off` unchecks); `--add` appends verbatim (repeatable); `--rm` deletes at an index; `--reword` replaces its text. Mode flags are mutually exclusive; an out-of-range index is exit 2.
- **`dep`** — variadic add/remove in a single all-or-nothing write (a bad dep-id aborts without partial change); acyclic and idempotent. `--list` reads (never mutates) the dependency neighborhood **both ways** — `depends_on` and `blocks` (what unblocks if I finish this) — resolved to id+title+lane; a dangling dep resolves to its id alone (lint flags it).
- **`epic`** — the box's subcommands: `add` (never active at birth), `ls` (active first; `--all` includes closed; the board's repo scope applies as on the task reads, with a stderr note for what it hid), `show` (goal, meta, members, and the **waiting** state — every non-terminal member done and a parked member with a due still ahead prints `⏳ waiting until <due> (<task>)`, `waiting: {until, task}` in `--json`, and silences `epic_all_done`), `set` (title/goal/meta/labels/repos, and the **`--standing`**/**`--pinned`** channel declarations — a standing box is exempt from the lifecycle nags and carries the review cadence, a pinned box's tasks lead `next`/`brief`; mandate = both, parking lot = standing), `activate` (at most one active box per repo; `--reason` is logged in the body), `deactivate`, `done` (closes and clears `active`; never picks a successor but **discloses open members** — `open_members` `{id, title, status, repeat}`, a repeating member called out since its `epic-closed` warn returns every cycle — and **suggests the previous active box** computed from the activation log), `reopen`, `rm` (see `rm`), and `dep` (a box WAITS ON others — information, not enforcement: `activate` warns and proceeds, `revisit` raises `epic_dep_done`, `lint` backstops with `epic-dep-cycle`/`epic-dep-missing`/`epic-dep-open`). Each subcommand's `--help` is its contract; a task's membership is `add -e` / `set -e` (`-e ''` unfiles).
- **`repo`** — each value must be a full `owner/repo` or a short name uniquely resolving against the board's repos (else exit 2 with `candidates`); a task with no repos is a draft.
- **`review`** — an id naming a task stamps its `reviewed` (apart from `updated`); a repo records the per-repo review clock; an epic ref stamps that box's `reviewed` — the reset for `epic_review_due`. `--by agent` logs a sweep without advancing the human clock.
- **`apply`** — parses `SetStatus-task: <body-link> [<lane>]` directives from PR/commit text (stdin or `--body-file`) — the CI hook behind [auto status updates](#ci-auto-update-a-tracker-from-prs). `--on open` nudges to in-progress; `--on merge` applies the lane. Validation is non-blocking. `--dry-run` runs the same parse and validation and reports what each directive would do without writing — the local footer check before `gh pr create` (same exit code; `--json` adds `dry_run: true`).
- **`archive`** — with ids it retires exactly those (done lane only); with none it sweeps aged done tasks under the board scope (`-r` swaps it, `-r ''` sweeps the whole board, `--older-than` adjusts the age). Assets follow the hold rule: a store loses a file only when nothing remaining there holds it, reported as `assets {copied, deleted, kept}`. Previews unless `--yes`; a round trip via `unarchive`.
- **`unarchive`** — restores archived tasks to the hot board, all-or-nothing, exactly as archived (done lane, `closed`, body, assets) — reopening stays `move`'s job. Every mutator's not-found error names an archived id (`details.archived`).
- **`rm`** — deletes tasks outright: shard, body, and the assets nothing else still shows (git history is the only way back). The **withdrawal of a filing**, not a retirement (`archive` folds done work; parking is the icebox lane). Previews unless `--yes`; all-or-nothing. A target something still points at — a dep edge, a live `[[id]]` link, a box's members or the epics whose deps name it — is **refused** (exit 2, kind `referenced`, `details.references`); `--force` severs instead, without advancing anyone's `updated`. Removing the live occurrence of a **repeat** series ends the series — disclosed on the preview and the apply, never refused. `--json` is one report `{dry_run, force, tasks, references}`; **`epic rm`** is the box twin.
- **`tidy`** — prunes dead bookkeeping, per-run opt-in: `--done-deps` (dep edges from open tasks to done-lane tasks) and `--unknown-keys` (the parked unknown keys across shards and `meta.json` — the operator's tool, since hand-editing a shard is off the table). A bare `tidy` previews both; `--yes` requires naming what to apply; the apply never advances `updated`.
- **`lint`** — every finding carries a stable kebab-case `code` (`dangling-link`, `dep-cycle`, `epic-required`, `conflict-marker`, `unknown-shard-key`, `schema-outdated`, `archive-backlog`, …; the hidden `furrow vocab` prints the whole set); branch on the code, not the message. Errors are the states the board lies in: a dep cycle, a task in an actionable lane with an unsatisfied dep (`ready-blocked`), a promised instant that has passed (`due-overdue` — board-wide, no epic scope), an open task with no epic once the board has any (`epic-required`), two active epics on one repo (`epic-multi-active`), a done task with no `closed` or still carrying a repeat rule (`repeat-on-closed`), a body with git conflict markers (`conflict-marker`). Warns are the nudges: `due-today`, `updated-in-future`, `priority-duplicate`, `epic-closed`, `epic-no-active`, dangling links, reconcile gaps, asset hygiene, a board behind the binary, unknown shard keys, `blank-entry`, `repeat-orphan-anchor`, `repo-as-label`, `done-draft`, config clamps, the `[lint].archive_done` nudge, and the opt-in `provenance-missing` / `title-scope-marker` / `stale-inbox`. Narrow with `--code` / `--exclude-code` / `--severity error|warn` (an unknown `--code` is exit 2 with `candidates`); `[lint].ignore_codes` suppresses codes on every run and `[lint.severity]` re-levels one — every consumer, this exit code and the `sync`/`brief` count included, sees the effective level. **The filter drives the exit code**: excluding or ignoring the last error exits 0, and `--severity warn` always exits 0.
- **`migrate`** — dry-run by default (`--yes` applies); unmapped headings and wikilinks are reported, never dropped. `-l` and `-e` stamp the whole batch (`-e` resolves before anything is written; a bare import inherits the scope's single active box like `add`); an import that would land open tasks under no box on a board that has boxes is reported as a warning.

---

## Claude Code / agent integration

furrow needs no MCP server and no plugin — the plain CLI **is** the agent interface: `--json`/`--ndjson` on every read, machine-actionable error envelopes, and a clonable plain-text store the agent can read (and, for bodies, write) directly. A daemon or a second protocol would add operational surface without adding a capability (see [docs/non-goals.md](docs/non-goals.md)). The integration is a `CLAUDE.md` contract plus the `--json` flag. The rules:

- **Never hand-edit `tasks/<id>.json` (or `meta.json`).** A single deterministic marshaller owns those files; a manual edit will churn the diff (and likely lose the canonical ordering). Mutate tasks through the commands above. `meta.json`'s `schema_version` is raised by **`furrow upgrade` alone** — no other command touches it.
- **Pre-flight a board you are about to write with `furrow board --json`.** It never fails on a version mismatch, it reports one: branch on `writable` / `schema_state` (`current`/`outdated`/`too-new`/`unreadable`) rather than discovering the problem as a failed write. A write to a board behind the binary is `schema-upgrade-required` (exit 2 — run `furrow upgrade`); a board ahead of it is `schema-too-new` (exit 3 — update furrow). Both carry `details {board_schema, binary_schema}`.
- **`bodies/*.md` are yours to edit.** Prose lives there and is plain Markdown — edit it directly, or via `furrow edit <id>` (which prints the absolute path in a non-interactive context). To *replace* a body and keep the shard's `updated` honest in the same write, pipe the new content to `furrow edit <id> --body -`; to *append* a progress paragraph, `furrow note <id>`.
- **Use `--json` for machine reads (and writes).** JSON is written to **stdout only**; logs, confirmations, and errors go to **stderr**, so piping stdout into `jq` is always clean. `--ndjson` is the compact one-value-per-line form and is honored on every command that emits JSON (mutations and reports included), so a line-oriented agent never gets a silent human-prose degrade. Filters: `--status/-s`, `--label/-l`, `--repo/-r`, `--limit/-n` (a comma within `-s`/`-l` is OR within that field).
- **Batch by id with `show <id>... --no-body`.** Cross-checking a specific id set (audit sweeps, dependency checks) is one process, metadata only — no `body_text` bloating the output. Add `--ndjson` for an arity-independent one-task-per-line shape; a partial miss still emits the found tasks and reports the rest in `details.missing`.

furrow is **non-interactive by default** — it never prompts. **Destructive operations preview unless `--yes`** — the one list: `archive`, `rm`, `epic rm`, `tidy`, `upgrade`, `migrate`, and a `-q`/`-l`/`-r` selection on `set`/`done`/`move`.

**Exit codes:**

| Code | Meaning |
|---|---|
| `0` | OK — **including an empty query result** (`ls`/`next`/`revisit` matching nothing still succeeded) |
| `1` | a **specifically requested id** was not found (e.g. `show <id>`) — never an empty list; also `furrow doctor`'s **problems found** (kind `doctor-unhealthy`, the health-check convention) |
| `2` | bad usage / validation |
| `3+` | internal / I/O error |
| `130` / `143` | a `SIGINT` / `SIGTERM` interrupted the run (128+signal by Unix convention) — e.g. Ctrl-C during `furrow sync`, which returns `sync-interrupted` (retryable). A deliberate `sync-conflict` is not a cancellation and keeps its exit `3`. |

The **schema gate** is the one place where the exit code, not the kind, says which side is stale: `schema-upgrade-required` (exit 2) = the board is behind, run `furrow upgrade`; `schema-too-new` (exit 3) = the binary is behind, update furrow (in CI, bump the `sync-task-status.yml@vX.Y.Z` pin). Both carry `details {board_schema, binary_schema}`; to ask "can I write here?" **without provoking an error**, read `furrow board --json`'s `writable`/`schema_state` (see [the layout gate](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it)). The same contract is printed by `furrow --help` (and each affected command's help), so it is discoverable from the binary, not just here.

On a non-zero exit, furrow prints a structured error object to stderr:

```json
{"error":{"kind":"unknown-lane","subject":"t-0001","retryable":false,"exit":2,"message":"unknown lane \"backlogg\" (configured: …)","candidates":["backlog","ready"]}}
```

Two fields carry the decision, so a consumer never parses prose:

- **`kind`** — the stable kebab-case failure class, a **closed vocabulary** (`furrow vocab error-kinds` prints it; every member below). The generic trio mirrors the exit classes — `not-found`, `validation`, `internal` — and a named kind exists exactly where the remedy is more specific than the exit code: `body-conflict-marker`, `doctor-unhealthy`, `epic-active-clash`, `epic-ambiguous`, `epic-not-found`, `git-failed`, `git-missing`, `no-git-repo`, `query-parse`, `query-type`, `query-unknown-field`, `query-unknown-flag`, `referenced`, `repo-ambiguous`, `repo-unknown`, `schema-too-new`, `schema-upgrade-required`, `session-busy`, `sync-busy`, `sync-conflict`, `sync-interrupted`, `sync-lock-stale`, `sync-op-in-progress`, `sync-push-rejected`, `sync-stash-stranded`, `sync-unmerged`, `unknown-lane`, `unknown-subcommand`.
- **`retryable`** — `true` means re-running the same command is the documented recovery (`sync-busy`, `sync-push-rejected`, `sync-interrupted`); always present, so "retry or escalate?" is one key, not a memorized table.

`subject` names the entity the failure is about — a task or epic id, an
`owner/repo`, an asset name, or a store file (`config`, `meta`) — and is omitted
when there is none; `exit` mirrors the process exit code (named `exit`, not
`code`: in furrow's vocabulary `code` is lint's kebab-case problem slug). When
an input *almost* resolved, the envelope adds a `"candidates": [ … ]` array
so a script picks an alternative instead of parsing prose — an ambiguous repo
short name, an unknown lane, an unknown (sub)command, or a label that uniquely
names a repo (the did-you-mean guard). A partial
`show` batch adds `"details": {"missing": ["t-…", …]}` and exits 1; the version
gate adds `"details": {"board_schema": N, "binary_schema": M}`. Branch on the
kind, the flag, and the arrays — never the message.

### CI: auto-update a tracker from PRs

`furrow apply` turns a PR into a status update — the `Closes #N` idea, for a
furrow tracker. Add a footer to the PR body pointing at a task's body file:

```
SetStatus-task: https://github.com/<owner>/<tracker>/blob/main/.furrow/bodies/<id>.md done
```

On PR **open** (incl. draft) the task is nudged to in-progress; on **merge** the
named lane is applied (omit the lane to only annotate the body). `apply` reads the
text from `--body-file` or stdin and is CI/VCS-agnostic.

The CI apply is non-blocking, so a footer naming an unknown id or lane is only
reported on the PR — and noticed after the merge left the lane untouched. Check
it locally first: `furrow apply --on merge --dry-run --body-file <pr-body>`
validates every directive (exit 2 on a bad lane, with the configured lanes as
`candidates` under `--json`) and writes nothing.

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
    uses: akira-toriyama/furrow/.github/workflows/sync-task-status.yml@v6.0.0
    secrets:
      PROJECTS_WRITE_PAT: ${{ secrets.PROJECTS_WRITE_PAT }}
```

The workflow downloads the furrow **release binary matching its own tag**
(checksum-verified) — the workflow revision and the binary revision cannot
diverge, and CI upgrades only when you bump the pin. Auth is one fine-grained
PAT (`PROJECTS_WRITE_PAT`: Contents Read & write on the tracker repo only);
until it exists the job skips cleanly. Validation is non-blocking: an unknown
id or lane is reported, never a merge blocker.

That pin is exactly what a board upgrade breaks, so the workflow **pre-flights
the schema**: it runs `furrow board --json` against the tracker and, when
`.writable != true`, fails with one annotated error naming both versions and the
remedy (bump this repo's pin) — instead of letting a pinned-but-outdated binary
report "task not found" for every id. Which is why the ordering above is not
optional: release furrow → bump every caller's pin → *then* `furrow upgrade`.

---

## Central board

A **central board** is a `.furrow` that lives *outside* the repos it backs and
is reached by configuration rather than by sitting in the checkout. Because it
sits outside, one tracker repo can back many repos at once — clonable,
greppable, diffable, each checkout auto-scoped to its own repo (`owner/repo`,
the first-class `repos` field). That is the GitHub-Projects-alternative shape,
but the count is a consequence, not the definition: the
[work-machine recipe](#standalone-a-local-board-with-no-remote) below is a
central board backing exactly one repo. Wire it up once for whole trees of
repos (user-level config), or per repo (a pointer file).

### User-level config (no per-repo file)

Point furrow at one or more central boards covering whole trees of repos, with
**zero per-repo setup** — new repos are covered automatically. Scaffold it with
`furrow config init` (run inside the central board's repo, it fills the board
path and scope in for you; elsewhere it writes a commented placeholder to edit),
or write `~/.config/furrow/config.toml` (or `$XDG_CONFIG_HOME/furrow/config.toml`)
by hand; `furrow config path` prints where it lives.

```toml
[[board]]
path        = "~/src/github.com/me/projects/.furrow"  # the central .furrow (~, relative to this file, or absolute)
scopes      = ["~/src/github.com/me"]                 # activate only under these dirs (at least one is required)
repo        = "auto"                                  # "auto" = derive owner/repo from the checkout | "" = none | a literal "owner/repo"
label       = ""                                      # optional literal tag `add` applies (never filters reads)
auto_filter = true                                    # scope ls/next/revisit to the board repo (default true; false = whole board)
autocommit  = false                                   # commit .furrow/ after each mutating command (default false; best-effort, no push)
```

A board activates **only when the current directory is under one of its
`scopes`**; everywhere else furrow behaves exactly as without it. Repeat the
`[[board]]` table to send different trees to different boards — when several
scopes enclose the cwd, the **most specific (longest) one wins** (ties go to the
first in the file). A board with no `scopes` is ignored rather than guessed, so a
half-written entry never breaks furrow elsewhere — and because that makes it
silent, `furrow lint` and `furrow config path` report whatever was clamped.

`repo = "auto"` derives the scope repo from the nearest enclosing git checkout
— file reads only, no `git` subprocess: the FIRST `url` of `[remote "origin"]`
(a worktree's `.git` file is followed to the shared config, so a worktree named
`chord-fix-y` still derives `owner/chord`), else a ghq-style path, else the
board opens **unscoped** with a stderr note and `add` creates drafts — a bare
directory name is never written into `repos`. `FURROW_BOARD=<path>` is the env
form: one synthetic board for one-offs and tests, outranking only the config
file's entries (see Discovery precedence). The retired `label = "auto"` is
ignored with a warning.

`autocommit = true` makes furrow **git-commit the board's `.furrow/` after every
mutating command** — the standalone board's backup habit as a tool guarantee.
It lives here, in the **per-machine** user config, never the board's committed
`config.toml` (which syncs to every clone and CI). It commits by `sync`'s own
rule plus the body this command wrote, is **best-effort** (a commit failure is
a stderr warning, the command still exits 0), **never fetches or pushes**, and
skips a board whose enclosing git repo is not its own. Mechanism:
[docs/architecture.md](docs/architecture.md).

### Per-repo pointer

A single repo can instead redirect with a `.furrow-pointer.toml` at its root
(this **wins over** the user-level central boards):

```toml
board = "../projects/.furrow"   # the central .furrow (relative to this file, ~, or absolute)
default_repo = "me/chord"       # optional: scope to one owner/repo ("auto" derives it; "" = redirect only)
```

### Discovery precedence

`FURROW_DIR` (explicit, no scope injection) → the nearest ancestor directory
holding a `.furrow` (a real local store wins) → a `.furrow-pointer.toml`
redirecting to a board → a **central board**: `FURROW_BOARD` (env override —
one synthetic board) if set, otherwise the user-level config file's `[[board]]`
entries (when the cwd is under one of their `scopes`; most specific scope wins)
→ `furrow init`. So `FURROW_BOARD` only outranks the config-file boards, never a
nearer `FURROW_DIR` / local `.furrow` / pointer.

That order picks the **store**. The **scope** is a second question: a pointer
or a `[[board]]` answers it (including with "none"), while `FURROW_DIR` and a
local `.furrow` declare nothing and fall back to the board's own
[`default_repo`](#configuration). With a scope in effect, `add` unions the
scope repo into `repos` (`--draft` suppresses exactly that; an explicit `-r`
adds), `epic add` falls back to it when no `-r` is given, and the filtering
reads (`ls`/`next`/`revisit`/`search`/`stats`/`brief`, `epic ls`) filter to
it — silently, but never silently *hiding*: a hidden draft or box gets one
stderr hint. `auto_filter = false` shows the whole board while `add` still
attaches; `-r ''` is the per-command escape.

---

## Multi-machine: `furrow sync`

A **shared board** — one with a remote — needs only one ritual: pull before you
read, push after you write. `furrow sync` is that ritual as one
non-interactive command — a thin git wrapper, not a sync daemon or server
(see [docs/non-goals.md](docs/non-goals.md)):

1. auto-commit, **scoped to `.furrow/`**: machine-written files (the shards,
   `meta.json`, `config.toml`, `bodies/assets/`, the board git dotfiles, the
   `archive/` copies) and brand-new bodies always commit; a merely-modified
   `bodies/<id>.md` commits only when **furrow itself wrote it** (`note`,
   `edit --body`, `done --note`, `apply` journal the id per checkout) or when
   named with `-b/--body` / swept with `--all-bodies` — otherwise it is left
   for its author and reported in `pending_bodies`, so a **co-located
   checkout** never commits another operator's prose under the wrong author.
   A file furrow does not own (an editor swap, a `.tmp-*`) is never committed
   and is disclosed in `foreign_files`. The commit (`-m` overrides the
   message) carries a `Furrow-sync: host=… pid=… via=… dir=…` trailer, so a
   shared board's history can say which execution wrote it.
2. `git fetch`, then `git rebase --autostash @{u}` — rebasing onto the upstream
   **tracking ref**, never `FETCH_HEAD`, so a co-writer's concurrent fetch in a
   co-located checkout can't make it `fatal: Cannot rebase onto multiple branches`
3. `git push` (one pull→push retry on non-fast-forward)

Per-task shards make true conflicts rare — two machines *adding* tasks touch
disjoint files; only both sides editing the *same* task conflicts. **Bodies go
one step further:** `furrow init` scaffolds `.furrow/.gitattributes` with
`bodies/*.md merge=union` (and the `archive/bodies/` twin), so the commonest
collision — a bot appending a status marker while a session appends a note to the
same body — folds both paragraphs instead of stopping the sync (a *shard*
conflict stays real — union on JSON would corrupt it). A board initialized before
the scaffold just adds that line; `furrow doctor` warns `no-body-union-merge`
until it does. On a real conflict sync **aborts the rebase automatically** (the
board is never left with markers; your local sync commit survives) and exits 3
with `"kind": "sync-conflict"` + `"details": {"paths": [...]}`. The progress object
`{committed, pulled, pushed, conflict, complete, committed_bodies,
pending_bodies, pending_stash, foreign_files, switches, incoming}` prints to
stdout on
success and
failure alike (empty lists omitted); **`complete`** — not `pushed` — is the
"fully published" flag, `false` whenever a body or stash is left pending,
`foreign_files` names the non-furrow junk deliberately left uncommitted, and
`switches` names any `epic activate` records this sync published (the switch
log's exit point), and `incoming` classifies the task changes the pull brought
IN — the other machines' and CI's writes, read off the pre-pull..post-pull
shard diff: `created` / `closed` / `reopened` / `moved` / `refiled` /
`archived` (the pulled tree holds its `archive/` copy) / `removed` (`furrow rm`:
nothing to unarchive) / `updated`, with the old and new lane or epic on the moves — also
rendered as one `incoming:` human line, so the CI that closed your in-progress
task surfaces in the sync that pulled it, not on a later re-read.

## Sync failure modes

This is the failure taxonomy; every kind is branch-on-the-`kind`, with `retryable` saying whether a re-run is the fix (`furrow vocab error-kinds` prints the whole vocabulary):

- **A stranded autostash.** Step 2 stashes your *other* dirty files; if git's
  re-apply conflicts it keeps them **in the stash** and **exits 0** — the edits
  silently leave your working tree. So sync probes the stash: the run that
  strands one fails (`sync-stash-stranded`, exit 3, nothing pushed — `git stash
  pop`, then re-run), any leftover is re-reported in `pending_stash` until
  popped, and the unmerged index it leaves is explained by a pre-flight
  (`sync-unmerged`, exit 2). A body carrying conflict markers is refused before
  commit (`body-conflict-marker`, exit 2); `lint`'s `conflict-marker` covers any
  that got in.
- **A concurrent writer.** A foreign rebase caught mid-flight is waited out
  with a bounded backoff and, if still going, exits 3 with the **retryable**
  `sync-busy`; a lock/ref race is retried and a lock still blocking past the
  budget fails **terminally** (`sync-lock-stale`) naming the lock; a co-writer
  that keeps winning the **push** race is the retryable `sync-push-rejected`
  (the board is untouched, re-running is the whole fix). `sync-task-status.yml`
  retries whatever the envelope marks `retryable` (`sync-push-rejected`,
  `sync-busy`, `sync-interrupted`) and treats every other kind as terminal.

On a **successful** sync furrow also prints a repo-scoped `revisit` summary —
`dep_done` and `stale` task ids, the box signals (`epic_all_done`, quiet for a
box that is WAITING on a parked member's future due; `epic_stuck`, standing
boxes exempt; `epic_stale`; `epic_dep_done`; `epic_review_due`), and the
repos whose review clock lapsed (`unreviewed`); `--json` gains a `revisit` key,
each list omitted when empty and the whole key when clean. Two more
ride-alongs share that contract: a lint **error** count by code (`lint: 2
error(s) (epic-required 2) — furrow lint`; never sync's exit code) and the
`epic activate` records this sync publishes (`switches`).

## Board git hooks (optional)

The design lens: **remote automation is GitHub Actions; local automation is git
hooks.** furrow ships three POSIX-sh hooks in
[`scripts/board-hooks/`](scripts/board-hooks/) that put `furrow lint` at git's
extension points, so a board that goes inconsistent (a dep pointing at a task
someone archived, an orphaned body, a duplicate shard from a merge) is caught the
moment it happens — and never reaches the remote.

| hook | fires after | action | blocking |
|---|---|---|---|
| `post-merge`   | `git merge` / plain `git pull` | `furrow lint` | no (nudge) |
| `post-rewrite` | `git rebase` / `--amend` / `git pull --rebase` | `furrow lint` | no (nudge) |
| `pre-push`     | before a push | `furrow lint` | **yes, on errors** |

Only `pre-push` blocks, and only on lint **errors**; when it blocks a `furrow
sync`, the hook's stderr is relayed verbatim (a `git push stderr:` block, and
`details.stderr` in the envelope), since git's own summary says nothing. One
error is deliberately **excluded from the gate**: `due-overdue` — the only
finding that appears with no edit, and a gate fails on what THIS push did; it
stays an error everywhere it is *reported*. `furrow sync` pulls with `--rebase`
internally, so it trips these hooks too (which is why sync carries no lint of
its own).

Enable them once per machine — git does not turn on hooks at clone time, by
design — with the same one line furrow's own repo uses:

```sh
git config core.hooksPath scripts/hooks   # after placing the hooks there
```

`core.hooksPath` **replaces** `.git/hooks`, so move any hook you already keep
there in beside these (compose a same-name hook, don't replace it). Each hook
skips cleanly without `furrow` on `PATH` or without a `.furrow/`.

## Standalone: a local board with no remote

The common setup on a work machine, where there is no remote you can push to: keep a board on **one machine, under its own git, never pushed** — no `furrow sync`, no CI. Everything in [The store](#the-store) works identically; you just don't sync. Two small pieces of config make it seamless for you and a coding agent.

1. **Give the board its own git repo, ignored by the code repo.** A workspace dir beside the code, with its own `git init` and no remote, keeps the board's history out of the code repo:

   ```
   <code-repo>/                     # has its own remote (e.g. github.com/acme/app)
   ├── .git/info/exclude    →  claude_workspace/     # keep the board out of the code repo
   └── claude_workspace/            # its own `git init`, no remote, never pushed
       └── .furrow/
           ├── config.toml          # mode = "standalone", default_repo = "acme/app"
           └── meta.json, tasks/, bodies/
   ```

2. **Register it in your user-level config so it resolves from inside the checkout.** A board in a subdirectory isn't found by walking up from the code (that finds the *code* repo's git), so scope it explicitly — the same `[[board]]` mechanism as a [central board](#user-level-config-no-per-repo-file), because that is what this is — a *standalone central board*, the two axes crossing:

   ```toml
   # ~/.config/furrow/config.toml
   [[board]]
   path       = "/abs/path/to/<code-repo>/claude_workspace/.furrow"
   scopes     = ["/abs/path/to/<code-repo>"]   # `furrow` run anywhere under here uses this board
   repo       = "auto"                          # auto-tag new tasks with the checkout's owner/repo
   autocommit = true                            # commit the board after each change — the backup habit, automated
   ```

Then set **`mode = "standalone"`** and **`default_repo = "<owner>/<repo>"`** in the board's `config.toml` ([Configuration](#configuration)): the mode changes wording only (`furrow upgrade` drops the flag-day checklist and the sync line), and `default_repo` closes the hole that a command run from *inside* `claude_workspace/` finds the board by local discovery, which carries none of the `[[board]]` entry's scope — without it the same board answers `ls` differently by directory and a bare `add` there drafts. With no remote, commits are the only undo, so `autocommit = true` above is the backup habit automated ([details](#user-level-config-no-per-repo-file)).

A fully separate directory (e.g. `~/furrow-boards/app/.furrow`, outside the code repo) works too — same two-config setup, just a different `path`/`scopes`.

### What a standalone board can't use

Everything shared-board-shaped is N/A here, and knowing the failure shapes saves a debugging detour:

- **`furrow sync`** assumes an upstream: on a no-remote board it prints its progress object, then fails with **exit 3, kind `git-failed`** relaying git's wording — branch on the kind. Nothing is broken; backup here is `autocommit`, not sync.
- **PR→status automation** (`furrow apply`, the `SetStatus-task:` footer, the reusable workflow) is shared-board-only: the footer points CI at a body-file **URL** a never-pushed board does not have. Status transitions are manual.

---

## Configuration

`.furrow/config.toml` is the one human-edited file in the store. Reads apply a **clamp-don't-reject** policy: unknown keys are ignored and out-of-range values fall back to a safe default with a warning surfaced by `furrow lint` — so a typo can never break the tool. The one command that WRITES it is **`furrow config set <key> <value>`** — a surgical, git-config-style edit (comments and every untouched byte survive; a multi-line value collapses to the new one-line value) that is strict where reads are lenient: an unknown key is exit 2 with the key vocabulary in `candidates`, and a value the reader would clamp away is refused *before* the write, so what you set is exactly what a read will honor. Dotted keys (`lanes.default`, `next.lanes`, `alias.<name>`; bare `mode`); a list value is comma-split. `--user [--board <ref>]` targets a `[[board]]` entry of the user-level config instead (ref = path or scope, exact else unique substring, `candidates` on a miss). A board write rides the next `furrow sync` like every machine-written file.

The full annotated reference is the repo-root [`config.toml`](config.toml) —
the **canonical copy**: it is byte-for-byte the file `furrow init` writes
(check.sh and CI diff the two), so unlike a prose copy it cannot rot. The
sections: `[lanes]`, `[next]`, `[priority]`, `[ids]`, `[labels]`, `[archive]`,
`[lint]`, `[due]`, `[revisit]`, `[review]`, `[session]`, `[alias]`, plus the
top-level `mode` and `default_repo`. (The annotated copy that used to sit here had
already drifted — it lost `[lint].provenance_markers` and `[review]`'s epic
review clock — which is exactly why it is now a pointer.)

A board `[alias]` names a frequent command string; `furrow <name> <extra args>` expands it git-style (the alias tokens replace the name, the rest of the argv is appended), so every flag, board scope, and auto-filter composes for free. It lives in the **board** config (not the user-level one), so it syncs with the board and every machine/agent shares it. A real command always wins — an alias that shadows a builtin (`ls`, `next`, …) is inert and `furrow lint` flags it (`alias-shadow`); a blank alias value is dropped with a clamp warning. Put global flags *after* the alias (`furrow triage --json`), as with git.

`mode` is the board's MODE axis (`"shared"`, the default, or `"standalone"`) and changes **wording only**, never behavior — see [Two questions, four shapes](#two-questions-four-shapes); the LAYOUT axis is not a config key, it follows from how discovery reached the board. `default_repo = "owner/repo"` is the board's *fallback* scope, consulted only when discovery declared none (see [Discovery precedence](#discovery-precedence)); a literal `owner/repo` only, since the file is committed and shared (`"auto"` is clamped away with a `lint` warning), and it filters reads as well as attaching on `add`.

`done` stamps `closed`; moving a task *out* of the done lane clears it. Other terminal lanes (`icebox`, `waiting`) do **not** stamp `closed`, which is why parked tasks are never archived.

---

## Determinism

furrow's write path is byte-stable on purpose: every shard goes through one marshaller (`core.MarshalTask`) with a fixed byte recipe, so the bytes furrow writes equal what a human or an agent would hand-edit, a `Save` rewrites only the shards whose bytes actually changed (an untouched store is **zero git churn**), and `git diff` shows only the field you changed. `meta.json` is not rewritten by an ordinary `Save` at all — only `furrow init` and `furrow upgrade` ever touch it (the board's declared version is the write's *input* — see [the layout version gate](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it)). The full recipe and the guards that freeze it are in [docs/architecture.md](docs/architecture.md).

---

## Status

- **Working:** furrow is **CLI-only** and everything in this README ships:
  the first-class `repos` field, epics (with epic-to-epic deps and a review
  clock), due dates, recurrence (board layout v10 behind the two-sided version
  gate), `sync`, `apply`, and `migrate`. `sh scripts/check.sh` is the full
  verification.
- **Released:** tags are cut with GoReleaser → the Homebrew tap (see the
  [Releases page](https://github.com/akira-toriyama/furrow/releases); the
  bundled task-status Action ships since `v0.5.0`, the first-class `repos` field
  since `v0.6.0`, layout v4 since `v0.8.0`, layout v5 since `v0.10.0`, layout v6 — the epic pivot — since `v1.0.0`, layout v7 — epic-to-epic deps + standing/pinned — since `v2.0.0`, layout v8 — per-task due dates — since `v3.0.0`, layout v9 — the per-epic review clock — since `v4.0.0`, layout v10 — per-task recurrence — since `v6.0.0`). The nix `flake.nix` carries a
  real, pinned `vendorHash` with a
  committed `flake.lock` (since `v0.4.0`).
- **Future (low priority):** an interactive TUI/GUI as a **separate front-end**
  that drives furrow through its CLI/JSON contract (it does not import furrow's
  Go packages), and a read-only web viewer over the task shards.

Design notes: architecture in [`docs/architecture.md`](docs/architecture.md),
terms in [`docs/glossary.md`](docs/glossary.md), due dates and launchd
recipes in [`docs/scheduling.md`](docs/scheduling.md), and what furrow
deliberately
doesn't do (with rationale) in [`docs/non-goals.md`](docs/non-goals.md).

---

## License

MIT © akira-toriyama
