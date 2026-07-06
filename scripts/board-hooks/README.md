# Board git hooks

Ready-to-use git hooks for a **board repo** (a repo whose `.furrow/` is the store
— e.g. a central board like `projects`, or any repo-local board). They put
furrow's `lint` at git's extension points so an inconsistent board is caught the
moment it appears and never reaches the remote.

| hook | when it fires | action | blocking |
|---|---|---|---|
| `post-merge`   | after `git merge` / plain `git pull` | `furrow lint` (nudge) | no |
| `post-rewrite` | after `git rebase` / `--amend` / `git pull --rebase` | `furrow lint` (nudge) | no |
| `pre-push`     | before a push | `furrow lint` | **yes on errors** (exit 2) |

Detection over enforcement: only `pre-push` blocks, and only on lint **errors**;
warnings flow through and are surfaced non-blockingly after a merge/rebase. Each
hook **skips** cleanly when `furrow` is not on `PATH` or the repo has no
`.furrow/`, so it never holds a checkout hostage.

## Install (per board repo, once per machine)

git does not enable hooks on clone (a security boundary), so each machine
activates them once. These hooks assume `core.hooksPath` is the hook directory
(the same convention furrow's own repo uses for `scripts/hooks`):

```sh
# copy the three files into your board's hook dir, then:
git config core.hooksPath scripts/hooks
```

`core.hooksPath` **replaces** `.git/hooks` — git then consults only this
directory — so any hook you already keep in the default `.git/hooks/` location
must be **moved into** the hooks dir too, or it silently stops running. Once both
live here, a same-name hook (e.g. a `pre-push` that protects `main`) is a
collision to **compose**, not replace: keep the existing body and add the
furrow-lint block (see the `pre-push` template header).

`furrow sync` pulls with `--rebase` internally, so it trips `post-rewrite` too —
which is why `sync` carries no lint of its own (it is delegated to these hooks).

See the furrow README, "Board git hooks", for the full story.
