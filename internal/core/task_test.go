package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sampleTask is one deliberately-noisy task covering the same tricky cases the
// index golden does, but for a single shard: CJK + HTML-ish title (must survive
// SetEscapeHTML(false)), a set value/effort, labels that are unsorted AND
// duplicated (must sort+dedupe), populated deps/refs/checklist, and an open task
// (closed == null).
func sampleTask() *Task {
	mk := func(y int, mo time.Month, d int) time.Time {
		return time.Date(y, mo, d, 1, 2, 3, 0, time.UTC)
	}
	vi := func(n int) *int { return &n }
	return &Task{
		ID: "t-0001", Title: "畝を一本進める <b>&amp;</b> 完了", Status: "in-progress",
		Priority: 110, Value: vi(4), Effort: vi(2),
		Labels:    []string{"zmk", "canon", "zmk"},                                                    // unsorted + duplicated
		Repos:     []string{"akira-toriyama/furrow", "akira-toriyama/chord", "akira-toriyama/furrow"}, // unsorted + duplicated
		Deps:      []string{"t-0002"},
		Refs:      []string{"docs/x.md#L10", "https://example.com"},
		Checklist: []ChecklistItem{{Text: "design", Done: true}, {Text: "ship", Done: false}},
		Created:   mk(2026, 6, 2), Updated: mk(2026, 6, 21), Closed: nil,
		Body: BodyPath("t-0001"),
	}
}

func TestTaskPath(t *testing.T) {
	if got := TaskPath("t-0042"); got != "tasks/t-0042.json" {
		t.Errorf("TaskPath = %q, want %q", got, "tasks/t-0042.json")
	}
	if got := TaskPath("t-k3m9p"); got != "tasks/t-k3m9p.json" {
		t.Errorf("TaskPath = %q, want %q", got, "tasks/t-k3m9p.json")
	}
}

