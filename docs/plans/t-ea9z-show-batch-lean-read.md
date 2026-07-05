# t-ea9z — `show`: batch (variadic ids) + lean (`--no-body`) read

Task: [projects t-ea9z](https://github.com/akira-toriyama/projects/blob/main/.furrow/bodies/t-ea9z.md)
Branch: `feat/t-ea9z-show-batch-lean-read` (worktree `../furrow-t-ea9z`)

## Why

Agent-driven cross-checks of specific ids (audit sweeps, dependency checks,
"which of these 6 should I do next") currently cost one process per id, and
`show --json` always ships `body_text`, which bloats/truncates tool output.
One process, no body, stable shape — that is the whole feature.

## Design (approved 2026-07-05)

CLI surface — `show <id>... [--no-body] [--backlinks]`:

- Variadic ids (`MinimumNArgs(1)`). Duplicates dedupe first-wins; output is in
  **input order** (keeps the agent's request↔response correlation).
- `--no-body`: drop `body_text` from JSON (key absent, not empty), skip the
  body section in human output, and never call `LoadBody`.
- 1 id: byte-compatible with today (`--json` = single object, human detail,
  miss = `task not found: <id>`) — except the miss error now also carries
  `details.missing` (additive).
- ≥2 ids: `--json` = array / human = detail blocks separated by a `---` line.
- `--ndjson`: honored by show at **any** arity — one JSON line per task
  (previously accepted-but-ignored; agents get an arity-independent shape).
- `--backlinks` composes: per-task `mentioned_by` ([] never null).

Partial miss (≥2 ids): found tasks still go to stdout in the normal shape,
then exit 1 with `{"error":{"code":1,"message":"K of N ids not found",
"details":{"missing":[...]}}}` on stderr. Same shape when all miss
(`--json` prints `[]`). Precedent: `next` already emits data + exit 1.
Agents branch on `details.missing`, never the message.

JSON views (no omitempty tricks; `mentioned_by` stays []-not-null):

| body | backlinks | view |
|------|-----------|------|
| yes  | no        | `taskView` (existing) |
| yes  | yes       | `backlinkView` (existing) |
| no   | no        | bare `core.Task` (same shape as `ls`) |
| no   | yes       | new `metaBacklinkView{core.Task; mentioned_by}` |

App layer: `App.GetBatch(ids, withBody) (items []ShowItem, missing []string,
err error)` — one `load()`, one pass; `ShowItem{Task, Body}`. A missing id is
data (`missing`), not an error, so partial success stays representable.
Backlinks loop per found id (cheap at this scale, matches `Backlinks`).
No change to core/store/schema/TUI.

Rejected: `ls --id a,b,c` — array shape for free, but leaves `show`'s
`body_text` bloat unsolved and collides with `ls`'s "empty is exit 0"
contract; two read surfaces for one need is YAGNI.

## Checklist

- [ ] `App.GetBatch` + table test (order, dedupe, missing, withBody)
- [ ] `show` variadic + `--no-body` + partial-miss + `--ndjson` (CLI tests)
- [ ] `scripts/check.sh` green
- [ ] README.md / README.ja.md / CLAUDE.md contract line
- [ ] Adversarial review pass, fix findings
- [ ] PR with `SetStatus-task` footer (lane `done`); delete this file on merge
