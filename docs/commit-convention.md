# Commit convention

furrow uses **[gitmoji](https://gitmoji.dev/) + [Conventional Commits](https://www.conventionalcommits.org/)** for every commit
message. The text-form gitmoji + Conventional Commits combo keeps the changelog
tooling and commit hooks simple and greppable.

## The form

```
<:gitmoji:> <type>(<scope>)<!>: <subject>
```

- **`<:gitmoji:>`** — the gitmoji in its **`:code:` text form** (e.g. `:sparkles:`), **not**
  the emoji glyph. The text form survives copy/paste, greps cleanly, and is what the
  changelog preprocessor strips. Writing the literal ✨ glyph is wrong here.
- **`<type>`** — a Conventional Commits type from the table below.
- **`(<scope>)`** — *optional*. One of the suggested furrow scopes (see below).
- **`<!>`** — *optional* `!` immediately before the colon to flag a **breaking change**.
- **`<subject>`** — **imperative present tense** ("add", not "added"/"adds"), no trailing
  period. Keep the whole header to roughly **72 columns**.

Minimal valid header (no scope, no breaking flag):

```
:bug: fix: clear closed stamp when moving a task out of done
```

Full shape (scope + breaking flag):

```
:recycle: refactor(core)!: route all index writes through a single marshaller
```

## Type → gitmoji → use

| `type`     | gitmoji              | Use for                                                        |
| ---------- | -------------------- | -------------------------------------------------------------- |
| `feat`     | `:sparkles:`         | A new user-facing feature or command.                          |
| `fix`      | `:bug:`              | A bug fix.                                                     |
| `docs`     | `:memo:`             | Documentation only (README, `docs/`, comments).                |
| `refactor` | `:recycle:`          | Code change that neither fixes a bug nor adds a feature.        |
| `perf`     | `:zap:`              | A performance improvement.                                     |
| `test`     | `:test_tube:`        | Adding or fixing tests (golden files, table-driven cases).      |
| `build`    | `:hammer:`           | Build system, `go.mod`/`go.sum`, scripts, tooling.              |
| `ci`       | `:construction_worker:` | CI configuration and workflows (`.github/workflows/`).      |
| `chore`    | `:rocket:` / `:arrow_up:` | Maintenance. Use `:arrow_up:` for dependency bumps, `:rocket:` for releases/other chores. |
| `style`    | `:art:`              | Formatting / code structure with no behavior change.            |
| `revert`   | `:rewind:`           | Reverting a previous commit.                                   |

## Suggested scopes

Scopes track furrow's hexagonal layers and cross-cutting concerns. Pick the one that
best names *where* the change lives:

| Scope       | Covers                                                                 |
| ----------- | --------------------------------------------------------------------- |
| `core`      | `internal/core` — pure domain: `Index`/`Task`, the single `Marshal` path, ports, validate. |
| `store`     | `internal/store/fsstore` and `internal/store/memstore`.                |
| `config`    | `internal/config` — `.furrow/config.toml` loading and clamp logic.     |
| `cli`       | `internal/cli` — the cobra adapter and command surface.                |
| `tui`       | `internal/tui` — the bubbletea UI (`furrow ui`).                       |
| `index`     | `.furrow/index.json` schema, marshalling, or determinism rules.        |
| `body`      | `.furrow/bodies/<id>.md` handling.                                     |
| `migrate`   | `internal/migrate` and the `migrate` command.                          |
| `packaging` | GoReleaser, Homebrew tap, nix flake, release plumbing.                 |
| `ci`        | CI workflows and commit linting.                                      |
| `docs`      | `docs/`, README, glossary, non-goals.                                 |

A scope is optional — omit the parentheses entirely when a change is genuinely
cross-cutting (e.g. `:art: style: run gofmt across the tree`).

## Breaking changes

Append `!` directly before the colon to mark a breaking change:

```
:boom: feat(index)!: bump schema_version to 2
```

A breaking commit **must** also carry a body that explains the break and how to
migrate. Use the Conventional Commits `BREAKING CHANGE:` footer:

```
:boom: feat(index)!: bump schema_version to 2

BREAKING CHANGE: index.json now requires a top-level "schema_version": 2.
Document the upgrade steps for an existing .furrow/ store in the footer.
```

## Enabling local enforcement

The house style validates commit headers with a `commit-msg` hook checked into the repo
under `scripts/hooks/`. Point git at that directory **once per clone**:

```sh
git config core.hooksPath scripts/hooks
```

After that, the `scripts/hooks/commit-msg` hook rejects any commit whose subject line
does not match `<:gitmoji:> <type>(<scope>)<!>: <subject>`.

## CI

In addition to the local hook, **CI lints commit messages** on push / pull request, so the
same `<:gitmoji:> <type>(<scope>)<!>: <subject>` rule is enforced server-side even if a
contributor skipped `git config core.hooksPath scripts/hooks`. (The commit-lint workflow
lives under `.github/workflows/`; see the status note above for what is wired today.)

## Examples

```
:sparkles: feat(cli): add `furrow next` to list actionable tasks

:bug: fix(core): keep nil slices as [] so app writes match hand edits

:recycle: refactor(store): build .furrow/bodies/<id>.md paths only in fsstore

:memo: docs: document the gitmoji + Conventional Commits convention
```

---

*(reviewed 2026-06-25)*
