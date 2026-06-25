<!-- 1 item = 1 PR (squash). Keep ROADMAP.md / docs in sync in the SAME PR. -->

## What & why

<!-- The change in one or two sentences, and the reason. Link the ROADMAP phase
     or .furrow task id this closes. -->

## Verification

- [ ] `go build ./...`
- [ ] `go test ./...`
- [ ] `go vet ./...` + `golangci-lint run`
- [ ] `sh scripts/check-marshal-singlepath.sh`
- [ ] Docs / ROADMAP updated in this PR (未達成を暗黙にしない)

## Notes

<!-- Anything not done yet, follow-ups, or risks — state them explicitly. -->
