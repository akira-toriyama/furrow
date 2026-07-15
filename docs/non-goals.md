# furrow — Non-goals

> What furrow deliberately does **not** do, and why. A non-goal is a choice, not
> an oversight: every line below is something we considered and rejected on
> purpose. Recording them keeps the scope honest and saves the next person (or
> agent) from "re-discovering" a road we already chose not to take.
>
> The rationale for each choice is recorded inline below.
> See also [`architecture.md`](architecture.md) and [`glossary.md`](glossary.md).

---

## Integration

### No MCP server
furrow ships no Model Context Protocol server. — *The plain CLI is already the
agent interface: `--json`/`--ndjson` on every read, `{before, after, changed}`
on mutations, machine-actionable error envelopes (`candidates`,
`sync-conflict` paths), and a plain-text store the agent can read — and, for
bodies, write — directly. That holds for multi-repo, multi-machine central
boards too: the board is a git repo, so "remote access" is `git clone`, not a
protocol. An MCP server would add a daemon to run and a second interface to
keep in lockstep with the CLI, for zero new capability.*

### No Claude Code plugin
furrow is not packaged as a Claude Code plugin. — *The integration contract is
a short `CLAUDE.md` block that lives in the tracked repo itself — versioned,
cloned, and reviewed with everything else, on every machine and for every
operator at once. A plugin would move that contract into a per-machine install
that can go stale against the board it drives, and (as with MCP) it would add
no capability the CLI does not already expose.*

The actual integration layer is small and deliberate: a `~15`-line `CLAUDE.md`
block plus `--json` on the read commands. The rules that block encodes:
never hand-edit `.furrow/tasks/<id>.json` (a single deterministic marshaller in
`internal/core` owns it, so manual edits churn git and can break the
determinism contract); `.furrow/bodies/<id>.md` files **are** meant to be
hand- or agent-edited; mutate state through the CLI commands, not the JSON.

### No GitHub Issue sync
furrow does not sync to, mirror, or import from GitHub Issues. furrow is an
**alternative** to Issues, not a mirror of them. — *Issues are **public**, mix
with other people's issues, and lag behind the local plain text; a clonable
plain-text store is the whole point.* GitHub-friendly here means "non-binary,
commits to the repo and diffs cleanly as plain text" — not "talks to the GitHub
API."

The boundary, stated once: the two GitHub touchpoints that **do** exist are
deliberately not API integrations. `furrow sync` is a **thin git wrapper**
(git is the transport — see *No sync daemon* below), and the task-status
GitHub Actions workflow is **PR-event → own-board reflection**: it runs
`furrow apply` over the PR's own text to update the board *in its own repo*,
calling no Issues/Projects API and mirroring no external state.

A speculative one-shot `furrow import --from-gh-project` seed (a one-time
import, not an ongoing sync) has been floated but is **not built** today.

---

### No sync daemon / server
Multi-machine use is `furrow sync` — a **thin git wrapper** (auto-commit
scoped to `.furrow/` — shards and `meta.json` always, a hand-edited
`bodies/<id>.md` only when new or opted in with `-b`/`--all-bodies`, otherwise
reported in `pending_bodies`; then `fetch` + `rebase --autostash @{u}`,
`push`, abort-and-report on conflict) that the user or agent runs explicitly. There is no background process, no file
watcher, no hosted relay, and none is planned: git is already the
synchronization layer, and per-task shards already make concurrent writes
merge cleanly. — *A daemon would add an always-on failure mode to a tool whose
whole premise is "plain files in your repo".* To run furrow **on a schedule**
(periodic archive, a `next` digest), the trigger lives outside furrow in your OS
scheduler — see [scheduling.md](scheduling.md) for launchd recipes.

## Storage format

The storage model is a hybrid: per-task `.furrow/tasks/<id>.json` shards
(structured metadata, machine-written) + `.furrow/meta.json`
(`{"schema_version": 4}`, the board-wide layout version) +
`.furrow/bodies/<id>.md` (long-form prose, hand/agent
editable) + `.furrow/bodies/assets/` (media copied in by `furrow attach` as
collision-free `<id>-<name>` files, referenced from the body by a relative
markdown line — the explicit binary exception, delegated to git(-lfs); the
non-binary, clean-git-diff arguments below concern the structured store) +
`.furrow/config.toml` (human config) +
`.furrow/archive/` (aged done tasks, itself a sibling sharded store). The
rejected alternatives below are *not*
shortcuts we skipped — they are formats we evaluated and ruled out.

### No single-file `tasks.json`
We do not keep everything in one JSON file. — *Long-form prose collapses into a
single `\n`-escaped string, so any body edit churns the entire file in git
(reproducing the `Task.md` pain in JSON), and an agent editing a 300-line
escaped string tends to break it.*

### No JSONL / single-line-per-task store
One physical line per task is also rejected. — *The prose problem is unchanged:
a body still has to live on one physical line, so the escape/churn issue is
identical to single-file JSON.*

### No YAML config
`.furrow/config.toml` is TOML, not YAML. — *YAML is whitespace-fragile, which
makes it easy for an agent to break on edit; TOML survives mechanical edits*
(the config loader uses `pelletier/go-toml/v2`).

