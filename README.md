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

## Three ways to run it

- **A central board** — one clonable tracker repo backs *all* your repos: each
  task carries the repos it relates to (the first-class `repos` field,
  `owner/repo`), each checkout is auto-scoped to its own repo, and
  `furrow sync` keeps clones on several machines converged. This is the
  GitHub-Projects-alternative mode — see [Central board](#central-board).
- **Repo-local** — another way to run it: a single repo carries its own
  `.furrow/` next to the code (`furrow init` and go). Fully supported; the
  Quickstart below runs this way, and everything except the board scoping
  works identically on a central board.
- **Standalone (local, no remote)** — a board you keep on one machine, under
  its own git, and never push: no `furrow sync`, no CI. The common shape on a
  work machine where you can't create a shared tracker repo — see
  [Standalone](#standalone-a-local-board-with-no-remote).

---

## Quickstart

```sh
# create a .furrow store in the current repo
furrow init

# add a task (id is assigned automatically, frozen, never reused)
furrow add "Wire up the config loader" --label core --label config

# list tasks in canonical lane -> priority -> id order
furrow ls

# move it out of intake once it's ready to pick up (add defaults to inbox)
furrow move t-0001 ready

# show what's ready to work (lane in [next].lanes — default ready + in-progress — and all deps done)
furrow next

# open the task's Markdown body in $EDITOR (prints the path when non-interactive)
furrow edit t-0001

# inspect a single task with its body
furrow show t-0001

# mark it done (stamps the closed timestamp)
furrow done t-0001
```

`add` defaults the lane to `lanes.default` (`inbox`) and appends within the lane using the sparse priority step. Pass `--status/-s`, `--priority/-p`, `--label/-l`, `--epic/-e`, `--dep`, `--ref`, or `--body` to set fields up front.

### Typed query — `-q`

Every filtering read — `ls`, `next`, `revisit`, `stats`, `search` — takes `-q "<query>"`, a GitHub-Projects-style query folded into one string and compiled by ONE shared evaluator, so it means the same thing everywhere (`brief` is deliberately excluded — a fixed session-orient read). It is a **flat AND-list**: whitespace between terms is AND, a comma inside one value is OR, a leading `-` is NOT, and it ANDs with the other filters (`-s/-l/-r`, `--sort`, …) so a query never widens a scoped board. No cross-field OR, no grouping, no in-query sort — GitHub's own ceiling; `--json | jq` owns the long tail.

```sh
furrow ls -q 'is:actionable label:cli,dx -status:icebox'   # workable now, (cli OR dx), not iced
furrow next -q 'value:>=4 -label:chore'                     # ready AND worth it
furrow ls -q 'is:open updated:<-30d'                        # open but untouched for a month
furrow ls -q 'closed:2026-07-01..2026-07-15'                # closed inside a window
furrow ls -q 'depends-on:t-k3m9p is:blocked'                # what waits on t-k3m9p, still stuck
furrow stats -q is:stale                                    # the stale board's shape
furrow ls -q 'roi:>2 "typed query"'                         # ROI>2 with a text phrase
```

- **qualifiers** (`field:value`, comma = OR, repeat = AND): `status`/`lane`, `epic`, `label` (a value containing `*` is a wildcard — `label:*ui*`, `label:area/*` — spanning any run; a plain value stays exact), `repo` (resolved exactly as `-r` does: a full `owner/repo` or a short name naming exactly one — ambiguous is exit 2 with candidates), `id` (prefix), `title`, `body`, and the graph fields `depends-on`/`blocks`/`descendant-of`/`ancestor-of` (see **graph**). A bad query is **exit 2**, never a silent empty result — but the keys differ by fault: an unknown *qualifier* or `is:` flag carries both a stable error `kind` (`query-unknown-field` / `query-unknown-flag`) and did-you-mean `candidates`; an unknown `status` value or an ambiguous `repo:` routes through furrow's existing lane/repo errors (`unknown-lane` / `repo-ambiguous`), which carry `candidates`; an operator on a non-ordered field is kind `query-type` with no candidates but `allowed_operators` in `details`. Every parse/bind fault also carries the offending `term` and its byte `offset` in `details`, so a front-end can underline the token without re-lexing the query.
- **ordinal** (`value`, `effort`, `priority`, `roi`): comparison `>`, `>=`, `<`, `<=` and range `2..4` / `*..3` / `3..*`. An unset estimate (and an undefined `roi`) never satisfies a comparison.
- **dates** (`created`, `updated`, `closed`, `reviewed`, `due` — the `-q` generalization of `--since/--until`): absolute `YYYY-MM-DD` (the whole UTC day, so `created:>2026-07-01` means *after* that day) or RFC3339, plus signed **relative offsets** from now, `updated:>=-2w` (JQL's units `m/h/d/w` — `m` is minutes, not months; an offset needs a comparison or range, never a bare equality). Ranges are inclusive with `*` open ends. A `null` `closed`/`reviewed`/`due` satisfies **no** comparison (existence is `has:`/`no:`'s job) — so negation *includes* the unset, like the estimates. One deliberate asymmetry, per field: a bare day on the machine stamps (`created`/`updated`/`closed`/`reviewed`) is a **UTC** day (nobody types those values — they are compared against stored UTC stamps), while a bare day on **`due`** is the **operator's LOCAL day** — `due` is the one date a human authors in wall clock through this same CLI, and a UTC day here would mean `-q due:2026-08-04` could not find what `--due 2026-08-04` had just written (the tool disagreeing with itself, up to 9h apart on a +09:00 machine). Spell an exact instant when the boundary matters.
- **graph** (exact ids; an unknown id just matches nothing): `epic:X` selects a box's members (lenient on an unknown id — a box that does not exist simply has no members, and `lint` owns reporting the dangling reference; the STRICT spelling is the `-e` flag, which resolves and fails with candidates); `depends-on:X` and `blocks:X` are the two directions of the dep edge (the tasks waiting on X, and the tasks X waits on); `descendant-of:X` and `ancestor-of:X` are their **transitive** twins over the deps DAG — everything that transitively waits on X, and everything X transitively waits on (start ids excluded from their own closure, cycles terminate). Epics do not nest, so there is no hierarchy walk to spell — box membership is `epic:`.
- **presence**: `has:FIELD` / `no:FIELD` over `label`, `repo` (`no:repo` = a draft), `epic` (`no:epic` = unfiled, the `epic-required` lint state), `value`, `effort`, `deps`, `refs`, `checklist`, `closed`, `reviewed`, `due`, `body` (non-whitespace content — note `add` seeds every body with a heading).
- **computed flags** (furrow's own, no GitHub equivalent): `is:actionable` (a next lane with every dep done — a strict SUPERSET of what `furrow next` hands you, since next also scopes to the active epic), `is:blocked`, `is:stale` (no update within `[revisit].stale_days` — revisit's own definition, a pure age test that does not imply open), `is:unfiled` (no epic), `is:overdue` (a promised instant that has passed — `lint`'s `due-overdue` as a filter, minus every lane exemption, the done lane included: `-q` returns what you asked for, and which lanes stay QUIET is a lint/brief policy, not a fact about the date), `is:open`/`is:closed`/`is:draft`.
- **free text**: a bare word or `"quoted phrase"` is a case-insensitive substring over **title + body** — exactly `furrow search`'s matcher, so `ls -q foo` finds what `search foo` finds. Bodies are loaded only when a term actually reads them (free text, `body:`, `has:body`), once per task; free text additionally skips the load when the title already matched. On `title:` a **quoted** value flips substring to whole-field equality (`title:'Bug fix'`), still case-insensitive. `body:` stays a substring match even when quoted — quoting is also the way to include a space, and whole-*document* equality would make a quoted `body:` unusable rather than exact.

Deliberately out, permanently: cross-field OR, grouping, and in-query sort — GitHub's own ceiling, and the long tail is `--json | jq`'s job.

---

## The store

furrow uses a **hybrid** layout: one machine-written JSON shard per task for structured metadata, and one hand-editable Markdown file per task for prose. A pure JSON or JSONL store would collapse long bodies into one escaped line — every prose edit would churn the whole file and an agent could easily corrupt the escaping. Splitting prose into `bodies/<id>.md` keeps both halves diffable. Sharding the metadata one file per task means two operators adding or editing tasks on separate worktrees/PRs touch distinct files, so a git merge is a conflict-free union instead of a fight over one sorted array.

```text
.furrow/
├── config.toml          # human config (furrow only READS this; never rewrites it)
├── meta.json            # board-wide layout version {"schema_version": 8} — written ONLY by furrow, raised ONLY by `furrow upgrade`
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
  "schema_version": 8
}
```

### The layout version gates writes (and only `furrow upgrade` raises it)

That number is the **board's** — not the binary's — and it is an **input** to every write, never an output. The gate has two sides, and the exit code alone says which to fix:

- **The board is newer than your furrow** → refused: `schema-too-new`, exit 3. Update the binary (in CI: bump the `sync-task-status.yml@vX.Y.Z` pin).
- **The board is older than your furrow** → fully **readable** but **read-only**: a write fails with `schema-upgrade-required`, exit 2. The board is the stale side, and an explicit command fixes it.

Both carry `"details": {"board_schema": N, "binary_schema": M}`. An ordinary command **never** migrates a board as a side effect — `meta.json` is stamped only when a genuinely empty store is created (`furrow init`). `furrow upgrade` is the one deliberate raiser, and it is a **flag day**: once it lands, no older furrow can write that board — including any CI pinned to an older release. furrow cannot see those pins, so you keep the order:

```sh
furrow board                # schema:   v4 (board) / v5 (binary) — READ-ONLY: run `furrow upgrade`
# 1. release a furrow that ships the new layout
# 2. bump every caller's sync-task-status.yml@vX.Y.Z pin to it
furrow upgrade              # 3. preview: which stores change, and how many shards
furrow upgrade --yes && furrow sync
```

On a **standalone board** (`standalone = true`, see [Standalone](#standalone-a-local-board-with-no-remote)) there is no fleet to coordinate, so `furrow upgrade` skips the flag-day checklist and the `furrow sync` step. The gate itself is unchanged; only the guidance differs.

`furrow board` reports the whole triple (`schema_version`, `binary_schema_version`, `schema_state`, `writable`) plus the board repo's local `git` state (`state`, HEAD's `commit`/`commit_time`/`subject`, whether `.furrow/` is `dirty`, and `ahead`/`behind` as of the last fetch) and — by design — **never fails on a mismatch**: it is the one command that still answers when board and binary disagree, which is why the bundled task-status workflow pre-flights it instead of emitting N mysterious "task not found"s. `furrow lint` warns (`schema-outdated`) without erroring, because a read-only board is the legitimate middle of a flag day. Why the gate exists — and the 2026-07-13 outage a side-effecting `Save` once caused — is in [docs/architecture.md](docs/architecture.md) and [docs/non-goals.md](docs/non-goals.md).

### A key furrow doesn't know is preserved, not dropped

The gate above only fires when someone **bumps** the version. A field added *without* a bump would leave `meta.json` still saying v5, so no gate fires and an older binary's lenient parse would drop the unknown key and write the loss back — one ordinary write, one destroyed field, no error. So furrow **parks every top-level key it does not recognise and re-emits it** (sorted, after the known ones) in all three machine-written files. Stated as a pair: *the gate stops a bumped layout from being misread; the passthrough stops an unbumped one from being destroyed.*

Four limits keep it honest: it is **not retroactive** (releases ≤ `v0.9.0` still destroy unknown keys — so keep bumping the layout on every field addition until every pinned CI caller is past them); **top-level only** (a key inside a `checklist` item is still dropped, which is why the schemas flip the three top-level objects to `"additionalProperties": true` but keep `$defs/checklistItem` `false`); and **preserved is not honoured** (an old binary carries a future `"blocked": true` faithfully and still hands you that task in `furrow next` — `furrow lint` warns **`unknown-shard-key`** so the carried-but-ignored case is visible). A corollary: a hand-edit typo (`"lables"`) is now **permanent**, because auto-deleting a key furrow doesn't understand *is* the bug being fixed — one more reason the shards are furrow's to write, not yours. The full mechanism (why "known?" is decided with `strings.EqualFold`, not `strings.ToLower`) is in [docs/architecture.md](docs/architecture.md).

Notes on the fields: `id` is frozen and is the stem of both the shard file (`tasks/t-0001.json`) and the body file (`bodies/t-0001.md`); `priority` is a sparse 10-step integer so an ordinary reorder edits one field instead of renumbering (`reorder --before/--after <id>` computes it relative to a lane-mate; only an exhausted gap respaces the lane, atomically in the same write); `status` is a lane defined in `config.toml`; `repos` is the first-class set of repositories the task relates to (`owner/repo` identifiers, 0..N — an empty set means a **draft**, the GitHub-Issues-draft analogue; labels are pure tags, a repo is *not* a label); `closed` is `null` while open and stamped when a task enters the done lane; empty collections serialize as `[]`, never `null`. `value` and `effort` are an optional coarse 1..5 estimate (importance and cost) — both omitted while unset, so dropping an idea into the inbox stays friction-free — and out-of-range scores clamp to 1..5. `due` is the instant a task is promised for (omitted while unset, like the estimates): an **instant**, not a day, so `--due 2026-08-04` binds the **end** of that day in your zone and a task promised for the 4th is not overdue at 00:01 on the 4th. The JSON Schema for a shard lives at [`docs/schema/furrow.task.v2.json`](docs/schema/furrow.task.v2.json) and for `meta.json` at [`docs/schema/furrow.meta.v2.json`](docs/schema/furrow.meta.v2.json); both are emitted by `furrow schema` (`task` by default, `meta` for the board version).

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

It renders wherever Markdown does (GitHub, Obsidian, an editor preview) — but **not in the terminal** (`show` prints the text, not the picture). furrow itself does nothing special with these files; they are just part of your repo. A few practical notes:

- Keep screenshots small and scrub anything secret — git history is permanent.
- On a **private** repo, committing the image in-repo and linking it relatively is the reliable option; external/raw image URLs typically need auth and expire. On a public repo you can also link an external host.
- For large media such as videos, track them with **Git LFS** (a `.gitattributes` rule) *before* committing the first one, so they never bloat the plain history (adding LFS afterwards only helps new files; cleaning existing blobs needs a history rewrite).
- `furrow lint` backs these habits up: it warns on a body that references a missing asset, an asset that no body references, and any asset ≥5 MiB — a nudge to LFS-track or shrink it *before* the blob lands in history (once committed it can't be un-committed).

---

## Command reference

All commands below are implemented and working today. furrow is CLI-only; a TUI/GUI is a separate, planned front-end that drives it through the CLI/JSON contract (see [Status](#status)).

The table is **generated from the binary**: the cobra tree's `Use`/`Short`/aliases/flags are the single source of truth — `scripts/gen-command-table.sh` splices it in, and check.sh/CI fail when the block and the binary disagree — so a command or flag can no longer ship without appearing here (hand-kept lists kept losing commands; the audit found four missing). `furrow <cmd> --help` says the same one-liners; the [command notes](#command-notes) below carry the behavior contracts a one-liner can't.

<!-- commands:begin — generated by scripts/gen-command-table.sh from internal/cli (Use/Short/flags). Edit those, rerun the script, commit both. Hand edits inside this block are overwritten. -->
| Command | What it does | Flags |
|---|---|---|
| `init` | Create a .furrow store in the current directory | — |
| `add <title>...` | Add a task (or many with --stdin) | `--body`, `--check`, `--dep`, `--draft`, `--due`, `--effort`, `-e/--epic`, `-l/--label`, `-p/--priority`, `--ref`, `-r/--repo`, `-s/--status`, `--stdin`, `--value` |
| `ls [<epic>]` (alias `list`) | List tasks (canonical lane->priority->id order), or group them by epic with --tree | `--actionable`, `--archived`, `--blocked`, `--drafts`, `-e/--epic`, `-l/--label`, `-n/--limit`, `-q/--query`, `-r/--repo`, `--reverse`, `--since`, `--sort`, `-s/--status`, `--tree`, `--until` |
| `show <id>...` | Show tasks or epics with metadata and markdown body (batch-friendly) | `--archived`, `--backlinks`, `--no-body` |
| `next` | Show actionable tasks (in the next-lanes, all deps done, in the active epic) | `--all-epics`, `-e/--epic`, `-l/--label`, `--lanes`, `-n/--limit`, `-q/--query`, `-r/--repo` |
| `brief` | One-shot session-orient read: active epic, next picks with bodies, blocked, revisit, drafts | `-l/--label`, `-n/--limit`, `-r/--repo`, `--stale-days` |
| `revisit` | List open tasks needing re-evaluation (agent re-weighing signal) | `-e/--epic`, `-l/--label`, `-n/--limit`, `-q/--query`, `-r/--repo`, `--stale-days` |
| `search <term>` | Full-text search over task titles and bodies | `--archived`, `-e/--epic`, `-l/--label`, `-n/--limit`, `-q/--query`, `-r/--repo`, `-s/--status` |
| `stats` | Summarize the board: counts by lane, repo, and label | `-e/--epic`, `-l/--label`, `-q/--query`, `-r/--repo`, `--since`, `-s/--status`, `--until` |
| `board` | Print the active board: store path, scope, lane vocabulary, and schema state | — |
| `boards` | List the configured boards (user-level config), independent of cwd | — |
| `doctor [dir...]` | Diagnose this machine's board setup: config, boards, scopes, git freshness | — |
| `edit <id>` | Edit a task's or epic's markdown body in $EDITOR | — |
| `note <id> <text>` | Append a paragraph to a task's or epic's body and advance its updated time | — |
| `attach <id> <file>` | Attach a media file to a task (copies into bodies/assets/, links it from the body) | — |
| `done <id>...` | Move tasks into the done lane (stamps closed) | `--note` |
| `move <id>... <lane>` | Move tasks to a lane | — |
| `reorder <id> [<priority>]` | Set a task's priority — absolute, or relative with --before/--after | `--after`, `--before` |
| `retitle <id> <title...>` | Rename a task (updates the shard title and the body heading) | — |
| `set <id>...` | Apply several triage edits at once (lane, priority, value, effort, labels, epic, due) | `--add-label`, `--after`, `--before`, `--clear-due`, `--clear-effort`, `--clear-value`, `--due`, `--effort`, `-e/--epic`, `-p/--priority`, `--rm-label`, `-s/--status`, `--value` |
| `value <id> <1-5>` | Set a task's value estimate (coarse 1..5), or clear it with --clear | `--clear` |
| `effort <id> <1-5>` | Set a task's effort estimate (coarse 1..5), or clear it with --clear | `--clear` |
| `check <id> [item-index]` | Toggle, add, remove, or reword a checklist item | `--add`, `--off`, `--reword`, `--rm` |
| `dep <id> [<dep-id>...]` | Add/remove a task's dependencies, or list them both ways with --list | `--list`, `--rm` |
| `epic add <title>` | Create an epic (never active — open it with `epic activate`) | `--body`, `--goal`, `-l/--label`, `--meta`, `-r/--repo` |
| `epic ls` | List epics (open only by default), active first | `--all`, `-l/--label`, `-n/--limit`, `-r/--repo` |
| `epic show <epic>` | Show one epic: goal, meta, progress, member tasks, and its body | — |
| `epic set <epic>` | Edit an epic's title, goal, meta, labels, repos, or its standing/pinned declarations | `--add-label`, `--add-repo`, `--goal`, `--meta`, `--pinned`, `--rm-label`, `--rm-meta`, `--rm-repo`, `--standing`, `--title` |
| `epic activate <epic>` | Make this the active epic for its repos (at most one each) | `--reason` |
| `epic deactivate <epic>` | Clear the active flag without closing the epic (suggests where to return) | — |
| `epic done <epic>` | Close an epic (clears active; suggests the previous active, never picks it) | — |
| `epic dep <epic> [<dep-epic>...]` | Add/remove an epic's deps (open after those close), or list them both ways with --list | `--list`, `--rm` |
| `label <id>` | Add and/or remove labels on a task | `--add`, `--rm` |
| `repo <id>` | Attach and/or detach repos (owner/repo) on a task | `--add`, `--rm` |
| `ref <id>` | Add and/or remove refs (file:line or URL) on a task | `--add`, `--rm` |
| `review <repo\|id>` | Record a review: stamp a task's reviewed time, or a repo's last-reviewed clock | `--by` |
| `apply --on <open\|merge> [--ref <src>] [--body-file <path>]` | Apply SetStatus-task directives parsed from PR/commit text | `--body-file`, `--on`, `--open-lane`, `--ref` |
| `sync` | Commit the board, pull --rebase, push (thin git wrapper) | `--all-bodies`, `-b/--body`, `-m/--message` |
| `archive [<id>...]` | Retire done tasks to .furrow/archive/ — by id, or the aged sweep (preview unless --yes) | `--older-than`, `-r/--repo`, `--yes` |
| `migrate <task-file.md>` | Import a Task.md-style tracker into furrow (preview unless --yes) | `-l/--label`, `--yes` |
| `upgrade` | Raise the board's on-disk layout to this furrow's schema (flag day; preview unless --yes) | `--yes` |
| `lint` | Check index<->body consistency, lanes, deps, links, assets, and config | `--code`, `--exclude-code`, `--severity` |
| `config init` | Write the user-level furrow config (central-board template) | `--path`, `--scope` |
| `config path` | Print the resolved path to the user-level furrow config | — |
| `schema [task\|meta\|repo]` | Print the JSON Schema for a task shard, meta.json, or a repo review shard | — |
| `version` | Print the furrow version (with build commit/date when stamped) | — |
<!-- commands:end -->

On the read commands, `-r/--repo` filters by the first-class `repos` field and is the scope control: a short name resolves case-insensitively at a `/` boundary (`-r furrow` → `akira-toriyama/furrow`; ambiguity is exit 2 with `candidates`), an explicit `-r` overrides the board scope, and `-r ''` shows the whole board. `-l/--label` is a pure tag filter that ANDs with the scope. Within a single `-s` or `-l`, a comma is OR (`-s inbox,backlog`, `-l bug,urgent`); the flags still AND across fields. Both `-s` and `-l` also union when **repeated** (`-s inbox -s backlog` is the same OR-set as `-s inbox,backlog`, and likewise `-l bug -l urgent`), so a repeated filter no longer silently keeps only the last value. `-s` and `-l` part ways on an *unknown* token: a lane is a closed vocabulary, so an unknown `-s` lane **exits 2 with the configured lanes in `candidates`** (symmetric with `move`/`add` — a typo like `-s in_progress` never silently returns `[]`), whereas an unknown `-l` tag just matches nothing (labels are open). When a label filter matches nothing but the name uniquely resolves to a repo that has tasks, furrow exits 2 pointing you at `-r` (the did-you-mean guard) — on **every** filtering read (`ls`/`next`/`revisit`/`search`/`stats`), so the same typo cannot be exit 2 on one command and a confident zero on another; an unknown `-r` short name likewise carries the board's repo universe in `candidates`. Run `furrow board` to see the lanes and the active scope without provoking an error. **A read never narrows or truncates silently**: when a repo scope (explicit `-r` or the board's auto scope) hides drafts on `ls`/`next`/`search`, one stderr hint line (`N draft(s) hidden — furrow ls --drafts`, or the `-r ''` remedy on `search`, which has no `--drafts` flag) points at them, and when a `-n` cap bites, a stderr note names the uncapped total (`note: showing 1 of 19 (-n)`) — on `ls` (flat and `--tree`, where the counts are groups), `next`, `revisit`, `search`, and `epic ls`. The JSON payloads are unchanged (a bare array stays a bare array — the counts are stderr notes, `brief --json`'s `next_total` remains the machine-readable uncapped count); stdout stays pure data.

Global flags: `--json` and `--ndjson` are honored **wherever furrow emits JSON**, not just the read/list commands — `--ndjson` is the same payload as `--json`, compact, one value per line (a list command — and the batch mutators `done`/`move`/`set`, whose `--json` is always an envelope array — streams one record per line; a single-object command like a mutation or `board` prints one compact line). Mutations (`done`, `move`, `note`, `set`, `reorder`, `retitle`, `value`, `effort`, `check`, `dep`, `epic`, `label`, `repo`) emit `{before, after, changed}` so a caller sees the effect without a follow-up `show` (a relative `reorder` that had to respace the lane adds a `renumbered` array); `apply` emits a per-directive report (`{on, ref, outcomes}`); `add`/`attach`/`init`/`lint`/`archive`/`migrate`/`version` all honor both flags too. `edit` prefers `$FURROW_EDITOR`, then `$VISUAL`, then `$EDITOR`, falling back to `vi`.

### Command notes

The generated table is the machine-guaranteed surface; these are the behavior contracts that don't fit a one-liner. (Commands whose whole story fits their table row — `init`, `retitle`, `label`, `schema`, `version` — have no entry, and `attach`, `sync`, `upgrade`, and `config` have their own sections: [attachments](#attaching-images-and-media), [multi-machine sync](#multi-machine-furrow-sync), [the layout gate](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it), [the central board](#central-board).)

- **`add`** — creates one task per stdin line with `--stdin`; `--check` (repeatable) seeds checklist items (body prose alone never populates the shard checklist); an out-of-range `--value`/`--effort` clamps to 1..5 with a stderr note; a title starting with `-` needs a `--` separator (the error says so); `-e/--epic` files the task under a box (id, unique id prefix, or unique title substring; a miss or ambiguity is exit 2 with `candidates` — leaving it unfiled is legal at add time and an error in `furrow lint` while the task is open); `--due` promises the task for a date — `2026-08-04` (that WHOLE day: it binds 23:59:59 in your zone, so the day itself never starts out overdue), `2026-08-04T10:30`, an RFC3339 instant, or a signed offset like `+1d`.
- **`ls`** — lists in canonical `lane -> priority -> id` order. `--drafts` shows only repo-less tasks (bypasses the board scope). `--since`/`--until` window by `updated` (bare `YYYY-MM-DD` or full RFC3339; a bare `--until` includes the whole day); `--sort updated|created|value|effort` reorders (newest/highest first, `--reverse` flips, unset estimates stay last either way) and makes `-n` the top-N of the sorted set; an unknown `--sort` field or bad date is exit 2. `--archived` reads the archive store with the same filters. Every flat row carries a one-character **state glyph** — ★ actionable (a next lane, every dep done — ready to pick up; `furrow next` additionally scopes to the active epic, so ★ is a superset of what next hands you), ✓ done, ~ parked, · open but not available — and `--json`/`--ndjson` add `actionable` and `blocked_by` per row. Filter on the state with `--actionable` or `--blocked` (mutually exclusive; both AND with `-s/-l/-r`, so `-s ready --blocked` is the ready rows that are actually stuck), and on box membership with `-e <epic>` (strict — the unfiled pile is `-q no:epic`, not a box name). **`--tree`** groups the same rows by **epic** instead of a flat table: the active epic first, then open epics by id, then closed ones, then the unfiled group — or one box's group with an `<epic>` argument. Filters still apply and the grouping is built over what MATCHED, so `--tree` never shows fewer tasks than the same flags without it; `-n` caps the number of **groups**, not tasks. Each group carries the epic's member `progress` (`{done,total}`, counted over the FULL board so a read filter can't under-count the box) and a `stuck` flag (open members, none actionable). Under `--tree`, `--json` nests the member tasks per group and `--ndjson` streams one whole group per line. A dated row carries its promise in the title cell — `due 2026-08-04 10:30`, or `overdue …` once the instant has passed (a tag, not a column: only a few tasks carry a date, and a column would widen every board).
- **`show`** — any number of ids in one read, in input order: `--json` is **always an array**, one element per found id (a single id is a one-element array, a total miss prints `[]`); the human output separates entries with `---`; `--ndjson` is one task per line at any arity. `--no-body` omits `body_text` — the lean metadata-only batch read. A partial miss still prints the found tasks and exits 1 with `details.missing`; if a missing id is **archived**, the error also carries `details.archived` and the message says to retry with `--archived`. `--backlinks` adds the tasks whose body mentions each one via `[[id]]` (a `mentioned_by` array under `--json`; can't combine with `--archived`). An id may also name an **epic**: store membership routes each one (never the id's prefix, the rule `note` follows), and a box renders as the box view `epic show` prints — goal, member roll-up, body — so a mixed batch's array carries one shape per entity, and an epic-shaped miss says `epic not found`. Entries dedupe by the **resolved** entity, so a box named twice (its id and a unique prefix, say) is read once. `--archived` reads the task archive only (boxes are never archived) and `--backlinks` is a task relation (`[[id]]` links carry the task prefix), so a box entry simply has no `mentioned_by`.
- **`next`** — "actionable" means: lane is in `[next].lanes` (default `ready` + `in-progress`, so intake stays out) **and** every dep is done. On a board with at least one epic, the result is ALSO scoped to the **active** epic for the repo, plus the unfiled pile — a **pinned** box's actionable tasks pass through that scope entirely and LEAD the result (v7: the always-visible channel), then the focus box's tasks, then the unfiled rescue; with no active epic the result is deliberately empty except for the pinned band (exit 0, a stderr hint names the state either way). `-e <epic>` reads one box explicitly (strict) and `--all-epics` ignores the scope; a board with no epics behaves classically. `--lanes <csv>` overrides which lanes count as "now" for this call only (config untouched): `next --lanes backlog,ready` surfaces a no-dependency backlog task without first promoting it; an unknown lane is exit 2 with `candidates`. `--json` attaches a `reason` per task (`in_next_lane` — the lane it matched — and `deps_satisfied`). A **due** promise that has arrived is noted on **stderr** (`note: N due (M OVERDUE)`), never folded into the rows: the dated work usually sits in a lane `next` excludes, so it has to be visible without changing what "actionable" means — and stdout stays a clean array.
- **`brief`** — the one-shot session-orient read, and the place a **due** date lands: the section LEADS the output (`due`, `{overdue, today}`, longest-overdue first) because a date is the only thing on the board that expires — everything else brief reports waits for you. It is board-wide as to epics (promised work is usually parked outside the active focus) and ignores the lane filter, though it obeys the repo scope like every other section; the key is omitted entirely when nothing is due. `lint`'s `due-overdue`/`due-today` are its twin — this is the surface a session cannot miss, that one is the check a sweep goes looking for. Then the **epic header** (`active`, the open+active epic(s) `next` scopes to with their member roll-up; `pinned`, the open pinned channel(s) whose tasks lead `next`, deduped against active; and `epics_declared`, which tells a non-participating board apart from "nothing active, so `next` is deliberately empty"); `next`'s top `-n` picks (default 3) **with their bodies** (`body_text` — the `show` follow-up folded in) plus `next_total`, the uncapped actionable count (a cap never hides the queue size); `blocked`, the next-lane tasks with an unsatisfied dep and their `blocked_by` (started or queued work that plain `next` deliberately hides); the `revisit` summary in `sync`'s shape; the `drafts` count (board-wide by definition — a draft has no repo, so no scope can own it); and `sync`'s `lint` error-count ride-along (omitted when clean). Scope with `-r`/`-l` like every read. Human mode is a compact dashboard **without** bodies (prose is `--json`'s payload for agents). Read-only and git-free: orient on a shared board with `furrow sync && furrow brief`.
- **`revisit`** — read-only; `--json` attaches a `revisit` array of `{code, detail}` (`no_repo`, `value_unset`, `effort_unset`, `stale`, `dep_done`; the box-level `epic_all_done` / `epic_stuck` / `epic_stale` / `epic_dep_done` are reported alongside, keyed by epic id) so an agent knows what to fix. Drafts surface regardless of scope. `--stale-days 0` disables the stale signal.
- **`search`** — case-insensitive substring over every title **and** Markdown body, in canonical order; several words are one literal phrase. Honors the same `-s/-l/-r/-n` scope as `ls`, and the same `-q` typed query. `--archived` searches the sibling archive store **instead of** the hot board (the meaning it has on `ls`/`show`), so digging up why something was done no longer falls back to grepping `.furrow/archive/bodies` — the archive's own bodies are read, not the hot store's. Each hit reports `matched_field` (`title`|`body`) and a one-line `snippet`; a title match never reads the body.
- **`stats`** — `total`, `drafts` (spanning the repo dimension like `brief`'s — a draft has no repo, so no repo scope can own one; `-s`/`-l`/`-q` still bind it, and on a bare read it equals `brief`'s count), and counts `by_lane` (a complete histogram in configured lane order, 0-count lanes included), `by_repo`, and `by_label` (most-used first). `stats -r ''` describes the whole board — the call that learns the label/repo vocabulary before guessing a `-l`/`-r`. `--since`/`--until` window by the updated timestamp exactly like `ls` **and** add a `window` section with the flow inside the bounds: the ids `created` there and the ids `closed` there (counts + `created_ids`/`closed_ids`, archive store unioned so a sweep can't deflate `closed`; membership is by `created`/`closed` alone, so a task closed in the window and touched after it still counts). That section is the machine side of the session budget check (`created ≤ closed − 1`): a session's closing counts are verifiable with `furrow stats -r '' --since <session-start> --json` instead of being taken on faith.
- **`board`** — the introspection snapshot: store path, discovery `source` (`env`/`local`/`pointer`/`user-config`), repo scope, lane vocabulary, stale/archive windows, and the schema triple (`schema_version`, `binary_schema_version`, `schema_state`, `writable`). It **never fails on a version mismatch — it reports one**, so it is the pre-flight that diagnoses a board no other command can open.
- **`boards`** — the machine-wide sibling of `board`: every `[[board]]` in the user-level config, in file order, **without resolving against cwd** — it exits 0 (a listing, possibly empty) exactly where other commands exit 2 with "no board", so it is the diagnosis for a machine whose scopes were never configured and the bootstrap call for a GUI front-end running outside every scope. The JSON is `{config, boards: []}`: `config` names the file read (the path is reported even when the file is absent); each entry carries the resolved `store`/`scopes`, the **declared** `repo`/`label` (`"auto"` cannot resolve without a checkout), an `exists` flag, and the same vocabulary/schema keys as `board` (shared structs — one parser reads both views; a missing board keeps an *empty* vocabulary, reported never guessed). `FURROW_BOARD` — a per-invocation override, not machine config — is deliberately not listed.
- **`doctor`** — `boards`' opinionated sibling: the machine-wide board-setup **health check**, read-only and network-free (it never fetches). It checks that the user config parses and at least one usable `[[board]]` exists (`no-boards` — the classic half-set-up machine, where every use is a bare exit 2 naming nothing), that every board is on disk, readable, and on this binary's schema, that every scope directory exists, a git-backed board's freshness vs its upstream *as of the last fetch* (`board-behind` → sync before reading; `board-ahead` → unpushed writes; an in-progress rebase/merge warns too), and where discovery would **not** pick the board inside its own scopes (`scope-shadowed`: a nearer `.furrow`/pointer wins — severity `info`, a fact to see, never unhealthy; when the shadow is the board's OWN tree, a bare `add` there also warns at the moment it drafts, so the finding is no longer doctor-only). Discovery is simulated at cwd (informational) and at every dir passed as an argument (an **assertion**: `dir-unresolved` is an error carrying the fix). Every finding has a stable kebab-case `code`; exit `0` = healthy (info included), `1` = problems found (`doctor-unhealthy`), so it can sit in shell init or CI: `furrow doctor --json | jq -e '.healthy'`.
- **`edit`** — opens `bodies/<id>.md` in the editor; with no TTY it prints the path instead. `<id>` may name a **task or an epic** (same membership routing as `note` — both entities' prose is one file in one directory). Prefer `note` for progress records: a direct file edit does not advance `updated`.
- **`note`** — appends the text as a new paragraph **and** advances `updated` in one write, so `lint`'s `reconcile-gap` stays honest for progress recorded in prose; `-` as the text reads the note from stdin (multi-line). `--json` adds `appended` beside the envelope (`changed` tracks metadata only, so it is `[]` when just the body moved). `<id>` may name a **task or an epic**: the two entities share the `bodies/` directory, so a box's progress record is the same write to the same file — only the shard that stamps `updated` differs, and the envelope is that entity's own. **Membership routes it, never the id's prefix** (a prefix guess misroutes: `[ids].epic_prefix` may extend `[ids].prefix`, and ids are prefix + random base32, so some ordinary task ids are shaped like epic ids). A ref naming a real task is the task; otherwise a ref the epic store resolves — exact id, unique id prefix, unique title substring, every other epic reference's contract — is that box. A ref that resolves to neither fails on the side its prefix suggests: an unknown `e-` id is exit 2 with `candidates`, an unknown task id exit 1.
- **`done`** — `--note "<text>"` folds the closing word ("→ continued in t-xxx") into the close itself: the text lands on **every** closed task's body under the note command's contract (a new paragraph, `updated` advances, `appended` beside each `--json` envelope, `-` reads stdin), and an empty note is exit 2 — never a silent plain close.
- **`move`** — clears `closed` when a task leaves the done lane (and `done` stamps it).
- **`reorder`** — the absolute form sets the sparse integer directly; `--before`/`--after <id>` compute it instead, slotting the task immediately next to a lane-mate (both tasks must share a lane — relative order across lanes is meaningless, so a cross-lane target is exit 2). When the sparse gap next to the target is exhausted, the whole lane is respaced **in the same single write** (all-or-nothing): `--json` adds a `renumbered` array of the neighbors' `{id, from, to}` moves and a stderr note names the count. The respaced neighbors' `updated` stamps deliberately do **not** advance — a respace is positional bookkeeping, not progress, so staleness signals stay honest.
- **`value` / `effort`** — an out-of-range score clamps to 1..5 **and is signaled**: a `clamped` key nested by field (`clamped.value.{requested, stored}` / `clamped.effort.{…}`) in the `--json` envelope plus a stderr note, so an explicit arg is never silently rounded. Via `add`, the clamp is stderr-only (`add --json` prints the created task, no envelope). `--clear` unsets.
- **`set`** — the routine triage edits (lane, priority, value, effort, labels, epic, due) in **one** write instead of several commands. `--due` sets or re-dates the promise and **is the snooze** (`--due +1d`, measured from now — so pushing an already-overdue task always lands in the future); `--clear-due` removes it. An empty `--due ''` is exit 2, never a silent clear: a caller interpolating an unset variable has a bug, and the clear has its own spelling. Several ids apply the same edits to all of them in one all-or-nothing write (bulk triage: a miss sets nothing and exits 1 with every miss in `details.missing`; `--json` is always an array of envelopes, one per id). The position flags (`--priority`/`--before`/`--after`) place ONE task and are exit 2 for a batch. `--priority` sets the position directly; `--before`/`--after <id>` place the task relative to one in the **destination** lane, so a cross-column drop (`-s <lane> --before <id>`) is lane + position in one write — the same respace/`renumbered` contract as `reorder`, and the three position flags are mutually exclusive. At least one change required; an unknown lane (or an unresolvable `-e` epic) is exit 2 with `candidates`, and so is a relative target outside the destination lane; under `[labels].required` a set that would strip the last label is refused.
- **`check`** — indexes are zero-based; marking done is an idempotent set, not a toggle (`--off` unchecks); `--add` appends verbatim (repeatable); `--rm` deletes at an index; `--reword` replaces its text. Mode flags are mutually exclusive; an out-of-range index is exit 2.
- **`dep`** — variadic add/remove in a single all-or-nothing write (a bad dep-id aborts without partial change); acyclic and idempotent. `--list` reads (never mutates) the dependency neighborhood **both ways** — `depends_on` and `blocks` (what unblocks if I finish this) — resolved to id+title+lane; a dangling dep resolves to its id alone (lint flags it).
- **`epic`** — the box's own subcommands: `add` (never active at birth — opening a box is a separate, deliberate act), `ls` (active first, then open by id, then closed; `--all` includes closed; the board's repo scope applies exactly as on the task reads — a bare `epic ls` inside a scoped checkout lists this repo's boxes, the population `brief`'s epic header draws from, with a stderr note for what the scope hid — `-r` overrides it and `-r ''` lists the whole board, and `-l` is the task reads' comma-OR tag filter), `show` (goal, meta, member progress, and the members in canonical order), `set` (title/goal/meta/labels/repos), `activate` (enforces at most one active epic **per repo** — a clash is exit 2 naming the incumbent in `details.held`; `--reason` is recorded, with the timestamp, in the epic's body — the same body `furrow note <epic-id>` appends to — and `furrow sync` surfaces the switches it publishes), `deactivate`, `done` (stamps `closed` and clears `active` in the same write; deliberately never picks the next box — that judgment is the human's, and `lint`'s `epic-no-active` nags until someone decides — but both suggest the **previous active** box to return to: the open, currently-inactive one with the newest activation record, computed fresh from the activation log `activate` writes into each box's body (no stored "previous" pointer to go stale) — one `previous:` line plus a `previous` key in `--json` (null = unknown, the honest answer when no record decides it; records exist since v6, and stamps are minute-precision local time, fine for a suggestion the human confirms by running `epic activate` themselves)), and `dep` (v7): `epic dep <epic> <dep-epic>...` makes a box WAIT ON others — "open this one after those close" — with the task-side `dep` contract (variadic, all-or-nothing, acyclic at write time, `--rm`, `--list` = both directions resolved to id+title+state). The edge is information, not enforcement: `activate` on a box with an open dep warns and proceeds (stderr note + `open_deps` beside the envelope), a dep on a closed epic is satisfied, `epic ls`/`brief` mark the waiting box (`open_deps` / ⏳ waits), `revisit` raises `epic_dep_done` when every dep is closed, and `lint` backstops merges (`epic-dep-cycle`, `epic-dep-missing`, `epic-dep-open`). Two more v7 declarations ride `set`: **`--standing`** marks a PERMANENT box (a mandate inbox, a parking lot) — exempt from the finish-shaped nags `epic_all_done`/`epic_dep_done` (stuck still fires) — and **`--pinned`** makes the box's actionable tasks lead `next`/`brief` regardless of the active scope (the pass-through channel; no repo slot needed, the tasks repo-filter themselves). Orthogonal to each other and to `activate` (mandate = standing+pinned, parking lot = standing only); clear with `--standing=false`/`--pinned=false`. Which boxes carry them is the operator's convention — furrow stays name-independent. A task's own membership is edited with `add -e` / `set -e` (`-e ''` unfiles).
- **`repo`** — each value must be a full `owner/repo` or a short name uniquely resolving against the board's repos (else exit 2 with `candidates`); a task with no repos is a draft.
- **`review`** — an id-shaped argument stamps that task's `reviewed` timestamp (tracked apart from `updated`: a review changes no content); anything else records a per-repo review clock. `--by human` (default) advances the staleness-nudge clock; `--by agent` logs a sweep without advancing it, so an autonomous re-evaluation never stops furrow nudging a human.
- **`apply`** — parses `SetStatus-task: <body-link> [<lane>]` directives from PR/commit text (stdin or `--body-file`) — the CI hook behind [auto status updates](#ci-auto-update-a-tracker-from-prs). `--on open` nudges to in-progress; `--on merge` applies the lane. Validation is non-blocking.
- **`archive`** — with ids it retires exactly those (each must be in the done lane, else exit 2 — no stranding live work); with none it sweeps aged done **inheriting the board scope, like every read** (`-r` (repeatable) swaps that scope; `-r ''` sweeps the whole board and has to be typed — the widest blast radius costs the most keystrokes, not the fewest), `--older-than` adjusting the age guard (sweep-only flags; combining them with ids is exit 2). A task's attached media travels with it into `.furrow/archive/`, never orphaned in the hot store. Previews unless `--yes`.
- **`lint`** — every finding carries a stable kebab-case `code` (`dangling-link`, `dep-cycle`, `epic-required`, `conflict-marker`, `unknown-shard-key`, `schema-outdated`, `archive-backlog`, …); branch on the code, not the message — the `id` field is contextual (a task id, an asset name, an `owner/repo`, `meta`, or `config`). Errors: dep cycles, a task sitting in an actionable (`[next].lanes`) lane with an unsatisfied dep (`ready-blocked` — `furrow next` will never hand it out, so the lane is lying: move the task back or drop the dep; `brief`'s `blocked` band is the read-side view of the same set), a task whose promised instant has passed (`due-overdue` — board-wide, no epic scope, since dated work is usually parked outside the focus; the escape is the date itself, `set --due +1d` or `--clear-due`, not silence), an OPEN task with no epic once the board has any (`epic-required` — quiet on a board that never declared a box), two active epics naming the same repo (`epic-multi-active`), a done-lane task with no `closed`, a body carrying git conflict markers (a half-merged progress record; markers inside a ``` fence are documentation and not flagged). Warns: a task promised for TODAY (`due-today`), an open task under a closed epic (`epic-closed`), a repo with open work but no active epic (`epic-no-active`), dangling `[[id]]` links (archived ids are not dangling; `[[e-…]]` epic links are resolved too), reconcile gaps, asset hygiene (missing/orphan/≥5 MiB), a board whose layout is behind the binary, a file carrying keys this furrow doesn't know, a blank entry left in `labels`/`refs`/`repos`/`deps` by a binary that predates the write-time refusal (`blank-entry`), a label that names a repo — full `owner/repo` or a short name the board's repo universe resolves — where the first-class `repos` field belongs (`repo-as-label`; the read-side `-l` did-you-mean guard never second-guesses an existing label, so this warn is what keeps that guard honest), config clamp warnings, the `[lint].archive_done` backlog nudge, and — only on a board that opted in via `[lint].provenance_markers` — an open, non-terminal task whose body carries none of the board's provenance markers (`provenance-missing`: the "where did this come from / how was it verified" pressure, in the board's own words; furrow ships no default vocabulary, so the check is off until a board declares one). Narrow with `--code` (allow-list) / `--exclude-code` (deny-list; wins) / `--severity error|warn` — an unknown `--code` token is exit 2 with the vocabulary in `candidates`, while an unknown `[lint].ignore_codes` config entry only warns (clamp-don't-reject). **The filter drives the exit code**: a filtered-out problem is as if never found, so excluding or ignoring the last error exits 0 (the point — silence a permanently-dead check without reddening CI), and `--severity warn` always exits 0.
- **`migrate`** — dry-run by default (`--yes` applies); unmapped headings and `[[wikilink]]`s are reported, never dropped.

---

## Claude Code / agent integration

furrow needs no MCP server and no plugin — the plain CLI **is** the agent interface: `--json`/`--ndjson` on every read, machine-actionable error envelopes, and a clonable plain-text store the agent can read (and, for bodies, write) directly. A daemon or a second protocol would add operational surface without adding a capability (see [docs/non-goals.md](docs/non-goals.md)). The integration is just a small `CLAUDE.md` block plus the `--json` flag. The rules:

- **Never hand-edit `tasks/<id>.json` (or `meta.json`).** A single deterministic marshaller owns those files; a manual edit will churn the diff (and likely lose the canonical ordering). Mutate tasks through the commands above. `meta.json`'s `schema_version` is raised by **`furrow upgrade` alone** — no other command touches it.
- **Pre-flight a board you are about to write with `furrow board --json`.** It never fails on a version mismatch, it reports one: branch on `writable` / `schema_state` (`current`/`outdated`/`too-new`/`unreadable`) rather than discovering the problem as a failed write. A write to a board behind the binary is `schema-upgrade-required` (exit 2 — run `furrow upgrade`); a board ahead of it is `schema-too-new` (exit 3 — update furrow). Both carry `details {board_schema, binary_schema}`.
- **`bodies/*.md` are yours to edit.** Prose lives there and is plain Markdown — edit it directly, or via `furrow edit <id>` (which prints the absolute path in a non-interactive context).
- **Use `--json` for machine reads (and writes).** JSON is written to **stdout only**; logs, confirmations, and errors go to **stderr**, so piping stdout into `jq` is always clean. `--ndjson` is the compact one-value-per-line form and is honored on every command that emits JSON (mutations and reports included), so a line-oriented agent never gets a silent human-prose degrade. Filters: `--status/-s`, `--label/-l`, `--repo/-r`, `--limit/-n` (a comma within `-s`/`-l` is OR within that field).
- **Batch by id with `show <id>... --no-body`.** Cross-checking a specific id set (audit sweeps, dependency checks) is one process, metadata only — no `body_text` bloating the output. Add `--ndjson` for an arity-independent one-task-per-line shape; a partial miss still emits the found tasks and reports the rest in `details.missing`.

furrow is **non-interactive by default** — it never prompts. Destructive operations are guarded: `archive` only previews unless you pass `--yes`.

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

- **`kind`** — the stable kebab-case failure class, a **closed vocabulary** (`furrow vocab error-kinds` prints it; every member below). The generic trio mirrors the exit classes — `not-found`, `validation`, `internal` — and a named kind exists exactly where the remedy is more specific than the exit code: `body-conflict-marker`, `doctor-unhealthy`, `epic-active-clash`, `epic-ambiguous`, `epic-not-found`, `git-failed`, `git-missing`, `no-git-repo`, `query-parse`, `query-type`, `query-unknown-field`, `query-unknown-flag`, `repo-ambiguous`, `repo-unknown`, `schema-too-new`, `schema-upgrade-required`, `sync-busy`, `sync-conflict`, `sync-interrupted`, `sync-lock-stale`, `sync-op-in-progress`, `sync-push-rejected`, `sync-stash-stranded`, `sync-unmerged`, `unknown-lane`, `unknown-subcommand`.
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
    uses: akira-toriyama/furrow/.github/workflows/sync-task-status.yml@v3.0.0
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

This is the GitHub-Projects-alternative mode: many repos share one central
board (e.g. a private cross-repo tracker repo — clonable, greppable, diffable),
each auto-scoped to its own repo (`owner/repo`, the first-class `repos` field).
Wire it up once for whole trees of repos (user-level config), or per repo (a
pointer file).

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

`repo = "auto"` derives the scope repo from the nearest enclosing git checkout —
file reads only, no `git` subprocess: it parses the FIRST `url` of
`[remote "origin"]` in `.git/config` (scp-like `git@host:o/r.git`, `ssh://`,
`git+ssh://`, and `http(s)://` forms, with or without `.git`; `pushurl`, second
`url` lines, and other remotes never count). A worktree's `.git` FILE is
followed through `gitdir` and `commondir` to the shared config, so a worktree
named `chord-fix-y` still derives `owner/chord`. With no usable origin it falls
back to a ghq-style `…/github.com/<owner>/<repo>` path; failing that the board
opens **unscoped** with a stderr note and `add` creates drafts — a bare
directory name is never written into `repos`. Outside any git repo the board
still opens, with the same note. `FURROW_BOARD=<path>` is the env form of the
central board: it replaces the user-level config file's `[[board]]` entries with
one synthetic board for one-offs and tests (its scope is the board repo's
parent). It does **not** override a nearer store — `FURROW_DIR`, a local
`.furrow`, and a `.furrow-pointer.toml` all still win over it (see Discovery
precedence). The retired `label = "auto"` mode is ignored with a warning pointing
at `repo = "auto"`.

`autocommit = true` makes furrow **git-commit the board's `.furrow/` after every
mutating command** — the standalone habit "touch furrow → always commit" as a
tool guarantee, so an undo/backup point exists without remembering `furrow sync`.
It lives here, in the **per-machine** user config, not the board's committed
`config.toml`: a board-config switch would ride `furrow sync` to every clone and
to CI (silently breaking the status-sync workflow), whereas autocommit is a
property of *this* machine. It reuses `sync`'s commit rule (machine-written shards
always; a co-located operator's untouched, modified body never) plus the body
*this* command wrote, so a `furrow note`'s own prose lands. It is **best-effort
and never blocks the mutation** (any commit failure — a board outside git, a clean
tree, a `commit.gpgsign` prompt, a conflict-marker body — is a one-line stderr
warning while the command still exits 0), it **never fetches or pushes** (that
stays `furrow sync`'s job — autocommit is a purely local backup), and it **skips a
board whose enclosing git repo isn't its own** (the classic slip of forgetting
`git init` in the board's directory, which would drop board commits into a code
repo). Distinct from the board-config
[`standalone`](#standalone-a-local-board-with-no-remote) flag, which changes only
`furrow upgrade`'s wording, never behavior.

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

That order picks the **store**. The **scope** is a second, independent question,
and only two of those arms declare one at all. The other two — `FURROW_DIR` and
a local `.furrow` — fall back to the board's own
[`default_repo`](#configuration) when it declares one. A pointer or a `[[board]]`
having *answered* the scope question ends it, **including when the answer is
"none"**: `repo = ""` means the whole board, and a `repo = "auto"` that fails to
derive has already said on stderr that nothing is scoped. The board's own key
never contradicts either.

With a scope in effect (from a pointer, a user-level board, or the board's own
`default_repo`):

- `furrow add "…"` unions the scope repo into the task's `repos` (an explicit
  `-r x` adds to it rather than replacing); `add --draft` suppresses exactly
  that union. The board's literal `label` (if any) still unions into labels.
- The filtering reads (`ls`/`next`/`revisit`/`search`/`stats`/`brief`, and
  `epic ls` for boxes) filter to the scope repo — with no banner, but never
  silently *hiding*: when the scope hides drafts (`ls`/`next`/`search`) or
  boxes (`epic ls`), one stderr hint line points at the escape hatch.
  A user-level board can opt out with `auto_filter = false` to show the whole
  board while `add` still attaches the repo; a pointer and a board's own
  `default_repo` always filter. Scope
  control is `-r`: pass `-r ''` to see the whole board for one command, or
  `-r <repo>` for another repo. An explicit `-l tag` filters *within* the scope
  (it ANDs; it does not clear it).

---

### Multi-machine: `furrow sync`

A central board cloned on several machines needs only one ritual: pull before
you read, push after you write. `furrow sync` is that ritual as one
non-interactive command — a thin git wrapper, not a sync daemon or server
(see [docs/non-goals.md](docs/non-goals.md)):

1. auto-commit, **scoped to `.furrow/`** — other dirty files in the board repo
   (notes, drafts) are never swept in. Within `.furrow/`, machine-written shards
   (`tasks/`, `meta.json`) are always committed, but a hand-edited
   `bodies/<id>.md` is committed **only when it is new or named with `-b/--body`**
   — a merely-modified body is left for its author (surfaced in `pending_bodies`)
   so a shared checkout never commits a co-located operator's in-progress prose
   under the wrong author. `--all-bodies` restores the old sweep for a checkout
   you know is yours alone. Default message
   `:card_file_box:(board) sync via furrow`; override with `-m`.
2. `git fetch`, then `git rebase --autostash @{u}` — rebasing onto the upstream
   **tracking ref**, never `FETCH_HEAD`, so a co-writer's concurrent fetch in a
   shared checkout can't make it `fatal: Cannot rebase onto multiple branches`
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
pending_bodies, pending_stash, switches}` prints to stdout on success and
failure alike (empty lists omitted); **`complete`** — not `pushed` — is the
"fully published" flag, `false` whenever a body or stash is left pending, and
`switches` names any `epic activate` records this sync published (the switch
log's exit point).

### Sync failure modes

Two failures are worth knowing; both are branch-on-the-`kind` (with `retryable` saying whether a re-run is the fix), and the full
taxonomy (with the git-level reasons) is in
[docs/architecture.md](docs/architecture.md):

- **A stranded autostash.** Step 2 stashes your *other* dirty files for the
  rebase; if git's re-apply conflicts it keeps them **in the stash**, warns on
  stderr, and **exits 0** — the edits silently leave your working tree (and if
  one was a half-written body, that is a progress record left in mid-air). So
  sync probes the stash: the run that strands one fails (`sync-stash-stranded`,
  exit 3, nothing pushed — recover with `git stash pop`, then re-run), any
  leftover is re-reported in `pending_stash` every sync until popped (your own
  `git stash` entries are never touched), and the unmerged index it leaves is
  explained by a pre-flight (`sync-unmerged`, exit 2) instead of git's opaque
  error. A body carrying conflict markers is refused before commit
  (`body-conflict-marker`, exit 2); `furrow lint`'s `conflict-marker` rule covers
  any that got in.
- **A concurrent writer.** A shared checkout races: a foreign rebase caught
  mid-flight is waited out with a bounded backoff and, if still going, exits 3
  with the **retryable** `sync-busy` (re-run — not the `exit 2` "fix the args"
  class — usually the other writer has finished by then; a rebase genuinely stuck
  *here* is cleared with a manual `git rebase --abort`); a fetch/lock race is
  retried, and a lock still blocking past the budget (a likely-stale
  `.git/*.lock`) fails **terminally** (`sync-lock-stale`) naming the lock, rather
  than looping an agent on a `sync-busy` that will never clear. A co-writer that keeps winning
  the **push** race is its own **retryable** kind, `sync-push-rejected` — the board
  is untouched and your local sync commit intact, so re-running is the whole fix.
  It is a kind of its own precisely so a caller can retry it without matching
  error text: furrow's own `sync-task-status.yml` retries whatever the envelope
  marks `retryable` (today `sync-push-rejected`, `sync-busy`, `sync-interrupted`),
  and treats every other kind as terminal.

On a **successful** sync furrow also prints a repo-scoped `revisit` summary —
open tasks with a done dependency (`dep_done`) or gone stale (`stale`), epics
whose members are all done (`epic_all_done`), that hold open members with none
actionable (`epic_stuck`), that are ACTIVE and untouched (`epic_stale`), or parked with every dep epic closed (`epic_dep_done` — its turn to open), and repos whose review clock has lapsed
(`unreviewed`) — a nudge to run `furrow revisit`; `--json` gains a `revisit` key
with the id lists, each omitted when empty and the whole key omitted when the
board is clean. Two more ride-alongs share that quiet-when-clean contract: a
lint **error** count by code (`lint: 2 error(s) (epic-required 2) — furrow
lint`; a `lint` JSON key — never sync's exit code, so a red lint can never
block publishing the board) and the `epic activate` records this sync
publishes (`epic: activated e-k3m9 "…" 2026-07-29 14:32 — <reason>`; a
`switches` JSON key), so a focus switch surfaces in the session that made it.

### Board git hooks (optional)

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

Only `pre-push` blocks, and only on lint **errors** (`furrow lint` exits 2);
warnings flow through and are surfaced non-blockingly after a merge or rebase.
One error is deliberately **excluded from the gate**: `due-overdue`. It is the
only finding that appears with no edit — a promised instant passes and a push
that has nothing to do with that task is refused, `furrow sync` included, so the
board stops publishing at the moment a date lapses. A gate fails on what THIS
push did; a date is state. It stays an error everywhere it is *reported* (`brief`
leads with it, a bare `furrow lint` names it, a scheduled board-lint can file it
as a task on the day) — reporting is where a date belongs. A
`git pull --rebase` fires `post-rewrite` (not `post-merge`), so a board wants
both — and since `furrow sync` pulls with `--rebase` internally, sync trips these
hooks too (which is why sync carries no lint of its own).

Enable them once per machine — git does not turn on hooks at clone time, by
design — with the same one line furrow's own repo uses:

```sh
git config core.hooksPath scripts/hooks   # after placing the hooks there
```

`core.hooksPath` **replaces** `.git/hooks` rather than augmenting it — git then
consults only this directory — so move any hook you already keep in the default
`.git/hooks/` into the hooks dir too, or it silently stops running. Once both
live there, a same-name hook (say a `pre-push` that protects `main`) is a
collision to **compose** — keep the existing body and add the furrow-lint block —
not to replace. Each hook also **skips cleanly** when `furrow` is absent from
`PATH` or the repo has no `.furrow/`, so it never wedges a checkout.

## Standalone: a local board with no remote

The common setup on a work machine, where you can't create a shared tracker repo: keep a board on **one machine, under its own git, never pushed** — no `furrow sync`, no CI. Everything in [The store](#the-store) works identically; you just don't sync. Two small pieces of config make it seamless for you and a coding agent.

1. **Give the board its own git repo, ignored by the code repo.** A workspace dir beside the code, with its own `git init` and no remote, keeps the board's history out of the code repo:

   ```
   <code-repo>/                     # has its own remote (e.g. github.com/acme/app)
   ├── .git/info/exclude    →  claude_workspace/     # keep the board out of the code repo
   └── claude_workspace/            # its own `git init`, no remote, never pushed
       └── .furrow/
           ├── config.toml          # standalone = true, default_repo = "acme/app"
           └── meta.json, tasks/, bodies/
   ```

2. **Register it in your user-level config so it resolves from inside the checkout.** A board in a subdirectory isn't found by walking up from the code (that finds the *code* repo's git), so scope it explicitly — the same `[[board]]` mechanism as a [central board](#user-level-config-no-per-repo-file):

   ```toml
   # ~/.config/furrow/config.toml
   [[board]]
   path       = "/abs/path/to/<code-repo>/claude_workspace/.furrow"
   scopes     = ["/abs/path/to/<code-repo>"]   # `furrow` run anywhere under here uses this board
   repo       = "auto"                          # auto-tag new tasks with the checkout's owner/repo
   autocommit = true                            # commit the board after each change — the backup habit, automated
   ```

Then set **`standalone = true`** in the board's `config.toml` (see [Configuration](#configuration)). It changes **only wording, never behavior**: `furrow upgrade` drops the shared-board flag-day checklist and the "run `furrow sync` to publish" line — a single-machine board has no pinned CI to coordinate and no remote to publish to. The write gate, schema, and on-disk format are byte-for-byte identical to a shared board.

Set **`default_repo = "<owner>/<repo>"`** in that same `config.toml` too. The `[[board]]` entry above only scopes commands run from **under `scopes`** — but the board's `.furrow` sits *inside* that tree, so a command run from inside `claude_workspace/` finds it by plain local discovery, which outranks the `[[board]]` entry and carries none of its `repo`/`auto_filter`. The same board then shows a different `ls` depending on which directory you happened to be in, and a bare `add` there writes a repo-less draft. `default_repo` is what closes that hole: it travels with the board, so every way of reaching it agrees.

On a standalone board with no remote, commits *are* your only undo/backup. Set **`autocommit = true`** in the `[[board]]` entry above (a per-machine user-config key, [detailed here](#user-level-config-no-per-repo-file)) so furrow commits the board after every mutating command instead of relying on you or an agent to remember — best-effort (it warns and carries on if git can't commit), never pushes, and never sweeps a co-located session's in-progress body.

A fully separate directory (e.g. `~/furrow-boards/app/.furrow`, outside the code repo) works too — same two-config setup, just a different `path`/`scopes`.

### What a standalone board can't use

Everything hosted-board-shaped is N/A here, and knowing the failure shapes saves a debugging detour:

- **`furrow sync`** assumes an upstream. Run on a no-remote board it still prints its progress object first (which can say `"complete": true` — nothing was pending), then fails with **exit 3, kind `git-failed`**, relaying git's own wording about the missing upstream for your branch. The exact prose is git's and varies by version — branch on the kind, not the message. Nothing is broken afterwards; there was simply nothing to pull from or push to. Backup on a standalone board is `autocommit` (above), not sync.
- **PR→status automation** — `furrow apply`, the `SetStatus-task:` PR footer, and the reusable `sync-task-status.yml` workflow — is hosted-board-only: the footer points CI at the board's body file **URL** (`https://…/blob/main/.furrow/bodies/<id>.md`), and a board that is never pushed has no such URL to point at. Status transitions on a standalone board are manual: `furrow move` / `furrow done`.

---

## Configuration

`.furrow/config.toml` is the one human-edited file in the store. furrow only **reads** it (it never rewrites it) and applies a **clamp-don't-reject** policy: unknown keys are ignored and out-of-range values fall back to a safe default with a warning surfaced by `furrow lint` — so a typo can never break the tool.

```toml
# standalone = false              # a local single-machine board (no remote / `furrow sync` / CI);
                                  # when true, `furrow upgrade` drops the shared-board flag-day wording
# default_repo = "me/app"         # the repo this board is FOR — `add` attaches it and reads filter by it
                                  # whenever discovery supplied no scope (a literal owner/repo; "auto" is refused)
[lanes]
# The status enum AND the top->bottom sort rank.
order   = ["inbox", "backlog", "ready", "in-progress", "waiting", "done", "icebox"]
default = "inbox"                 # lane `furrow add` uses when --status is omitted
done    = "done"                  # lane `furrow done` moves into (where `closed` is stamped)
terminal = ["done", "icebox", "waiting"]  # lanes never actionable (done/parked); what `next` shows is [next].lanes below

[next]
lanes = ["ready", "in-progress"]  # lanes `furrow next` considers "ready to work" (besides the deps-done check);
                                  # intake/planning lanes are excluded — set to all non-terminal lanes to show everything actionable

[priority]
step    = 10                      # sparse step so reordering edits one field
default = 100

[ids]
prefix = "t-"                     # frozen id: prefix + random base32 suffix (collision-free)
epic_prefix = "e-"                # epics get their own prefix, so an id names its entity kind on sight
width  = 5                        # number of random suffix chars, e.g. t-k3m9p

[labels]
# required = false                # when true, a label-less task is exit 2 on `add` and an error in `lint`

[archive]
older_than_days = 30              # default window for `furrow archive --older-than`

[lint]
# archive_done = 0                # `furrow lint` warns once this many done tasks are old enough to archive (0 = off)
# ignore_codes = ["reconcile-gap"] # codes to suppress everywhere lint runs (an unknown code only warns)

[due]
# ignore_lanes = ["icebox"]       # lanes where a due date raises nothing (the done lane is always exempt);
                                  # terminal lanes are NOT exempt as a class — a `waiting` task is what dates are for

[revisit]
stale_days = 30                   # `furrow revisit` flags a task with no update in this many days (0 disables)

[review]
stale_after_days = 14             # days before `furrow sync`/`brief` nudge a repo as unreviewed (0 disables)

[alias]                           # name your frequent filters; `furrow <name> …` expands git-style
triage = "ls -s inbox,backlog"    #   `furrow triage -r app` -> `furrow ls -s inbox,backlog -r app`
wip    = "ls -s in-progress"       #   the remaining args append, so all existing flags/scope compose
```

A board `[alias]` names a frequent command string; `furrow <name> <extra args>` expands it git-style (the alias tokens replace the name, the rest of the argv is appended), so every flag, board scope, and auto-filter composes for free. It lives in the **board** config (not the user-level one), so it syncs with the board and every machine/agent shares it. A real command always wins — an alias that shadows a builtin (`ls`, `next`, …) is inert and `furrow lint` flags it (`alias-shadow`); a blank alias value is dropped with a clamp warning. Put global flags *after* the alias (`furrow triage --json`), as with git.

`standalone = true` marks a local single-machine board (no remote / `furrow sync` / CI). It changes **only wording** — never behavior, the schema gate, or the on-disk format: `furrow upgrade` drops the shared-board flag-day checklist and the `furrow sync` publish line, which would only misdirect a solo operator with no fleet to coordinate. Default `false` (shared board). See [Standalone](#standalone-a-local-board-with-no-remote).

`default_repo = "owner/repo"` is the repo the board itself is **for**. It is the *fallback* scope: consulted only when discovery ran an arm that declares no scope at all, and when it applies it filters reads as well as attaching the repo on `add`. So the key bites exactly the two arms that declare nothing — cwd **inside the board's own directory tree** (`source=local`) and `FURROW_DIR` — while a pointer's `default_repo` and a user-level `[[board]]`'s `repo` keep the last word, *including when they resolve to no repo at all* (see [Discovery precedence](#discovery-precedence)). Without it, the same board answers `ls` differently depending on which directory you ran from, and a bare `add` from inside the board silently produces repo-less drafts. Unlike the pointer key of the same name it takes a **literal** `owner/repo` only — `config.toml` is committed and shared, so a derived `"auto"` would differ per checkout and per machine, reintroducing exactly the cwd-dependence the key removes (it is clamped away with a `furrow lint` warning). There is deliberately no board-side `auto_filter`: declaring the scope declares it for reads too, and `-r ''` remains the per-command escape hatch.

`done` stamps `closed`; moving a task *out* of the done lane clears it. Other terminal lanes (e.g. `icebox` — parked, not finished; `waiting` — the GTD *Waiting-For* lane for work delegated or blocked on someone external) do **not** stamp `closed`, which is why parked tasks are never archived.

---

## Determinism

furrow's write path is byte-stable on purpose: every shard goes through one marshaller (`core.MarshalTask`) with a fixed byte recipe, so the bytes furrow writes equal what a human or an agent would hand-edit, a `Save` rewrites only the shards whose bytes actually changed (an untouched store is **zero git churn**), and `git diff` shows only the field you changed. `meta.json` is not rewritten by an ordinary `Save` at all — only `furrow init` and `furrow upgrade` ever touch it (the board's declared version is the write's *input* — see [the layout version gate](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it)). The full recipe and the guards that freeze it are in [docs/architecture.md](docs/architecture.md).

---

## Status

- **Working:** furrow is **CLI-only** — the core domain (`internal/core`) with
  the first-class `repos`
  field, first-class **epics** with epic-to-epic deps, and per-task **due dates** (board layout v8 + the two-sided version gate: read-refuse a newer board,
  write-refuse an older one, and `furrow upgrade` as the only raiser), config
  loader, filesystem store, app
  coordinator, the full CLI (incl. `repo`, drafts, `-r` scoping, `apply`, and
  `sync`), and **`migrate`** (importing a
  legacy `Task.md`). `go test ./...` + golangci clean; `sh scripts/check.sh`
  runs the full verification (core + store + app + cli + migrate).
- **Released:** tags are cut with GoReleaser → the Homebrew tap (see the
  [Releases page](https://github.com/akira-toriyama/furrow/releases); the
  bundled task-status Action ships since `v0.5.0`, the first-class `repos` field
  since `v0.6.0`, layout v4 since `v0.8.0`, layout v5 since `v0.10.0`, layout v6 — the epic pivot — since `v1.0.0`, layout v7 — epic-to-epic deps + standing/pinned — since `v2.0.0`, layout v8 — per-task due dates — since `v3.0.0`). The nix `flake.nix` carries a
  real, pinned `vendorHash` with a
  committed `flake.lock` (since `v0.4.0`).
- **Future (low priority):** an interactive TUI/GUI as a **separate front-end**
  that drives furrow through its CLI/JSON contract (it does not import furrow's
  Go packages), and a read-only web viewer over the task shards.

Design notes: architecture in [`docs/architecture.md`](docs/architecture.md),
terms in [`docs/glossary.md`](docs/glossary.md), and what furrow deliberately
doesn't do (with rationale) in [`docs/non-goals.md`](docs/non-goals.md).

---

## License

MIT © akira-toriyama
