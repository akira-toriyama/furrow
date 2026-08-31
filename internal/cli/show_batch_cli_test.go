package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// showOne unwraps an always-array --json output (show, and the batch mutators
// done/move/set): it asserts the output is a one-element array and returns
// that element's raw JSON for the caller's own struct (the always-array rule
// made every single-id parse go through here).
func showOne(t *testing.T, out string) []byte {
	t.Helper()
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("--json must be an array (always-array rule): %v\n%s", err, out)
	}
	if len(arr) != 1 {
		t.Fatalf("want a one-element array, got %d elements:\n%s", len(arr), out)
	}
	return arr[0]
}

// jsonIDs parses a JSON task array and returns the ids in order.
func jsonIDs(t *testing.T, s string) []string {
	t.Helper()
	var arr []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		t.Fatalf("parse task array: %v\n%s", err, s)
	}
	ids := []string{}
	for _, e := range arr {
		ids = append(ids, e.ID)
	}
	return ids
}

func TestShowBatchJSONArrayInputOrderDedupe(t *testing.T) {
	initStore(t)
	a := addTask(t, "task a")
	b := addTask(t, "task b")

	// ≥2 ids -> a JSON array, in input order, duplicates first-wins.
	out, code := run(t, "--json", "show", b, a, b)
	if code != 0 {
		t.Fatalf("show batch exit=%d:\n%s", code, out)
	}
	if got, want := jsonIDs(t, out), []string{b, a}; !reflect.DeepEqual(got, want) {
		t.Errorf("ids = %v, want %v", got, want)
	}
	if !strings.Contains(out, `"body_text"`) {
		t.Errorf("batch without --no-body should still carry body_text:\n%s", out)
	}
}

func TestShowSingleJSONIsAOneElementArray(t *testing.T) {
	initStore(t)
	a := addTask(t, "task a")

	// The always-array rule: `show <id>...` has array cardinality by signature,
	// so one id is a one-element array — never the pre-v1 bare object, which
	// forked the shape on runtime argv length.
	out, code := run(t, "--json", "show", a)
	if code != 0 {
		t.Fatalf("show exit=%d:\n%s", code, out)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("single-id --json must be an array: %v\n%s", err, out)
	}
	if len(items) != 1 || items[0]["id"] != a {
		t.Errorf("want a one-element array carrying %s, got:\n%s", a, out)
	}
}

func TestShowNDJSONOneLinePerTaskAnyArity(t *testing.T) {
	initStore(t)
	a := addTask(t, "task a")
	b := addTask(t, "task b")

	out, code := run(t, "--ndjson", "show", a, b)
	if code != 0 {
		t.Fatalf("show --ndjson exit=%d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 NDJSON lines, got %d:\n%s", len(lines), out)
	}

	// --ndjson is honored for a single id too (arity-independent shape).
	out, code = run(t, "--ndjson", "show", a)
	if code != 0 {
		t.Fatalf("show --ndjson single exit=%d:\n%s", code, out)
	}
	lines = strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "{") {
		t.Errorf("single id --ndjson should emit exactly one JSON line:\n%s", out)
	}
}

func TestShowNoBodyNDJSONBatchLeanLines(t *testing.T) {
	initStore(t)
	a := addTask(t, "task a", "--body", "SECRETBODY")
	b := addTask(t, "task b", "--body", "OTHERBODY")

	out, code := run(t, "--ndjson", "show", a, b, "--no-body")
	if code != 0 {
		t.Fatalf("show --ndjson --no-body exit=%d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 NDJSON lines, got %d:\n%s", len(lines), out)
	}
	if strings.Contains(out, "body_text") || strings.Contains(out, "SECRETBODY") || strings.Contains(out, "OTHERBODY") {
		t.Errorf("--no-body must drop the body from every NDJSON line:\n%s", out)
	}
}

func TestShowNoBodyOmitsBodyTextKey(t *testing.T) {
	initStore(t)
	a := addTask(t, "task a", "--body", "SECRETBODY")

	out, code := run(t, "--json", "show", a, "--no-body")
	if code != 0 {
		t.Fatalf("show --no-body exit=%d:\n%s", code, out)
	}
	if strings.Contains(out, "body_text") || strings.Contains(out, "SECRETBODY") {
		t.Errorf("--no-body must drop the body_text key entirely:\n%s", out)
	}
}

