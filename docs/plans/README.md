# furrow — `docs/plans/`

> Working space for **multi-session tasks**. While a task is in flight, its plan
> file here is the single source of truth for that one task. Ported from the
> author's house style (chord / facet). The governing rule is simple:
> **never leave unfinished work implicit** (未達成を暗黙にしない) — every
> in-flight task is tracked somewhere you can see it.

---

## The three places work lives

| File / dir | What it holds | Lifespan |
|---|---|---|
| [`../../ROADMAP.md`](../../ROADMAP.md) | Decisions, requirements, the phase plan and its checkboxes | Permanent |
| [`../../MEMO.md`](../../MEMO.md) | Research log — *why* each decision was made | Permanent (append-only) |
| `docs/plans/<task>.md` (this dir) | The live plan for **one** in-flight task | Temporary — deleted when the task merges |

`ROADMAP.md` is the **decision** ("we will do X in Phase N"); `MEMO.md` is the
**rationale** ("here is the evidence and the rejected alternatives"); a plan file
here is the **execution detail** for a task currently being worked.

## The rule

Nothing in flight may be implicit. At any moment, every unfinished piece of work
must be visible as **either**:

- a **checkbox** in [`../../ROADMAP.md`](../../ROADMAP.md) (phase-level scope), **or**
- a **plan file** in this directory (a task too detailed or too long-running for
  a single checkbox).

If you are mid-task and stop, the next session (you, or Claude Code) must be able
to resume from these files alone — no state should live only in your head or in a
chat transcript.

## One file per in-flight task

- Create `docs/plans/<short-task-name>.md` when you start a task that won't finish
  in one sitting.
- Keep it current as you work: goal, the plan, what's done, what's left, open
  questions, relevant `file:line` / doc references.
- **Delete it when the task merges.** A finished task's outcome belongs in the
  commit history (and, if a decision changed, in `ROADMAP.md` / `MEMO.md`) — not
  in a stale plan file. This directory should only ever hold *active* work.

## The intended replacement: dogfood furrow itself

These ad-hoc plan files are a stopgap. furrow exists to replace exactly this kind
of scattered, hand-maintained task tracking. **Once furrow is usable, track its
own work in `.furrow/`** — dogfooding the tool — and let `docs/plans/` fade out.

Today (2026-06-25) that transition is in progress:

- The **core**, **config**, **store** (`fsstore` / `memstore`), **app**, and
  **CLI** layers are built. The CLI commands are real and usable:
  `init`, `add`, `ls` (alias `list`), `show`, `next`, `edit`, `done`, `move`,
  `reorder`, `check`, `archive`, `lint`, `schema`, `version`.
- `furrow ui` exists but is a **stub** — the bubbletea TUI is ROADMAP **Phase 6**
  and not wired up yet; the command returns a "not implemented" error on purpose
  rather than pretending.
- `furrow migrate` (importing `Task.md` and friends) is ROADMAP **Phase 5** and is
  **not built yet** — it lives as a checkbox in `ROADMAP.md`, per the rule above.

Until furrow is comfortably eating its own dog food, plan files here remain the
fallback. After that, prefer `furrow add` over a new `docs/plans/*.md`.

---

*(reviewed 2026-06-25)* — Author/owner: akira-toriyama (Tommy). When furrow is
self-hosting its tasks in `.furrow/`, this directory and this README can be
retired. See [`../../ROADMAP.md`](../../ROADMAP.md) and [`../../MEMO.md`](../../MEMO.md).
