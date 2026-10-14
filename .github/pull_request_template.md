<!-- 1 item = 1 PR (squash). Keep the docs in sync in the SAME PR. -->

## What & why

<!-- The change in one or two sentences, and the reason. If it moves a task,
     add a `SetStatus-task: <body URL or id> [<lane>]` line. -->

## Verification

- `sh scripts/check.sh` green (build, vet, golangci, tests, drift guards, smoke).
- Docs updated in this PR (each fact stated once — the rest point at it).

## Notes

<!-- Anything not done yet, follow-ups, or risks — state them explicitly. -->
