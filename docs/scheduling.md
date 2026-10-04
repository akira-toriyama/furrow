# Scheduling furrow

furrow is **non-daemon** by design (see [non-goals.md](non-goals.md) → *No sync
daemon / server*): every command is a one-shot process you or an agent run
explicitly. So "run furrow on a schedule" is not a furrow feature — the trigger
lives **outside** furrow, in your OS scheduler. On macOS that is **launchd**;
this page is a set of copy-paste recipes. (For Linux use `cron` or a
`systemd` timer; the furrow command lines are identical.)

## The one thing to get right: discovery

A furrow command discovers its store from the current directory (walking up for a
`.furrow`, a `.furrow-pointer.toml`, or a user-level board — see the README's
*Discovery precedence*). A launchd job starts **in `/` with a minimal
environment** unless its plist says otherwise, so make the store explicit. Two
clean options:

- **`FURROW_DIR=/abs/path/to/.furrow`** — name the store: the `.furrow`
  directory itself, as an absolute path (furrow does not expand `~`). No walk
  and no scope gate, so it works from any cwd. It declares no repo scope, so
  the job sees the whole board — unless the board's own `config.toml` sets
  `default_repo`, which then narrows the filtering reads (`next`, `ls`,
  `revisit`, `brief`'s picks, …) and the `archive` sweep, though never `lint` or
  brief's due band (Recipe 5); `-r ''` (in a plist, the one argument `--repo=`)
  widens such a command back to the whole board. Best for a central board,
  whatever number of repos it backs.
- **`WorkingDirectory=/abs/path/to/repo`** — run "inside" a repo whose store (or
  pointer, or enclosing board scope) discovery then finds normally.

Not `FURROW_BOARD`: it is gated like a `[[board]]` entry, with one scope two
levels above its store (`…/you/projects/.furrow` → `…/you`), so from `/` it
resolves no board and every furrow call below exits 2 (kind `validation`) —
which Recipe 1's log shows, but a digest that discards stderr turns into a blank
notification.

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
furrow does, never just because there is nothing to do. The title travels as an
argument, so a quote in it cannot break the AppleScript.

## Recipe 3 — interval instead of a clock time

For "every 4 hours" use `StartInterval` (seconds) instead of
`StartCalendarInterval`:

```xml
<key>StartInterval</key>
<integer>14400</integer>
```

launchd also fires a missed job **once** on wake if the machine was asleep at the
scheduled time — which is exactly what you want for a housekeeping task.

## Recipe 4 — a weekly review reminder

`furrow review <repo>` (the GTD weekly-review verb) is built: it records a
per-repo review clock, and `furrow sync` / `furrow brief` surface the repos
whose clock has lapsed (`unreviewed`). The same launchd pattern as Recipe 2 —
its `FURROW_DIR` export, a weekly `StartCalendarInterval` — schedules the nudge;
point the script at `furrow brief --json | jq '.revisit.unreviewed'` and post
the digest, then `furrow review <repo>` after the review to reset the clock.

`furrow revisit` is the per-TASK view (`no_repo`, `value_unset`, `effort_unset`,
`stale`, `dep_done`); a box's signals (`epic_all_done` / `epic_stuck` /
`epic_stale` / `epic_dep_done` / `epic_review_due`) and the repo clock are
board-level tallies, so they ride on the summary that `sync` and `brief`
return, not on `revisit`. Since board layout v9 the same `stale_after_days`
clock also drives a STANDING box's review cadence: `furrow review <epic-ref>`
stamps the box, and `epic_review_due` fires when that review lapses — fold the
boxes into the same weekly ritual as the repos.

## Recipe 5 — a due-date digest

A task can carry a **due** stamp (`furrow add --due` / `furrow set --due`), but
furrow never pushes: nothing fires at the promised instant. The date is surfaced
by the two reads that already exist — `furrow brief` LEADS with it (the session
you start is where it lands), and `furrow lint` finds it (`due-overdue` is an
error, `due-today` a warning, and `due-inversion` — a dated task waiting on a
dependency promised later than itself — a warning too). **Both are board-wide**: `lint` has no repo filter
at all, and brief's band drops every AUTOMATIC narrowing (the epic focus, the
lane filter, the board repo scope — cwd-derived or the board's `default_repo`),
so the two count the same set and
a digest built on either sees the whole promise whichever repo you are sitting
in. Only an explicitly typed `-r`/`-l` narrows brief's band. Scheduling only turns those
pull-reads into a push, with the same launchd pattern as Recipe 2:

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

furrow will not grow a `--daemon`, a `furrow schedule` subcommand, or a built-in
notifier — that would put an always-on process behind a tool whose whole premise
is "plain files in your repo" (see [non-goals.md](non-goals.md)). The scheduler
is the OS's job; furrow stays a one-shot command. A **due date is not a
reminder** in that sense either: it is state a read reports when you run one,
never an alarm furrow rings.
