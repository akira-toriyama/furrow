package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/app"
	"github.com/akira-toriyama/furrow/v9/internal/core"
)

func TestCLINextReasons(t *testing.T) {
	initStore(t)
	base := addTask(t, "base", "-s", "ready")
	follow := addTask(t, "follow", "-s", "ready", "--dep", base)
	run(t, "done", base) // base leaves next; follow becomes actionable

	out, code := run(t, "--json", "next")
	if code != 0 {
		t.Fatalf("next --json exit = %d:\n%s", code, out)
	}
	var views []struct {
		ID     string `json:"id"`
		Reason struct {
			InNextLane    string   `json:"in_next_lane"`
			DepsSatisfied []string `json:"deps_satisfied"`
		} `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &views); err != nil {
		t.Fatalf("next --json should be an array with reasons, got %v:\n%s", err, out)
	}
	var got *struct {
		ID     string `json:"id"`
		Reason struct {
			InNextLane    string   `json:"in_next_lane"`
			DepsSatisfied []string `json:"deps_satisfied"`
		} `json:"reason"`
	}
	for i := range views {
		if views[i].ID == follow {
			got = &views[i]
		}
	}
	if got == nil {
		t.Fatalf("%s (follow) should be actionable after its dep is done:\n%s", follow, out)
	}
	if got.Reason.InNextLane != "ready" {
		t.Errorf("reason.in_next_lane = %q, want ready", got.Reason.InNextLane)
	}
	if len(got.Reason.DepsSatisfied) != 1 || got.Reason.DepsSatisfied[0] != base {
		t.Errorf("reason.deps_satisfied = %v, want [%s]", got.Reason.DepsSatisfied, base)
	}
}

func TestCLIDoneAndNextFlow(t *testing.T) {
	initStore(t)
	base := addTask(t, "base", "-s", "ready")
	addTask(t, "dependent", "-s", "ready", "--dep", base)

	out, _ := run(t, "next", "--ndjson")
	if strings.Contains(out, "dependent") {
		t.Errorf("dependent should be blocked before base is done:\n%s", out)
	}
	if _, code := run(t, "done", base); code != 0 {
		t.Fatalf("done exit = %d", code)
	}
	out, _ = run(t, "next", "--ndjson")
	if !strings.Contains(out, "dependent") {
		t.Errorf("dependent should be actionable after base done:\n%s", out)
	}
}

func TestCLIArchiveJSONIsArrayNotNull(t *testing.T) {
	initStore(t)
	out, code := run(t, "--json", "archive")
	if code != 0 {
		t.Fatalf("archive --json exit = %d", code)
	}
	if !strings.Contains(out, `"tasks": []`) {
		t.Errorf("empty archive --json should emit \"tasks\": [], got:\n%s", out)
	}
	if strings.Contains(out, `"tasks": null`) {
		t.Errorf("archive --json must never emit null tasks:\n%s", out)
	}
}

func TestCLIArchiveRepoScope(t *testing.T) {
	// Seed two aged done tasks in different repos directly through the store
	// (old Closed makes them archivable under the default age guard), then drive
	// the CLI with -r to fold only one repo's done.
	dir := t.TempDir()
	ia, err := app.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idx, _ := ia.Store.Load()
	idx.Add(core.Task{ID: "t-aaa1", Title: "a done", Status: "done", Priority: 100,
		Created: old, Updated: old, Closed: &old, Repos: []string{"owner/a"}, Body: core.BodyPath("t-aaa1")})
	idx.Add(core.Task{ID: "t-bbb1", Title: "b done", Status: "done", Priority: 110,
		Created: old, Updated: old, Closed: &old, Repos: []string{"owner/b"}, Body: core.BodyPath("t-bbb1")})
	ia.Store.Save(idx)
	ia.Store.SaveBody("t-aaa1", "# a\n")
	ia.Store.SaveBody("t-bbb1", "# b\n")
	t.Setenv(app.EnvDir, filepath.Join(dir, app.DirName))

	out, code := run(t, "--json", "archive", "-r", "owner/a", "--yes")
	if code != 0 {
		t.Fatalf("archive -r exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "t-aaa1") {
		t.Errorf("owner/a task should be archived:\n%s", out)
	}
	if strings.Contains(out, "t-bbb1") {
		t.Errorf("owner/b task must NOT be archived (out of scope):\n%s", out)
	}
	if !strings.Contains(out, `"repos": [`) || !strings.Contains(out, "owner/a") {
		t.Errorf("archive --json should record the repo scope in a repos array:\n%s", out)
	}

	// An unresolvable short repo name is a validation error (exit 2), same
	// contract as the read commands — never a silent whole-board sweep.
	if _, code := run(t, "archive", "-r", "ghost", "--yes"); code != int(core.CodeValidation) {
		t.Errorf("unresolvable -r should exit 2, got %d", code)
	}
}

// TestCLIArchiveSweepNamesItsScope pins the sweep's report on the fact that
// decides its blast radius. Every read in a cwd is silently scoped to the board
// repo, so an operator reads a whole session's worth of one repo's tasks and then
// types the obvious housekeeping command — but the sweep is board-wide, and the
// line it printed named no scope at all. Only the NARROW variant said "in
// owner/a", which made the 3x-wider one read as the neutral default. The report
// must name the scope in both directions, and it reuses the vocabulary `board`
// already prints for the same fact ("whole board") rather than inventing a
// second word for it.
func TestCLIArchiveSweepNamesItsScope(t *testing.T) {
	dir := t.TempDir()
	ia, err := app.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idx, _ := ia.Store.Load()
	idx.Add(core.Task{ID: "t-aaa1", Title: "a done", Status: "done", Priority: 100,
		Created: old, Updated: old, Closed: &old, Repos: []string{"owner/a"}, Body: core.BodyPath("t-aaa1")})
	idx.Add(core.Task{ID: "t-bbb1", Title: "b done", Status: "done", Priority: 110,
		Created: old, Updated: old, Closed: &old, Repos: []string{"owner/b"}, Body: core.BodyPath("t-bbb1")})
	if err := ia.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	t.Setenv(app.EnvDir, filepath.Join(dir, app.DirName))

	// The wide sweep — no -r — must say so on the same line as the count.
	out, code := run(t, "archive")
	if code != 0 {
		t.Fatalf("archive exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "whole board") {
		t.Errorf("an unscoped sweep must name its scope on the report line, got:\n%s", out)
	}

	// The narrow sweep keeps naming the repo it is limited to.
	out, code = run(t, "archive", "-r", "owner/a")
	if code != 0 {
		t.Fatalf("archive -r exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "in owner/a") {
		t.Errorf("a scoped sweep must name its repo, got:\n%s", out)
	}
	if strings.Contains(out, "whole board") {
		t.Errorf("a scoped sweep must NOT claim the whole board, got:\n%s", out)
	}
}

// TestCLIListMultiValueOR pins the -s/-l comma = OR plumbing end-to-end through
// the real cobra flags. The OR semantics rely on the comma reaching the app layer
// intact: both -s and -l are StringArrayVar (which, unlike StringSliceVar, does
// NOT split on commas — joinOrFilter re-joins the repeats and the single
// app-layer split stays the one parser). If a maintainer ever switched either to
// StringSliceVarP or added a CLI-layer split, cobra would consume the comma and
// this test would fail where the app-level TestListMultiValueOR could not see it.
func TestCLIListMultiValueOR(t *testing.T) {
	initStore(t)
	addTask(t, "i-bug", "-s", "inbox", "-l", "bug")
	addTask(t, "b-urgent", "-s", "backlog", "-l", "urgent")
	addTask(t, "r-bug", "-s", "ready", "-l", "bug")

	out, code := run(t, "--ndjson", "ls", "-s", "inbox,backlog")
	if code != 0 {
		t.Fatalf("ls -s exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "i-bug") || !strings.Contains(out, "b-urgent") || strings.Contains(out, "r-bug") {
		t.Errorf("`ls -s inbox,backlog` should OR to i-bug + b-urgent only:\n%s", out)
	}

	out, _ = run(t, "--ndjson", "ls", "-l", "bug,urgent")
	for _, want := range []string{"i-bug", "b-urgent", "r-bug"} {
		if !strings.Contains(out, want) {
			t.Errorf("`ls -l bug,urgent` should include %q:\n%s", want, out)
		}
	}

	out, _ = run(t, "--ndjson", "ls", "-s", "inbox,backlog", "-l", "bug")
	if !strings.Contains(out, "i-bug") || strings.Contains(out, "b-urgent") || strings.Contains(out, "r-bug") {
		t.Errorf("`ls -s inbox,backlog -l bug` should AND to i-bug only:\n%s", out)
	}
}

// TestCLILsStatusRepeatUnion pins t-1bwc: a REPEATED -s (as opposed to a single
// comma-joined -s) unions its lanes instead of silently keeping only the last —
// the "silent last-wins" trap of the old StringVarP flag. It is order-independent,
// composes with a comma within one -s, and is shared by every -s command (ls +
// search proven here; stats reuses the same joinOrFilter helper).
func TestCLILsStatusRepeatUnion(t *testing.T) {
	initStore(t)
	addTask(t, "i-task", "-s", "inbox")
	addTask(t, "b-task", "-s", "backlog")
	addTask(t, "r-task", "-s", "ready")

	// repeated -s unions (the fix): -s inbox -s backlog matches the first two, not
	// just the last (which was the pre-fix behavior: backlog only).
	out, code := run(t, "--ndjson", "ls", "-s", "inbox", "-s", "backlog")
	if code != 0 {
		t.Fatalf("ls -s inbox -s backlog exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "i-task") || !strings.Contains(out, "b-task") || strings.Contains(out, "r-task") {
		t.Errorf("repeated -s should OR to i-task + b-task only:\n%s", out)
	}

	// order-independent: the reversed spelling must union identically (the old bug
	// was order-sensitive — the LAST -s won).
	out, _ = run(t, "--ndjson", "ls", "-s", "backlog", "-s", "inbox")
	if !strings.Contains(out, "i-task") || !strings.Contains(out, "b-task") || strings.Contains(out, "r-task") {
		t.Errorf("reversed repeated -s should union identically:\n%s", out)
	}

	out, _ = run(t, "--ndjson", "ls", "-s", "inbox,backlog", "-s", "ready")
	for _, want := range []string{"i-task", "b-task", "r-task"} {
		if !strings.Contains(out, want) {
			t.Errorf("comma+repeat -s should include %q:\n%s", want, out)
		}
	}

	// an unknown token in ANY repeat still fails fast (exit 2 + candidates), the
	// same closed-vocabulary contract a single -s honors.
	fe, _ := runErr(t, "ls", "-s", "inbox", "-s", "ghost")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Errorf("a bad lane in a repeated -s should exit 2 with candidates, got %+v", fe)
	}

	// the fix is shared by every -s command via joinOrFilter: search unions too.
	out, _ = run(t, "--ndjson", "search", "task", "-s", "inbox", "-s", "backlog")
	if !strings.Contains(out, "i-task") || !strings.Contains(out, "b-task") || strings.Contains(out, "r-task") {
		t.Errorf("search should honor repeated -s the same way:\n%s", out)
	}
}

// TestCLILabelRepeatUnion pins t-k1sr: a REPEATED -l unions its tags instead of
// silently keeping only the last — the same "silent last-wins" trap #128 fixed
// for -s, now closed for -l. It is order-independent, composes with a comma
// within one -l, is shared by every -l command (ls/next/search/revisit/stats
// all proven here), and does NOT regress the single-token DidYouMeanRepo guard
// (the -l-specific risk of comma-joining).
func TestCLILabelRepeatUnion(t *testing.T) {
	initStore(t)
	addTask(t, "wip bug", "-s", "ready", "-l", "bug")
	addTask(t, "wip urgent", "-s", "ready", "-l", "urgent")
	addTask(t, "wip chore", "-s", "ready", "-l", "chore")

	// repeated -l unions (the fix): -l bug -l urgent matches the first two, not
	// just the last (the pre-fix behavior was chore/urgent-only, order-dependent).
	out, code := run(t, "--ndjson", "ls", "-l", "bug", "-l", "urgent")
	if code != 0 {
		t.Fatalf("ls -l bug -l urgent exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("repeated -l should OR to bug + urgent only:\n%s", out)
	}

	// order-independent: the old bug kept whichever -l came LAST.
	out, _ = run(t, "--ndjson", "ls", "-l", "urgent", "-l", "bug")
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("reversed repeated -l should union identically:\n%s", out)
	}

	out, _ = run(t, "--ndjson", "ls", "-l", "bug,urgent", "-l", "chore")
	for _, want := range []string{"wip bug", "wip urgent", "wip chore"} {
		if !strings.Contains(out, want) {
			t.Errorf("comma+repeat -l should include %q:\n%s", want, out)
		}
	}

	// shared by every -l command via joinOrFilter: next (a different scopedQuery
	// consumer than ls) unions too — all three are in a next lane with no deps.
	out, _ = run(t, "--ndjson", "next", "-l", "bug", "-l", "urgent")
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("next should honor repeated -l the same way:\n%s", out)
	}
	// and search (a positional term + -l filter).
	out, _ = run(t, "--ndjson", "search", "wip", "-l", "bug", "-l", "urgent")
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("search should honor repeated -l the same way:\n%s", out)
	}
	// and revisit (all three are open with unset estimates, so all surface; the -l
	// repeat must narrow to bug+urgent, not last-wins to one).
	out, _ = run(t, "--ndjson", "revisit", "-l", "bug", "-l", "urgent")
	if !strings.Contains(out, "wip bug") || !strings.Contains(out, "wip urgent") || strings.Contains(out, "wip chore") {
		t.Errorf("revisit should honor repeated -l the same way:\n%s", out)
	}
	// and stats: the aggregate total counts the union (2), not a single -l (1).
	out, code = run(t, "--json", "stats", "-l", "bug", "-l", "urgent")
	if code != 0 {
		t.Fatalf("stats -l bug -l urgent exit = %d:\n%s", code, out)
	}
	var st struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("parse stats --json: %v\n%s", err, out)
	}
	if st.Total != 2 {
		t.Errorf("stats repeated -l should total the union (2), got %d:\n%s", st.Total, out)
	}

	// DidYouMeanRepo must NOT regress. Seed a repo-scoped task so a label that
	// happens to name that repo triggers the did-you-mean guard.
	addTask(t, "in webapp", "-s", "ready", "-r", "owner/webapp")
	// a SINGLE -l naming a repo (and matching no tag) still exits 2 pointing at -r.
	fe, _ := runErr(t, "ls", "-l", "webapp")
	if fe == nil || fe.Code != core.CodeValidation || len(fe.Candidates) == 0 {
		t.Errorf("single -l naming a repo should still exit 2 with candidates, got %+v", fe)
	}
	// a REPEATED -l that matches nothing must NOT misfire the guard: the joined
	// "webapp,ghost" resolves to no repo, so it is a clean empty result (exit 0).
	out, code = run(t, "--ndjson", "ls", "-l", "webapp", "-l", "ghost")
	if code != 0 {
		t.Errorf("repeated -l matching nothing should be a clean empty result, got %d:\n%s", code, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("repeated -l webapp -l ghost should match nothing:\n%s", out)
	}
}

// TestCLILsUnknownLaneExit2 pins that a typo'd -s lane fails fast (exit 2)
// carrying the configured lanes in the error's candidates array — not a silent
// [] exit 0 — even when a comma filter mixes a known and an unknown token. This
// is the read-side symmetry with move/add (t-bec7 案B).
func TestCLILsUnknownLaneExit2(t *testing.T) {
	initStore(t)
	addTask(t, "i", "-s", "inbox")

	for _, bad := range []string{"in_progress", "inbox,ghost"} {
		fe, _ := runErr(t, "ls", "-s", bad)
		if fe == nil || fe.Code != core.CodeValidation {
			t.Fatalf("ls -s %q should exit 2, got %+v", bad, fe)
		}
		if len(fe.Candidates) == 0 {
			t.Errorf("ls -s %q error should carry lane candidates, got %+v", bad, fe)
		}
	}
}

// TestCLIArchiveByID pins t-kx76 (e): `archive <id> --yes` retires exactly that
// task; a non-done id is exit 2; --older-than/-r can't combine with an id list.
func TestCLIArchiveByID(t *testing.T) {
	initStore(t)
	doneID := addTask(t, "finished", "-s", "ready")
	run(t, "done", doneID)
	openID := addTask(t, "in flight", "-s", "ready")

	// a non-done id is refused (exit 2) — you can't strand live work in archive/.
	if _, code := run(t, "archive", openID, "--yes"); code != int(core.CodeValidation) {
		t.Errorf("archiving a non-done id should exit 2, got %d", code)
	}
	if _, code := run(t, "archive", doneID, "--older-than", "0", "--yes"); code != int(core.CodeValidation) {
		t.Errorf("archive <id> --older-than should exit 2, got %d", code)
	}
	// retire the done one by id.
	out, code := run(t, "--json", "archive", doneID, "--yes")
	if code != 0 {
		t.Fatalf("archive by id exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, doneID) {
		t.Errorf("archive --json should list the retired id:\n%s", out)
	}
	if _, code := run(t, "show", doneID); code != int(core.CodeNotFound) {
		t.Errorf("archived task should be not-found in the hot store, got %d", code)
	}
}

// TestCLIMigrateAppliesLabel pins that `migrate --label` stamps the label on
// every imported task (the unblocker for importing into a label-required
// cross-repo store). Verified via the label filter: `ls -l facet` must return
// every migrated task, and a different label must match nothing.
func TestCLIMigrateAppliesLabel(t *testing.T) {
	initStore(t)
	src := filepath.Join(t.TempDir(), "Task.md")
	md := "# Demo\n\n## 🎯 Open\n\n### 1. First task\nDetail one.\n\n### 2. Second task\nDetail two.\n"
	if err := os.WriteFile(src, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, "migrate", src, "--label", "facet", "--yes"); code != 0 {
		t.Fatalf("migrate --yes exit = %d", code)
	}
	// Filtering by the applied label returns every imported task.
	out, code := run(t, "ls", "-l", "facet", "--json")
	if code != 0 {
		t.Fatalf("ls -l facet exit = %d", code)
	}
	if !strings.Contains(out, "First task") || !strings.Contains(out, "Second task") {
		t.Errorf("facet-labeled ls missing migrated tasks:\n%s", out)
	}
	// A different label matches nothing (the label is specifically facet).
	out, _ = run(t, "ls", "-l", "other", "--json")
	if strings.Contains(out, "First task") || strings.Contains(out, "Second task") {
		t.Errorf("ls -l other should not return the facet tasks:\n%s", out)
	}
}

// TestCLIApplyDryRunWritesNothing pins the local-gate use of `apply --dry-run`
// (the footer check before `gh pr create`): the store directory is
// byte-identical before and after, the exit code is the real apply's (a bad
// lane is still 2), and the report is the same shape plus dry_run.
func TestCLIApplyDryRunWritesNothing(t *testing.T) {
	initStore(t)
	id := addTask(t, "task one", "-s", "ready")
	dir := os.Getenv(app.EnvDir)

	snapshot := func() map[string]string {
		t.Helper()
		files := map[string]string{}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(b)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}

	before := snapshot()
	out, code := runIn(t, "SetStatus-task: "+id+" done\n", "apply", "--on", "merge", "--ref", "x#1", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, id+"  would move → done") {
		t.Errorf("dry-run should say what it would do:\n%s", out)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Errorf("dry-run changed the store directory")
	}

	out, code = runIn(t, "SetStatus-task: "+id+" ghostlane\n", "--json", "apply", "--on", "merge", "--dry-run")
	if code != 2 {
		t.Errorf("a bad lane under --dry-run should exit 2 like a real apply, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, `"dry_run": true`) || !strings.Contains(out, `"candidates"`) {
		t.Errorf("dry-run --json should carry dry_run and the lane candidates:\n%s", out)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Errorf("a failing dry-run changed the store directory")
	}

	// The real apply afterwards is unaffected by the previews.
	if _, code := runIn(t, "SetStatus-task: "+id+" done\n", "apply", "--on", "merge"); code != 0 {
		t.Errorf("real apply after previews exit %d", code)
	}
}
