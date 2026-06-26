# furrow — `docs/plans/`

> Working space for **multi-session tasks**. While a task is in flight, its plan
> file here is the single source of truth for that one task. The governing rule
> is simple: **never leave unfinished work implicit** (未達成を暗黙にしない) —
> every in-flight task is tracked somewhere you can see it.

---

## The rule

Nothing in flight may be implicit. If you are mid-task and stop, the next session
(you, or a coding agent) must be able to resume from the repo alone — no state
should live only in your head or in a chat transcript. A task too detailed or
too long-running to finish in one sitting gets a **plan file** in this directory.

## One file per in-flight task

- Create `docs/plans/<short-task-name>.md` when you start a task that won't finish
  in one sitting.
- Keep it current as you work: goal, the plan, what's done, what's left, open
  questions, relevant `file:line` / doc references.
- **Delete it when the task merges.** A finished task's outcome belongs in the
  commit history — not in a stale plan file. This directory should only ever hold
  *active* work.

## The intended replacement: dogfood furrow itself

These ad-hoc plan files are a stopgap. furrow exists to replace exactly this kind
of scattered, hand-maintained task tracking. Once furrow is comfortably eating
its own dog food, prefer tracking work in a `.furrow/` store (or a central furrow
tracker) over a new `docs/plans/*.md`.

---

*(reviewed 2026-06-25)*
