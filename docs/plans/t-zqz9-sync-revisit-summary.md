# furrow sync revisit-summary — Implementation Plan (t-zqz9 slice 1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put the already-existing `revisit` signal (`dep_done`/`stale`) into `furrow sync` output, so tracker staleness surfaces in the command the session loop actually runs.

**Architecture:** `app.Sync` stays a pure git wrapper. A new read-only `app.RevisitSummary` counts dep_done/stale over the current repo scope; `cmd_sync` computes it after a successful sync and renders one extra human line (or a `revisit` key in `--json`). Reuses `core.RevisitReasons` and `QueryOpts.match` — no new core code, no layer crossing.

**Tech Stack:** Go (stdlib only), cobra CLI, memstore/real-git test harnesses.

Task: <https://github.com/akira-toriyama/projects/blob/main/.furrow/bodies/t-zqz9.md>

## Global Constraints

- **Layers:** `core` is pure (no new imports); `cli` reads/mutates only through `app`. No `json.Marshal` on `*Index`/`*Task`/`*Meta` (irrelevant here — `SyncProgress`/summary are transient DTOs, freely marshalled via the existing `mustJSON`).
- **Signals counted:** exactly `dep_done` and `stale`. Never `no_repo`/`value_unset`/`effort_unset`.
- **Scope:** current auto repo (`a.DefaultRepo` when `a.AutoFilter`), else board-wide. Uses `QueryOpts.match` (strict) so **repo-less drafts are excluded**. No new `-r`/`--stale-days` flags on `sync` this slice.
- **Quiet when clean:** both counts zero → no extra human line, no `revisit` JSON key (`omitempty`).
- **Compute only on success:** summary computed only when `a.Sync` returns `err == nil`.
- **Commits:** gitmoji + Conventional; `feat(sync)` for the behavior, `docs` for README/CLAUDE.md. Footer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- **Verify:** `sh scripts/check.sh` green before finishing.

## Design context

`furrow sync` is the only loop command that runs `pull --rebase`, so it is the only place a *foreign merge's* freshly-done dependency surfaces the moment it lands (the RCA "backflow channel": close X → re-judge overlapping Y). `next` never pulls. Scope is repo-local to keep a shared multi-repo board un-noisy; drafts stay `revisit`'s job.

Human output:
```
sync: committed=false pulled=true pushed=true conflict=false
revisit: 2 dep_done, 1 stale (furrow) — furrow revisit
```
JSON output (`revisit` omitted entirely when empty):
```json
{"committed":false,"pulled":true,"pushed":true,"conflict":false,
 "revisit":{"dep_done":["t-0046"],"stale":["t-yyyy"]}}
```

## File map

- Modify `internal/app/revisit.go` — add `RevisitSummary` type + `(*App).RevisitSummary`.
- Modify `internal/app/revisit_test.go` — memstore unit tests for the counts/scope/exclusions.
- Modify `internal/cli/cmd_sync.go` — compute + render (helpers `revisitScopeLabel`, `revisitLine`, `syncOutput` struct).
- Modify `internal/cli/sync_cli_test.go` — pure render tests + real-git E2E (`initGitBoard` helper).
- Modify `README.md`, `README.ja.md`, `CLAUDE.md` — document the new `revisit` key.

---

### Task 1: `app.RevisitSummary` — count dep_done/stale in scope

**Files:**
- Modify: `internal/app/revisit.go`
- Test: `internal/app/revisit_test.go`

**Interfaces:**
- Consumes: `core.RevisitReasons(core.Task, time.Time, int, map[string]bool) []core.RevisitReason`, `core.RevisitDepDone`, `core.RevisitStale`; `QueryOpts.match(*core.Task) bool`; `a.Cfg.DoneLane`, `a.Cfg.IsTerminal`, `a.Clock.Now()`, `a.load()`.
- Produces: `type RevisitSummary struct { DepDone []string; Stale []string }`, `(s RevisitSummary) Empty() bool`, `(a *App) RevisitSummary(o QueryOpts, staleDays int) (RevisitSummary, error)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/app/revisit_test.go`:

