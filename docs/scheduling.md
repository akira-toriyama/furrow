# Scheduling furrow

furrow is **non-daemon** by design (see [non-goals.md](non-goals.md) → *No sync
daemon / server*): every command is a one-shot process you or an agent run
explicitly. So "run furrow on a schedule" is not a furrow feature — the trigger
lives **outside** furrow, in your OS scheduler. On macOS that is **launchd**;
this page is a set of copy-paste recipes. (For Linux use `cron` or a
`systemd` timer; the furrow command lines are identical.)

## The one thing to get right: discovery

Discovery starts from the current directory
([README → Discovery precedence](../README.md#discovery-precedence)), and a
launchd job starts **in `/` with a minimal environment** unless its plist says
otherwise, so make the store explicit. Two clean options:

- **`FURROW_DIR=/abs/path/to/.furrow`** — the `.furrow` directory itself, as an
  absolute path (furrow does not expand `~`); it works from any cwd. Best for a
  central board, whatever number of repos it backs. It declares no repo scope,
  so the board's own `default_repo`, if set, still narrows the filtering reads
  (same README section) and the `archive` sweep (`furrow archive --help`); `-r ''`
  (in a plist, the one argument `--repo=`) widens such a command back to the
  whole board.
- **`WorkingDirectory=/abs/path/to/repo`** — run "inside" a repo whose store (or
  pointer, or enclosing board scope) discovery then finds normally.

Not `FURROW_BOARD`: its one scope sits two levels above its store
([glossary.md](glossary.md) → *scope*), so from `/` no board resolves and every
furrow call below exits 2 (kind `validation`) — Recipe 1's log shows it, but a
digest that discards stderr turns it into a blank notification.

Always use the **absolute path to the `furrow` binary** (launchd's `PATH` does
not include Homebrew/nix profiles). Find it with `command -v furrow`.

## Recipe 1 — periodic archive (housekeeping)

Fold done tasks closed more than 30 days ago into `.furrow/archive/` every day at
03:00, so the hot board stays light. `--yes` is required (without it `archive`
only previews), and `--repo=` keeps the sweep board-wide even where the board's
`default_repo` would narrow it.

`~/Library/LaunchAgents/dev.furrow.archive.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>dev.furrow.archive</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/homebrew/bin/furrow</string>
    <string>archive</string>
    <string>--older-than</string>
    <string>30</string>
    <string>--repo=</string>
    <string>--yes</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>FURROW_DIR</key>
    <string>/Users/you/src/github.com/you/projects/.furrow</string>
  </dict>
  <key>StartCalendarInterval</key>
  <dict>
    <key>Hour</key><integer>3</integer>
    <key>Minute</key><integer>0</integer>
  </dict>
  <key>StandardOutPath</key>
  <string>/tmp/furrow-archive.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/furrow-archive.log</string>
</dict>
</plist>
```

Load it:

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.furrow.archive.plist
# older macOS: launchctl load -w ~/Library/LaunchAgents/dev.furrow.archive.plist
launchctl kickstart -k gui/$(id -u)/dev.furrow.archive   # run once now to test
```

To re-load after an edit, `launchctl bootout gui/$(id -u)/dev.furrow.archive`
first: bootstrapping a label that is already loaded fails (`Bootstrap failed: 5:
Input/output error`).

> On a **shared central board** commit the move so other machines see it: point
> `ProgramArguments` at a wrapper script that runs this `archive` line and then
> `furrow sync` (the way Recipe 2 wraps its call), or leave it for this
> machine's next `furrow sync`. A bare `archive` only writes locally.

## Recipe 2 — a `furrow next` digest (nudge)

Surface "what's ready to work" as a desktop notification every weekday at 09:00.
launchd can't post a notification directly, so wrap the furrow call in a script.

`~/bin/furrow-next-digest.sh`:

```sh
#!/bin/sh
export FURROW_DIR="/Users/you/src/github.com/you/projects/.furrow"
json="$(/opt/homebrew/bin/furrow next -n1 --json)" || exit   # fail the job, never a blank notification
top="$(printf '%s' "$json" | /opt/homebrew/bin/jq -r '.[0].title // "nothing actionable"')"
osascript -e 'on run argv' -e 'display notification (item 1 of argv) with title "furrow: next up"' -e 'end run' "$top"
```

`~/Library/LaunchAgents/dev.furrow.next.plist` points `ProgramArguments` at the
script's absolute path (`chmod +x` it first) and gives `StartCalendarInterval`
an array of five dicts — one per `Weekday` 1–5 (Mon–Fri), each with `Hour` 9 and
`Minute` 0; a single dict fires on its one weekday only. Because `next` on an
empty board exits 0 (an empty result is healthy), the digest fails only when
furrow does, never just because there is nothing to do. With epics but none
active, add `--all-epics` (`next` alone returns only pinned work). The title travels as an
argument, so a quote in it cannot break the AppleScript.

## Recipe 3 — interval instead of a clock time

For "every 4 hours" use `StartInterval` (seconds) instead of
`StartCalendarInterval`:

```xml
<key>StartInterval</key>
<integer>14400</integer>
```

An interval missed while the machine was asleep is skipped, not run on wake; only
`StartCalendarInterval` fires a missed job **once** on wake — which is exactly
what you want for a housekeeping task.

## Recipe 4 — a weekly review reminder

`furrow review <repo>` (the GTD weekly-review verb) is built: it records a
per-repo review clock, and `furrow sync` / `furrow brief` surface the repos
whose clock has lapsed (`unreviewed`). The same launchd pattern as Recipe 2 —
its `FURROW_DIR` export, a weekly `StartCalendarInterval` — schedules the nudge;
point the script at `furrow brief --json | jq -c '.revisit | .unreviewed // [], .epic_review_due // []'` and post
the digest, then `furrow review <repo>` after the review to reset the clock.

The box signals and the repo clock ride on the summary `sync` and `brief`
return, not on the per-task `furrow revisit` (README → *Command notes*,
`revisit`). A STANDING box's review runs on the same clock: `furrow review
<epic-ref>` stamps it, and `epic_review_due` fires when that review lapses —
fold the boxes into the same weekly ritual as the repos.

## Recipe 5 — a due-date digest

A task can carry a **due** stamp (`furrow add --due` / `furrow set --due`), but
nothing fires at the promised instant. `furrow brief` LEADS with it and
`furrow lint` finds it (`due-overdue` an error, `due-today` a warning — the
codes: [glossary.md](glossary.md) → *due*). **Both are board-wide**: `lint` has
no repo filter, and brief's band drops every AUTOMATIC narrowing (`furrow brief
--help`), so a digest built on either sees the whole promise whichever repo you
are sitting in; only an explicit `-r`/`-l` narrows the band. Scheduling turns
those pull-reads into a push, with the same launchd pattern as Recipe 2:

```sh
export FURROW_DIR="/Users/you/src/github.com/you/projects/.furrow"
set -o pipefail   # or the pipe swallows lint's exit code (see below)

# what has come due, as a digest (empty output = nothing due)
/opt/homebrew/bin/furrow lint --code due-overdue --code due-today --json |
  jq -r '.[] | "\(.severity)\t\(.id)\t\(.message)"'
```

Note the exit code: `lint` exits non-zero while anything is overdue, so under
`set -e` the job "fails" every day until the promise is kept or pushed (`furrow
set <id> --due +1d`). That is the intended pressure — but a POSIX pipeline
reports only its LAST command's status, so without the `set -o pipefail` above
the `jq` succeeds and the failure never surfaces. Add `|| true` instead if the
job does something else afterwards.

## Not this

No daemon, `furrow schedule` subcommand or built-in notifier, and a due date never
rings: [non-goals.md](non-goals.md) → *No sync daemon / server*, *No reminders or
alarms*, *No recurrence on a CLOCK*.
