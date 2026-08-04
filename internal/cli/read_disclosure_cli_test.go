package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// This file pins the one disclosure rule every listing read follows (t-sv35):
// when a read NARROWS (scope hides rows) it says so on stderr, when it
// TRUNCATES (-n bites) it names the uncapped total on stderr, and when an
// explicit filter zeroes the result while its token uniquely names a repo it
// is exit 2 + candidates — uniformly, not per-command.

// search used to be the one read that scoped silently: ls said "N draft(s)
// hidden" while search dropped a title-matching draft with no hint at all.
func TestSearch_ScopeHidesDraftsHint(t *testing.T) {
	a, _ := pointerRepoLayout(t)
	if _, err := a.Add("nix in scope", app.AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("nix draft ball", app.AddOpts{Draft: true}); err != nil {
		t.Fatal(err)
	}

	so, se := runLs(t, "search", "nix")
	if strings.Contains(so, "nix draft ball") {
		t.Errorf("the draft must be hidden from the scoped search:\n%s", so)
	}
	if !strings.Contains(se, "draft(s) hidden") || !strings.Contains(se, "-r ''") {
		t.Errorf("stderr should disclose the hidden draft with the -r '' remedy, got:\n%s", se)
	}

	// The remedy works and mutes the hint: -r '' searches the whole board.
	so, se = runLs(t, "search", "nix", "-r", "")
	if !strings.Contains(so, "nix draft ball") {
		t.Errorf("-r '' should surface the draft:\n%s", so)
	}
	if strings.Contains(se, "draft(s) hidden") {
		t.Errorf("-r '' must not re-hint, stderr:\n%s", se)
	}

	// A search whose drafts do not match the term stays quiet.
	_, se = runLs(t, "search", "scope")
	if strings.Contains(se, "draft(s) hidden") {
		t.Errorf("no matching draft was hidden, stderr:\n%s", se)
	}
}

// The -l did-you-mean guard (a tag filter that matched nothing while uniquely
// naming a repo with tasks) fires on search and stats exactly as on ls — the
// same typo must not be exit 2 on one read and a confident zero on another.
func TestSearchStats_LabelDidYouMeanRepo(t *testing.T) {
	initStore(t)
	addTask(t, "demo task", "-r", "me/demo")

	fe, _ := runErr(t, "search", "task", "-l", "demo")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("search -l <repo> should be exit 2, got %+v", fe)
	}
	if len(fe.Candidates) != 1 || fe.Candidates[0] != "me/demo" {
		t.Errorf("search candidates = %v, want [me/demo]", fe.Candidates)
	}

	fe, _ = runErr(t, "stats", "-l", "demo")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("stats -l <repo> should be exit 2, got %+v", fe)
	}
	if len(fe.Candidates) != 1 || fe.Candidates[0] != "me/demo" {
		t.Errorf("stats candidates = %v, want [me/demo]", fe.Candidates)
	}

	// A tag that exists is never second-guessed, even with zero search hits.
	addTask(t, "tagged", "-l", "demo")
	if _, code := run(t, "search", "zzz-no-hit", "-l", "demo"); code != 0 {
		t.Errorf("an existing tag with no hits is a clean empty result, got exit %d", code)
	}
}

// An unknown -r short name now carries the board's repo universe as candidates
// — the machine-readable half the unknown-lane error always had.
func TestUnknownRepoCarriesCandidates(t *testing.T) {
	initStore(t)
	addTask(t, "seed", "-r", "me/demo")

	fe, _ := runErr(t, "ls", "-r", "nope")
	if fe == nil || fe.Kind != core.KindRepoUnknown {
		t.Fatalf("ls -r nope should fail repo-unknown, got %+v", fe)
	}
	if len(fe.Candidates) == 0 || fe.Candidates[0] != "me/demo" {
		t.Errorf("candidates = %v, want the repo universe [me/demo]", fe.Candidates)
	}
}

