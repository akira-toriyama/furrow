package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

type depListJSON struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	DependsOn []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Status    string   `json:"status"`
		BlockedBy []string `json:"blocked_by"`
	} `json:"depends_on"`
	Blocks []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Status    string   `json:"status"`
		BlockedBy []string `json:"blocked_by"`
	} `json:"blocks"`
}

// rowOf returns the output line that names id, "" when none does.
func rowOf(out, id string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, id) {
			return line
		}
	}
	return ""
}

// TestDepListBlocksSayWhatThisCloseMoves pins t-wwk2: a blocks row answers
// "what moves if I close this" on its own — its blocked_by (JSON) and a
// trailing note (human) — instead of one `show` per row. Seven furrow-test
// drill runs retyped show for exactly this.
func TestDepListBlocksSayWhatThisCloseMoves(t *testing.T) {
	initStore(t)
	base := addTask(t, "base task", "-s", "ready")
	other := addTask(t, "other dep", "-s", "ready")
	more := addTask(t, "one more dep", "-s", "ready")
	last := addTask(t, "last-dep task", "-s", "backlog")    // base is its only open dep
	two := addTask(t, "two-deps task", "-s", "backlog")     // base + other
	three := addTask(t, "three-deps task", "-s", "backlog") // base + other + more: the plural
	parked := addTask(t, "parked task", "-s", "icebox")     // base, but nothing moves a parked row
	for _, args := range [][]string{{last, base}, {two, base, other}, {three, base, other, more}, {parked, base}} {
		if _, code := run(t, append([]string{"dep"}, args...)...); code != 0 {
			t.Fatalf("dep add %v failed: %d", args, code)
		}
	}

	out, code := run(t, "dep", base, "--list")
	if code != 0 {
		t.Fatalf("dep --list exit=%d:\n%s", code, out)
	}
	if row := rowOf(out, last); !strings.HasSuffix(row, "← unblocks on this close") {
		t.Errorf("last-dep row should say this close unblocks it:\n%s", out)
	}
	if row := rowOf(out, two); !strings.HasSuffix(row, "← 1 other open dep") {
		t.Errorf("two-deps row should count the other open dep:\n%s", out)
	}
	if row := rowOf(out, three); !strings.HasSuffix(row, "← 2 other open deps") {
		t.Errorf("three-deps row should count both other open deps, plural:\n%s", out)
	}
	if row := rowOf(out, parked); row == "" || strings.Contains(row, "←") {
		t.Errorf("a parked dependent is listed but carries no note (nothing moves it):\n%s", out)
	}

	out, code = run(t, "--json", "dep", base, "--list")
	if code != 0 {
		t.Fatalf("dep --list --json exit=%d:\n%s", code, out)
	}
	var r depListJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	got := map[string][]string{}
	for _, b := range r.Blocks {
		got[b.ID] = b.BlockedBy
	}
	if want := []string{base}; !equalStrings(got[last], want) {
		t.Errorf("last blocked_by = %v, want %v", got[last], want)
	}
	if want := []string{base, other}; !equalStrings(got[two], want) {
		t.Errorf("two blocked_by = %v, want %v (deps are a sorted set; so is blocked_by)", got[two], want)
	}
	if want := []string{base}; !equalStrings(got[parked], want) {
		t.Errorf("parked blocked_by = %v, want %v (JSON states the fact; only the human note is withheld)", got[parked], want)
	}

	// depends_on rows carry the same key: a dep with nothing in its way is [].
	out, _ = run(t, "--json", "dep", two, "--list")
	if !strings.Contains(out, `"blocked_by": []`) || strings.Contains(out, "null") {
		t.Errorf("depends_on rows should carry blocked_by as [] (never null):\n%s", out)
	}

	// Once the subject is done the notes read in the past tense.
	if _, code := run(t, "done", base); code != 0 {
		t.Fatalf("done exit=%d", code)
	}
	out, _ = run(t, "dep", base, "--list")
	if row := rowOf(out, last); !strings.HasSuffix(row, "← unblocked") {
		t.Errorf("after the close the last-dep row should read unblocked:\n%s", out)
	}
	if row := rowOf(out, two); !strings.HasSuffix(row, "← 1 open dep") {
		t.Errorf("after the close the two-deps row should count what is still open:\n%s", out)
	}
	out, _ = run(t, "--json", "dep", base, "--list")
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	found := false
	for _, b := range r.Blocks {
		if b.ID != last {
			continue
		}
		found = true
		if len(b.BlockedBy) != 0 {
			t.Errorf("after the close last blocked_by = %v, want []", b.BlockedBy)
		}
	}
	if !found {
		t.Errorf("after the close the blocks side should still list %s:\n%s", last, out)
	}
}

