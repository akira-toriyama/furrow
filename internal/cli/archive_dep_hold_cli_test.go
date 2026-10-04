package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// TestCLIArchiveByIDRefusesALiveDep pins t-tf56: archiving a done task a ready
// task depends on used to exit 0, drop the dependent out of `next` and leave
// the board in two lint errors (dep-missing, ready-blocked). It is refused
// whole, and the board stays as it was; once the dependent is done the two
// retire together.
func TestCLIArchiveByIDRefusesALiveDep(t *testing.T) {
	initStore(t)
	dep := addTask(t, "the dep", "-s", "ready")
	waiter := addTask(t, "the waiter", "-s", "ready", "--dep", dep)
	run(t, "done", dep)

	fe, _ := runErr(t, "archive", dep, "--yes")
	if fe == nil || fe.Kind != core.KindReferenced || fe.Code != core.CodeValidation {
		t.Fatalf("archiving a live dep should be exit 2 kind referenced, got %+v", fe)
	}
	refs, _ := json.Marshal(fe.Details)
	if want := `"deps":[{"from":"` + waiter + `","to":"` + dep + `"}]`; !strings.Contains(string(refs), want) {
		t.Errorf("details.references should carry the edge %s:\n%s", want, refs)
	}
	if out, code := run(t, "show", dep); code != 0 {
		t.Errorf("the refused dep must still be on the hot board (exit %d):\n%s", code, out)
	}
	if out, _ := run(t, "next"); !strings.Contains(out, waiter) {
		t.Errorf("the dependent must still be handed out by next:\n%s", out)
	}
	if out, code := run(t, "lint"); code != 0 {
		t.Errorf("a refused archive must leave lint as it was (exit %d):\n%s", code, out)
	}

	run(t, "done", waiter)
	if out, code := run(t, "archive", dep, waiter, "--yes"); code != 0 {
		t.Fatalf("a dep and its done dependent retire together (exit %d):\n%s", code, out)
	}
	if out, code := run(t, "lint"); code != 0 {
		t.Errorf("lint after the joint retire (exit %d):\n%s", code, out)
	}
}

// TestCLIArchiveSweepHoldsLiveDeps: the age sweep leaves a selected task that a
// staying task depends on — transitively, since a held task is itself staying —
// and names it in `held`; everything else aged still goes, and the held chain
// goes on the sweep after its dependent has.
func TestCLIArchiveSweepHoldsLiveDeps(t *testing.T) {
	dir := t.TempDir()
	ia, err := app.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idx, _ := ia.Store.Load()
	seed := func(id, status string, closed *time.Time, deps ...string) {
		idx.Add(core.Task{ID: id, Title: id, Status: status, Priority: 10 * (len(idx.Tasks) + 1),
			Created: old, Updated: old, Closed: closed, Deps: deps, Body: core.BodyPath(id)})
		ia.Store.SaveBody(id, "# "+id+"\n")
	}
	seed("t-root", "done", &old)
	seed("t-mid0", "done", &old, "t-root")
	seed("t-open", "ready", nil, "t-mid0")
	seed("t-free", "done", &old)
	seed("t-pair", "done", &old, "t-free") // leaves with its dep: holds nothing
	ia.Store.Save(idx)
	t.Setenv(app.EnvDir, filepath.Join(dir, app.DirName))

	type report struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
		Held []app.HeldTask `json:"held"`
	}
	sweep := func() report {
		t.Helper()
		out, code := run(t, "--json", "archive", "-r", "", "--yes")
		if code != 0 {
			t.Fatalf("sweep exit %d:\n%s", code, out)
		}
		var r report
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("parse: %v\n%s", err, out)
		}
		return r
	}
	ids := func(r report) string {
		var s []string
		for _, x := range r.Tasks {
			s = append(s, x.ID)
		}
		return strings.Join(s, ",")
	}

	r := sweep()
	if got := ids(r); got != "t-free,t-pair" {
		t.Errorf("sweep moved %q, want t-free,t-pair", got)
	}
	held, _ := json.Marshal(r.Held)
	want := `[{"id":"t-root","title":"t-root","held_by":["t-mid0"]},{"id":"t-mid0","title":"t-mid0","held_by":["t-open"]}]`
	if string(held) != want {
		t.Errorf("held =\n%s\nwant\n%s", held, want)
	}
	if out, _ := run(t, "next", "-r", ""); !strings.Contains(out, "t-open") {
		t.Errorf("the dependent must still be handed out by next:\n%s", out)
	}
	if out, _ := run(t, "lint"); strings.Contains(out, "dep-missing") || strings.Contains(out, "ready-blocked") {
		t.Errorf("the sweep must not strand a dep edge:\n%s", out)
	}

	out, _ := run(t, "archive", "-r", "")
	if !strings.Contains(out, "would be held in the hot store: t-mid0  t-mid0 — still a dep of t-open") {
		t.Errorf("the preview should name the held task and its dependent:\n%s", out)
	}

	// Once the dependent has aged out too, the chain goes whole.
	idx, _ = ia.Store.Load()
	o, _ := idx.Find("t-open")
	o.Status, o.Closed = "done", &old
	ia.Store.Save(idx)
	r = sweep()
	if got := ids(r); got != "t-root,t-mid0,t-open" || len(r.Held) != 0 {
		t.Errorf("second sweep moved %q held %v, want the whole chain and none held", got, r.Held)
	}
}