// -n truncation is disclosed on stderr with the uncapped total, on every
// listing read; a cap that does not bite stays silent, and the JSON payload
// (a bare array) is untouched.
func TestCapDisclosure(t *testing.T) {
	initStore(t)
	addTask(t, "alpha", "-s", "ready")
	addTask(t, "beta", "-s", "ready")
	addTask(t, "gamma", "-s", "ready")

	for _, args := range [][]string{
		{"ls", "-n", "1"},
		{"next", "-n", "1"},
		{"search", "a", "-n", "1"},
	} {
		_, se := runLs(t, args...)
		if !strings.Contains(se, "showing 1 of 3 (-n)") {
			t.Errorf("%v: stderr should disclose the cap, got:\n%s", args, se)
		}
	}

	// revisit: all three tasks carry no value/effort, so all three surface.
	_, se := runLs(t, "revisit", "-n", "1")
	if !strings.Contains(se, "showing 1 of 3 (-n)") {
		t.Errorf("revisit -n1: stderr should disclose the cap, got:\n%s", se)
	}

	// A -n that does not bite stays silent.
	_, se = runLs(t, "ls", "-n", "5")
	if strings.Contains(se, "showing") {
		t.Errorf("uncut ls must not hint, stderr:\n%s", se)
	}
	_, se = runLs(t, "ls", "-n", "3")
	if strings.Contains(se, "showing") {
		t.Errorf("-n == total must not hint, stderr:\n%s", se)
	}
}

// ls --tree -n caps GROUPS; the disclosure says so in the same stderr shape.
func TestCapDisclosure_TreeGroups(t *testing.T) {
	initStore(t)
	mustRun(t, "epic", "add", "box one")
	mustRun(t, "epic", "add", "box two")
	addTask(t, "unfiled")

	_, se := runLs(t, "ls", "--tree", "-n", "1")
	if !strings.Contains(se, "of 3 groups (-n)") {
		t.Errorf("tree cap should disclose groups, got:\n%s", se)
	}
}

// epic ls -n discloses like every other read.
func TestCapDisclosure_EpicLs(t *testing.T) {
	initStore(t)
	mustRun(t, "epic", "add", "box one")
	mustRun(t, "epic", "add", "box two")

	_, se := runLs(t, "epic", "ls", "-n", "1")
	if !strings.Contains(se, "showing 1 of 2 (-n)") {
		t.Errorf("epic ls -n1 should disclose the cap, got:\n%s", se)
	}
}

// epic ls obeys the board scope like every task read (it was the one read
// whose population ignored it — same cwd, and brief's epic header disagreed),
// discloses what the scope hid, and -r ” escapes to the whole board.
func TestEpicLs_BoardScopeAppliesAndDiscloses(t *testing.T) {
	_, _ = pointerRepoLayout(t)
	mustRun(t, "epic", "add", "demo box", "-r", "me/demo")
	mustRun(t, "epic", "add", "other box", "-r", "me/other")

	so, se := runLs(t, "epic", "ls")
	if !strings.Contains(so, "demo box") {
		t.Errorf("in-scope box missing:\n%s", so)
	}
	if strings.Contains(so, "other box") {
		t.Errorf("the board scope must filter the other repo's box:\n%s", so)
	}
	if !strings.Contains(se, "box(es) outside me/demo hidden") || !strings.Contains(se, "-r ''") {
		t.Errorf("stderr should disclose the hidden box(es) with the -r '' remedy, got:\n%s", se)
	}

	so, se = runLs(t, "epic", "ls", "-r", "")
	if !strings.Contains(so, "demo box") || !strings.Contains(so, "other box") {
		t.Errorf("-r '' should list the whole board:\n%s", so)
	}
	if strings.Contains(se, "hidden") {
		t.Errorf("-r '' must not re-hint, stderr:\n%s", se)
	}
}

// epic ls -l follows the task reads' OR semantics (comma-separated or
// repeated); the old single-token match silently dropped the second value.
func TestEpicLs_LabelCommaOR(t *testing.T) {
	initStore(t)
	mustRun(t, "epic", "add", "box a", "-l", "aa")
	mustRun(t, "epic", "add", "box b", "-l", "bb")
	mustRun(t, "epic", "add", "box c", "-l", "cc")

	so, _ := runLs(t, "epic", "ls", "-l", "aa,bb")
	if !strings.Contains(so, "box a") || !strings.Contains(so, "box b") {
		t.Errorf("-l aa,bb should OR both boxes:\n%s", so)
	}
	if strings.Contains(so, "box c") {
		t.Errorf("-l aa,bb must still filter:\n%s", so)
	}

	so, _ = runLs(t, "epic", "ls", "-l", "aa", "-l", "bb")
	if !strings.Contains(so, "box a") || !strings.Contains(so, "box b") {
		t.Errorf("repeated -l should union:\n%s", so)
	}
}

// mustRun is run() asserting exit 0 (for setup steps whose output is noise).
func mustRun(t *testing.T, args ...string) {
	t.Helper()
	out, code := run(t, args...)
	if code != 0 {
		t.Fatalf("%v exit = %d:\n%s", args, code, out)
	}
}
