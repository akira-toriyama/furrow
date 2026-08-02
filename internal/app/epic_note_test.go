package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// A box's progress record is the SAME operation as a task's: the paragraph
// lands in the shared bodies/ file and the BOX's shard stamps updated — the
// half a hand-edit cannot do, and the whole reason `furrow note` takes either
// entity's id instead of an `epic note` verb.
func TestEpicNoteAppendsParagraphAndBumpsUpdated(t *testing.T) {
	a, clk := appWithClock(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	e, err := a.EpicAdd("a box", EpicAddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	created := e.Updated

	// Time passes before the note is written.
	clk.t = clk.t.Add(48 * time.Hour)

	before, after, err := a.EpicNote(e.ID, "方針: pinned に寄せる。")
	if err != nil {
		t.Fatal(err)
	}
	if !before.Updated.Equal(created) {
		t.Errorf("before must be the pre-note snapshot: got %s want %s", before.Updated, created)
	}
	if !after.Updated.After(created) {
		t.Errorf("a note must advance Updated: created=%s updated=%s", created, after.Updated)
	}
	if !after.Updated.Equal(clk.Now()) {
		t.Errorf("Updated should be the note's clock time: got %s want %s", after.Updated, clk.Now())
	}

	body, _ := a.Store.LoadBody(e.ID)
	if want := "# a box\n\n方針: pinned に寄せる。\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}

	// A second note appends another paragraph separated by exactly one blank
	// line, and is not deduped — the AddNote contract, unchanged.
	if _, _, err := a.EpicNote(e.ID, "方針: pinned に寄せる。"); err != nil {
		t.Fatal(err)
	}
	body, _ = a.Store.LoadBody(e.ID)
	if n := strings.Count(body, "pinned に寄せる"); n != 2 {
		t.Errorf("a repeated note must still append (want 2 copies), got %d in:\n%s", n, body)
	}
	if strings.Contains(body, "\n\n\n") {
		t.Errorf("paragraphs must be separated by exactly one blank line:\n%q", body)
	}
}

// The activation log and a note share one body, and neither clobbers the other:
// `epic activate --reason` appends through the same helper.
func TestEpicNoteCoexistsWithTheActivationLog(t *testing.T) {
	a, _ := appWithClock(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	e, err := a.EpicAdd("a box", EpicAddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.EpicActivate(e.ID, "the user asked"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.EpicNote(e.ID, "止まった所: dispatch まで。"); err != nil {
		t.Fatal(err)
	}
	body, _ := a.Store.LoadBody(e.ID)
	for _, want := range []string{"activated — the user asked", "止まった所: dispatch まで。"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lost %q:\n%s", want, body)
		}
	}
}

// A box reference resolves the way every other epic ref does — and that is why
// an unknown box is exit 2 with candidates where an unknown task id is exit 1.
func TestEpicNoteResolutionAndValidation(t *testing.T) {
	a, _ := appWithClock(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	e, err := a.EpicAdd("a box", EpicAddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}

	// A unique id prefix and a unique title substring both resolve.
	if _, after, err := a.EpicNote(e.ID[:4], "by prefix"); err != nil {
		t.Errorf("id prefix should resolve: %v", err)
	} else if after.ID != e.ID {
		t.Errorf("prefix resolved to %s, want %s", after.ID, e.ID)
	}
	if _, after, err := a.EpicNote("box", "by title"); err != nil {
		t.Errorf("title substring should resolve: %v", err)
	} else if after.ID != e.ID {
		t.Errorf("title resolved to %s, want %s", after.ID, e.ID)
	}

	// An empty note is refused BEFORE resolution, exactly as AddNote refuses it
	// before the index load: a whitespace note is never a silent no-op append.
	if _, _, err := a.EpicNote(e.ID, "   \n\t "); err == nil {
		t.Error("empty/whitespace note should be a validation error")
	} else if fe := core.AsError(err); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("empty note want CodeValidation, got %v", err)
	}

	// Unknown box: the epic resolver's exit 2 + candidates, NOT the task path's
	// NotFound — pinned because the two entities' misses differ on purpose.
	_, _, err = a.EpicNote("e-nope0", "x")
	fe := core.AsError(err)
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("unknown box want CodeValidation, got %v", err)
	}
	if len(fe.Candidates) == 0 {
		t.Errorf("unknown box should carry candidates, got %+v", fe)
	}

	// The refused writes left the body alone.
	body, _ := a.Store.LoadBody(e.ID)
	if strings.Count(body, "\n\n") != 2 { // heading + the two accepted notes
		t.Errorf("a refused note must not touch the body:\n%q", body)
	}
}

// The routing table on an ordinary board: a real id of either entity goes to
// its own store, an epic REF resolves like every other one, and a ref naming
// nothing routes by shape — which only picks whose error the caller gets.
func TestRefTargetsEpicRoutes(t *testing.T) {
	a, _ := appWithClock(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	tk, err := a.Add("a task", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.EpicAdd("a box", EpicAddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ref  string
		want bool
		why  string
	}{
		{tk.ID, false, "a real task id"},
		{e.ID, true, "a real epic id"},
		{e.ID[:4], true, "a unique epic id prefix"},
		{"box", true, "a unique epic title substring"},
		{"t-nope0", false, "an unknown task-shaped id: the task path's exit 1"},
		{"e-nope0", true, "an unknown epic-shaped id: the box resolver's candidates"},
		{"nothing like this", false, "an unresolvable non-id ref"},
		{"E-K3M9", false, "ids are lowercase base32"},
		{"e-", true, "a bare prefix still resolves while it is UNIQUE — the epic ref rule, not a shape rule"},
		{"", false, ""},
	} {
		got, err := a.RefTargetsEpic(tc.ref)
		if err != nil {
			t.Fatalf("RefTargetsEpic(%q): %v", tc.ref, err)
		}
		if got != tc.want {
			t.Errorf("RefTargetsEpic(%q) = %v, want %v — %s", tc.ref, got, tc.want, tc.why)
		}
	}
}

// The regression this routing exists for. [ids] accepts an epic_prefix that
// EXTENDS the task prefix (config only clamps the equal case), and ids are
// prefix + random base32 — so on such a board ~1 task id in 32 is SHAPED like
// an epic id, and ~1 epic id in 32 like a task id. A prefix guess sends those
// to the wrong store: real tasks lost `furrow note` entirely (exit 2 "unknown
// epic"), and real boxes were unreachable (exit 1 "task not found"). Membership
// decides instead, so both stay reachable. The ids here are planted rather than
// minted because the collision is random.
func TestRefTargetsEpicIgnoresOverlappingPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cfg        string
		taskID     string
		epicID     string
		bothShapes string // the id that matches BOTH patterns
	}{
		{
			name: "epic prefix extends the task prefix",
			cfg:  "[ids]\nprefix = \"t-\"\nepic_prefix = \"t-e\"\n",
			// t-en4xz matches ^t-[0-9a-z]+$ AND ^t-e[0-9a-z]+$.
			taskID: "t-en4xz", epicID: "t-e3cn3", bothShapes: "t-en4xz",
		},
		{
			name:   "task prefix extends the epic prefix",
			cfg:    "[ids]\nprefix = \"e-t\"\nepic_prefix = \"e-\"\n",
			taskID: "e-tk3m9", epicID: "e-tahv1", bothShapes: "e-tahv1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := appWithConfig(t, tc.cfg)
			plantTask(t, a, tc.taskID)
			plantEpic(t, a, tc.epicID)

			if got, err := a.RefTargetsEpic(tc.taskID); err != nil || got {
				t.Errorf("task %s routed to the box path (%v, %v)", tc.taskID, got, err)
			}
			if got, err := a.RefTargetsEpic(tc.epicID); err != nil || !got {
				t.Errorf("epic %s routed to the task path (%v, %v)", tc.epicID, got, err)
			}
			// Both are reachable, which is the point: the shape alone would have
			// lost whichever entity does not own the longer prefix.
			if _, err := a.AddNote(tc.taskID, "task progress"); err != nil {
				t.Errorf("note on task %s: %v", tc.taskID, err)
			}
			if _, _, err := a.EpicNote(tc.epicID, "box progress"); err != nil {
				t.Errorf("note on epic %s: %v", tc.epicID, err)
			}
			if !a.isEpicIDShape(tc.bothShapes) && !a.Cfg.EpicIDPattern().MatchString(tc.bothShapes) {
				t.Fatalf("precondition: %s should match the epic shape too", tc.bothShapes)
			}
		})
	}
}

// appWithConfig builds an App on a memstore whose id prefixes come from an
// inline config.toml — the only way to exercise a non-default [ids] block,
// since Config compiles its patterns at load time.
func appWithConfig(t *testing.T, toml string) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st := memstore.New(cfg.IDPrefix, cfg.EpicIDPrefix, cfg.IDWidth)
	return NewWithStore(st, cfg, &fixedClock{t: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)})
}

// plantTask writes a task with a CHOSEN id straight through the store — the
// random minter cannot produce the prefix collision under test.
func plantTask(t *testing.T, a *App, id string) {
	t.Helper()
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	now := a.Clock.Now()
	idx.Tasks = append(idx.Tasks, core.Task{
		ID: id, Title: "planted " + id, Status: a.Cfg.DefaultLane,
		Created: now, Updated: now, Body: core.BodyPath(id),
	})
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.SaveBody(id, "# planted "+id+"\n"); err != nil {
		t.Fatal(err)
	}
}

// plantEpic is plantTask's box twin.
func plantEpic(t *testing.T, a *App, id string) {
	t.Helper()
	now := a.Clock.Now()
	e := core.Epic{ID: id, Title: "planted " + id, Repos: []string{"o/r"},
		Created: now, Updated: now, Body: core.BodyPath(id)}
	if err := a.Store.SaveEpic(&e); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.SaveBody(id, "# planted "+id+"\n"); err != nil {
		t.Fatal(err)
	}
}
