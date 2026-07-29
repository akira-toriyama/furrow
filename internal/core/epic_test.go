package core

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sampleEpic is one epic covering every shape the recipe has to normalize: an
// unsorted+duplicated label set, a meta map whose keys are NOT written in sorted
// order, a sub-second non-UTC timestamp, and a null Closed (the open state).
func sampleEpic() *Epic {
	created := time.Date(2026, 7, 28, 1, 2, 3, 456_000_000, time.UTC) // sub-second: must truncate
	return &Epic{
		ID:     "e-k3m9",
		Title:  "旅行の準備",
		Goal:   "旅行前の準備をして <b>パンフレット</b> を作る",
		Active: true,
		Labels: []string{"travel", "aug", "travel"}, // unsorted + duplicated
		Repos:  []string{"akira-toriyama/furrow"},
		Meta: map[string]string{
			"場所": "北海道",
			"期間": "2026/08/10 ~ 2026/08/15", // inserted before 場所? map order is random either way
		},
		Created: created,
		Updated: created,
		Closed:  nil,
		Body:    "bodies/e-k3m9.md",
		Deps:    []string{"e-zz10", "e-aa01", "e-zz10"}, // unsorted + duplicated, like Labels
	}
}

func TestEpicPath(t *testing.T) {
	if got := EpicPath("e-k3m9"); got != "epics/e-k3m9.json" {
		t.Errorf("EpicPath = %q, want %q", got, "epics/e-k3m9.json")
	}
}

func TestEpicIsOpen(t *testing.T) {
	e := sampleEpic()
	if !e.IsOpen() {
		t.Error("a nil Closed must read as open")
	}
	now := time.Now()
	e.Closed = &now
	if e.IsOpen() {
		t.Error("a set Closed must read as closed")
	}
}

func TestMarshalEpicGolden(t *testing.T) {
	got, err := MarshalEpic(sampleEpic())
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "epic.golden.json")
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
		t.Errorf("MarshalEpic output != golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestMarshalEpicDeterministic: re-marshalling an epic parsed from canonical
// bytes yields byte-identical output (zero churn when an epic shard is re-saved
// untouched) — the per-epic twin of the task determinism contract.
func TestMarshalEpicDeterministic(t *testing.T) {
	first, err := MarshalEpic(sampleEpic())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := UnmarshalEpic(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalEpic(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("re-marshal not byte-identical\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestMarshalEpicMetaIsDeterministic pins the reason Meta is a FLAT
// map[string]string: encoding/json emits a map's keys sorted, so a hundred
// marshals of the same map produce the same bytes even though Go's map iteration
// order is randomized per run. If Meta ever became `map[string]any`, this is the
// property that would quietly break (and take fsstore's zero-churn save with it).
func TestMarshalEpicMetaIsDeterministic(t *testing.T) {
	want, err := MarshalEpic(sampleEpic())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := MarshalEpic(sampleEpic())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("meta key order not stable on iteration %d\n--- got ---\n%s\n--- want ---\n%s", i, got, want)
		}
	}
	// And the order is sorted, not insertion: 場所 (U+5834) sorts BEFORE 期間
	// (U+671F) by code point, while the struct literal writes 場所 first and 期間
	// second — so this only holds if encoding/json is doing the sorting.
	if i, j := bytes.Index(want, []byte("場所")), bytes.Index(want, []byte("期間")); i < 0 || j < 0 || i > j {
		t.Errorf("meta keys are not sorted by code point:\n%s", want)
	}
}

// TestMarshalEpicDetails: the byte recipe applies to an epic exactly as it does
// to a task — CJK and < > & survive verbatim (no HTML escaping), sets are sorted
// and deduped, timestamps are whole-second UTC, an unset Closed is null, and the
// document ends with a newline.
func TestMarshalEpicDetails(t *testing.T) {
	b, err := MarshalEpic(sampleEpic())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"title": "旅行の準備"`,
		`"goal": "旅行前の準備をして <b>パンフレット</b> を作る"`, // NOT \u003cb\u003e
		`"labels": [`,
		`"aug",`,   // sorted before travel …
		`"travel"`, // … and deduped to one entry
		`"場所": "北海道"`,
		`"created": "2026-07-28T01:02:03Z"`, // sub-second truncated
		`"closed": null`,
		`"active": true`,
		`"body": "bodies/e-k3m9.md"`,
	} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("marshalled epic missing %s:\n%s", want, b)
		}
	}
	if bytes.Count(b, []byte(`"travel"`)) != 1 {
		t.Errorf("duplicated label was not deduped:\n%s", b)
	}
	if !bytes.HasSuffix(b, []byte("\n")) {
		t.Errorf("marshalled epic must end with a newline:\n%q", b)
	}
	if bytes.Contains(b, []byte("extras")) {
		t.Errorf("the extras carrier must never appear on disk:\n%s", b)
	}
}

// TestMarshalEpicEmptyCollections: nil Labels/Repos/Meta serialize as empty
// containers, never null — the []-not-null rule, extended to the meta map. A
// null would make "no labels" and "labels absent" two different on-disk shapes.
func TestMarshalEpicEmptyCollections(t *testing.T) {
	b, err := MarshalEpic(&Epic{ID: "e-bare", Title: "bare"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"labels": []`, `"repos": []`, `"meta": {}`, `"goal": ""`} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("bare epic missing %s:\n%s", want, b)
		}
	}
}

// TestEpicRoundTripPreservesUnknownKeys: the passthrough covers epics too. A key
// a newer furrow wrote survives an old binary's load->save, is re-emitted sorted
// AFTER the known keys, and is reported by ExtraKeys so `furrow lint` can say
// "preserved, but IGNORED".
func TestEpicRoundTripPreservesUnknownKeys(t *testing.T) {
	raw := []byte(`{
  "id": "e-k3m9",
  "title": "旅行の準備",
  "goal": "",
  "active": false,
  "labels": [],
  "repos": [],
  "meta": {},
  "created": "2026-07-28T00:00:00Z",
  "updated": "2026-07-28T00:00:00Z",
  "closed": null,
  "body": "bodies/e-k3m9.md",
  "budget": 1200,
  "owner_hint": "tommy"
}
`)
	e, err := UnmarshalEpic(raw)
	if err != nil {
		t.Fatal(err)
	}
	keys := e.ExtraKeys()
	if len(keys) != 2 || keys[0] != "budget" || keys[1] != "owner_hint" {
		t.Fatalf("ExtraKeys = %v, want [budget owner_hint]", keys)
	}
	out, err := MarshalEpic(e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"budget": 1200`)) || !bytes.Contains(out, []byte(`"owner_hint": "tommy"`)) {
		t.Errorf("unknown keys were destroyed on write:\n%s", out)
	}
	// Sorted, and after every known key — a random order would rewrite every shard
	// on every save and destroy fsstore's zero-churn byte comparison.
	bi, oi, known := bytes.Index(out, []byte(`"budget"`)), bytes.Index(out, []byte(`"owner_hint"`)), bytes.Index(out, []byte(`"body"`))
	if known > bi || bi > oi {
		t.Errorf("unknown keys must follow the known ones, sorted:\n%s", out)
	}
	// And the re-emitted document must still be valid JSON with the same shape.
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-emitted epic is not valid JSON: %v\n%s", err, out)
	}
}

// TestUnmarshalEpicRejectsGarbage: a malformed epic shard is a validation error.
func TestUnmarshalEpicRejectsGarbage(t *testing.T) {
	if _, err := UnmarshalEpic([]byte("{ not json")); err == nil {
		t.Error("expected a validation error on malformed epic shard")
	}
}
