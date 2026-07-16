# furrow

> An alternative to GitHub Projects / Issues — a clonable, git-native, plain-text task tracker you and your coding agent can both edit cleanly.

**furrow** keeps your tasks as plain text *in a git repo*: structured metadata in one deterministic JSON shard per task, long-form prose in per-task Markdown files. The case against Issues is simple. An issue can't be cloned — plain text can, so the tracker works offline and greps with your code. An agent can *read and write* it with ordinary file and CLI operations, no API client. And because the tracker lives in git next to the work, status never drifts from reality — the same push that changes the code can change the task. Writes are byte-stable, so `git diff` only ever shows what actually changed.

**When to reach for which.** GitHub Issues are the right tool for *intake from anyone* — a public inbox where a stranger can file a bug without write access to your repo. furrow is the opposite tool for the opposite job: *private, in-group* tasks for you and your agent. Its "you must be able to push to create a task" is **access control, not a defect** — the same permission boundary that guards your code guards your backlog.

**Local and instant, not a round-trip.** Much of this GitHub *can* do — but through the API: online-only, rate-limited, a network round-trip per call. furrow does it against plain files on disk: milliseconds, offline, no quota. Backlinks are the concrete example — `show --backlinks` answers "which tasks mention this one?" (the `[[id]]` links in their bodies) by scanning local files, where the GitHub equivalent is an online "mentioned in" panel behind an API call.

**Multi-person, honestly.** furrow is single-operator-first today, and that is the polished path. Several people *can* work one board — it is a git repo, so they clone, push, and `furrow sync` (per-task shards make concurrent edits a clean union). But per-person niceties — an `@mention` and a task **assignee** — are **not built yet**; they are on the roadmap, not a permanent non-goal.

Written in Go (module `github.com/akira-toriyama/furrow`, Go 1.25+). No database, no daemon, no cloud.

