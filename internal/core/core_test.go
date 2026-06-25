package core

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// -update regenerates the golden files: go test ./internal/core -update
var update = flag.Bool("update", false, "update golden files")

var testLanes = []string{"inbox", "backlog", "ready", "in-progress", "done", "icebox"}

// sampleIndex is a fixed, deliberately-unsorted index covering the tricky cases:
// CJK + HTML-ish characters in titles (must survive SetEscapeHTML(false)),
// an open task (closed == null) and a closed one, nil vs populated slices, and
// tasks out of canonical order so the marshaller must sort them.
func sampleIndex() *Index {
	mk := func(y int, mo time.Month, d int) time.Time {
		return time.Date(y, mo, d, 1, 2, 3, 0, time.UTC)
	}
	closed := mk(2026, 6, 20)
	return &Index{
		SchemaVersion: SchemaVersion,
		Tasks: []Task{
			{
				ID: "t-0003", Title: "done item <b>&amp;</b> 完了", Status: "done",
				Priority: 100, Labels: nil, Deps: nil, Refs: nil, Checklist: nil,
				Created: mk(2026, 6, 1), Updated: mk(2026, 6, 20), Closed: &closed,
				Body: BodyPath("t-0003"),
			},
			{
				ID: "t-0001", Title: "畝を一本進める", Status: "in-progress",
				Priority: 110, Labels: []string{"zmk", "canon"}, Deps: []string{"t-0002"},
				Refs:      []string{"docs/x.md#L10", "https://example.com"},
				Checklist: []ChecklistItem{{Text: "design", Done: true}, {Text: "ship", Done: false}},
				Created:   mk(2026, 6, 2), Updated: mk(2026, 6, 21), Closed: nil,
				Body: BodyPath("t-0001"),
			},
			{
				ID: "t-0002", Title: "ready task", Status: "in-progress",
				Priority: 100, Created: mk(2026, 6, 3), Updated: mk(2026, 6, 3),
				Body: BodyPath("t-0002"),
			},
		},
	}
}

func TestMarshalGolden(t *testing.T) {
	got, err := Marshal(sampleIndex(), testLanes)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "index.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update first): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("marshal output != golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestMarshalDeterministic: the canonical contract — re-marshalling an index
// parsed from canonical bytes yields byte-identical output (no churn), AND the
// sort is stable regardless of input task order.
func TestMarshalDeterministic(t *testing.T) {
	first, err := Marshal(sampleIndex(), testLanes)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Unmarshal(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(parsed, testLanes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("re-marshal not byte-stable\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMarshalDetails(t *testing.T) {
	got, err := Marshal(sampleIndex(), testLanes)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)

	if !bytes.HasSuffix(got, []byte("\n")) {
		t.Error("output must end with a trailing newline")
	}
	// SetEscapeHTML(false): CJK and < > & survive literally.
	for _, lit := range []string{"畝を一本進める", "<b>&amp;</b>", "完了"} {
		if !bytes.Contains(got, []byte(lit)) {
			t.Errorf("expected literal %q to survive un-escaped, output:\n%s", lit, s)
		}
	}
	// [] not null: a task with no labels still emits "labels": [].
	if !bytes.Contains(got, []byte(`"labels": []`)) {
		t.Errorf("nil slices must marshal as [], not null:\n%s", s)
	}
	// open task -> "closed": null
	if !bytes.Contains(got, []byte(`"closed": null`)) {
		t.Errorf("open task must serialize closed as null:\n%s", s)
	}
}

func TestCanonicalSort(t *testing.T) {
	got, err := Marshal(sampleIndex(), testLanes)
	if err != nil {
		t.Fatal(err)
	}
	// in-progress (rank 3) sorts before done (rank 4). Within in-progress,
	// priority 100 (t-0002) before 110 (t-0001). done is last (t-0003).
	order := []string{`"id": "t-0002"`, `"id": "t-0001"`, `"id": "t-0003"`}
	last := -1
	for _, needle := range order {
		i := bytes.Index(got, []byte(needle))
		if i < 0 {
			t.Fatalf("missing %s", needle)
		}
		if i < last {
			t.Errorf("task order wrong: %s appeared before the previous id\n%s", needle, got)
		}
		last = i
	}
}

func TestValidate(t *testing.T) {
	pat := regexp.MustCompile(`^t-[0-9]+$`)
	idx := &Index{
		SchemaVersion: SchemaVersion,
		Tasks: []Task{
			{ID: "t-0001", Status: "ready", Body: BodyPath("t-0001"), Deps: []string{"t-0099"}},
			{ID: "t-0001", Status: "nope", Body: "wrong/path.md", Parent: "t-0404"}, // dup id, bad lane, bad body, missing parent
			{ID: "BAD", Status: "ready", Body: BodyPath("BAD")},                     // id pattern fail
		},
	}
	ps := Validate(idx, testLanes, pat)

	want := map[string]bool{
		"duplicate id: t-0001":                                   false,
		`status "nope" is not a configured lane`:                 false,
		`body path "wrong/path.md" should be "bodies/t-0001.md"`: false,
		`parent "t-0404" does not exist`:                         false,
		`dep "t-0099" does not exist`:                            false,
	}
	for _, p := range ps {
		if _, ok := want[p.Msg]; ok {
			want[p.Msg] = true
		}
	}
	for msg, found := range want {
		if !found {
			t.Errorf("expected a problem %q; got %+v", msg, ps)
		}
	}
	if !HasErrors(ps) {
		t.Error("expected HasErrors == true")
	}
	// problems must be deterministically ordered (errors before warns).
	for i := 1; i < len(ps); i++ {
		if ps[i-1].Severity == SevWarn && ps[i].Severity == SevError {
			t.Errorf("problems not ordered: warn before error at %d", i)
		}
	}
}

func TestNextPriority(t *testing.T) {
	idx := &Index{Tasks: []Task{
		{ID: "t-1", Status: "ready", Priority: 100},
		{ID: "t-2", Status: "ready", Priority: 130},
		{ID: "t-3", Status: "done", Priority: 200},
	}}
	if got := idx.NextPriority("ready", 100, 10); got != 140 {
		t.Errorf("NextPriority(ready) = %d, want 140", got)
	}
	if got := idx.NextPriority("backlog", 100, 10); got != 100 {
		t.Errorf("NextPriority(empty lane) = %d, want 100", got)
	}
}

func TestActionable(t *testing.T) {
	idx := &Index{Tasks: []Task{
		{ID: "t-1", Status: "ready", Deps: []string{"t-2"}},
		{ID: "t-2", Status: "done"},
		{ID: "t-3", Status: "ready", Deps: []string{"t-9"}}, // unknown dep -> blocked
		{ID: "t-4", Status: "icebox"},
	}}
	terminal := map[string]bool{"done": true, "icebox": true}
	doneIDs := map[string]bool{"t-2": true}

	cases := map[string]bool{"t-1": true, "t-2": false, "t-3": false, "t-4": false}
	for id, want := range cases {
		tk, _ := idx.Find(id)
		if got := idx.Actionable(tk, terminal, doneIDs); got != want {
			t.Errorf("Actionable(%s) = %v, want %v", id, got, want)
		}
	}
}
