# furrow — Non-goals

> What furrow deliberately does **not** do, and why. A non-goal is a choice, not
> an oversight: every line below is something we considered and rejected on
> purpose. Recording them keeps the scope honest and saves the next person (or
> agent) from "re-discovering" a road we already chose not to take.
>
> Rationale is drawn from [`MEMO.md`](../MEMO.md) (the decision log) and locked in
> [`ROADMAP.md`](../ROADMAP.md). Section references below point at `MEMO.md`.
> See also [`architecture.md`](architecture.md) and [`glossary.md`](glossary.md).

---

## Integration

### No MCP server
furrow ships no Model Context Protocol server. — *2026 guidance: MCP is for
cross-agent / remote / auth scenarios, none of which apply to a solo, local,
single-repo tool; a plain CLI is simpler and has no daemon to run* (MEMO §4).

### No Claude Code plugin
furrow is not packaged as a Claude Code plugin. — *Plugins are for team-wide
distribution; for a solo author the overhead is pure cost* (MEMO §4).

The actual integration layer is small and deliberate: a `~15`-line `CLAUDE.md`
block plus `--json` on the read commands (MEMO §4). The rules that block encodes:
never hand-edit `.furrow/index.json` (a single deterministic marshaller in
`internal/core` owns it, so manual edits churn git and can break the
determinism contract); `.furrow/bodies/<id>.md` files **are** meant to be
hand- or agent-edited; mutate state through the CLI commands, not the JSON.

### No GitHub Issue sync
furrow does not sync to, mirror, or import from GitHub Issues. — *Issues are
**public**, mix with other people's issues, and lag behind the local plain text;
a private, repo-local store is the whole point* (MEMO §1; ROADMAP "ハード要件":
"GitHub フレンドリー = 非バイナリならOK … Issue 連携は不要").

GitHub-friendly here means "commits to the repo and diffs cleanly as plain
text" — not "talks to the GitHub API." A speculative `furrow import
--from-gh-project 5` one-shot seed is listed as an *optional* item under ROADMAP
Phase 5; it is **not built** today, and even if built it is a one-time import,
not an ongoing sync.

---

## Storage format

The storage model is a hybrid: `.furrow/index.json` (structured metadata,
machine-written) + `.furrow/bodies/<id>.md` (long-form prose, hand/agent
editable) + `.furrow/config.toml` (human config) + `.furrow/seq` (id counter) +
`.furrow/archive/` (aged done tasks). The rejected alternatives below are *not*
shortcuts we skipped — they are formats we evaluated and ruled out (MEMO §3).

### No single-file `tasks.json`
We do not keep everything in one JSON file. — *Long-form prose collapses into a
single `\n`-escaped string, so any body edit churns the entire file in git
(reproducing the `Task.md` pain in JSON), and an agent editing a 300-line
escaped string tends to break it* (MEMO §3, ❌ 単一 `tasks.json`).

### No JSONL / single-line-per-task store
One physical line per task is also rejected. — *The prose problem is unchanged:
a body still has to live on one physical line, so the escape/churn issue is
identical to single-file JSON* (MEMO §3, ❌ JSONL).

### No YAML config
`.furrow/config.toml` is TOML, not YAML. — *YAML is whitespace-fragile, which
makes it easy for an agent to break on edit; TOML matches the house
`config.toml`-driven style and survives mechanical edits* (MEMO §3, §5; the
config loader uses `pelletier/go-toml/v2`).

### No SQLite (or any binary store)
furrow does not use SQLite or any binary database. — *A binary file is not
git-diffable, which violates the core "non-binary, clean git-diff" requirement*
(MEMO §1, §3; Taskwarrior v3's binary SQLite is named as a reason it was passed
over).

### Not pure markdown-with-frontmatter
For completeness: a pure "one markdown file per task with YAML frontmatter"
store was also rejected, because cross-cutting queries ("open tasks by
priority") would require scanning every file and cross-cutting updates would
rewrite many files (MEMO §3). The hybrid keeps small structured metadata in one
JSON index (fast `jq`/Go queries, field-level diffs) and prose in per-task
markdown (no escaping, task-level diffs).

---

## Backend & UI

### No cloud / hosted / web-app backend
furrow has no server, no account, no hosted state. — *The store lives in the
user's own repo under `.furrow/`; cloud-/Issue-/account-backed candidates
(Linear, Notion, GitHub Projects, CCPM, Spec Kit) were explicitly dropped for
assuming a remote backend* (MEMO §1).

### Web / React UI is low priority, not a near-term goal
A rich web or React UI is deliberately deferred. — *CLI and TUI are the
high-priority surfaces; web/React is low priority and explicitly future-only*
(ROADMAP "ハード要件"; MEMO §5).

When web does happen it is **ROADMAP Phase 8** (marked low-priority / future),
and the first step is a *read-only* static viewer built on Go `net/http` +
`embed.FS` that simply reads `index.json` — no Node toolchain, single binary
(MEMO §5). `templ+htmx`, `Wails`, and the React + Electron stack are explicitly
out of scope for the viewer (the React *component shape* may be borrowed later;
the host — Electron vs. Go static — is held open as a Phase 8 question, MEMO
§7.5). A future React UI works precisely because it only has to read the JSON
index — which is itself an argument for keeping the index as plain JSON.

---

## Built vs. planned — honesty note

To keep this list honest about today's reality (not aspirations):

- **Built and real today** (`internal/cli`): `init`, `add`, `ls` (alias
  `list`), `show`, `next`, `edit`, `done`, `move`, `reorder`, `check`,
  `archive`, `lint`, `schema`, `version`. Read commands honor `--json` /
  `--ndjson`; `ls` supports `--status`/`-s`, `--label`/`-l`, `--limit`/`-n`.
  Destructive ops are guarded: `archive` previews unless `--yes`. Exit-code
  contract: `0` ok / `1` not-found|empty / `2` bad-usage|validation / `3+`
  internal|IO, with `{"error":{"code","id","message"}}` to stderr
  (`internal/core/errors.go`).
- **Stub today** — `furrow ui` exists as a command but is **not implemented**;
  it returns a "not implemented yet — see ROADMAP Phase 6" validation error
  (`internal/cli/ui.go`). The bubbletea v1 TUI (`internal/tui`) is **ROADMAP
  Phase 6** and not yet wired.
- **Not built** — `furrow migrate` (importing `Task.md`) is **ROADMAP Phase 5**
  and does not exist in the CLI yet; the optional `furrow import
  --from-gh-project 5` seed is likewise unbuilt. Neither command is registered
  in `internal/cli/root.go`.

---

*(reviewed 2026-06-25)* — Author/owner: akira-toriyama (Tommy). When a non-goal
changes, update this file and the corresponding section of `MEMO.md` together.
