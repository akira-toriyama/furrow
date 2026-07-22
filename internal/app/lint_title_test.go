package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// TestLintFlagsControlCharTitle: a title that reached the store with an interior
// control character (a bulk import or a hand-edited shard, bypassing
// NormalizeTitle) is flagged `control-char` (warn) so it is visible and fixable
// with `furrow retitle`.
func TestLintFlagsControlCharTitle(t *testing.T) {
	a := newApp()
	idx, _ := a.Store.Load()
	idx.Add(core.Task{ID: "t-ctrl1", Title: "bad\ntitle", Status: "inbox", Priority: 100, Body: core.BodyPath("t-ctrl1")})
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	a.Store.SaveBody("t-ctrl1", "# bad\n")

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ps {
		if p.Code == "control-char" && p.ID == "t-ctrl1" {
			if p.Severity != core.SevWarn {
				t.Errorf("control-char must be a warning, got %q", p.Severity)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("lint must flag a control-char title, got %+v", ps)
	}
}