func TestShowNoBodyHumanOmitsBodySection(t *testing.T) {
	initStore(t)
	a := addTask(t, "task a", "--body", "SECRETBODY")

	out, code := run(t, "show", a, "--no-body")
	if code != 0 {
		t.Fatalf("show --no-body exit=%d:\n%s", code, out)
	}
	if strings.Contains(out, "SECRETBODY") {
		t.Errorf("--no-body human output must omit the body section:\n%s", out)
	}
	if !strings.Contains(out, "task a") {
		t.Errorf("--no-body must still print the metadata:\n%s", out)
	}
}

func TestShowBatchPartialMissEmitsFoundAndDetails(t *testing.T) {
	initStore(t)
	a := addTask(t, "task a")

	fe, out := runErr(t, "--json", "show", a, "t-zzzz1", "t-zzzz2")
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("partial miss should be a CodeNotFound error, got %+v", fe)
	}
	if got, want := jsonIDs(t, out), []string{a}; !reflect.DeepEqual(got, want) {
		t.Errorf("found tasks must still be emitted, ids = %v, want %v", got, want)
	}
	want := map[string][]string{"missing": {"t-zzzz1", "t-zzzz2"}}
	if !reflect.DeepEqual(fe.Details, want) {
		t.Errorf("details = %#v, want %#v", fe.Details, want)
	}
}

func TestShowBatchAllMissEmitsEmptyArray(t *testing.T) {
	initStore(t)
	addTask(t, "task a")

	fe, out := runErr(t, "--json", "show", "t-zzzz1", "t-zzzz2")
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("all-miss should be a CodeNotFound error, got %+v", fe)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("all-miss --json should still print [], got:\n%s", out)
	}
}

func TestShowSingleMissEmitsEmptyArrayPlusBatchError(t *testing.T) {
	initStore(t)
	addTask(t, "task a")

	// The always-array rule's error half: a total miss still emits the found
	// entries ([]) on stdout, and the error is the batch shape at any arity —
	// success and error forms both follow the SIGNATURE, never the argv length.
	fe, out := runErr(t, "--json", "show", "t-zzzz1")
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("single miss should be CodeNotFound, got %+v", fe)
	}
	if fe.Msg != "1 of 1 ids not found" || fe.Subject != "t-zzzz1" {
		t.Errorf("single-id miss reports the batch count shape (subject still names the id), got %+v", fe)
	}
	want := map[string][]string{"missing": {"t-zzzz1"}}
	if !reflect.DeepEqual(fe.Details, want) {
		t.Errorf("details = %#v, want %#v", fe.Details, want)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("single-id miss prints the empty found-array on stdout, got:\n%s", out)
	}
}

func TestShowBatchHumanSeparator(t *testing.T) {
	initStore(t)
	a := addTask(t, "task a")
	b := addTask(t, "task b")

	out, code := run(t, "show", a, b)
	if code != 0 {
		t.Fatalf("show batch human exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "\n---\n") {
		t.Errorf("batch human output should separate blocks with ---:\n%s", out)
	}
	if !strings.Contains(out, "task a") || !strings.Contains(out, "task b") {
		t.Errorf("batch human output should contain both tasks:\n%s", out)
	}
}

func TestShowBatchBacklinks(t *testing.T) {
	initStore(t)
	target := addTask(t, "target task")
	other := addTask(t, "other task")
	mentioner := addTask(t, "the mentioner", "--body", "see [["+target+"]]")

	// --backlinks composes with batch: every element carries mentioned_by.
	out, code := run(t, "--json", "show", target, other, "--backlinks")
	if code != 0 {
		t.Fatalf("show batch --backlinks exit=%d:\n%s", code, out)
	}
	var arr []struct {
		ID          string `json:"id"`
		BodyText    *string
		MentionedBy []struct {
			ID string `json:"id"`
		} `json:"mentioned_by"`
	}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(arr) != 2 {
		t.Fatalf("want 2 tasks, got %d:\n%s", len(arr), out)
	}
	if len(arr[0].MentionedBy) != 1 || arr[0].MentionedBy[0].ID != mentioner {
		t.Errorf("target should be mentioned by %s, got %+v", mentioner, arr[0].MentionedBy)
	}
	if arr[1].MentionedBy == nil {
		t.Errorf("mentioned_by must be [] (never null) for unmentioned tasks:\n%s", out)
	}

	out, code = run(t, "--json", "show", target, other, "--backlinks", "--no-body")
	if code != 0 {
		t.Fatalf("show --no-body --backlinks exit=%d:\n%s", code, out)
	}
	if strings.Contains(out, "body_text") {
		t.Errorf("--no-body must drop body_text even with --backlinks:\n%s", out)
	}
	if !strings.Contains(out, "mentioned_by") {
		t.Errorf("--backlinks must keep mentioned_by with --no-body:\n%s", out)
	}
}