func TestMarshalTaskGolden(t *testing.T) {
	got, err := MarshalTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "task.golden.json")
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
		t.Errorf("MarshalTask output != golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestMarshalTaskDeterministic: the per-task twin of the index contract —
// re-marshalling a task parsed from canonical bytes yields byte-identical output
// (zero churn when a shard is re-saved untouched).
func TestMarshalTaskDeterministic(t *testing.T) {
	first, err := MarshalTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := UnmarshalTask(first)
	if err != nil {
		t.Fatal(err)
	}
	if parsed == nil {
		t.Fatal("UnmarshalTask returned nil task")
	}
	second, err := MarshalTask(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("re-marshal not byte-stable\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMarshalTaskDetails(t *testing.T) {
	got, err := MarshalTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)

	if !bytes.HasSuffix(got, []byte("\n")) {
		t.Error("shard output must end with a trailing newline")
	}
	// A shard is a bare Task object: it must NOT carry the index's schema_version
	// (meta.json owns the board version, not each shard).
	if bytes.Contains(got, []byte("schema_version")) {
		t.Errorf("a task shard must not contain schema_version:\n%s", s)
	}
	// SetEscapeHTML(false): CJK and < > & survive literally.
	for _, lit := range []string{"畝を一本進める", "<b>&amp;</b>", "完了"} {
		if !bytes.Contains(got, []byte(lit)) {
			t.Errorf("expected literal %q to survive un-escaped:\n%s", lit, s)
		}
	}
	// Labels are a set: sorted AND deduped, so ["zmk","canon","zmk"] -> [canon zmk].
	ci, zi := bytes.Index(got, []byte(`"canon"`)), bytes.Index(got, []byte(`"zmk"`))
	if ci < 0 || zi < 0 || ci > zi {
		t.Errorf("labels must be sorted (canon before zmk):\n%s", s)
	}
	if n := bytes.Count(got, []byte(`"zmk"`)); n != 1 {
		t.Errorf("labels must be deduped: want 1 \"zmk\", got %d:\n%s", n, s)
	}
	// open task -> "closed": null
	if !bytes.Contains(got, []byte(`"closed": null`)) {
		t.Errorf("open task must serialize closed as null:\n%s", s)
	}
	// a set estimate serializes its key.
	if !bytes.Contains(got, []byte(`"value": 4`)) || !bytes.Contains(got, []byte(`"effort": 2`)) {
		t.Errorf("a task with value/effort must serialize them:\n%s", s)
	}
}

func TestUnmarshalTaskRoundTrip(t *testing.T) {
	orig := sampleTask()
	data, err := MarshalTask(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalTask(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != orig.ID || got.Title != orig.Title || got.Status != orig.Status {
		t.Errorf("round-trip lost scalar fields: got %+v", got)
	}
	if got.Value == nil || *got.Value != 4 || got.Effort == nil || *got.Effort != 2 {
		t.Errorf("round-trip lost value/effort: %s/%s", fmtIntp(got.Value), fmtIntp(got.Effort))
	}
	// canonicalization applied on marshal: labels came back sorted+deduped.
	if len(got.Labels) != 2 || got.Labels[0] != "canon" || got.Labels[1] != "zmk" {
		t.Errorf("labels not canonical after round-trip: %v", got.Labels)
	}
	if len(got.Checklist) != 2 || got.Checklist[0].Text != "design" || !got.Checklist[0].Done {
		t.Errorf("round-trip lost checklist: %v", got.Checklist)
	}
}

// UnmarshalTask reports a parse failure as a validation error (malformed input),
// mirroring Unmarshal for the index.
func TestUnmarshalTaskRejectsGarbage(t *testing.T) {
	if _, err := UnmarshalTask([]byte("{not json")); err == nil {
		t.Error("expected a validation error for malformed shard bytes")
	}
}

// A shard with nil slices must serialize them as [] (not null) — the single most
// load-bearing determinism rule (see Marshal's doc and CLAUDE.md). sampleTask
// populates every slice, so this is the ONLY shard test that exercises
// canonicalizeTask's nil-guards; Refs and Checklist rely on them alone (Labels
// and Deps have sortDedup as a second backstop). Deleting a guard here must fail.
func TestMarshalTaskFillsNilSlices(t *testing.T) {
	epoch := time.Unix(0, 0).UTC()
	bare := &Task{
		ID: "t-bare", Title: "bare", Status: "ready", Priority: 10,
		Created: epoch, Updated: epoch, Body: BodyPath("t-bare"),
		// Labels, Deps, Refs, Checklist deliberately left nil.
	}
	got, err := MarshalTask(bare)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, key := range []string{"labels", "deps", "refs", "checklist"} {
		if !bytes.Contains(got, []byte(`"`+key+`": []`)) {
			t.Errorf("nil %s must marshal as [], not null:\n%s", key, s)
		}
		if bytes.Contains(got, []byte(`"`+key+`": null`)) {
			t.Errorf("%s serialized as null; nil slices must become []:\n%s", key, s)
		}
	}
}

// Timestamps are coerced to the on-disk contract: UTC, whole seconds. sampleTask
// already supplies canonical times, so feed a fractional-second, non-UTC time
// here and assert the shard drops the fraction and offset (ends in Z) — a
// regression in normTime would otherwise pass every other shard test.
func TestMarshalTaskNormalizesTimestamps(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	// 2026-06-02 10:02:03.5 +09:00 == 2026-06-02T01:02:03Z once UTC + truncated.
	ts := time.Date(2026, 6, 2, 10, 2, 3, 500_000_000, jst)
	tk := &Task{
		ID: "t-ts", Title: "ts", Status: "ready", Priority: 10,
		Created: ts, Updated: ts, Body: BodyPath("t-ts"),
	}
	got, err := MarshalTask(tk)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !bytes.Contains(got, []byte(`"created": "2026-06-02T01:02:03Z"`)) {
		t.Errorf("created must normalize to whole-second UTC (…Z):\n%s", s)
	}
	if bytes.Contains(got, []byte(".5")) || bytes.Contains(got, []byte("+09:00")) {
		t.Errorf("fractional seconds / zone offset must not survive normalization:\n%s", s)
	}
}