```go
func TestRevisitSummaryCountsScopedDepDoneAndStale(t *testing.T) {
	a, clk := revisitApp()

	// A dependency we finish, plus a stale in-scope task at T0.
	dep, _ := a.Add("dep", AddOpts{Status: "ready", Value: p(1), Effort: p(1), Repos: []string{"o/r"}})
	a.Done(dep.ID) // -> done lane (terminal)
	staleIn, _ := a.Add("stale-in", AddOpts{Status: "ready", Value: p(3), Effort: p(2), Repos: []string{"o/r"}})
	a.Add("stale-other", AddOpts{Status: "ready", Value: p(3), Effort: p(2), Repos: []string{"x/y"}}) // other repo
	a.Add("stale-draft", AddOpts{Status: "ready", Value: p(3), Effort: p(2)})                          // draft (no repo)
	a.Add("parked", AddOpts{Status: "icebox", Repos: []string{"o/r"}})                                 // terminal

	// Age everything 60d, then a fresh dependent (in scope) whose dep is done.
	clk.t = clk.t.AddDate(0, 0, 60)
	user, _ := a.Add("dep-user", AddOpts{Status: "ready", Value: p(3), Effort: p(2), Repos: []string{"o/r"}, Deps: []string{dep.ID}})

	sum, err := a.RevisitSummary(QueryOpts{ScopeRepo: "o/r"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{user.ID}; !eq(sum.DepDone, want) {
		t.Errorf("DepDone = %v, want %v", sum.DepDone, want)
	}
	// Only the in-scope stale task: other-repo, draft, terminal, and the fresh
	// dependent (updated 0d ago) are all excluded.
	if want := []string{staleIn.ID}; !eq(sum.Stale, want) {
		t.Errorf("Stale = %v, want %v", sum.Stale, want)
	}
	if sum.Empty() {
		t.Error("summary should not be Empty")
	}
}

func TestRevisitSummaryStaleDaysZeroDisablesStale(t *testing.T) {
	a, clk := revisitApp()
	a.Add("old", AddOpts{Status: "ready", Value: p(1), Effort: p(1), Repos: []string{"o/r"}})
	clk.t = clk.t.AddDate(0, 0, 90)

	sum, err := a.RevisitSummary(QueryOpts{ScopeRepo: "o/r"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Stale) != 0 || !sum.Empty() {
		t.Errorf("staleDays=0 must disable stale; got %+v", sum)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/app/ -run TestRevisitSummary -v`
Expected: FAIL — `a.RevisitSummary undefined` (compile error).

- [ ] **Step 3: Write minimal implementation**

Append to `internal/app/revisit.go`:

```go
// RevisitSummary is the two "silent staleness" counts the session loop must not
// miss, within a scope: task ids whose dependency is already done (reconcile-on-
// close) and task ids past the stale threshold. Ids are in canonical order.
type RevisitSummary struct {
	DepDone []string // task ids with >=1 dependency in the done lane
	Stale   []string // task ids not updated within staleDays
}

// Empty reports whether nothing is worth surfacing (a clean board).
func (s RevisitSummary) Empty() bool { return len(s.DepDone) == 0 && len(s.Stale) == 0 }

// RevisitSummary tallies the dep_done and stale signals over the open
// (non-terminal) tasks passing o.match — strict scope, so repo-less drafts are
// excluded (that is the difference from Revisit, which surfaces drafts as
// no_repo). staleDays <= 0 disables the stale half (matching core.RevisitReasons).
// It is purely read-only; it drives the `furrow sync` staleness nudge.
func (a *App) RevisitSummary(o QueryOpts, staleDays int) (RevisitSummary, error) {
	idx, err := a.load()
	if err != nil {
		return RevisitSummary{}, err
	}
	doneIDs := map[string]bool{}
	for _, t := range idx.Tasks {
		if t.Status == a.Cfg.DoneLane {
			doneIDs[t.ID] = true
		}
	}
	now := a.Clock.Now()
	sum := RevisitSummary{}
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if a.Cfg.IsTerminal(t.Status) || !o.match(t) {
			continue
		}
		var depDone, stale bool
		for _, r := range core.RevisitReasons(*t, now, staleDays, doneIDs) {
			switch r.Code {
			case core.RevisitDepDone:
				depDone = true
			case core.RevisitStale:
				stale = true
			}
		}
		if depDone {
			sum.DepDone = append(sum.DepDone, t.ID)
		}
		if stale {
			sum.Stale = append(sum.Stale, t.ID)
		}
	}
	return sum, nil
}
```

Confirm `internal/app/revisit.go` already imports `"github.com/akira-toriyama/furrow/internal/core"` (it does — `Revisit` uses it). No new import.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local go test ./internal/app/ -run TestRevisitSummary -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/app/revisit.go internal/app/revisit_test.go
git commit -m ":sparkles: feat(app): RevisitSummary — scoped dep_done/stale counts

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: render the summary in `furrow sync`

**Files:**
- Modify: `internal/cli/cmd_sync.go`
- Test: `internal/cli/sync_cli_test.go`

