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
*Discovery precedence*). A launchd job has **no meaningful cwd and a minimal
environment**, so make the store explicit. Two clean options:

- **`FURROW_BOARD=/abs/path/to/.furrow`** — point straight at a central board
  (its scope is derived from the board repo's parent). Best for a cross-repo
  central board.
- **`WorkingDirectory=/abs/path/to/repo`** — run "inside" a repo whose store (or
  pointer, or enclosing board scope) discovery then finds normally.

Always use the **absolute path to the `furrow` binary** (launchd's `PATH` does
not include Homebrew/nix profiles). Find it with `command -v furrow`.

## Recipe 1 — periodic archive (housekeeping)

Fold done tasks closed more than 30 days ago into `.furrow/archive/` every day at
03:00, so the hot board stays light. `--yes` is required (without it `archive`
only previews).

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
    <string>--yes</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>FURROW_BOARD</key>
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

Load it (and re-load after any edit):

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.furrow.archive.plist
# older macOS: launchctl load -w ~/Library/LaunchAgents/dev.furrow.archive.plist
launchctl kickstart -k gui/$(id -u)/dev.furrow.archive   # run once now to test
```

> On a **shared central board** commit the move so other machines see it — either
> chain `furrow sync` after the archive (a second `ProgramArguments` job, or a
> tiny wrapper script) or let each machine's own `sync` pick it up. A bare
> `archive` only writes locally.

## Recipe 2 — a `furrow next` digest (nudge)

Surface "what's ready to work" as a desktop notification every weekday at 09:00.
launchd can't post a notification directly, so wrap the furrow call in a script.

`~/bin/furrow-next-digest.sh`:

```sh
#!/bin/sh
export FURROW_BOARD="/Users/you/src/github.com/you/projects/.furrow"
top="$(/opt/homebrew/bin/furrow next -n1 --json 2>/dev/null | /opt/homebrew/bin/jq -r '.[0].title // "nothing actionable"')"
osascript -e "display notification \"$top\" with title \"furrow: next up\""
```

`~/Library/LaunchAgents/dev.furrow.next.plist` uses `StartCalendarInterval` with
a `Weekday` (1–5 = Mon–Fri) and points `ProgramArguments` at the script. Because
`next` on an empty board now exits 0 (an empty result is healthy), the digest
never fails just because there is nothing to do.

## Recipe 3 — interval instead of a clock time

For "every 4 hours" use `StartInterval` (seconds) instead of
`StartCalendarInterval`:

```xml
<key>StartInterval</key>
<integer>14400</integer>
```

launchd also fires a missed job **once** on wake if the machine was asleep at the
scheduled time — which is exactly what you want for a housekeeping task.

## Future: a review reminder

Once `furrow review` lands (the GTD weekly-review verb, tracked in the board) the
same launchd pattern schedules a weekly review nudge. Until then, Recipe 2's
`next` digest is the closest standing signal.

## Not this

furrow will not grow a `--daemon`, a `furrow schedule` subcommand, or a built-in
notifier — that would put an always-on process behind a tool whose whole premise
is "plain files in your repo" (see [non-goals.md](non-goals.md)). The scheduler
is the OS's job; furrow stays a one-shot command.
