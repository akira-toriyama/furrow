package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// (a) lint must flag a done-lane task with no Closed timestamp — the backstop for
// zombies created before the fix or by a hand-edit.
func TestLintFlagsDoneWithoutClosed(t *testing.T) {
	a := newApp()
	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-zomb2", Title: "zombie", Status: "done", Priority: 100, Body: core.BodyPath("t-zomb2")})
	a.Store.Save(idx)
	a.Store.SaveBody("t-zomb2", "# z\n")

	probs, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range probs {
		if p.ID == "t-zomb2" && p.Severity == core.SevError && strings.Contains(p.Msg, "closed") {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should error on a done task with no closed timestamp; got %+v", probs)
	}
}

// TestLintArchiveBacklogNudge pins t-0051: `furrow lint` warns (archive-backlog)
// when the archivable-done pile reaches [lint].archive_done, and 0 disables it.
func TestLintArchiveBacklogNudge(t *testing.T) {
	a := newApp()
	a.Cfg.LintArchiveDone = 2
	// Under the fixed clock a task done "now" is closed now; a negative window
	// pushes the cutoff into the future so those done tasks count as archivable
	// (the age mechanics themselves are covered by Archivable's own tests).
	a.Cfg.ArchiveOlderThanDays = -1

	hasNudge := func() bool {
		ps, err := a.Lint()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if p.Code == "archive-backlog" {
				return true
			}
		}
		return false
	}

	if hasNudge() {
		t.Fatal("no done tasks -> no archive nudge")
	}
	a.Add("d1", AddOpts{Status: "done"})
	if hasNudge() {
		t.Error("1 archivable done < threshold 2 -> no nudge yet")
	}
	a.Add("d2", AddOpts{Status: "done"})
	if !hasNudge() {
		t.Error("2 archivable done >= threshold 2 -> nudge should fire")
	}
	a.Cfg.LintArchiveDone = 0
	if hasNudge() {
		t.Error("[lint].archive_done=0 disables the nudge")
	}
}

func TestLintFlagsMissingBody(t *testing.T) {
	a := newApp()
	tk, _ := a.Add("has body", AddOpts{})
	// inject a task with no body file directly into the store.
	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-0099", Title: "ghost", Status: "ready", Body: core.BodyPath("t-0099")})
	a.Store.Save(idx)

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range ps {
		if p.ID == "t-0099" && p.Severity == core.SevError {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should flag t-0099 (no body), got %+v", ps)
	}
	_ = tk
}

// A task shard whose filename disagrees with the id it carries is a hand-edit
// corruption the old monolith couldn't have (the id was a field, not a
// filename). lint must catch it by directory enumeration.
func TestLintFlagsShardFilenameMismatch(t *testing.T) {
	dir := t.TempDir()
	ia, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-write a shard at tasks/t-wrongnm.json whose id field is t-realid0,
	// with a matching body so the ONLY inconsistency is the filename/id mismatch.
	fdir := filepath.Join(dir, ".furrow")
	shard := `{
  "id": "t-realid0",
  "title": "mismatched shard",
  "status": "ready",
  "priority": 100,
  "labels": [],
  "deps": [],
  "refs": [],
  "checklist": [],
  "created": "2026-06-25T00:00:00Z",
  "updated": "2026-06-25T00:00:00Z",
  "closed": null,
  "body": "bodies/t-realid0.md"
}
`
	if err := os.WriteFile(filepath.Join(fdir, "tasks", "t-wrongnm.json"), []byte(shard), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ia.Store.SaveBody("t-realid0", "# mismatched shard\n"); err != nil {
		t.Fatal(err)
	}

	ps, err := ia.Lint()
	if err != nil {
		t.Fatal(err)
	}
	// The ONLY inconsistency is the filename/id mismatch, so lint must report it
	// exactly once — no phantom "missing body" for the wrong filename and no
	// phantom "orphan body" for the real id whose shard is merely misnamed.
	var errs []core.Problem
	for _, p := range ps {
		if p.Severity == core.SevError {
			errs = append(errs, p)
		}
		if strings.Contains(p.Msg, "no body file") || strings.Contains(p.Msg, "orphan body") {
			t.Errorf("a misnamed shard must not cascade into a body finding: %+v", p)
		}
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Msg, "filename") {
		t.Errorf("expected exactly one filename-mismatch error, got %+v", ps)
	}
}

// A stray estimate cannot be WRITTEN: canonicalizeTask clamps it on the way to a
// shard, so SetValue(id, 9) stores 5 — what the CLI surfaces as `clamped`. The
// only way a 9 exists on a board is a HAND-EDITED shard, which is precisely what
// this warn is for (Lint deliberately reads the raw, pre-clamp index) and why the
// test edits the file. Seeding it through SetValue alone used to "work" only
// because memstore skipped the canonicalization fsstore performs — the assertion
// held against a shape no real board can hold.
func TestLintWarnsOutOfRangeEstimate(t *testing.T) {
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Add("x", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := a.SetValue(tk.ID, ptr(9))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Value == nil || *stored.Value != core.EstimateMax {
		t.Fatalf("a write must clamp 9 into 1..%d, got %v", core.EstimateMax, stored.Value)
	}

	shard := filepath.Join(dir, DirName, core.TaskPath(tk.ID))
	b, err := os.ReadFile(shard) // #nosec G304 -- a test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(b), `"value": 5`, `"value": 9`, 1)
	if edited == string(b) {
		t.Fatalf("shard carries no clamped value to hand-edit:\n%s", b)
	}
	if err := os.WriteFile(shard, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	probs, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range probs {
		if p.ID == tk.ID && p.Severity == core.SevWarn && strings.Contains(p.Msg, "value 9") {
			found = true
		}
	}
	if !found {
		t.Errorf("lint should warn that value 9 is out of range; got %+v", probs)
	}
}