**Interfaces:**
- Consumes: `app.RevisitSummary`, `(*app.App).RevisitSummary`, `a.DefaultRepo`, `a.AutoFilter`, `a.Cfg.RevisitStaleDays`; existing `out`, `flagJSON`, `flagNDJSON`, `printJSON`, `printNDJSONValue`, `mustJSON`.
- Produces: `revisitScopeLabel(a *app.App) string`, `revisitLine(sum app.RevisitSummary, scope string) string`, `type syncOutput struct{ *app.SyncProgress; Revisit *app.RevisitSummary }` — all in `internal/cli`.

- [ ] **Step 1: Write the failing render tests**

Append to `internal/cli/sync_cli_test.go`. Add the app import to that file (`"github.com/akira-toriyama/furrow/internal/app"`); `"strings"` is already imported.

```go
func TestRevisitLineRendering(t *testing.T) {
	// Non-empty: counts, scope tag, and the call-to-action.
	got := revisitLine(app.RevisitSummary{DepDone: []string{"t-1", "t-2"}, Stale: []string{"t-3"}}, "furrow")
	want := "revisit: 2 dep_done, 1 stale (furrow) — furrow revisit"
	if got != want {
		t.Errorf("revisitLine = %q, want %q", got, want)
	}
	// Empty summary renders nothing (clean board stays quiet).
	if got := revisitLine(app.RevisitSummary{}, "furrow"); got != "" {
		t.Errorf("empty revisitLine = %q, want \"\"", got)
	}
	// Board-wide fallback tag.
	if got := revisitLine(app.RevisitSummary{Stale: []string{"t-3"}}, "board"); !strings.Contains(got, "(board)") {
		t.Errorf("board tag missing: %q", got)
	}
}

func TestSyncOutputJSONShape(t *testing.T) {
	prog := &app.SyncProgress{Pulled: true, Pushed: true}
	// With a summary: revisit object carries the ids.
	withSum := mustJSON(syncOutput{prog, &app.RevisitSummary{DepDone: []string{"t-0046"}}})
	for _, want := range []string{`"pulled": true`, `"revisit"`, `"dep_done"`, `"t-0046"`} {
		if !strings.Contains(string(withSum), want) {
			t.Errorf("json missing %s:\n%s", want, withSum)
		}
	}
	// Without a summary: no revisit key at all (omitempty via nil pointer).
	noSum := mustJSON(syncOutput{prog, nil})
	if strings.Contains(string(noSum), "revisit") {
		t.Errorf("empty summary must omit revisit key:\n%s", noSum)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/cli/ -run 'TestRevisitLineRendering|TestSyncOutputJSONShape' -v`
Expected: FAIL — `revisitLine`/`syncOutput` undefined.

- [ ] **Step 3: Implement render + wire into RunE**

In `internal/cli/cmd_sync.go`, add helpers above `newSyncCmd` and rewrite the output switch. Replace the file's body with:

