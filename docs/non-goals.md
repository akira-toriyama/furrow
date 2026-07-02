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
furrow ships no Model Context Protocol server. — *MCP is for
cross-agent / remote / auth scenarios, none of which apply to a local,
single-repo tool; a plain CLI is simpler and has no daemon to run.*

### No Claude Code plugin
furrow is not packaged as a Claude Code plugin. — *Plugins are for team-wide
distribution; for a repo-local, single-author tool the overhead is pure cost.*

The actual integration layer is small and deliberate: a `~15`-line `CLAUDE.md`
block plus `--json` on the read commands. The rules that block encodes:
never hand-edit `.furrow/tasks/<id>.json` (a single deterministic marshaller in
`internal/core` owns it, so manual edits churn git and can break the
determinism contract); `.furrow/bodies/<id>.md` files **are** meant to be
hand- or agent-edited; mutate state through the CLI commands, not the JSON.

### No GitHub Issue sync
furrow does not sync to, mirror, or import from GitHub Issues. — *Issues are
**public**, mix with other people's issues, and lag behind the local plain text;
a repo-local store is the whole point.* GitHub-friendly here means "non-binary,
commits to the repo and diffs cleanly as plain text" — not "talks to the GitHub
API."

A speculative one-shot `furrow import --from-gh-project` seed (a one-time
import, not an ongoing sync) has been floated but is **not built** today.

---

### No sync daemon / server
Multi-machine use is `furrow sync` — a **thin git wrapper** (commit only
`.furrow/`, `pull --rebase`, `push`, abort-and-report on conflict) that the
user or agent runs explicitly. There is no background process, no file
watcher, no hosted relay, and none is planned: git is already the
synchronization layer, and per-task shards already make concurrent writes
merge cleanly. — *A daemon would add an always-on failure mode to a tool whose
whole premise is "plain files in your repo".*

## Storage format

The storage model is a hybrid: per-task `.furrow/tasks/<id>.json` shards
(structured metadata, machine-written) + `.furrow/meta.json`
(`{"schema_version": 2}`, the board-wide layout version) +
`.furrow/bodies/<id>.md` (long-form prose, hand/agent
editable) + `.furrow/config.toml` (human config) +
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

---

## Backend & UI

### No cloud / hosted / web-app backend
furrow has no server, no account, no hosted state. — *The store lives in the
user's own repo under `.furrow/`; cloud-/Issue-/account-backed candidates
(Linear, Notion, GitHub Projects, CCPM, Spec Kit) were explicitly dropped for
assuming a remote backend.*

### Web / React UI is low priority, not a near-term goal
A rich web or React UI is deliberately deferred. — *CLI and TUI are the
high-priority surfaces; web/React is low priority and explicitly future-only.*

If web does happen, the first step is a *read-only* static viewer built on Go
`net/http` + `embed.FS` that simply reads the `tasks/*.json` shards — no Node
toolchain,
single binary. `templ+htmx`, `Wails`, and the React + Electron stack are out of
scope for that viewer (the React *component shape* may be borrowed later; the
host — Electron vs. Go static — is held open). A future React UI works precisely
because it only has to read the JSON shards — which is itself an argument for
keeping them as plain JSON.

---

## Built vs. planned — honesty note

To keep this list honest about today's reality (not aspirations):

- **Built and real today** (`internal/cli`): `init`, `add`, `ls` (alias
  `list`), `show`, `next`, `revisit`, `edit`, `done`, `move`, `reorder`,
  `check`, `dep`, `sync`, `migrate`, `archive`, `lint`, `schema`, `version`, `ui`. Read
  commands honor
  `--json` / `--ndjson`; `ls` supports `--status`/`-s`, `--label`/`-l`,
  `--limit`/`-n`.
  Destructive ops are guarded: `archive` previews unless `--yes`. Exit-code
  contract: `0` ok / `1` not-found|empty / `2` bad-usage|validation / `3+`
  internal|IO, with `{"error":{"code","id","message"}}` to stderr
  (`internal/core/errors.go`). The bubbletea TUI (`internal/tui`, `furrow ui`)
  and `furrow migrate` (importing a legacy `Task.md`) are wired and working too.
- **Not built** — a hosted/web backend and a rich React UI remain out of scope
  for now (see *Backend & UI* above); the optional `furrow import
  --from-gh-project` seed is also unbuilt.

---

*(reviewed 2026-06-25)* — When a non-goal changes, update this file and the
relevant `docs/` so the scope stays honest.