// equalStrings compares two id lists as sets: deps are stored sorted, and ids
// are random, so the order a test handed them in says nothing.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func TestDepListJSONBothDirections(t *testing.T) {
	initStore(t)
	base := addTask(t, "base task")
	mid := addTask(t, "middle task")
	top := addTask(t, "top task")
	if _, code := run(t, "dep", mid, base); code != 0 { // mid depends on base
		t.Fatalf("dep add failed: %d", code)
	}
	if _, code := run(t, "dep", top, mid); code != 0 { // top depends on mid
		t.Fatalf("dep add failed: %d", code)
	}

	out, code := run(t, "--json", "dep", mid, "--list")
	if code != 0 {
		t.Fatalf("dep --list exit=%d:\n%s", code, out)
	}
	var r depListJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if r.ID != mid {
		t.Errorf("subject id = %q, want %q", r.ID, mid)
	}
	if len(r.DependsOn) != 1 || r.DependsOn[0].ID != base || r.DependsOn[0].Title != "base task" || r.DependsOn[0].Status == "" {
		t.Errorf("depends_on should resolve base with title+status, got %+v", r.DependsOn)
	}
	if len(r.Blocks) != 1 || r.Blocks[0].ID != top || r.Blocks[0].Title != "top task" {
		t.Errorf("blocks should resolve top, got %+v", r.Blocks)
	}
}

func TestDepListJSONEmptyArraysNotNull(t *testing.T) {
	initStore(t)
	lone := addTask(t, "lonely")

	out, code := run(t, "--json", "dep", lone, "--list")
	if code != 0 {
		t.Fatalf("dep --list exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, `"depends_on": []`) || !strings.Contains(out, `"blocks": []`) {
		t.Errorf("empty edges must be [] not null:\n%s", out)
	}
}

func TestDepListNDJSONSingleObjectLine(t *testing.T) {
	initStore(t)
	lone := addTask(t, "lonely")

	out, code := run(t, "--ndjson", "dep", lone, "--list")
	if code != 0 {
		t.Fatalf("dep --list --ndjson exit=%d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "{") || !strings.Contains(lines[0], `"blocks"`) {
		t.Errorf("--ndjson should emit one compact object line:\n%s", out)
	}
}

func TestDepListHumanTwoSections(t *testing.T) {
	initStore(t)
	base := addTask(t, "base task")
	mid := addTask(t, "middle task")
	run(t, "dep", mid, base)

	out, code := run(t, "dep", mid, "--list")
	if code != 0 {
		t.Fatalf("dep --list human exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "depends on (1):") || !strings.Contains(out, "blocks (0):") {
		t.Errorf("human output should label both sections with counts:\n%s", out)
	}
	if !strings.Contains(out, base) || !strings.Contains(out, "base task") {
		t.Errorf("human output should resolve the dep to id+title:\n%s", out)
	}
}

func TestDepListNotFoundExit1(t *testing.T) {
	initStore(t)
	addTask(t, "something")

	fe, _ := runErr(t, "--json", "dep", "t-zzzzz", "--list")
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("an unknown id should be NotFound (exit 1), got %+v", fe)
	}
}

func TestDepListRejectsRmCombo(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")
	run(t, "dep", a, b)

	// --list and --rm are mutually exclusive (cobra usage error -> exit 2).
	_, code := run(t, "dep", a, "--list", "--rm")
	if code != int(core.CodeValidation) {
		t.Fatalf("--list with --rm should exit 2, got %d", code)
	}
}

func TestDepListRejectsExtraArgs(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")

	// --list takes exactly the subject id; a stray dep-id is a usage error.
	_, code := run(t, "dep", a, b, "--list")
	if code != int(core.CodeValidation) {
		t.Fatalf("--list with an extra arg should exit 2, got %d", code)
	}
}

func TestDepAddStillWorks(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")

	// Regression: the mutation path (no --list) is unchanged.
	out, code := run(t, "--json", "dep", a, b)
	if code != 0 {
		t.Fatalf("dep add exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, `"deps"`) || !strings.Contains(out, b) {
		t.Errorf("dep add should still emit the mutation envelope with the new dep:\n%s", out)
	}
}
