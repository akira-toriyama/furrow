package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// unarchive makes archive a round trip: the task comes back exactly as
// archived (done lane, closed stamp), leaves the archive store, and a plain
// `move` then reopens it (t-yszb).
func TestUnarchiveRoundTrip(t *testing.T) {
	initStore(t)
	id := archiveOne(t, "comes back")

	out, code := run(t, "unarchive", id, "--json")
	if code != 0 {
		t.Fatalf("unarchive: exit %d\n%s", code, out)
	}
	var envs []struct {
		Before     *core.Task `json:"before"`
		After      *core.Task `json:"after"`
		Unarchived bool       `json:"unarchived"`
	}
	if err := json.Unmarshal([]byte(out), &envs); err != nil || len(envs) != 1 {
		t.Fatalf("envelopes = %s (err %v), want a one-element array", out, err)
	}
	e := envs[0]
	if e.Before != nil || e.After == nil || !e.Unarchived {
		t.Fatalf("envelope = %+v, want before:null + unarchived:true", e)
	}
	if e.After.Status != "done" || e.After.Closed == nil {
		t.Errorf("restored task = %+v, want done lane with the closed stamp preserved", e.After)
	}

	// Back on the hot board, gone from the archive.
	if out, code := run(t, "show", id, "--json"); code != 0 || !strings.Contains(out, "comes back") {
		t.Errorf("hot show after restore: exit %d\n%s", code, out)
	}
	if _, code := run(t, "show", id, "--archived"); code == 0 {
		t.Errorf("archive store still holds %s after restore", id)
	}

	// Reopening stays move's job — and works now that the task is back.
	if out, code := run(t, "move", id, "backlog", "--json"); code != 0 || !strings.Contains(out, `"closed": null`) {
		t.Errorf("move after restore: exit %d\n%s", code, out)
	}
}

// The batch is all-or-nothing (a miss restores nothing), a never-archived id
// is the not-found shape, and an id already on the hot board is exit 2.
func TestUnarchiveGuards(t *testing.T) {
	initStore(t)
	archived := archiveOne(t, "stays put on a miss")
	hot := addTask(t, "already here")

	fe, _ := runErr(t, "unarchive", archived, "t-nope1")
	if fe == nil || fe.Code != core.CodeNotFound {
		t.Fatalf("miss = %+v, want exit 1", fe)
	}
	det, _ := fe.Details.(map[string]any)
	if miss, _ := det["missing"].([]string); len(miss) != 1 || miss[0] != "t-nope1" {
		t.Errorf("details.missing = %v, want [t-nope1]", det["missing"])
	}
	if _, code := run(t, "show", archived, "--archived"); code != 0 {
		t.Errorf("all-or-nothing broken: %s left the archive on a failed batch", archived)
	}

	if fe, _ := runErr(t, "unarchive", hot); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("hot id = %+v, want exit 2 (nothing to restore)", fe)
	}
}

// Every mutator's miss now says "it is archived" with details.archived — the
// guidance show alone used to give (t-yszb). One single-task path, one batch
// path, and one non-move mutator prove the funnels.
func TestMutatorMissHintsArchived(t *testing.T) {
	initStore(t)
	id := archiveOne(t, "retired")

	for name, args := range map[string][]string{
		"move (batch funnel)":   {"move", id, "inbox"},
		"set (batch funnel)":    {"set", id, "-s", "inbox"},
		"note (single funnel)":  {"note", id, "progress line"},
		"value (single funnel)": {"value", id, "3"},
		"dep --list (read)":     {"dep", id, "--list"},
	} {
		fe, _ := runErr(t, args...)
		if fe == nil || fe.Code != core.CodeNotFound {
			t.Errorf("%s: err = %+v, want exit 1", name, fe)
			continue
		}
		det, _ := fe.Details.(map[string]any)
		arch, _ := det["archived"].([]string)
		if len(arch) != 1 || arch[0] != id {
			t.Errorf("%s: details.archived = %v, want [%s]", name, det["archived"], id)
		}
		if !strings.Contains(fe.Msg, "unarchive") {
			t.Errorf("%s: message %q must name the restore command", name, fe.Msg)
		}
	}
}

// Attached assets travel BOTH ways: after archive → unarchive the asset is
// back in the hot store and lint sees a consistent board (no dangling link,
// no orphan asset).
func TestUnarchiveRestoresAssets(t *testing.T) {
	initStore(t)
	id := addTask(t, "with media")
	src := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(src, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := run(t, "attach", id, src); code != 0 {
		t.Fatalf("attach: exit %d\n%s", code, out)
	}
	run(t, "done", id)
	if _, code := run(t, "archive", id, "--yes"); code != 0 {
		t.Fatal("archive failed")
	}
	if _, code := run(t, "unarchive", id); code != 0 {
		t.Fatal("unarchive failed")
	}
	out, _ := run(t, "lint", "--json")
	for _, code := range []string{"dangling-link", "orphan-asset"} {
		if strings.Contains(out, code) {
			t.Errorf("lint reports %s after the round trip:\n%s", code, out)
		}
	}
}
