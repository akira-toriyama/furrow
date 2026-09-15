# Board git hooks

Git hooks for a **board repo** (one whose `.furrow/` is the store): `post-merge`
and `post-rewrite` run `furrow lint` as a non-blocking nudge, `pre-push` blocks
on lint **errors** (exit 2). Each skips cleanly without `furrow` on `PATH` or
without a `.furrow/`. Install once per machine: copy the three files into the
board's hook dir, then `git config core.hooksPath <that dir>` — which REPLACES
`.git/hooks`, so move any existing hook in beside them (compose a same-name
hook, don't replace it). The full story: README, "Board git hooks".
