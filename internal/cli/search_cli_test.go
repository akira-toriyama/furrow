package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// searchHits parses a JSON search-result array.
func searchHits(t *testing.T, s string) []struct {
	ID           string `json:"id"`
	MatchedField string `json:"matched_field"`
	Snippet      string `json:"snippet"`
} {
	t.Helper()
	var arr []struct {
		ID           string `json:"id"`
		MatchedField string `json:"matched_field"`
		Snippet      string `json:"snippet"`
	}
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		t.Fatalf("parse search array: %v\n%s", err, s)
	}
	return arr
}

func TestSearchJSONTitleAndBodyHits(t *testing.T) {
	initStore(t)
	title := addTask(t, "adopt teatest harness")
	body := addTask(t, "unrelated title", "--body", "we should use teatest here")
	addTask(t, "no relation", "--body", "plain prose")

	out, code := run(t, "--json", "search", "teatest")
	if code != 0 {
		t.Fatalf("search exit=%d:\n%s", code, out)
	}
	hits := searchHits(t, out)
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d:\n%s", len(hits), out)
	}
	got := map[string]string{} // id -> matched_field
	for _, h := range hits {
		got[h.ID] = h.MatchedField
		if h.Snippet == "" {
			t.Errorf("every hit should carry a snippet, got empty for %s", h.ID)
		}
	}
	if got[title] != "title" {
		t.Errorf("%s should be a title hit, got %q", title, got[title])
	}
	if got[body] != "body" {
		t.Errorf("%s should be a body hit, got %q", body, got[body])
	}
}

func TestSearchNDJSONOneLinePerHit(t *testing.T) {
	initStore(t)
	addTask(t, "sync alpha")
	addTask(t, "sync beta")

	out, code := run(t, "--ndjson", "search", "sync")
	if code != 0 {
		t.Fatalf("search --ndjson exit=%d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 NDJSON lines, got %d:\n%s", len(lines), out)
	}
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "{") || !strings.Contains(ln, `"matched_field"`) {
			t.Errorf("each line should be a compact hit object with matched_field:\n%s", ln)
		}
	}
}

func TestSearchZeroMatchIsExit0EmptyArray(t *testing.T) {
	initStore(t)
	addTask(t, "something")

	out, code := run(t, "--json", "search", "nomatchxyz")
	if code != 0 {
		t.Fatalf("a zero-match search must exit 0, got %d:\n%s", code, out)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("zero-match --json should print [], got:\n%s", out)
	}
}

func TestSearchUnknownLaneExit2(t *testing.T) {
	initStore(t)
	addTask(t, "something")

	fe, _ := runErr(t, "--json", "search", "some", "-s", "ghost")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("an unknown -s lane should be a validation error (exit 2), got %+v", fe)
	}
	if len(fe.Candidates) == 0 {
		t.Errorf("unknown lane error should carry the configured lanes in candidates")
	}
}

func TestSearchMissingTermExit2(t *testing.T) {
	initStore(t)
	addTask(t, "something")

	// no term at all -> cobra arity error (exit 2), never a match-everything.
	_, code := run(t, "search")
	if code != int(core.CodeValidation) {
		t.Fatalf("search with no term should exit 2, got %d", code)
	}
}

func TestSearchHumanTableShowsSnippet(t *testing.T) {
	initStore(t)
	addTask(t, "boring title", "--body", "the SECRETNEEDLE is buried in the body")

	out, code := run(t, "search", "SECRETNEEDLE")
	if code != 0 {
		t.Fatalf("search human exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "SECRETNEEDLE") || !strings.Contains(out, "body") {
		t.Errorf("human table should show the field and the snippet:\n%s", out)
	}
	// the title rides alongside a body snippet so the row is self-explanatory.
	if !strings.Contains(out, "boring title") {
		t.Errorf("a body hit's human row should still name the task title:\n%s", out)
	}
}

// `search --archived` reads the archive store instead of the hot board — the
// same meaning --archived has on ls/show, so digging up why something was done
// stops falling back to grepping .furrow/archive/bodies by hand.
func TestCLISearchArchived(t *testing.T) {
	initStore(t)
	hot := addTask(t, "stays hot with zulu in the title")
	old := addTask(t, "will be archived")
	if _, code := run(t, "note", old, "the zulu paragraph lives only in the body"); code != 0 {
		t.Fatalf("note exit = %d", code)
	}
	if _, code := run(t, "done", old); code != 0 {
		t.Fatalf("done exit = %d", code)
	}
	// By id, so the test does not depend on an age window.
	if out, code := run(t, "archive", old, "--yes"); code != 0 {
		t.Fatalf("archive exit = %d:\n%s", code, out)
	}

	out, code := run(t, "--json", "search", "zulu")
	if code != 0 || !strings.Contains(out, hot) || strings.Contains(out, old) {
		t.Errorf("the hot search must see only the hot task (code %d):\n%s", code, out)
	}

	out, code = run(t, "--json", "search", "zulu", "--archived")
	if code != 0 {
		t.Fatalf("search --archived exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, old) || strings.Contains(out, hot) {
		t.Errorf("--archived must search the archive INSTEAD of the hot board:\n%s", out)
	}
	// The load-bearing half: the hit is in the BODY, which only the archive's
	// own loader can read.
	if !strings.Contains(out, `"matched_field": "body"`) {
		t.Errorf("an archived body hit must report matched_field=body:\n%s", out)
	}
}