```go
package cli

import (
	"fmt"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// syncOutput is the agent-facing sync object: the SyncProgress fields, plus a
// revisit summary (omitted entirely when empty). The embedded pointer promotes
// {committed,pulled,pushed,conflict} flat, so the JSON shape is a superset of
// the historical one.
type syncOutput struct {
	*app.SyncProgress
	Revisit *app.RevisitSummary `json:"revisit,omitempty"`
}

// revisitScopeLabel is the tag shown after the counts: the auto repo's short
// name (segment after the last "/"), or "board" when the sync ran board-wide.
func revisitScopeLabel(a *app.App) string {
	if a.DefaultRepo != "" && a.AutoFilter {
		if i := strings.LastIndex(a.DefaultRepo, "/"); i >= 0 && i+1 < len(a.DefaultRepo) {
			return a.DefaultRepo[i+1:]
		}
		return a.DefaultRepo
	}
	return "board"
}

// revisitLine is the one human line appended after the sync summary. Empty
// summary -> "" (the caller prints nothing, so a clean board stays quiet).
func revisitLine(sum app.RevisitSummary, scope string) string {
	if sum.Empty() {
		return ""
	}
	return fmt.Sprintf("revisit: %d dep_done, %d stale (%s) — furrow revisit",
		len(sum.DepDone), len(sum.Stale), scope)
}

// syncScope builds the strict repo scope for the post-sync summary: the board's
// auto repo when auto-filtering, else the whole board.
func syncScope(a *app.App) app.QueryOpts {
	o := app.QueryOpts{}
	if a.DefaultRepo != "" && a.AutoFilter {
		o.ScopeRepo = a.DefaultRepo
	}
	return o
}

func newSyncCmd() *cobra.Command {
	var message string
	c := &cobra.Command{
		Use:   "sync",
		Short: "Commit the board, pull --rebase, push (thin git wrapper)",
		Long: "sync runs the multi-machine board ritual as one command, against the git\n" +
			"repository enclosing the board:\n\n" +
			"  1. auto-commit, pathspec-limited to the .furrow/ directory (other dirty\n" +
			"     files in the repo are never swept in)\n" +
			"  2. git -c rebase.autoStash=true pull --rebase\n" +
			"  3. git push (one pull→push retry on non-fast-forward)\n\n" +
			"On a rebase conflict the rebase is aborted automatically (the board is never\n" +
			"left with conflict markers; your local sync commit survives) and the error\n" +
			"envelope carries id \"sync-conflict\" plus the conflicted paths. The progress\n" +
			"object {committed, pulled, pushed, conflict} goes to stdout even on failure.\n" +
			"After a successful sync it also reports a revisit summary (repo-scoped counts\n" +
			"of tasks with a done dependency or gone stale) so freshly-pulled staleness\n" +
			"surfaces in the loop; the JSON gains a \"revisit\" key when non-empty.\n" +
			"It is a thin git wrapper — not a daemon or a sync server (see docs/non-goals.md).",
		Example: "  furrow sync                   # commit .furrow/, pull --rebase, push\n" +
			"  furrow sync -m \"triage inbox\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			prog, syncErr := a.Sync(message)

			// Compute the revisit summary only on a fully-successful sync (a
			// fresh, consistent, freshly-pulled board). On failure, skip it.
			var sum app.RevisitSummary
			if syncErr == nil {
				if s, err := a.RevisitSummary(syncScope(a), a.Cfg.RevisitStaleDays); err == nil {
					sum = s
				}
			}

			switch {
			case flagNDJSON:
				printNDJSONValue(syncOutput{prog, summaryPtr(sum)})
			case flagJSON:
				printJSON(syncOutput{prog, summaryPtr(sum)})
			default:
				fmt.Fprintln(out, prog.SyncSummary())
				if line := revisitLine(sum, revisitScopeLabel(a)); line != "" {
					fmt.Fprintln(out, line)
				}
			}
			return syncErr
		},
	}
	c.Flags().StringVarP(&message, "message", "m", "", "auto-commit message (default \""+app.DefaultSyncMessage+"\")")
	return c
}

// summaryPtr returns nil for an empty summary so the revisit JSON key is omitted.
func summaryPtr(sum app.RevisitSummary) *app.RevisitSummary {
	if sum.Empty() {
		return nil
	}
	return &sum
}
```

- [ ] **Step 4: Run to verify render tests pass**

Run: `GOTOOLCHAIN=local go test ./internal/cli/ -run 'TestRevisitLineRendering|TestSyncOutputJSONShape' -v`
Expected: PASS.

- [ ] **Step 5: Run the full cli + app suites (no regressions)**

