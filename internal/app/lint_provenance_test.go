package app

import (
	"strings"
	"testing"
)

// [lint].provenance_markers: opt-in — an open task whose body carries none of
// the markers warns provenance-missing; terminal tasks and marker-carrying
// bodies stay quiet, and an empty marker list keeps the check off entirely.
func TestLintProvenanceMarkers(t *testing.T) {
	a := newApp()
	bare, err := a.Add("no provenance", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	sourced, _ := a.Add("has provenance", AddOpts{})
	if _, err := a.AddNote(sourced.ID, "source: measured on HEAD, verified by rerun"); err != nil {
		t.Fatal(err)
	}
	parked, _ := a.Add("parked", AddOpts{Status: "icebox"})

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Code == "provenance-missing" {
			t.Fatalf("check must be off with no markers, got %+v", p)
		}
	}

	a.Cfg.LintProvenanceMarkers = []string{"Source:", "Verified:"}
	ps, err = a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range ps {
		if p.Code == "provenance-missing" {
			got[p.ID] = p.Msg
		}
	}
	if _, ok := got[bare.ID]; !ok {
		t.Errorf("markerless open task should warn, got %v", got)
	}
	if _, ok := got[sourced.ID]; ok {
		t.Errorf("a body carrying a marker (case-insensitively) must not warn")
	}
	if _, ok := got[parked.ID]; ok {
		t.Errorf("a terminal-lane task must not warn")
	}
	if !strings.Contains(got[bare.ID], "Source:, Verified:") || !strings.Contains(got[bare.ID], bare.ID) {
		t.Errorf("message should name the markers and the task, got %q", got[bare.ID])
	}
}
