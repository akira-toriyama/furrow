# `furrow revisit` — agent-facing re-evaluation query

Tracker task: **t-0061** (projects). Sibling of t-0012 (human `review`). Prereqs
t-0060 (value/effort), t-0056 (dep), t-0052 (graph) are all done.

## Goal

A **read-only** command — the agent counterpart to `next`. `next` answers "what
should I work on now"; `revisit` answers "which tasks need a fresh judgment from
me." It never mutates; the agent acts on its output with the existing setters
(`furrow value|effort|dep`). Designed for Claude's own ergonomics:
machine-actionable `--json`, useful default, low ceremony.

## Signals (per open task → structured reasons)

Only **open** tasks are eligible (terminal lanes — done/icebox/waiting — are
skipped; re-evaluating parked/finished work is noise). Each surfaced task carries
a `[]RevisitReason`, one entry per signal that fired. A task qualifies when it has
≥1 reason.

| code           | condition                                  | agent's fix             |
|----------------|--------------------------------------------|-------------------------|
| `value_unset`  | `Value == nil`                             | `furrow value <id> 1-5` |
| `effort_unset` | `Effort == nil`                            | `furrow effort <id> 1-5`|
| `stale`        | `now - Updated >= stale_days` (when > 0)   | review body; update     |
| `dep_done`     | a dep id is in the done lane (one per dep) | `furrow dep <id> <dep> --rm` if the premise is satisfied |

`RevisitReason = { Code string, Detail string }`. `Detail` is factual, never a CLI
verb (keeps `core` decoupled): e.g. `stale` → `"no update in 47d (threshold 30d)"`;
`dep_done` → `"dep t-0012 is done"`.

## Stale threshold

Config `[revisit].stale_days` (default **30**, clamp-don't-reject via the existing
`clampPositive`: `<= 0` → default + a lint-surfaced warning) **plus** a
`--stale-days N` flag that overrides per invocation. `stale_days <= 0` disables
the stale signal entirely (the other three still fire).

## Output / contract

- `--json`: array of `{…task fields…, "revisit": [{code, detail}, …]}` — mirrors
  `actionableView` (anonymous `core.Task` embed + a named field). `--ndjson`: one
  per line. `-l/--label <repo>` and `-n/--limit N` like `ls`/`next`.
- Human (default): the standard task table (reasons shown in `--json`; the table
  stays the shared `printTaskTable` for greppability — revisit reasons are an
  agent concern).
- **Exit code: 0 even when empty** (unlike `next`, which exits 1). "Nothing to
  revisit" is the healthy state; an agent pipeline must not treat it as an error.
  An empty `--json` result is `[]`.

## Architecture (layer spine preserved)

- **`internal/core/revisit.go`** (new, pure — stdlib only):
  - `type RevisitReason struct { Code string `json:"code"`; Detail string `json:"detail,omitempty"` }`
  - reason-code consts `RevisitValueUnset|RevisitEffortUnset|RevisitStale|RevisitDepDone`
  - `func RevisitReasons(t Task, now time.Time, staleDays int, doneIDs map[string]bool) []RevisitReason`
    — the eligibility (terminal exclusion) is the caller's job; this is the pure
    signal computation.
- **`internal/app/revisit.go`** (new):
  - `type RevisitItem struct { Task core.Task; Reasons []core.RevisitReason }`
  - `func (a *App) Revisit(label string, staleDays, limit int) ([]RevisitItem, error)`
    — load index; build `doneIDs` (mirror `Next`); for each non-terminal task
    (`!a.Cfg.IsTerminal(t.Status)`) matching the label, compute reasons via
    `core.RevisitReasons(t, a.Clock.Now(), staleDays, doneIDs)`; keep those with
    ≥1; respect `limit`. Canonical order is already guaranteed by `load`.
- **`internal/config`**: add `Revisit struct { StaleDays *int `toml:"stale_days"` }`
  to `raw`; resolve in `fromRaw` with `clampPositive(r.Revisit.StaleDays,
  DefaultRevisitStaleDays, "revisit.stale_days", &warn)`; add field
  `RevisitStaleDays int` to `Config`, `DefaultRevisitStaleDays = 30` to defaults,
  wire into `Default()`; document the key in `template.go`.
- **`internal/cli`**: `newRevisitCmd()` (in `cmd_query.go`) with `-l`, `-n`,
  `--stale-days` (default = `a.Cfg.RevisitStaleDays`, flag overrides); register in
  `root.go` near `newNextCmd()`. Add `revisitView{ core.Task; Revisit
  []core.RevisitReason `json:"revisit"` }` + `emitRevisit(items)` in `output.go`
  (mirror `emitActionable`, but empty → exit 0).
- **Schema: no change** — revisit is derived/read-only; no new stored field. (So
  the 6-point schema dance from t-0060 does NOT apply here.)

## Tests (all headless)

- **core** (`revisit_test.go`): table tests — each signal alone, combinations,
  none; `staleDays <= 0` disables stale; `dep_done` one-per-done-dep; stale day
  math at the boundary.
- **app** (`app_test.go`/new): memstore — value/effort unset, old `Updated`,
  done deps; assert items + reasons; label filter; limit; `--stale-days`
  override; terminal lanes excluded.
- **config** (`config_test.go`): `stale_days` default, override, clamp (`<=0` →
  default + warning).
- **cli** (`cli_test.go`): `revisit --json` shape, empty → exit 0, `--stale-days`.
- Finish with `sh scripts/check.sh` (marshaller guard + build/vet/test +
  golangci + schema/config drift + smoke) — green == green CI.

## Docs (same PR)

- `README.md` / `README.ja.md`: add `revisit` to the command list + a one-line
  agent recipe (`furrow revisit -l <repo> --json`).
- `CLAUDE.md`: add `revisit` to the canonical-commands line.
- `.furrow/config.toml` template comment (via `template.go`) documents
  `[revisit].stale_days`.

## Out of scope (YAGNI)

- No `furrow review` (that is t-0012, human side, separate PR).
- No dangling/missing-dep detection here (that is `lint`'s integrity job).
- No ROI-based sorting baked in (caller sorts via `jq`, per t-0052/t-0060).

## PR

1 item = 1 PR (squash), docs in the same PR. Body footer:
`SetStatus-task: https://github.com/akira-toriyama/projects/blob/main/.furrow/bodies/t-0061.md in-progress`
(open → in-progress; merge applies the lane). Commit convention: gitmoji +
Conventional (`:sparkles: feat(cli): …`). Delete this plan file on merge.

## Implementation order (TDD)

1. core: `RevisitReason` + `RevisitReasons` (+ tests).
2. config: `[revisit].stale_days` (+ tests).
3. app: `Revisit` (+ tests).
4. cli: `revisitView`/`emitRevisit` + `newRevisitCmd` + register (+ smoke test).
5. docs: README×2, CLAUDE.md, template.
6. `sh scripts/check.sh` green; then adversarial code review.