Run: `GOTOOLCHAIN=local go test ./internal/cli/ ./internal/app/`
Expected: ok — existing `TestSyncOutsideGitPrintsProgressAndExits2` still passes (sync fails → summary skipped → JSON unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/cmd_sync.go internal/cli/sync_cli_test.go
git commit -m ":sparkles: feat(sync): surface repo-scoped dep_done/stale after a successful sync

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: real-git E2E — sync prints the revisit line

**Files:**
- Test: `internal/cli/sync_cli_test.go`

**Interfaces:**
- Consumes: `run`, `addTask`, `app.EnvDir`, `app.DirName`, `app.Init`; git via `exec.Command`.
- Produces: `initGitBoard(t) string` (returns the clone path; sets `FURROW_DIR`).

- [ ] **Step 1: Write the failing E2E test**

Append to `internal/cli/sync_cli_test.go` (add imports `"os/exec"`, `"path/filepath"`, `"encoding/json"`):

```go
// initGitBoard sets up a bare origin + one clone with an initialized board,
// points FURROW_DIR at the clone's .furrow, and returns the clone path. Skips
// when git is unavailable. sync can push/pull against origin for real.
func initGitBoard(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	gitT := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command(git, args...)
		c.Dir = dir
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	origin := t.TempDir()
	gitT(origin, "init", "-q", "--bare", "-b", "main")
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(filepath.Dir(clone), "clone", "-q", origin, clone)
	gitT(clone, "config", "user.name", "t")
	gitT(clone, "config", "user.email", "t@e")
	if _, err := app.Init(clone); err != nil {
		t.Fatal(err)
	}
	gitT(clone, "add", "-A")
	gitT(clone, "commit", "-q", "-m", "board")
	gitT(clone, "push", "-q", "-u", "origin", "main")
	t.Setenv(app.EnvDir, filepath.Join(clone, app.DirName))
	return clone
}

func TestSyncSurfacesRevisitLine(t *testing.T) {
	initGitBoard(t)

	// A done dependency and a ready task depending on it -> one dep_done.
	dep := addTask(t, "dep")
	if _, code := run(t, "done", dep); code != 0 {
		t.Fatalf("done exit %d", code)
	}
	user := addTask(t, "needs dep", "--dep", dep)

	// Human sync prints the revisit line (board-wide: no auto repo in a test board).
	hout, hcode := run(t, "sync")
	if hcode != 0 {
		t.Fatalf("sync exit %d:\n%s", hcode, hout)
	}
	if !strings.Contains(hout, "revisit: 1 dep_done") {
		t.Errorf("human sync missing revisit line:\n%s", hout)
	}

	// JSON sync carries the dependent id under revisit.dep_done.
	jout, jcode := run(t, "--json", "sync")
	if jcode != 0 {
		t.Fatalf("json sync exit %d:\n%s", jcode, jout)
	}
	var got syncOutput
	if err := json.Unmarshal([]byte(jout), &got); err != nil {
		t.Fatalf("parse sync --json: %v\n%s", err, jout)
	}
	if got.Revisit == nil || len(got.Revisit.DepDone) != 1 || got.Revisit.DepDone[0] != user {
		t.Errorf("revisit.dep_done = %+v, want [%s]", got.Revisit, user)
	}
}
```

(`add --dep <id>` is a verified repeatable flag; `done <id>` and `dep <id> <dep-id>` are the mutators. `EnvDir`/`DirName`/`Init` are exported from `internal/app`. The test board has no derivable auto repo, so the summary runs board-wide and counts the repo-less `user` task — hence `dep_done: [user]`.)

- [ ] **Step 2: Run the E2E**

Run: `GOTOOLCHAIN=local go test ./internal/cli/ -run TestSyncSurfacesRevisitLine -v`
Expected: PASS (skips cleanly if git is not installed).

- [ ] **Step 3: Commit**

```bash
git add internal/cli/sync_cli_test.go
git commit -m ":white_check_mark: test(sync): real-git E2E — sync surfaces the revisit line

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: docs — README (EN/JA) + CLAUDE.md + final check

**Files:**
- Modify: `README.md`, `README.ja.md`, `CLAUDE.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: Update the sync JSON contract in CLAUDE.md**

In `CLAUDE.md`, the integration-contract bullet describing `furrow sync` output. Find the sentence describing the emitted object (currently the `{committed, pulled, pushed, conflict}` shape) and add: after a successful sync the object also gains a `revisit` key (`{dep_done:[ids], stale:[ids]}`, repo-scoped; omitted when empty) — the loop-visible staleness nudge; run `furrow revisit` for detail.

- [ ] **Step 2: Update README.md and README.ja.md**

In both READMEs, wherever `furrow sync`'s `--json` shape or behavior is documented, add the `revisit` key (EN/JA parity): "on a successful sync, a repo-scoped `revisit` summary of tasks with a done dependency or gone stale (`dep_done`/`stale` id lists) is printed — a nudge to run `furrow revisit`; omitted when the board is clean."

Grep first to locate the exact spots:

Run: `grep -n 'committed\|pulled\|furrow sync' README.md README.ja.md`

- [ ] **Step 3: Full verification**

Run: `sh scripts/check.sh`
Expected: green (marshaller guard + build/vet/test + golangci + schema/config drift + CLI smoke). Fix any drift/lint it flags.

- [ ] **Step 4: Commit**

```bash
git add README.md README.ja.md CLAUDE.md
git commit -m ":memo: docs(sync): document the revisit summary in sync output

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Non-goals (this slice)

`-r`/`--stale-days` flags on sync; the `next`-side summary; dep/parent-edge rewiring; the `furrow lint` "non-terminal epic with a done dep" warning. Separate slices of t-zqz9.

## Done criteria

- `furrow sync` (human) prints the `revisit:` line only when in-scope dep_done/stale exist.
- `furrow sync --json`/`--ndjson` carry `revisit` (ids) when non-empty; byte-identical to before when empty.
- New app + cli + E2E tests pass; `sh scripts/check.sh` green.
- README (EN/JA) + CLAUDE.md document the key.
- Plan doc `docs/plans/t-zqz9-sync-revisit-summary.md` deleted in the PR that merges this (repo convention).
