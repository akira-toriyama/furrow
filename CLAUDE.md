# CLAUDE.md

Guidance for working in this repository (and for furrow itself as a tool).

## For Claude Code — integration contract (read first)

furrow's own tasks live on the **central board** (the private
`akira-toriyama/projects` repo) — this repo deliberately has **no local
`.furrow/`**, so `furrow` commands run here resolve to the central board via
the user-level config. When you work with any furrow store:

- furrow **OWNS the `.furrow/tasks/*.json` shards** (one per task), the
  `.furrow/repos/*.json` review shards (one per repo — `furrow review`), **and
  `.furrow/meta.json`**. **Never hand-edit them.** They are written by a
  single deterministic marshaller; manual edits will fight the next `furrow`
  write and churn git. Mutate tasks via commands, not the files.
- `.furrow/bodies/*.md` **ARE** safe to edit by hand or by you — that is the point
  of the hybrid store. One body file per task id, 1:1 with its shard.
- Canonical commands: `furrow add|ls|show|next|revisit|search|stats|board|edit|note|attach|done|move|set|reorder|retitle|value|effort|check|dep|parent|label|repo|review|sync|apply|archive|upgrade|lint|config|init`.
  `set <id>` combines lane/value/effort/labels/**type** in one write (the triage
  shortcut for move+value+effort+label); `dep <id> <dep-id>...` is variadic
  (add/remove several in one write), and `dep <id> --list` is the read-only
  reverse-deps view — both directions (`depends_on` / `blocks`) resolved to
  id+title+lane, one `--json` object — so "what waits on this?" is a command,
  not a full-board dump; `archive <id>...` retires specific done
  tasks by id (vs the age sweep).
- **`ls --tree [<id>]` draws the parent hierarchy** — one tree per top-level task,
  or just the subtree under `<id>`. It answers "what leads to this goal?", which was
  otherwise a full-board dump and a script. Every filter still applies, and the
  forest is built over what MATCHED: a task whose parent was filtered out becomes a
  root, never disappears (so `--tree` can't show fewer tasks than the same flags
  without it), and `-n` caps the number of TREES, not tasks. Each node carries the
  two facts a flat list can't: **`actionable`** (exactly what `furrow next` would
  hand you — in a next lane, every dep done; shown as ★) and **`blocked_by`** (the
  deps that are NOT done — what is actually in the way). `--json` nests `children`;
  `--ndjson` streams one whole tree per line.
- **`parent <id> <parent-id>` moves a task in the HIERARCHY; `--rm` detaches it
  (top-level); `--list` is its read-only both-directions view (`parent`, which is
  `null` for a top-level task, and `children`, `[]` when none — same shape as `dep
  --list`).** Before it, `parent` was write-once at `add --parent`, so re-filing a
  task meant hand-editing a shard — the thing this file tells you never to do; if
  you ever reached for `sed` on a `tasks/*.json`, this is the command you wanted.
  Re-parenting is acyclic (missing parent / self / a loop = exit 2), and a **done**
  parent is deliberately allowed — filing a leftover under the epic it came from is
  a legitimate record. `lint` names the two states that follow: `parent-cycle`
  (error, the git-merge backstop) and `parent-done` (warn — an open task under a
  closed epic).
- **`type` is first-class; an epic is a container, declared not inferred (schema
  v5).** A task carries a `type` from the closed `[types].order` vocabulary
  (default `task`, `epic`; `default`/`containers` alongside it, same
  clamp-don't-reject + exit-2-with-`candidates` discipline as lanes — and the
  built-in default applies on a board with no `[types]` block, so an old board's
  epics are containers the moment its binary is v5). A **container** type (default:
  `epic`) is a box: `furrow next` SKIPS it (a box is not work — surface a ready one
  with `next --containers`), `ls --tree` shows its rolled-up child `progress`
  (`{done,total}`, direct children by default, whole subtree with
  `--progress-recursive`) and a `stuck` flag (open work under it but no actionable
  descendant, org-mode's stuck-project — always walks the subtree, through
  sub-epics), and `revisit`/`sync` gain `children_done` (all children done —
  consider closing) and `stuck_container`. Set with `add --type` / `set --type`,
  filter with `ls --type` (by EFFECTIVE type, so `--type task` includes the
  type-less majority), read the vocab with `furrow board`. **Not inferred from
  structure**: an empty epic is a legitimate declaration (never nags
  `children_done`), and a plain task that happens to have children is NOT a box.
  furrow never auto-closes a container. `lint` warns `unknown-type` (a stray type)
  and `dep-mirrors-children` (a task whose deps point at its own children — the
  pre-v5 epic↔slice workaround; unwind with `dep <id> --rm`). The invariant
  `[types].default ∉ [types].containers` is enforced (a container default is
  clamped, else every type-less shard would vanish from `next`).
- **Repos are the scope; labels are pure tags.** A task's repositories live in
  the first-class `repos` field (`owner/repo`, 0..N; `[]` = a **draft**, the
  issue-draft analogue). `-r` is the scope control on reads: a full
  `owner/repo` or a short name resolving uniquely against the board's repos;
  an explicit `-r` overrides the board scope, `-r ''` = the whole board. `-l`
  filters by tag and ANDs with the scope. Within a single `-s` or `-l`, a comma
  is OR (`-s inbox,backlog`); flags still AND across fields. Both `-s` and `-l`
  also union when **repeated** (`-s inbox -s backlog` == `-s inbox,backlog`,
  `-l bug -l urgent` == `-l bug,urgent`), so a repeated filter no longer silently
  keeps only the last value. `-s` and `-l` diverge
  on an unknown token: a lane is a closed vocabulary, so an unknown `-s` lane is
  **exit 2 with the configured lanes in `candidates`** (symmetric with move/add — a
  typo never returns a silent `[]`), while an unknown `-l` tag just matches
  nothing. `furrow board [--json]` prints the store path, discovery source, repo
  scope, and lane vocabulary (lanes/next/default/done/terminal) — read it to
  learn the lanes and active scope without provoking an error. On a board, `add`
  unions the scope repo into `repos` (`--draft` suppresses exactly that); `ls
  --drafts` lists the repo-less tasks; `furrow repo <id> --add|--rm`
  attaches/detaches later.
- `--json` and `--ndjson` are honored **wherever furrow emits JSON** — reads,
  mutations, and reports alike (not just the list commands); **JSON goes to
  stdout only** (logs and errors go to stderr). `--ndjson` is the same payload
  compact, one value per line: a list command streams one record per line, a
  single-object command (a mutation's `{before,after,changed}`, `board`,
  `attach`, `init`, `version`, the `apply` report) prints one compact line, and
  `lint` streams one problem per line — so a line-oriented reader never gets a
  silent human-prose degrade. Filter reads with `--status/-s`, `--label/-l`,
  `--repo/-r`, `--limit/-n` — so you rarely need jq. Each `lint` problem carries
  a stable kebab-case `code` (`dangling-link`, `dep-cycle`, `parent-cycle`,
  `parent-done`, `orphan-asset`,
  `conflict-marker`, `unknown-shard-key`, …) — branch on it, not the message, since the `id` field
  is contextual (a task id, an asset name, an `owner/repo`, `meta`, or `config`).
  Mutations (`done|move|set|reorder|value|effort|check|dep|parent|label|repo`) with
  `--json` emit
  `{before, after, changed}`; an out-of-range `value`/`effort` (also via `set`/
  `add`) clamps to 1..5 and is signaled — a `clamped {requested, stored}` key in
  the envelope plus a stderr note, so an explicit arg is never silently rounded.
  `add --stdin` bulk-creates one task per stdin line;
  `next --json` attaches a `reason` (`in_next_lane`, `deps_satisfied`) and
  `revisit --json` a `revisit` array (`no_repo`, `value_unset`, `effort_unset`,
  `stale`, `dep_done`) per task.
- **Batch reads by id: `show <id>... --no-body`** — any id set in one process,
  metadata only (no `body_text`), input order. `--json` = array for ≥2 ids (a
  single id keeps the classic object), `--ndjson` = one line per task at any
  arity. A partial miss still emits the found tasks and exits 1 with
  `details.missing` — branch on that array. If a missing id is **archived**, the
  error also carries `details.archived` (the subset retrievable with
  `--archived`) and hints it in the message; `show <id> --archived` /
  `ls --archived` read the sibling `.furrow/archive/` store (same output shapes),
  so a retired task never falls off the read API.
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
  in-flight git and exits **128+signal (130 for SIGINT, 143 for SIGTERM)** with id
  `sync-interrupted` — retryable, just re-run (a genuine conflict is never masked
  by the signal: it still surfaces as `sync-conflict` with its `details.paths`,
  keeping its exit 3). Branch on the `id`, not the exit
  code, to tell these apart.
  **A sync can lose your WORK without losing the BOARD, and git's exit code only
  ever talks about the board.** `--autostash` stashes your other dirty files for
  the rebase; when the re-apply conflicts with what was pulled, git keeps them **in
  the stash**, warns on stderr, and **exits 0** — the edits are just gone from the
  working tree, and if one was a half-written body, that is furrow's progress
  record hanging in mid-air. So sync probes the stash: the run that strands one
  fails with id **`sync-stash-stranded`** (exit 3, nothing pushed) carrying
  `details.pending_stash` (`[{ref, commit, paths}]`), and ANY leftover autostash is
  re-reported by **every** subsequent sync in the `pending_stash` progress key until
  it is popped or dropped (your own `git stash` entries are never reported). The
  index that failure leaves behind (unmerged, no operation in progress) is explained
  by a pre-flight — id **`sync-unmerged`** (exit 2), naming the paths AND the stash
  still holding the other half — instead of relaying git's opaque `notes.md:
  unmerged (…)`. The
  wreckage such a failed re-apply leaves in the file — conflict markers — is refused
  at the door: a body carrying `<<<<<<<`/`=======`/`>>>>>>>` is **never**
  auto-committed (id **`body-conflict-marker`**, exit 2, `details.bodies`
  `[{id, path, lines}]`, nothing committed), because a commit cannot be
  un-published; `furrow lint` flags any that got in already (`conflict-marker`,
  **error**). A marker inside a fenced code block is documentation, not corruption,
  and is not flagged.
  A successful sync also gains a `revisit` key
  (`{dep_done:[ids], stale:[ids], unreviewed:[{repo,days}]}`, repo-scoped,
  omitted when empty) — the loop-visible staleness nudge; run `furrow revisit`
  for task detail, `furrow review <repo>` to reset a repo's `unreviewed` clock.
- **The board's layout version gates writes, and it is an INPUT — never an
  output.** A binary writes only a board whose `meta.json` already declares
  exactly its own `schema_version`; an ordinary write NEVER raises it (that is
  what `furrow upgrade` is for). Two stable ids, told apart by exit code alone:
  **`schema-upgrade-required`** (exit 2 — the BOARD is behind this binary; it
  stays fully readable but is read-only until `furrow upgrade` runs) and
  **`schema-too-new`** (exit 3 — the BINARY is behind the board; update furrow /
  bump the pin). Both carry `details {board_schema, binary_schema}` — branch on
  the id, not the message. **`furrow board [--json]` reports the state without
  failing**: `schema_version` (what the board declares; 0 = absent/unreadable),
  `binary_schema_version`, `schema_state` (`current`|`outdated`|`too-new`|
  `unreadable`) and `writable` — it is the last command that still works when
  board and binary disagree, so read it as a pre-flight instead of watching every
  task read fail with "task not found". `furrow lint` warns `schema-outdated`
  (never errors) meanwhile.
- **A shard key this binary does not know is PRESERVED, not dropped.** The gate
  above only fires when someone BUMPS the version; a field added without a bump
  would be silently destroyed by the next ordinary write (`encoding/json`'s
  lenient unmarshal drops it, the marshaller writes the loss back — one `retitle`,
  one dead field, no error). So `core.Unmarshal*` now parks every unknown
  **top-level** key and `core.Marshal*` re-emits it, sorted, after the known ones —
  in all **three** machine-written files: a task shard, a `repos/` review shard,
  and `meta.json`.
  Four things it does NOT mean, all load-bearing for an agent: (a) it is **not
  retroactive** — every furrow ≤ v0.9.0 still destroys those keys on write, so a
  shared board is safe only once EVERY writer has passthrough, including every
  pinned `sync-task-status.yml@vX.Y.Z` CI caller; (b) **top-level only** — an
  unknown key inside a known nested object (`checklist[]`) is still dropped;
  (c) **preserved ≠ honoured** — an older binary carries a future `"blocked": true`
  faithfully and still hands you that task in `furrow next` and lets you close it,
  so `furrow lint` warns **`unknown-shard-key`** (SevWarn, naming the keys and
  blaming the task id / the `owner/repo` / `meta`) to make "carried but IGNORED —
  update furrow" visible; (d) the `--json`
  views project the keys THIS binary knows — an unknown key lives on disk, not in
  the view (preserving beats displaying). Corollary for hand-edits: a typo in a
  hand-edited shard (`"lables"`) is now **permanent** — nothing removes it, because
  auto-deleting a key we don't understand IS the bug being fixed. That is also why
  `lint` must cover all three files and the published schemas all declare
  `additionalProperties: true`: the flip made the schema stop rejecting a typo, so
  `lint` is the only detector left. One more reason
  the shards are furrow's to write, not yours.
- `furrow edit <id>` with no TTY **prints the body file path** instead of opening
  an editor — read/edit that file directly. But a direct file edit does NOT touch
  the shard's `updated`, so it goes stale and `lint`'s `reconcile-gap` (a done
  dep's `closed` vs. `updated`) misfires on a task reconciled only in prose. To
  record progress/stop-points/next-steps across sessions, prefer **`furrow note
  <id> "<text>"`**: it appends the text as a new paragraph to the body AND stamps
  `updated`, in one write (`-` reads the note from stdin for multi-line). Unlike the
  `apply` annotation path it never dedupes and always advances `updated`, so the
  time-based lint stays honest. `--json` emits the `{before,after,changed}`
  envelope plus `appended` (the text), since `changed` tracks metadata only.
- Exit codes: `0` ok — **including an empty query result** (`ls`/`next`/`revisit`
  matching nothing still succeeded, so `set -e` never trips on "no work") / `1` a
  **specifically requested id** was not found (e.g. `show <id>`), never an empty
  list / `2` bad-usage|validation / `3+` internal|IO (a signal-interrupted run is
  `130`/`143` = 128+signal, not `3` — see `sync-interrupted` above). The contract
  is also in the binary's own `--help`. On non-zero, an `{"error":{"code","id","message"}}`
  object is on stderr — plus an optional machine-actionable `details` (see `sync`
  above) and an optional `candidates` array when an input almost resolved (an
  ambiguous repo short name, an unknown lane, a parent command's unknown
  subcommand like `config show`, or `-l <x>` matching nothing while `x` uniquely
  names a repo — the did-you-mean guard). Branch on the array, never regex the
  message.
- furrow is **CLI-only and non-interactive**; there is no in-repo TUI. A TUI/GUI
  is a **separate front-end** that drives furrow through its CLI/JSON contract —
  planned: **ridge** (github.com/akira-toriyama/ridge, a charm-v2 TUI, a CLI/JSON
  client) and **loom** (github.com/akira-toriyama/loom, a from-scratch TUI
  framework, future/gated). Destructive ops guard themselves: `furrow archive`
  previews unless `--yes`.

## What this is

furrow — an alternative to GitHub Projects/Issues: a clonable, git-native,
plain-text task tracker. One central board can back many repos (tasks carry
their repositories in the first-class `repos` field) or a store can live
repo-local. Structured metadata lives in
one JSON shard per task, `.furrow/tasks/<id>.json` (deterministic,
machine-written), with the board-wide layout version in `.furrow/meta.json`
(`{"schema_version": 5}`); long-form prose lives in
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
                      # build/vet/test + golangci + schema/config drift + a CLI
                      # smoke + (if goreleaser & syft are installed) a release
                      # dry-run. Green here == green build/govulncheck CI; the
                      # only CI-side extras are the TOML/workflow/commit-message
                      # lints (taplo, zizmor, glyph). Run it before finishing.
```

Everything is verifiable without a terminal:
- **CLI**: directly runnable headless (`init/add/ls --json/next/done/migrate/lint`).
  Tests cover core + store + app + cli + migrate.
- **Determinism / drift**: the golden round-trip test,
  `scripts/check-marshal-singlepath.sh` (encoders **and** decoders — a raw
  `json.Unmarshal` would drop a shard's unknown keys),
  `scripts/check-schema-write-guard.sh` (no ordinary write may name
  `core.SchemaVersion`), `TestShardFieldsGolden` (the shard's on-disk shape is
  frozen; changing it demands a deliberate `-update-fields` + a version-bump
  decision), **`TestFrozenBoardRoundTripsByteIdentical`** (a real board's BYTES,
  committed under `internal/store/fsstore/testdata/frozen-board/`, that Load→Save
  must reproduce exactly — the one fixture the code under test did not write), and
  the schema/config drift diffs (in `check.sh`) guard the load-bearing invariants.
- **The release pipeline**: it used to run only on a tag, so a defect in
  `.goreleaser.yaml`/`release.yml` surfaced *after* GoReleaser had published the
  draft and pushed the cask (v0.8.0 shipped broken twice). `build.yml` now runs a
  real `--snapshot` build (with syft, so the `sboms:` pipe actually runs) on every
  PR and asserts the artifact shape with
  **`scripts/check-release-artifacts.sh`** — every path the attest steps feed to
  `actions/attest` resolves to a real file (`sbom-path` is NOT glob-expanded), each
  SBOM is SPDX-2.3 (the predicate type the READMEs document is derived from it),
  and `checksums.txt` names each archive exactly once as a whole field (a
  substring match also hits the SBOM line). `release.yml` runs the SAME script to
  derive its version, so the paths asserted on the PR are the paths it attests.
  Note `goreleaser check` does NOT cover any of this — it only validates the
  config's schema.

## Source-of-truth references

Consult these before adding behavior, and keep terms consistent with them:
[docs/architecture.md](docs/architecture.md) (layers), [docs/glossary.md](docs/glossary.md)
(ubiquitous language), [docs/non-goals.md](docs/non-goals.md) (what furrow won't do).

## Non-obvious constraints — read before editing

### Layer rules (the spine)
`internal/core` is **pure** (stdlib only — no cobra, os, or filepath).
Ports (`Store`, `Clock`) are interfaces **defined in core**;
`internal/store/fsstore` is the **only** package that touches the filesystem;
`internal/store/memstore` is its in-memory twin for tests. `internal/cli` is the
only presentation layer and mutates **only** through `internal/app.App` (the
single mutation funnel); any TUI/GUI front-end (e.g. ridge/loom) lives out-of-repo
and drives the CLI, not these packages. Crossing a layer means a port is missing —
add the interface, don't add the import.

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

**Unknown-key passthrough — the other half of the version gate.**
`internal/core/passthrough.go`: `core.UnmarshalTask`/`UnmarshalRepo`/`UnmarshalMeta`
park every **top-level** key the binary does not know in an **unexported** `extras`
field, and the matching `Marshal*` re-emit them, **sorted, after the known keys**.
The gate (below) stops a *bumped* layout from being misread; the passthrough stops
an *unbumped* one from being destroyed — because a field added without a bump
leaves `meta.json` still saying v4, so no gate fires anywhere and an old binary's
lenient unmarshal drops the key and writes the loss back on the next ordinary
write. Two rules make it safe, and both are load-bearing:

- **"Is this key known?" must be answered with `encoding/json`'s OWN matcher, not
  an approximation of it.** json matches struct fields case-**IN**sensitively (a
  shard key `"BODY"` populates `Task.Body`), so a case-SENSITIVE set would park
  `BODY`, re-emit it, and leave a shard carrying both `body` and `BODY`. But the
  obvious fix — a `strings.ToLower` set — is **also wrong**, and worse, because
  json matches by Unicode simple case-**FOLDING**, and lowercasing is a different
  function. They disagree in both directions, and each direction is a corruption
  bug that shipped-and-was-caught in review:
  - json folds it, `ToLower` doesn't — `"statuſ"` (U+017F) is fed to `Task.Status`
    by json *and* parked as unknown. Extras are re-emitted LAST, so the stale copy
    wins on the next read: `furrow move` never takes and the task wedges forever.
  - `ToLower` folds it, json doesn't — `"İd"` (U+0130) lowercases to `id` but has
    an empty fold orbit, so json never matches it. A `ToLower` set calls it known
    and DROPS it while `Task.ID` stays empty: the key and the task's identity,
    destroyed. That is the very loss this file exists to prevent.
  `core.isKnown` therefore uses **`strings.EqualFold`** — json's own relation — so
  a key is parked **iff** json ignored it. `TestKnownKeysFoldExactlyLikeEncodingJSON`
  pins both directions and fails if the stdlib's matcher ever moves.
- **`Task` must NEVER grow a `MarshalJSON` method.** The `extras` carrier is
  unexported *structurally*, not stylistically: `encoding/json` cannot see it, so
  it can never surface as a literal `"extras"` key and can never leak into
  `internal/cli`'s `--json` views. Those views **embed** `core.Task` to put
  `body_text` / `reason` / `revisit` / `snippet` / `mentioned_by` beside it — a
  `MarshalJSON` on `Task` would be **promoted** to those outer structs, Go would
  call it for the whole view, and every sibling field would silently vanish **with
  no compile error**. (The first implementation did exactly that and emptied 10 CLI
  tests.) The splice therefore happens on the store's write path, in
  `core.MarshalTask`, where the data actually lives.

The byte recipe is untouched: the object is composed **compactly** and indented
once as a finished document, so the 2-space / no-HTML-escape / trailing-newline
rules still live in exactly one place. A shard with **no** extras marshals
byte-identically to what v0.9.0 wrote, so no existing board sees a single
rewritten shard. `fsstore.SetBoardVersion` **reads** `meta.json` and raises its
version rather than building a fresh `core.Meta` — otherwise `furrow upgrade`, the
one command whose whole job is to move a board FORWARD, would itself eat
`meta.json`'s forward-compatible keys.

`scripts/check-marshal-singlepath.sh` now guards **decoders too**
(`json.Unmarshal` / `json.NewDecoder`), not just encoders: a raw `json.Unmarshal`
into a `Task` bypasses `core.UnmarshalTask`, so the unknown keys are never parked
and the next write destroys them. A decoder that skips the single path is exactly
as lossy as an encoder that does.

Its sibling guard, **`scripts/check-schema-write-guard.sh`** (also in
`scripts/check.sh` + CI), greps the *other* single path: `core.SchemaVersion` —
the layout THIS BINARY writes — may only be named in `internal/core/*`,
`fsstore.go`, `memstore.go`, `internal/app/{upgrade,board,lint}.go`,
`internal/cli/cmd_board.go`, `internal/schema/schema.go`, and tests. Anywhere
else fails the build: an ordinary write must never name it (see Schema below —
that one line is what took the shared board down on 2026-07-13, and it fails
silently, since every test on a fresh store still passes).

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
default ready+in-progress), `[labels].required` (a label-less task errors on
`add` and in `lint`; default false), and `[lint].archive_done` (a count that
makes `lint` warn `archive-backlog` once that many done tasks are archivable;
default 0 = off). A board `[alias]` table (`name = "command
string"`) lets `furrow <name> …` expand git-style before dispatch (the rest of
argv appends); a builtin always wins (a shadowing alias is inert and `lint`
warns `alias-shadow`), and it lives in the **board** config so it syncs. The
user-level central-board config
(`~/.config/furrow/config.toml`) scopes each `[[board]]` by **repo**:
`repo = "auto" | "" | "owner/repo"` ("auto" derives owner/repo from the
checkout's git origin, worktree-aware, ghq-path fallback — `internal/app`'s
job, file reads only, never a bare dir name); a board's `label` is only a
literal add-time tag, and `label = "auto"` is a reserved tombstone (warned,
ignored).

### Schema
`internal/schema.TaskV2` and `internal/schema.MetaV2` are the sources of the JSON
Schemas; `furrow schema [task|meta|repo]` prints them (no arg or `task` = the shard
schema; `meta` = the `meta.json` schema) and CI diffs them against
`docs/schema/furrow.task.v2.json` and `docs/schema/furrow.meta.v2.json`. Change a
struct → update the schema const, the committed file, and the golden together.
A task carries a first-class `repos` set (owner/repo identifiers, same
sorted+deduped/[]-not-null semantics as labels; `[]` = draft). Labels are pure
free-form tags — a repo is NOT a label. The three top-level objects declare
`"additionalProperties": true` — furrow now legitimately writes shards carrying
keys it does not know (see the passthrough), and leaving it `false` would make the
published schema call furrow's own output invalid. `$defs.checklistItem` **stays
`false`**: passthrough is TOP-LEVEL ONLY, an unknown key inside a checklist item
really is still dropped, and the schema must not promise what the marshaller does
not do.

**Adding a shard field? The default answer is BUMP.** Passthrough makes an old
binary **preserve** a field it does not know. It does not make it **honour** one:
an old furrow carries a future `"blocked": true` faithfully through every write —
and then still surfaces that task in `furrow next` and still lets you close it, as
if the field were not there. Preservation downgrades silent DATA LOSS to silent
SEMANTIC MISBEHAVIOR — a real improvement (loss is unrecoverable; misbehavior is
fixed by updating the binary), but only `core.SchemaVersion` can say "refuse to
operate". So the rule "bump when the shard layout changes" now has **teeth**:
**`TestShardFieldsGolden`** (`internal/core/schema_fields_test.go` +
`testdata/shard-fields.golden`) freezes every persisted type's json keys, in struct
order, plus the layout version. Change a shard's shape and it FAILS, naming the
version to bump. Skip the bump only if **no** query, sort, filter, or lane decision
reads the new field — and note that every field ever added to `Task` (value,
effort, repos, reviewed, deps, refs, checklist, parent) is read by one: the "safe
for an old binary to ignore" class has never had a member. Accept a new shape with
`go test ./internal/core -run TestShardFieldsGolden -update-fields`, in the same
change as the schema const, the committed `docs/schema/` file, and the goldens.
New shard fields go at the **END** of the struct: a field declared mid-struct is
written there by a new binary and re-emitted at the end by an old one (extras are
appended), so alternating writes churn a one-line move — churn, not loss, but
avoidable.

**The teeth have a second row: the FROZEN BOARD.**
`TestShardFieldsGolden` reads the Go structs, so both sides of it move together —
it FAILS on a shape change, but `-update-fields` makes it green again whether or
not you bumped, because the teeth are the failure *message*, not a mechanical
check. `internal/store/fsstore/testdata/frozen-board/` is a real board's **bytes**,
written by an earlier furrow and committed;
**`TestFrozenBoardRoundTripsByteIdentical`** copies it, runs Load → Save →
SaveRepo → SetBoardVersion, and requires every file to come back **byte-identical**,
with the same file set and untouched mtimes. It is the only fixture in the repo the
code under test did not write, and it is what shows the DAMAGE rather than the
diff: add a non-`omitempty` field and it prints `+ "sprint": ""` appearing in every
shard — i.e. every board in the fleet rewritten on its next ordinary write, and
silently dropped by every older binary. Rename or remove a key and the on-disk key
becomes unknown, so the passthrough parks it and re-emits it *after* the known ones
— a key-ORDER change no in-memory test can see. It also pins the only two things
with no committed coverage at all: `meta.json`'s bytes, and the extras splice as it
actually lands on disk. Regenerate with `go test ./internal/store/fsstore -run
TestFrozenBoard -update-board` — which rewrites a committed board, so the diff makes
the decision visible in review, exactly as a flag day should be.

**The version gate is two-sided, and `core.SchemaVersion` is what THIS BINARY
writes — not what the board declares.** The board's number lives in `meta.json`
and is an **input** to every write:

- `core.CheckSchemaVersion(v)` — the READ gate. A board NEWER than the binary is
  refused (id **`schema-too-new`**, exit 3 — the fix is the binary, not the
  input), so an old binary can never MISREAD such a board: a v3-only binary would
  happily load a v4 shard and then act as if `reviewed` did not exist. (It no
  longer guards against DESTROYING the fields it doesn't know — the passthrough
  preserves those. But preserving is not understanding, which is exactly why this
  gate stays.)
- `core.CheckWritable(v)` — the WRITE gate. A binary may write only a board that
  already declares exactly its own layout. An OLDER board — or one with shards
  but no `meta.json` at all — is fully READABLE but READ-ONLY (id
  **`schema-upgrade-required`**, exit 2: the BOARD is stale and an explicit
  command fixes it). Both ids carry `details {board_schema, binary_schema}`; the
  exit code alone says which side is stale.
- Consequently **an ordinary write never touches `meta.json`'s
  `schema_version`.** `fsstore.Save` stamps it in exactly one case: a genuinely
  fresh, empty store (what `furrow init` hits). A garbled `meta.json` is an error
  (exit 3, id `meta`), never a fallback to "whatever version this binary is" —
  that old fallback silently DISABLED the gate.

**The only raiser is `furrow upgrade`** (preview unless `--yes`; raises
`.furrow/meta.json` and the `archive/` store's, re-serializes every shard through
`core.MarshalTask`, idempotent no-op on a current board; JSON
`{from,to,changed,applied,stores}`). It is a **flag day**: afterwards no older
furrow can write the board, including a CI pinned to an older release — and
furrow cannot see those pins, so the ORDER is the human's: (1) release a furrow
shipping the schema, (2) bump every caller's `sync-task-status.yml@vX.Y.Z` pin
**and** that workflow's `furrow-version` default, (3) only THEN `furrow upgrade
--yes` + `furrow sync`. There is no downgrade — recovery is `git revert` on the
board repo. Why all this: on 2026-07-13 `fsstore.Save` stamped `meta.json` with
the binary's version on every write, so one routine `furrow sync` from an
unreleased source build migrated the shared central board 3 → 4 and every pinned
release in the fleet lost it at once (v0.6.1 reported "task not found" for every
id; v0.7.0 exited 3). `scripts/check-schema-write-guard.sh` greps that guarantee
back into place — see the marshaller-path section.

## Conventions

- Commits: gitmoji + Conventional — `<:gitmoji:> <type>(<scope>)<!>: <subject>`.
  Enable the hook once: `git config core.hooksPath scripts/hooks`. Spec:
  [CONTRIBUTING.md](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md).
- `go build ./...` and `go test ./...` must pass before finishing a turn.
- Keep [README.md](README.md) / [README.ja.md](README.ja.md) carrying the same
  FACTS, not the same STRUCTURE, on any user-visible change (bilingual is the
  house style; JA is intentionally a superset). Two shared load-bearing facts
  stay in lockstep, and
  [`scripts/check-readme-parity.sh`](scripts/check-readme-parity.sh) enforces
  both by pure text extraction: the `sync-task-status.yml@vX.Y.Z` workflow-pin
  tag must be **identical in the two READMEs** (a reader copies it verbatim), and
  the `{"schema_version": N}` literal in each must equal `const SchemaVersion` in
  `internal/core/task.go` (the claim used to be made and NOT checked — which is
  exactly how both READMEs came to say "board layout v3" against a v4 board).
- **Don't push without explicit OK.** 1 item = 1 PR (squash); update docs
  in the same PR.

## References

<!-- broad → narrow; tag each (reviewed YYYY-MM-DD); re-check on a 6-month gap. -->
- clig.dev — CLI design guidelines (reviewed 2026-06-25)
- Conventional Commits 1.0.0; gitmoji.dev (reviewed 2026-06-25)
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