> **Status:** furrow is **CLI-only** — core (first-class `repos`, schema v2 +
> the two-sided version gate), CLI (incl.
> `repo`, drafts, `-r` scoping, `sync`, `apply`), and `migrate` all work
> (`go test ./...` + golangci green). A TUI/GUI is a separate, planned
> front-end that drives furrow through its CLI/JSON contract, not a part of
> this binary. Releases are published — see the [Releases page](https://github.com/akira-toriyama/furrow/releases) and [Status](#status).

[日本語版 README →](README.ja.md)

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

## Two ways to run it

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

`add` defaults the lane to `lanes.default` (`inbox`) and appends within the lane using the sparse priority step. Pass `--status/-s`, `--priority/-p`, `--label/-l`, `--parent`, `--dep`, `--ref`, or `--body` to set fields up front.

---

## The store

furrow uses a **hybrid** layout: one machine-written JSON shard per task for structured metadata, and one hand-editable Markdown file per task for prose. A pure JSON or JSONL store would collapse long bodies into one escaped line — every prose edit would churn the whole file and an agent could easily corrupt the escaping. Splitting prose into `bodies/<id>.md` keeps both halves diffable. Sharding the metadata one file per task means two operators adding or editing tasks on separate worktrees/PRs touch distinct files, so a git merge is a conflict-free union instead of a fight over one sorted array.

```text
.furrow/
├── config.toml          # human config (furrow only READS this; never rewrites it)
├── meta.json            # board-wide layout version {"schema_version": 5} — written ONLY by furrow, raised ONLY by `furrow upgrade`
├── tasks/
│   ├── t-0001.json      # one metadata shard per task — written ONLY by the single core.MarshalTask path
│   └── t-0002.json
├── repos/               # one review shard per repo — furrow review <repo> (last_reviewed clock)
│   └── akira-toriyama__furrow.json
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
  "body": "bodies/t-0001.md"
}
```

The board-wide layout version lives on its own in `meta.json` (never inside a shard, so a version bump touches one file and no shard becomes a merge point):

```json
{
  "schema_version": 5
}
```

### The layout version gates writes (and only `furrow upgrade` raises it)

That number is the **board's** — not the binary's — and it is an **input** to every write, never an output. The gate has two sides:

- **The board is newer than your furrow** → every read and write is refused: `schema-too-new`, exit 3. Update the binary (in CI: bump the `sync-task-status.yml@vX.Y.Z` pin). A lenient parse would **misread** such a board — silently behaving as if the fields it doesn't know were not there (sorting, filtering and closing tasks on a partial picture). It would no longer *destroy* them (see [unknown-key passthrough](#a-key-furrow-doesnt-know-is-preserved-not-dropped) below), but preserving is not understanding, which is exactly why this gate stays.
- **The board is older than your furrow** → it stays fully **readable**, but it is **read-only**: a write fails with `schema-upgrade-required`, exit 2. The board is the stale side, and an explicit command fixes it. Both errors carry `"details": {"board_schema": N, "binary_schema": M}`, and the exit code alone says which side to fix.

So an ordinary command **never** migrates a board as a side effect — `meta.json` is stamped only when a genuinely empty store is created (`furrow init`). `furrow upgrade` is the one deliberate raiser, and it is a **flag day**: once it lands, no older furrow can write that board — including any CI pinned to an older release. furrow cannot see those pins, so you keep the order:

```sh
furrow board                # schema:   v4 (board) / v5 (binary) — READ-ONLY: run `furrow upgrade`
# 1. release a furrow that ships the new layout
# 2. bump every caller's sync-task-status.yml@vX.Y.Z pin to it
furrow upgrade              # 3. preview: which stores change, and how many shards
furrow upgrade --yes && furrow sync
```

On a **standalone board** (`standalone = true`, see [Standalone](#standalone-a-local-board-with-no-remote)) there is no fleet to coordinate, so `furrow upgrade` skips the flag-day checklist and the `furrow sync` step — a single-machine board has no pinned CI and no remote. The gate itself is unchanged; only the guidance differs.

`furrow board` reports the whole triple (`schema_version`, `binary_schema_version`, `schema_state` = `current`/`outdated`/`too-new`/`unreadable`, `writable`) and — by design — **never fails on a mismatch**: it is the one command that still answers when board and binary disagree, which is why the bundled task-status workflow pre-flights it and fails with one legible error instead of N mysterious "task not found"s. `furrow lint` warns (`schema-outdated`) while a board waits to be upgraded; it does not error, because a read-only board is the legitimate middle of a flag day.

This is a scar: before the gate, `Save` stamped `meta.json` with the *binary's* version on every write, so a single routine `furrow sync` from an unreleased source build migrated a shared central board and every pinned release in the fleet lost it at once.

### A key furrow doesn't know is preserved, not dropped

The gate above only fires when someone **bumps** the version. If a future furrow adds a field and *doesn't* bump — because the change looks "additive" — `meta.json` still says v5, no gate fires anywhere, and an older binary would read the shard, drop the key it doesn't know (that is just what `encoding/json` does), and write the loss back on its next save. **One ordinary write, one destroyed field, no error.**

So it doesn't. furrow **parks every top-level key it does not recognise and re-emits it** (sorted, after the known ones) in all three machine-written files — a task shard, a `repos/` review shard, and `meta.json`. An old binary hands a future field back exactly as it found it. Stated as a pair: *the gate stops a bumped layout from being misread; the passthrough stops an unbumped one from being destroyed.*

Four limits, none of them papered over:

- **Not retroactive.** Every release up to and including `v0.9.0` still destroys unknown keys on write. A shared board is safe only once **every** writer has this — including each repo's pinned `sync-task-status.yml@vX.Y.Z` CI. Until the last pin is bumped past this release, keep bumping the layout version on every field addition.
- **Top-level only.** A key inside a known nested object (a `checklist` item) is still dropped. The published JSON Schemas say so: the three top-level objects declare `"additionalProperties": true` (furrow legitimately writes keys it doesn't know, and a schema that called its own output invalid would be a lie), while `$defs/checklistItem` stays `false`.
- **Preserved is not honoured.** An old binary carries a future `"blocked": true` faithfully — and still hands you that task in `furrow next`, and still lets you close it. Passthrough downgrades silent *data loss* to silent *semantic misbehavior*: a real improvement (loss is unrecoverable; misbehavior is fixed by updating the binary), but only the layout version can say "refuse to operate". `furrow lint` warns **`unknown-shard-key`** so the carried-but-ignored case is visible.
- **A hand-edit typo is now permanent.** Misspell a key (`"lables"`) and furrow will preserve it forever — auto-deleting a key it doesn't understand *is* the bug being fixed, so nothing will ever clean it up. `furrow lint` flags it; removing it is a hand-edit of your own. One more reason the shards are furrow's to write, not yours.

Notes on the fields: `id` is frozen and is the stem of both the shard file (`tasks/t-0001.json`) and the body file (`bodies/t-0001.md`); `priority` is a sparse 10-step integer so reordering edits one field instead of renumbering; `status` is a lane defined in `config.toml`; `repos` is the first-class set of repositories the task relates to (`owner/repo` identifiers, 0..N — an empty set means a **draft**, the GitHub-Issues-draft analogue; labels are pure tags, a repo is *not* a label); `closed` is `null` while open and stamped when a task enters the done lane; empty collections serialize as `[]`, never `null`. `value` and `effort` are an optional coarse 1..5 estimate (importance and cost) — both omitted while unset, so dropping an idea into the inbox stays friction-free — and out-of-range scores clamp to 1..5. The JSON Schema for a shard lives at [`docs/schema/furrow.task.v2.json`](docs/schema/furrow.task.v2.json) and for `meta.json` at [`docs/schema/furrow.meta.v2.json`](docs/schema/furrow.meta.v2.json); both are emitted by `furrow schema` (`task` by default, `meta` for the board version).

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
| `add <title>...` | Add a task (or many with --stdin) | `--body`, `--check`, `--dep`, `--draft`, `--effort`, `-l/--label`, `--parent`, `-p/--priority`, `--ref`, `-r/--repo`, `-s/--status`, `--stdin`, `--type`, `--value` |
| `ls [<id>]` (alias `list`) | List tasks (canonical lane->priority->id order), or draw the hierarchy with --tree | `--actionable`, `--archived`, `--blocked`, `--drafts`, `-l/--label`, `-n/--limit`, `--progress-recursive`, `-r/--repo`, `--reverse`, `--since`, `--sort`, `-s/--status`, `--tree`, `--type`, `--until` |
| `show <id>...` | Show tasks with metadata and markdown body (batch-friendly) | `--archived`, `--backlinks`, `--no-body` |
| `next` | Show actionable tasks (in the next-lanes, all deps done) | `--containers`, `-l/--label`, `--lanes`, `-n/--limit`, `-r/--repo` |
| `revisit` | List open tasks needing re-evaluation (agent re-weighing signal) | `-l/--label`, `-n/--limit`, `-r/--repo`, `--stale-days` |
| `search <term>` | Full-text search over task titles and bodies | `-l/--label`, `-n/--limit`, `-r/--repo`, `-s/--status` |
| `stats` | Summarize the board: counts by lane, repo, and label | `-l/--label`, `-r/--repo`, `-s/--status` |
| `board` | Print the active board: store path, scope, lane vocabulary, and schema state | — |
| `edit <id>` | Edit a task's markdown body in $EDITOR | — |
| `note <id> <text>` | Append a paragraph to a task's body and advance its updated time | — |
| `attach <id> <file>` | Attach a media file to a task (copies into bodies/assets/, links it from the body) | — |
| `done <id>` | Move a task into the done lane (stamps closed) | — |
| `move <id> <lane>` | Move a task to a lane | — |
| `reorder <id> <priority>` | Set a task's priority (sparse integer; lower = higher up) | — |
| `retitle <id> <title...>` | Rename a task (updates the shard title and the body heading) | — |
| `set <id>` | Apply several triage edits at once (lane, value, effort, labels) | `--add-label`, `--clear-effort`, `--clear-value`, `--effort`, `--rm-label`, `-s/--status`, `--type`, `--value` |
| `value <id> <1-5>` | Set a task's value estimate (coarse 1..5), or clear it with --clear | `--clear` |
| `effort <id> <1-5>` | Set a task's effort estimate (coarse 1..5), or clear it with --clear | `--clear` |
| `check <id> [item-index]` | Toggle, add, remove, or reword a checklist item | `--add`, `--off`, `--reword`, `--rm` |
| `dep <id> [<dep-id>...]` | Add/remove a task's dependencies, or list them both ways with --list | `--list`, `--rm` |
| `parent <id> [<parent-id>]` | Set, clear (--rm), or list (--list) a task's parent | `--list`, `--rm` |
| `label <id>` | Add and/or remove labels on a task | `--add`, `--remove` |
| `repo <id>` | Attach and/or detach repos (owner/repo) on a task | `--add`, `--rm` |
| `review <repo\|id>` | Record a review: stamp a task's reviewed time, or a repo's last-reviewed clock | `--by` |
| `apply --on <open\|merge> [--ref <src>] [--body-file <path>]` | Apply SetStatus-task directives parsed from PR/commit text | `--body-file`, `--on`, `--open-lane`, `--ref` |
| `sync` | Commit the board, pull --rebase, push (thin git wrapper) | `--all-bodies`, `-b/--body`, `-m/--message` |
| `archive [<id>...]` | Retire done tasks to .furrow/archive/ — by id, or the aged sweep (preview unless --yes) | `--older-than`, `-r/--repo`, `--yes` |
| `migrate <task-file.md>` | Import a Task.md-style tracker into furrow (preview unless --write) | `-l/--label`, `--write` |
| `upgrade` | Raise the board's on-disk layout to this furrow's schema (flag day; preview unless --yes) | `--yes` |
| `lint` | Check index<->body consistency, lanes, deps, links, assets, and config | `--code`, `--exclude-code`, `--severity` |
| `config init` | Write the user-level furrow config (central-board template) | `--path`, `--scope` |
| `config path` | Print the resolved path to the user-level furrow config | — |
| `schema [task\|meta\|repo]` | Print the JSON Schema for a task shard, meta.json, or a repo review shard | — |
| `version` | Print the furrow version (with build commit/date when stamped) | — |
<!-- commands:end -->

On the read commands, `-r/--repo` filters by the first-class `repos` field and is the scope control: a short name resolves case-insensitively at a `/` boundary (`-r furrow` → `akira-toriyama/furrow`; ambiguity is exit 2 with `candidates`), an explicit `-r` overrides the board scope, and `-r ''` shows the whole board. `-l/--label` is a pure tag filter that ANDs with the scope. Within a single `-s` or `-l`, a comma is OR (`-s inbox,backlog`, `-l bug,urgent`); the flags still AND across fields. Both `-s` and `-l` also union when **repeated** (`-s inbox -s backlog` is the same OR-set as `-s inbox,backlog`, and likewise `-l bug -l urgent`), so a repeated filter no longer silently keeps only the last value. `-s` and `-l` part ways on an *unknown* token: a lane is a closed vocabulary, so an unknown `-s` lane **exits 2 with the configured lanes in `candidates`** (symmetric with `move`/`add` — a typo like `-s in_progress` never silently returns `[]`), whereas an unknown `-l` tag just matches nothing (labels are open). When a label filter matches nothing but the name uniquely resolves to a repo that has tasks, furrow exits 2 pointing you at `-r` (the did-you-mean guard). Run `furrow board` to see the lanes and the active scope without provoking an error. When an explicit `-r` hides drafts on `ls`/`next`, one stderr hint line (`N draft(s) hidden — furrow ls --drafts`) points at them; stdout stays pure data.

Global flags: `--json` and `--ndjson` are honored **wherever furrow emits JSON**, not just the read/list commands — `--ndjson` is the same payload as `--json`, compact, one value per line (a list command streams one record per line; a single-object command like a mutation or `board` prints one compact line). Mutations (`done`, `move`, `note`, `set`, `reorder`, `retitle`, `value`, `effort`, `check`, `dep`, `parent`, `label`, `repo`) emit `{before, after, changed}` so a caller sees the effect without a follow-up `show`; `apply` emits a per-directive report (`{on, ref, outcomes}`); `add`/`attach`/`init`/`lint`/`archive`/`migrate`/`version` all honor both flags too. `edit` prefers `$FURROW_EDITOR`, then `$VISUAL`, then `$EDITOR`, falling back to `vi`.

### Command notes

The generated table is the machine-guaranteed surface; these are the behavior contracts that don't fit a one-liner. (Commands whose whole story fits their table row — `init`, `done`, `reorder`, `retitle`, `label`, `schema`, `version` — have no entry, and `attach`, `sync`, `upgrade`, and `config` have their own sections: [attachments](#attaching-images-and-media), [multi-machine sync](#multi-machine-furrow-sync), [the layout gate](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it), [the central board](#central-board).)

- **`add`** — creates one task per stdin line with `--stdin`; `--check` (repeatable) seeds checklist items (body prose alone never populates the shard checklist); an out-of-range `--value`/`--effort` clamps to 1..5 with a stderr note; a title starting with `-` needs a `--` separator (the error says so); `--type` sets the work-item type (a value from `[types].order`, e.g. `epic`; an unknown type is exit 2 with `candidates`).
- **`ls`** — lists in canonical `lane -> priority -> id` order. `--drafts` shows only repo-less tasks (bypasses the board scope). `--since`/`--until` window by `updated` (bare `YYYY-MM-DD` or full RFC3339; a bare `--until` includes the whole day); `--sort updated|created|value|effort` reorders (newest/highest first, `--reverse` flips, unset estimates stay last either way) and makes `-n` the top-N of the sorted set; an unknown `--sort` field or bad date is exit 2. `--archived` reads the archive store with the same filters. Every flat row carries a one-character **state glyph** — ★ actionable (a next lane, every dep done: exactly what `furrow next` hands you), ✓ done, ~ parked, ▣ a container box, · open but not available — and `--json`/`--ndjson` add `actionable`, `blocked_by`, `container`, `stuck` per row. Filter on the state with `--actionable` or `--blocked` (mutually exclusive; both AND with `-s/-l/-r`, so `-s ready --blocked` is the ready rows that are actually stuck). **`--tree`** draws the same facts as the parent hierarchy — one tree per top-level task, or the subtree under `<id>`. Filters still apply and the forest is built over what MATCHED: a task whose parent was filtered out becomes a root rather than disappearing, so `--tree` never shows fewer tasks than the same flags without it; `-n` caps the number of **trees**, not tasks. Under `--tree`, `--json` nests `children` and `--ndjson` streams one whole tree per line; a **container** node adds rolled-up child `progress` (`done/total`; direct children by default, whole subtree with `--progress-recursive`) and a `stuck` flag (open work under it, no actionable descendant). `--type` filters by **effective** type, so `--type task` includes the type-less majority.
- **`show`** — any number of ids in one read, in input order: several ids emit a `--json` array or `---`-separated text (one id keeps the classic single-object shape; `--ndjson` is one task per line at any arity). `--no-body` omits `body_text` — the lean metadata-only batch read. A partial miss still prints the found tasks and exits 1 with `details.missing`; if a missing id is **archived**, the error also carries `details.archived` and the message says to retry with `--archived`. `--backlinks` adds the tasks whose body mentions each one via `[[id]]` (a `mentioned_by` array under `--json`; can't combine with `--archived`).
- **`next`** — "actionable" means: lane is in `[next].lanes` (default `ready` + `in-progress`, so intake stays out) **and** every dep is done. A **container** (an epic) is a box, not work, so it is skipped — `--containers` surfaces a ready one too. `--lanes <csv>` overrides which lanes count as "now" for this call only (config untouched): `next --lanes backlog,ready` surfaces a no-dependency backlog task without first promoting it; an unknown lane is exit 2 with `candidates`. `--json` attaches a `reason` per task (`in_next_lane` — the lane it matched — and `deps_satisfied`).
- **`revisit`** — read-only; `--json` attaches a `revisit` array of `{code, detail}` (`no_repo`, `value_unset`, `effort_unset`, `stale`, `dep_done`) so an agent knows what to fix. Drafts surface regardless of scope. `--stale-days 0` disables the stale signal.
- **`search`** — case-insensitive substring over every title **and** Markdown body, in canonical order; several words are one literal phrase. Honors the same `-s/-l/-r/-n` scope as `ls`. Each hit reports `matched_field` (`title`|`body`) and a one-line `snippet`; a title match never reads the body.
- **`stats`** — `total`, `drafts`, and counts `by_lane` (a complete histogram in configured lane order, 0-count lanes included), `by_repo`, and `by_label` (most-used first). `stats -r ''` describes the whole board — the call that learns the label/repo vocabulary before guessing a `-l`/`-r`.
- **`board`** — the introspection snapshot: store path, discovery `source` (`env`/`local`/`pointer`/`user-config`), repo scope, lane vocabulary, stale/archive windows, and the schema triple (`schema_version`, `binary_schema_version`, `schema_state`, `writable`). It **never fails on a version mismatch — it reports one**, so it is the pre-flight that diagnoses a board no other command can open.
- **`edit`** — opens `bodies/<id>.md` in the editor; with no TTY it prints the path instead. Prefer `note` for progress records: a direct file edit does not advance `updated`.
- **`note`** — appends the text as a new paragraph **and** advances `updated` in one write, so `lint`'s `reconcile-gap` stays honest for progress recorded in prose; `-` as the text reads the note from stdin (multi-line). `--json` adds `appended` beside the envelope (`changed` tracks metadata only, so it is `[]` when just the body moved).
- **`move`** — clears `closed` when a task leaves the done lane (and `done` stamps it).
- **`value` / `effort`** — an out-of-range score clamps to 1..5 **and is signaled**: a `clamped` key nested by field (`clamped.value.{requested, stored}` / `clamped.effort.{…}`) in the `--json` envelope plus a stderr note, so an explicit arg is never silently rounded. Via `add`, the clamp is stderr-only (`add --json` prints the created task, no envelope). `--clear` unsets.
- **`set`** — the routine triage edits (lane, value, effort, labels, type) in **one** write instead of four commands. At least one change required; an unknown lane/type is exit 2 with `candidates`; under `[labels].required` a set that would strip the last label is refused.
- **`check`** — indexes are zero-based; marking done is an idempotent set, not a toggle (`--off` unchecks); `--add` appends verbatim (repeatable); `--rm` deletes at an index; `--reword` replaces its text. Mode flags are mutually exclusive; an out-of-range index is exit 2.
- **`dep`** — variadic add/remove in a single all-or-nothing write (a bad dep-id aborts without partial change); acyclic and idempotent. `--list` reads (never mutates) the dependency neighborhood **both ways** — `depends_on` and `blocks` (what unblocks if I finish this) — resolved to id+title+lane; a dangling dep resolves to its id alone (lint flags it).
- **`parent`** — re-files a task in the hierarchy (`--rm` detaches to top-level). Acyclic: the parent must exist, self-parenting and loop-closing edges are exit 2. A **done** parent is allowed — filing a leftover under the epic it shipped with is a legitimate record — and `lint` warns `parent-done` on an open task left under a closed one. `--list` reads both directions (`parent`, `null` when top-level; `children`, `[]` when none).
- **`repo`** — each value must be a full `owner/repo` or a short name uniquely resolving against the board's repos (else exit 2 with `candidates`); a task with no repos is a draft.
- **`review`** — an id-shaped argument stamps that task's `reviewed` timestamp (tracked apart from `updated`: a review changes no content); anything else records a per-repo review clock. `--by human` (default) advances the staleness-nudge clock; `--by agent` logs a sweep without advancing it, so an autonomous re-evaluation never stops furrow nudging a human.
- **`apply`** — parses `SetStatus-task: <body-link> [<lane>]` directives from PR/commit text (stdin or `--body-file`) — the CI hook behind [auto status updates](#ci-auto-update-a-tracker-from-prs). `--on open` nudges to in-progress; `--on merge` applies the lane. Validation is non-blocking.
- **`archive`** — with ids it retires exactly those (each must be in the done lane, else exit 2 — no stranding live work); with none it sweeps aged done, board-wide by default, `-r` (repeatable) scoping it per repo, `--older-than` adjusting the age guard (sweep-only flags; combining them with ids is exit 2). A task's attached media travels with it into `.furrow/archive/`, never orphaned in the hot store. Previews unless `--yes`.
- **`lint`** — every finding carries a stable kebab-case `code` (`dangling-link`, `dep-cycle`, `parent-cycle`, `conflict-marker`, `unknown-shard-key`, `schema-outdated`, `archive-backlog`, …); branch on the code, not the message — the `id` field is contextual (a task id, an asset name, an `owner/repo`, `meta`, or `config`). Errors: cycles on either edge (dep or parent — a parent cycle has no root, so every task in it belongs to no tree), a done-lane task with no `closed`, a body carrying git conflict markers (a half-merged progress record; markers inside a ``` fence are documentation and not flagged). Warns: an open task under a done parent, dangling `[[id]]` links (archived ids are not dangling), reconcile gaps, asset hygiene (missing/orphan/≥5 MiB), a board whose layout is behind the binary, a file carrying keys this furrow doesn't know, config clamp warnings, and the `[lint].archive_done` backlog nudge. Narrow with `--code` (allow-list) / `--exclude-code` (deny-list; wins) / `--severity error|warn` — an unknown `--code` token is exit 2 with the vocabulary in `candidates`, while an unknown `[lint].ignore_codes` config entry only warns (clamp-don't-reject). **The filter drives the exit code**: a filtered-out problem is as if never found, so excluding or ignoring the last error exits 0 (the point — silence a permanently-dead check without reddening CI), and `--severity warn` always exits 0.
- **`migrate`** — dry-run by default (`--write` applies); unmapped headings and `[[wikilink]]`s are reported, never dropped.

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
| `1` | a **specifically requested id** was not found (e.g. `show <id>`) — never an empty list |
| `2` | bad usage / validation |
| `3+` | internal / I/O error |
| `130` / `143` | a `SIGINT` / `SIGTERM` interrupted the run (128+signal by Unix convention) — e.g. Ctrl-C during `furrow sync`, which returns `sync-interrupted` (retryable). A deliberate `sync-conflict` is not a cancellation and keeps its exit `3`. |

The **schema gate** is the one place where the exit code, not the id, is what tells you which side is stale — so it is worth stating explicitly:

| id | Code | Which side is stale | What to do |
|---|---|---|---|
| `schema-upgrade-required` | `2` | the **board** — it is behind this binary. Fully readable, but **read-only** | `furrow upgrade` (a flag day: bump every pinned caller **first**) |
| `schema-too-new` | `3` | the **binary** — the board declares a layout it does not know | update furrow; in CI, bump the `sync-task-status.yml@vX.Y.Z` pin |

Both carry `details {board_schema, binary_schema}`. Note `schema-too-new` is a *deliberate* refusal that still exits `3`: the fix is the binary, not the input. To ask "can I write here?" **without provoking an error**, read `furrow board --json`'s `writable` / `schema_state` (it never fails on a mismatch — see [The layout version gates writes](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it)); `furrow lint` warns `schema-outdated`.

The same contract is printed by `furrow --help` (and each affected command's help), so it is discoverable from the binary, not just here.

On a non-zero exit, furrow prints a structured error object to stderr:

```json
{"error":{"code":2,"id":"t-0001","message":"unknown lane \"backlogg\""}}
```

When an input almost resolved — an ambiguous repo short name, an unknown lane, a
parent command's unknown subcommand (`config show`), or a label that uniquely
names a repo (the did-you-mean guard) — the envelope also carries
`"candidates": [ … ]`, so a script picks an alternative from the array instead
of parsing the message prose. Likewise a partial `show` batch
(some ids unknown) still prints the found tasks and exits 1 with
`"details": {"missing": ["t-…", …]}` — branch on the array, never the message.
The version gate uses the same shape: `schema-upgrade-required` (exit 2) and
`schema-too-new` (exit 3) both carry
`"details": {"board_schema": N, "binary_schema": M}`.

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
    types: [opened, reopened, ready_for_review, closed]
permissions:
  contents: read
  pull-requests: write
jobs:
  sync:
    uses: akira-toriyama/furrow/.github/workflows/sync-task-status.yml@v0.10.0
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

With a board in effect (pointer or user-level):

- `furrow add "…"` unions the scope repo into the task's `repos` (an explicit
  `-r x` adds to it rather than replacing); `add --draft` suppresses exactly
  that union. The board's literal `label` (if any) still unions into labels.
- `furrow ls|next|revisit` filter to the scope repo — **silently** (no banner).
  A user-level board can opt out with `auto_filter = false` to show the whole
  board while `add` still attaches the repo; a pointer always filters. Scope
  control is `-r`: pass `-r ''` to see the whole board for one command, or
  `-r <repo>` for another repo. An explicit `-l tag` filters *within* the scope
  (it ANDs; it does not clear it). When the scope hides drafts, one stderr hint
  line points at `furrow ls --drafts`.

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
   `:card_file_box: chore(board): sync via furrow`; override with `-m`.
2. `git fetch`, then `git rebase --autostash @{u}` — rebasing onto the upstream
   **tracking ref**, never `FETCH_HEAD`, so a co-writer's concurrent fetch in a
   shared checkout can't make it `fatal: Cannot rebase onto multiple branches`
3. `git push` (one pull→push retry on non-fast-forward)

Per-task shards make true conflicts rare — two machines *adding* tasks touch
disjoint files; only both sides editing the *same* task conflicts. When that
happens sync **aborts the rebase automatically** (the board is never left with
conflict markers; your local sync commit survives) and exits 3 with an error
envelope carrying `"id": "sync-conflict"` and `"details": {"paths": [...]}` so
an agent knows exactly which shards to reconcile. The progress object
`{committed, pulled, pushed, conflict, committed_bodies, pending_bodies,
pending_stash}` is printed to stdout on success and failure alike (the lists are
omitted when empty).

### The autostash git can't give back

Step 2 stashes your *other* dirty files (anything outside the sync commit) so the
rebase can run, and re-applies them afterwards. When that re-apply conflicts with
what was just pulled, git does something quiet: it keeps your changes **in the
stash**, prints a warning to stderr, and **exits 0**. The rebase "succeeded"; your
edits are simply no longer in the working tree. Nothing in an exit code can see it
— and if the file was a half-written `bodies/<id>.md`, that is furrow's progress
record hanging in mid-air.

So sync inspects the stash itself, and reports what it finds:

- The sync that strands one **fails** — `"id": "sync-stash-stranded"` (exit 3),
  `"details": {"pending_stash": [{"ref", "commit", "paths"}]}`, and nothing is
  pushed. Recover with `git stash pop`, then re-run.
- Any autostash entry still sitting there is reported by **every** sync (in
  `pending_stash`, plus a stderr warning) until it is popped or dropped — a
  leftover nobody is told about is exactly the failure being fixed. Your own
  `git stash` entries are yours, and are never touched or reported.
- The index such a failure leaves behind (unmerged paths, no operation in
  progress) is explained rather than relayed: a pre-flight fails with
  `"id": "sync-unmerged"` (exit 2), naming both the unmerged paths and the stash
  still holding the other half — instead of git's opaque `notes.md: unmerged (…)`.

And the wreckage such a failed re-apply leaves behind — conflict markers written
into the working-tree file — is refused at the door: a body carrying
`<<<<<<<` / `=======` / `>>>>>>>` is **never auto-committed** (`"id":
"body-conflict-marker"`, exit 2, nothing committed). A commit cannot be
un-published, and `furrow lint`'s `conflict-marker` rule (error) covers any that
got in before.

On a **successful** sync it also prints a repo-scoped `revisit` summary: open
tasks with a done dependency (`dep_done`) or gone stale (`stale`) — a nudge to
run `furrow revisit` for detail. Human output adds one line,
`revisit: <n> dep_done, <n> stale (<scope>) — furrow revisit` (`<scope>` is
the current repo's short name, or `board` when there is no auto repo);
`--json`/`--ndjson` gain a `revisit` key (`{dep_done:[ids], stale:[ids]}`)
with the id lists. Both are omitted entirely when the board is clean.

Because a bot or a second operator can be pushing at any moment, a shared
checkout races two ways, and sync handles each by its likely cause. (1) The
pre-flight can catch *their* rebase mid-flight — sync **waits it out** with a
bounded backoff (~5s); if it is still going it exits 3 with `"id": "sync-busy"`,
a **retryable** class (not the `exit 2` "fix the args" class) signalling that
re-running usually clears it (they will have finished, or a rebase here is
genuinely stuck and needs a manual `git rebase --abort`). (2) *Their* `git fetch`
can briefly contend a ref/index lock while ours runs — sync retries the pull
through the same backoff. A live race clears in well under a second, so if a lock
still blocks after the budget it is almost certainly a **stale** lock (a crashed
git left a `.git/*.lock`); sync then fails **terminally** telling you which lock
to remove, rather than looping an agent forever on a `sync-busy` that will never
clear.

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
warnings flow through and are surfaced non-blockingly after a merge or rebase. A
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
           ├── config.toml          # standalone = true
           └── meta.json, tasks/, bodies/
   ```

2. **Register it in your user-level config so it resolves from inside the checkout.** A board in a subdirectory isn't found by walking up from the code (that finds the *code* repo's git), so scope it explicitly — the same `[[board]]` mechanism as a [central board](#user-level-config-no-per-repo-file):

   ```toml
   # ~/.config/furrow/config.toml
   [[board]]
   path   = "/abs/path/to/<code-repo>/claude_workspace/.furrow"
   scopes = ["/abs/path/to/<code-repo>"]   # `furrow` run anywhere under here uses this board
   repo   = "auto"                          # auto-tag new tasks with the checkout's owner/repo
   ```

Then set **`standalone = true`** in the board's `config.toml` (see [Configuration](#configuration)). It changes **only wording, never behavior**: `furrow upgrade` drops the shared-board flag-day checklist and the "run `furrow sync` to publish" line — a single-machine board has no pinned CI to coordinate and no remote to publish to. The write gate, schema, and on-disk format are byte-for-byte identical to a shared board.

A fully separate directory (e.g. `~/furrow-boards/app/.furrow`, outside the code repo) works too — same two-config setup, just a different `path`/`scopes`.

---

## Configuration

`.furrow/config.toml` is the one human-edited file in the store. furrow only **reads** it (it never rewrites it) and applies a **clamp-don't-reject** policy: unknown keys are ignored and out-of-range values fall back to a safe default with a warning surfaced by `furrow lint` — so a typo can never break the tool.

```toml
# standalone = false              # a local single-machine board (no remote / `furrow sync` / CI);
                                  # when true, `furrow upgrade` drops the shared-board flag-day wording
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
width  = 5                        # number of random suffix chars, e.g. t-k3m9p

[archive]
older_than_days = 30              # default window for `furrow archive --older-than`

[lint]
# archive_done = 0                # `furrow lint` warns once this many done tasks are old enough to archive (0 = off)

[revisit]
stale_days = 30                   # `furrow revisit` flags a task with no update in this many days (0 disables)

[ui]
theme = "auto"                    # front-end display preference: auto | dark | light (NO_COLOR is always respected)

[alias]                           # name your frequent filters; `furrow <name> …` expands git-style
triage = "ls -s inbox,backlog"    #   `furrow triage -r app` -> `furrow ls -s inbox,backlog -r app`
wip    = "ls -s in-progress"       #   the remaining args append, so all existing flags/scope compose
```

A board `[alias]` names a frequent command string; `furrow <name> <extra args>` expands it git-style (the alias tokens replace the name, the rest of the argv is appended), so every flag, board scope, and auto-filter composes for free. It lives in the **board** config (not the user-level one), so it syncs with the board and every machine/agent shares it. A real command always wins — an alias that shadows a builtin (`ls`, `next`, …) is inert and `furrow lint` flags it (`alias-shadow`); a blank alias value is dropped with a clamp warning. Put global flags *after* the alias (`furrow triage --json`), as with git.

`standalone = true` marks a local single-machine board (no remote / `furrow sync` / CI). It changes **only wording** — never behavior, the schema gate, or the on-disk format: `furrow upgrade` drops the shared-board flag-day checklist and the `furrow sync` publish line, which would only misdirect a solo operator with no fleet to coordinate. Default `false` (shared board). See [Standalone](#standalone-a-local-board-with-no-remote).

`done` stamps `closed`; moving a task *out* of the done lane clears it. Other terminal lanes (e.g. `icebox` — parked, not finished; `waiting` — the GTD *Waiting-For* lane for work delegated or blocked on someone external) do **not** stamp `closed`, which is why parked tasks are never archived.

---

## Determinism

furrow's write path is byte-stable on purpose. Every shard write goes through one marshaller (`core.MarshalTask`) with a fixed contract: struct-field key order, 2-space indent, `SetEscapeHTML(false)` (so CJK and `< > &` survive verbatim), empty collections as `[]` not `null`, sorted-and-deduped label/dep sets, whole-second UTC RFC3339 timestamps, and a trailing newline. The result is that the bytes furrow writes are identical to what a human or an agent would hand-edit, and a `Save` rewrites only the shards whose bytes actually changed — so re-saving an untouched store produces **zero git churn** — diffs show only the field you actually changed.

`meta.json` goes further: a `Save` does not rewrite it *at all* (the board's declared version is what the write is checked against — see [the layout version gate](#the-layout-version-gates-writes-and-only-furrow-upgrade-raises-it)), so the only commits that ever touch it come from `furrow init` and `furrow upgrade`.

---

## Status

- **Working:** furrow is **CLI-only** — the core domain (`internal/core`) with
  the first-class `repos`
  field (board layout v5 + the two-sided version gate: read-refuse a newer board,
  write-refuse an older one, and `furrow upgrade` as the only raiser), config
  loader, filesystem store, app
  coordinator, the full CLI (incl. `repo`, drafts, `-r` scoping, `apply`, and
  `sync`), and **`migrate`** (importing a
  legacy `Task.md`). `go test ./...` + golangci clean; `sh scripts/check.sh`
  runs the full verification (core + store + app + cli + migrate).
- **Released:** tags are cut with GoReleaser → the Homebrew tap (see the
  [Releases page](https://github.com/akira-toriyama/furrow/releases); the
  bundled task-status Action ships since `v0.5.0`, the first-class `repos` field
  since `v0.6.0`, board layout v4 since `v0.8.0`, board layout v5 since `v0.10.0`). The nix `flake.nix` carries a
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