### No SQLite (or any binary store)
furrow does not use SQLite or any binary database. — *A binary file is not
git-diffable, which violates the core "non-binary, clean git-diff" requirement*
(Taskwarrior v3's binary SQLite is named as a reason it was passed over).

### Not pure markdown-with-frontmatter
For completeness: a pure "one markdown file per task with YAML frontmatter"
store was also rejected, because cross-cutting queries ("open tasks by
priority") would require scanning every file and cross-cutting updates would
rewrite many files. The hybrid keeps small structured metadata in per-task
JSON shards (fast `jq`/Go queries, field-level diffs) and prose in per-task
markdown (no escaping, task-level diffs).

### No automatic schema migration
furrow never raises a board's layout version on its own. An ordinary write to a
board older than the binary is **refused** (`schema-upgrade-required`, exit 2 —
the board stays fully readable, just read-only) rather than silently upgraded;
only the explicit `furrow upgrade` (preview unless `--yes`) moves the number. —
*A board is shared: one clone, many machines, and — through each repo's pinned
`sync-task-status.yml@vX.Y.Z` — many binaries that are deliberately NOT the
newest. Raising the layout is therefore a **flag day** for every one of them, and
a flag day cannot be a side effect. It was one, once: on 2026-07-13 a routine
`furrow sync` from an unreleased source build migrated the shared central board
3 → 4 because `Save` stamped `meta.json` with the binary's version on every
write, and every pinned release in the fleet lost the board within the hour.
"Helpfully" auto-migrating is exactly the convenience that broke it.* The
ordering the tool cannot enforce (it cannot see other repos' pins) is stated by
the `upgrade` preview itself: release furrow → bump every caller's pin → then
upgrade.

### No downgrade path
There is no `furrow downgrade`, and `upgrade` refuses a board newer than the
binary (`schema-too-new`, exit 3) rather than rewriting it downwards. — *A
downgrade would have to drop the fields the older layout has no place for —
which is the precise damage the version gate exists to prevent, performed on
purpose. And it is unnecessary: the board is a git repo, so the honest undo of a
flag day is the one git already provides — `git revert` the upgrade commit on the
board repo (every machine then wants the matching older binary again).*

---

## Backend & UI

### No cloud / hosted / web-app backend
furrow has no server, no account, no hosted state. — *The store lives under
`.furrow/` in a git repo the user owns — a code repo, or a dedicated central
tracker repo; multi-machine and multi-repo use is git (clone + `furrow sync`),
not a service. Cloud-/Issue-/account-backed candidates (Linear, Notion, GitHub
Projects, CCPM, Spec Kit) were explicitly dropped for assuming a remote
backend.*

### Any UI is a separate front-end, not part of this repo
furrow is **CLI-only**. The one presentation layer in-repo is `internal/cli`;
any interactive TUI or GUI lives in its own front-end repo and drives furrow
through the **CLI/JSON contract** (it does not import furrow's Go packages). —
*Planned front-ends: **ridge** (a charm-v2 TUI, a CLI/JSON client of furrow) and
**loom** (a from-scratch TUI framework, future/gated). A rich hosted web or React
UI remains out of scope; cloud-/Issue-/account-backed candidates were dropped for
assuming a remote backend.*

If web does happen, the first step is a *read-only* static viewer built on Go
`net/http` + `embed.FS` that simply reads the `tasks/*.json` shards — no Node
toolchain,
single binary. `templ+htmx`, `Wails`, and the React + Electron stack are out of
scope for that viewer (the React *component shape* may be borrowed later; the
host — Electron vs. Go static — is held open). A future React UI works precisely
because it only has to read the JSON shards — which is itself an argument for
keeping them as plain JSON. Every such front-end consumes furrow's `--json` /
`--ndjson` output rather than linking its packages.

---

## Built vs. planned — honesty note

To keep this list honest about today's reality (not aspirations):

- **Built and real today** (`internal/cli`): `init`, `add`, `ls` (alias
  `list`), `show`, `next`, `revisit`, `search`, `stats`, `board`, `edit`,
  `attach`, `done`, `move`, `set`, `reorder`,
  `retitle`, `value`, `effort`, `check`, `dep`, `label`, `repo`, `review`,
  `apply`, `sync`,
  `migrate`, `archive`, `upgrade`, `lint`, `config init|path`, `schema`, `version`.
  Read commands honor `--json` / `--ndjson`; `ls` supports `--status`/`-s`,
  `--label`/`-l`, `--repo`/`-r`, `--limit`/`-n`, and `--drafts`.
  Destructive ops are guarded: `archive` and `upgrade` preview unless `--yes`.
  Exit-code
  contract: `0` ok / `1` not-found|empty / `2` bad-usage|validation / `3+`
  internal|IO, with `{"error":{"code","id","message"}}` to stderr
  (`internal/core/errors.go`), plus optional `candidates` / `details` fields
  when there is something machine-actionable to say. `furrow migrate`
  (importing a legacy `Task.md`) is wired and working too.
- **Not built here** — furrow is CLI-only: any interactive TUI/GUI is a separate
  front-end repo consuming the CLI/JSON contract (planned: **ridge**, **loom**);
  a hosted/web backend and a rich React UI remain out of scope for now (see
  *Backend & UI* above); the optional `furrow import
  --from-gh-project` seed is also unbuilt.
- **Planned, not a non-goal** — the per-person collaboration niceties, namely an
  `@mention` (a *person*-directed notation, distinct from the task→task `[[id]]`
  **link** that already ships) and a task **assignee**, are **unbuilt but on the
  roadmap**. They are called out here so the gap stays honest: their absence
  today is a *not-yet*, **not** a deliberate permanent non-goal like the rows
  above.

---

*(reviewed 2026-07-02 — rationales for "No MCP server" and "No Claude Code
plugin" rewritten for the repos pivot: the non-goals stand, but their old
"local, single-repo / single-author" grounds became false once central boards
went multi-repo and multi-machine.)* — When a non-goal changes, update this
file and the relevant `docs/` so the scope stays honest.
