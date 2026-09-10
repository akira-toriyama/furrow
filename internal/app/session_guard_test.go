package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// fakeRegistry is the test twin of the claudecode adapter.
type fakeRegistry struct {
	sessions []core.Session
	err      error
}

func (f fakeRegistry) Sessions() ([]core.Session, error) { return f.sessions, f.err }

// fakeCheckout plants <root>/<name>/.git/config whose origin names repo, so
// the guard's cwd→repo derivation (repoForDir) resolves it without git.
func fakeCheckout(t *testing.T, root, name, repo string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = git@github.com:" + repo + ".git\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// guardedApp is newApp with the guard armed: self (pid 100) started 10 minutes
// before the clock, and one occupant (pid 1) in a checkout of o/glyph that
// started an hour before, last active `idle` ago (zero = unknown).
func guardedApp(t *testing.T, idle time.Duration, unknown bool) (*App, string) {
	t.Helper()
	a := newApp()
	now := a.Clock.Now()
	glyph := fakeCheckout(t, t.TempDir(), "glyph", "o/glyph")
	occ := core.Session{PID: 1, ID: "occ", Name: "glyph-ab", CWD: glyph, StartedAt: now.Add(-time.Hour)}
	if !unknown {
		occ.LastActive = now.Add(-idle)
	}
	a.Sessions = fakeRegistry{sessions: []core.Session{
		{PID: 100, ID: "self", CWD: t.TempDir(), StartedAt: now.Add(-10 * time.Minute), LastActive: now},
		occ,
	}}
	a.Self = SessionRef{PID: 100, ID: "self"}
	return a, glyph
}

func wantSessionBusy(t *testing.T, err error, subject string) *core.Error {
	t.Helper()
	fe := core.AsError(err)
	if fe == nil || fe.Kind != core.KindSessionBusy || fe.Code != core.CodeValidation {
		t.Fatalf("want session-busy exit 2, got %v", err)
	}
	if fe.Subject != subject {
		t.Errorf("subject = %q, want %q", fe.Subject, subject)
	}
	if fe.Retryable {
		t.Error("session-busy is not retryable (a minutes-long wait is not a re-run)")
	}
	return fe
}

func TestSessionGuardAddRefusesABusyOccupantAndNamesTheEscape(t *testing.T) {
	a, glyph := guardedApp(t, 30*time.Second, false)
	_, err := a.Add("into glyph", AddOpts{Repos: []string{"o/glyph"}})
	fe := wantSessionBusy(t, err, "")
	d, _ := fe.Details.(map[string]any)
	if d["hint"] != "--draft" {
		t.Errorf("details.hint = %v, want --draft", d["hint"])
	}
	clashes, _ := d["clashes"].([]core.SessionClash)
	if len(clashes) != 1 || clashes[0].Repo != "o/glyph" || clashes[0].PID != 1 || !clashes[0].Busy || clashes[0].CWD != glyph || clashes[0].Name != "glyph-ab" {
		t.Fatalf("clashes = %+v", clashes)
	}
	if clashes[0].IdleSeconds == nil || *clashes[0].IdleSeconds != 30 {
		t.Errorf("idle_seconds = %v, want 30", clashes[0].IdleSeconds)
	}
	for _, want := range []string{"o/glyph", "glyph-ab, pid 1", "--draft", "active 30s ago"} {
		if !strings.Contains(fe.Msg, want) {
			t.Errorf("message %q lacks %q", fe.Msg, want)
		}
	}
	// Nothing was written.
	if ts, _ := a.List(QueryOpts{}); len(ts) != 0 {
		t.Fatalf("a refused add must create nothing: %+v", ts)
	}
	if notes := a.TakeSessionNotes(); len(notes) != 0 {
		t.Errorf("no stand-down note on a refusal: %v", notes)
	}
}

func TestSessionGuardDraftAndOtherReposPass(t *testing.T) {
	a, _ := guardedApp(t, 30*time.Second, false)
	if _, err := a.Add("draft", AddOpts{Draft: true}); err != nil {
		t.Fatalf("--draft is the escape: %v", err)
	}
	if _, err := a.Add("elsewhere", AddOpts{Repos: []string{"o/furrow"}}); err != nil {
		t.Fatalf("a repo nobody occupies passes: %v", err)
	}
	if w := a.TakeSessionWarn(); len(w) != 0 {
		t.Errorf("no warning on clean writes: %+v", w)
	}
}

func TestSessionGuardBoardScopeUnionIsGuarded(t *testing.T) {
	// The incident shape: a bare `add` from inside the occupied checkout —
	// the repo arrives via withBoardRepo, not -r.
	a, _ := guardedApp(t, 30*time.Second, false)
	a.DefaultRepo = "o/glyph"
	_, err := a.Add("bare add", AddOpts{})
	wantSessionBusy(t, err, "")
}

func TestSessionGuardUnknownActivityIsBusy(t *testing.T) {
	a, _ := guardedApp(t, 0, true)
	_, err := a.Add("x", AddOpts{Repos: []string{"o/glyph"}})
	fe := wantSessionBusy(t, err, "")
	if !strings.Contains(fe.Msg, "activity unknown") {
		t.Errorf("message should say the activity is unknown: %q", fe.Msg)
	}
}

func TestSessionGuardIdleOccupantWarnsOnce(t *testing.T) {
	a, _ := guardedApp(t, 20*time.Minute, false)
	tk, err := a.Add("x", AddOpts{Repos: []string{"o/glyph"}})
	if err != nil {
		t.Fatalf("an idle occupant must not refuse: %v", err)
	}
	if _, err := a.AddNote(tk.ID, "still fine"); err != nil {
		t.Fatalf("second write: %v", err)
	}
	w := a.TakeSessionWarn()
	if len(w) != 1 || w[0].Repo != "o/glyph" || w[0].Busy {
		t.Fatalf("one merged idle clash expected, got %+v", w)
	}
	if line := SessionWarnLine(w); !strings.Contains(line, "o/glyph") || !strings.Contains(line, "idle 20m") {
		t.Errorf("warn line = %q", line)
	}
	if w := a.TakeSessionWarn(); len(w) != 0 {
		t.Errorf("Take drains: %+v", w)
	}
}

func TestSessionGuardEndedTurnWarnsHoweverFreshTheTranscript(t *testing.T) {
	// The occupant wrote its transcript 3 seconds ago — but that write was the
	// message that ENDED its turn. It is waiting for the human, not working.
	a, _ := guardedApp(t, 3*time.Second, false)
	reg := a.Sessions.(fakeRegistry)
	reg.sessions[1].TurnEnded = true
	tk, err := a.Add("x", AddOpts{Repos: []string{"o/glyph"}})
	if err != nil {
		t.Fatalf("an occupant whose turn ended must not refuse: %v", err)
	}
	if _, err := a.AddNote(tk.ID, "still fine"); err != nil {
		t.Fatalf("nor on a later write: %v", err)
	}
	w := a.TakeSessionWarn()
	if len(w) != 1 || w[0].Repo != "o/glyph" || w[0].Busy || !w[0].TurnEnded || w[0].IdleSeconds == nil || *w[0].IdleSeconds != 3 {
		t.Fatalf("one idle clash flagged turn_ended expected, got %+v", w)
	}
	if line := SessionWarnLine(w); !strings.Contains(line, "idle 3s, turn ended") {
		t.Errorf("warn line = %q", line)
	}
	// Mid-turn 3 seconds ago is the refusal, unchanged.
	b, _ := guardedApp(t, 3*time.Second, false)
	if _, err := b.Add("y", AddOpts{Repos: []string{"o/glyph"}}); err == nil {
		t.Fatal("a mid-turn occupant active 3s ago must still refuse")
	}
}

func TestSessionGuardBusySecondsZeroIsWarnOnly(t *testing.T) {
	a, _ := guardedApp(t, time.Second, false)
	a.Cfg.SessionBusySeconds = 0
	if _, err := a.Add("x", AddOpts{Repos: []string{"o/glyph"}}); err != nil {
		t.Fatalf("busy_seconds=0 never refuses a known activity: %v", err)
	}
	if w := a.TakeSessionWarn(); len(w) != 1 {
		t.Fatalf("it still warns: %+v", w)
	}
	// 0 is the warn-only switch for unknown activity too: the one escape a
	// non-add write has when an occupant's transcript cannot be found.
	b, _ := guardedApp(t, 0, true)
	b.Cfg.SessionBusySeconds = 0
	if _, err := b.Add("x", AddOpts{Repos: []string{"o/glyph"}}); err != nil {
		t.Fatalf("busy_seconds=0 never refuses: %v", err)
	}
	if w := b.TakeSessionWarn(); len(w) != 1 || w[0].Busy {
		t.Fatalf("warned, not busy: %+v", w)
	}
}

func TestSessionGuardFirstComeFirstServed(t *testing.T) {
	a, _ := guardedApp(t, time.Second, false)
	// Self started BEFORE the occupant: the earlier session owns the repo.
	reg := a.Sessions.(fakeRegistry)
	reg.sessions[0].StartedAt = reg.sessions[1].StartedAt.Add(-time.Minute)
	if _, err := a.Add("mine", AddOpts{Repos: []string{"o/glyph"}}); err != nil {
		t.Fatalf("the earlier session's own write must pass: %v", err)
	}
	if w := a.TakeSessionWarn(); len(w) != 0 {
		t.Errorf("and not even warn: %+v", w)
	}
}

func TestSessionGuardStandsDownWhenItCannotOrder(t *testing.T) {
	cases := []struct {
		name string
		prep func(a *App)
		note string
	}{
		{"registry unreadable", func(a *App) { a.Sessions = fakeRegistry{err: os.ErrPermission} }, "cannot read"},
		{"self not registered", func(a *App) { a.Self = SessionRef{PID: 999, ID: "ghost"} }, "not in the Claude Code session registry"},
		{"self's own transcript unfindable", func(a *App) {
			reg := a.Sessions.(fakeRegistry)
			reg.sessions[0].LastActive = time.Time{}
		}, "own transcript"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := guardedApp(t, time.Second, false)
			c.prep(a)
			if _, err := a.Add("x", AddOpts{Repos: []string{"o/glyph"}}); err != nil {
				t.Fatalf("stand down, never refuse on a guess: %v", err)
			}
			notes := a.TakeSessionNotes()
			if len(notes) != 1 || !strings.Contains(notes[0], c.note) {
				t.Fatalf("one stand-down note expected, got %v", notes)
			}
			if w := a.TakeSessionWarn(); len(w) != 0 {
				t.Errorf("no warning when standing down: %+v", w)
			}
		})
	}
	t.Run("guard off", func(t *testing.T) {
		a, _ := guardedApp(t, time.Second, false)
		a.Sessions = nil
		if _, err := a.Add("x", AddOpts{Repos: []string{"o/glyph"}}); err != nil {
			t.Fatal(err)
		}
		if n := a.TakeSessionNotes(); len(n) != 0 {
			t.Errorf("nothing to say with the guard off: %v", n)
		}
	})
}

// offGuard creates fixtures with the guard disarmed and re-arms it.
func offGuard(a *App, fn func()) {
	reg := a.Sessions
	a.Sessions = nil
	fn()
	a.Sessions = reg
}

func TestSessionGuardJudgesBothSidesOfARepoEdit(t *testing.T) {
	a, _ := guardedApp(t, time.Second, false)
	var elsewhere, inGlyph *core.Task
	offGuard(a, func() {
		elsewhere, _ = a.Add("elsewhere", AddOpts{Repos: []string{"o/furrow"}})
		inGlyph, _ = a.Add("in glyph", AddOpts{Repos: []string{"o/glyph"}})
	})
	// Attaching the occupied repo is a write INTO it.
	_, err := a.Rerepo(elsewhere.ID, []string{"o/glyph"}, nil)
	wantSessionBusy(t, err, elsewhere.ID)
	if got, _, _ := a.Get(elsewhere.ID); len(got.Repos) != 1 || got.Repos[0] != "o/furrow" {
		t.Fatalf("a refused edit must not land: %+v", got.Repos)
	}
	// Detaching it is a write OUT of it.
	_, err = a.Rerepo(inGlyph.ID, nil, []string{"o/glyph"})
	wantSessionBusy(t, err, inGlyph.ID)
	// set --add-repo, single and batch.
	_, _, err = a.Set(elsewhere.ID, SetOpts{AddRepos: []string{"o/glyph"}})
	wantSessionBusy(t, err, elsewhere.ID)
	_, err = a.SetMany([]string{elsewhere.ID}, SetOpts{AddRepos: []string{"o/glyph"}})
	wantSessionBusy(t, err, elsewhere.ID)
	// An edit that never touches the occupied repo passes.
	if _, err := a.Relabel(elsewhere.ID, []string{"tag"}, nil); err != nil {
		t.Fatalf("unrelated task: %v", err)
	}
}

func TestSessionGuardCoversEveryTaskWrite(t *testing.T) {
	a, _ := guardedApp(t, time.Second, false)
	var x, y *core.Task
	offGuard(a, func() {
		x, _ = a.Add("x", AddOpts{Repos: []string{"o/glyph"}, Status: "ready"})
		y, _ = a.Add("y", AddOpts{Repos: []string{"o/glyph"}, Status: "ready"})
	})
	one := func(fn func() (*core.Task, error)) func() error {
		return func() error { _, err := fn(); return err }
	}
	writes := map[string]func() error{
		"Done":            one(func() (*core.Task, error) { return a.Done(x.ID) }),
		"Move":            one(func() (*core.Task, error) { return a.Move(x.ID, "backlog") }),
		"DoneNote":        one(func() (*core.Task, error) { return a.DoneNote(x.ID, "bye") }),
		"AddNote":         one(func() (*core.Task, error) { return a.AddNote(x.ID, "note") }),
		"SetBody":         one(func() (*core.Task, error) { return a.SetBody(x.ID, "# new") }),
		"Reorder":         one(func() (*core.Task, error) { return a.Reorder(x.ID, 5) }),
		"SetValue":        one(func() (*core.Task, error) { return a.SetValue(x.ID, cloneIntp(intp(3))) }),
		"Retitle":         one(func() (*core.Task, error) { return a.Retitle(x.ID, "renamed") }),
		"Relabel":         one(func() (*core.Task, error) { return a.Relabel(x.ID, []string{"l"}, nil) }),
		"AddCheck":        one(func() (*core.Task, error) { return a.AddCheck(x.ID, "item") }),
		"AddDeps":         one(func() (*core.Task, error) { return a.AddDeps(x.ID, []string{y.ID}) }),
		"Set":             func() error { _, _, err := a.Set(x.ID, SetOpts{Status: strp("backlog")}); return err },
		"ReorderRelative": func() error { _, _, err := a.ReorderRelative(x.ID, y.ID, false); return err },
		"MoveMany":        func() error { _, err := a.MoveMany([]string{x.ID, y.ID}, "backlog"); return err },
		"DoneMany":        func() error { _, err := a.DoneMany([]string{x.ID}); return err },
		"DoneManyNote":    func() error { _, err := a.DoneManyNote([]string{x.ID}, "bye"); return err },
		"SetMany":         func() error { _, err := a.SetMany([]string{x.ID}, SetOpts{AddLabels: []string{"l"}}); return err },
		"AppendBody":      func() error { _, err := a.AppendBody(x.ID, "applied"); return err },
		"Attach":          func() error { _, err := a.Attach(x.ID, "shot.png", []byte("png")); return err },
	}
	for name, w := range writes {
		t.Run(name, func(t *testing.T) {
			wantSessionBusy(t, w(), x.ID)
		})
	}
	t.Run("RemoveDeps", func(t *testing.T) {
		offGuard(a, func() { _, _ = a.AddDeps(x.ID, []string{y.ID}) })
		_, err := a.RemoveDeps(x.ID, []string{y.ID})
		wantSessionBusy(t, err, x.ID)
	})
	t.Run("ReviewTask is board maintenance, not guarded", func(t *testing.T) {
		if _, err := a.ReviewTask(x.ID); err != nil {
			t.Fatal(err)
		}
	})
	// The store is untouched by every refusal above (bar the review stamp and
	// the dep the RemoveDeps fixture attached with the guard off).
	got, body, _ := a.Get(x.ID)
	if got.Status != "ready" || got.Title != "x" || len(got.Labels) != 0 || len(got.Deps) != 1 || body != "# x\n" {
		t.Fatalf("a refused write landed: %+v body=%q", got, body)
	}
}

// The three refusals a first review found landing bytes anyway: an asset
// written before the guard, an epic body replaced/annotated before it, and a
// batch --note annotating the free tasks before refusing on the occupied one.
func TestSessionGuardRefusalLeavesEveryFileUntouched(t *testing.T) {
	a, _ := guardedApp(t, time.Second, false)
	var occupied, free *core.Task
	var box *core.Epic
	offGuard(a, func() {
		occupied, _ = a.Add("occupied", AddOpts{Repos: []string{"o/glyph"}, Status: "ready"})
		free, _ = a.Add("free", AddOpts{Repos: []string{"o/furrow"}, Status: "ready"})
		box, _ = a.EpicAdd("box", EpicAddOpts{Repos: []string{"o/glyph"}})
	})
	_, err := a.Attach(occupied.ID, "shot.png", []byte("png"))
	wantSessionBusy(t, err, occupied.ID)
	if assets, _ := a.Store.ListAssets(); len(assets) != 0 {
		t.Fatalf("a refused attach must leave no asset: %+v", assets)
	}
	_, _, err = a.EpicSetBody(box.ID, "# clobbered")
	wantSessionBusy(t, err, box.ID)
	_, _, err = a.EpicNote(box.ID, "leaked")
	wantSessionBusy(t, err, box.ID)
	if body, _ := a.Store.LoadBody(box.ID); body != "# box\n" {
		t.Fatalf("a refused epic prose write must not land: %q", body)
	}
	_, err = a.DoneManyNote([]string{free.ID, occupied.ID}, "batch note")
	wantSessionBusy(t, err, occupied.ID)
	if body, _ := a.Store.LoadBody(free.ID); body != "# free\n" {
		t.Fatalf("a refused batch must not annotate the free task first: %q", body)
	}
	if got, _, _ := a.Get(free.ID); got.Status != "ready" {
		t.Fatalf("free task moved: %s", got.Status)
	}
}

func TestSessionGuardCoversEpicWrites(t *testing.T) {
	a, _ := guardedApp(t, time.Second, false)
	_, err := a.EpicAdd("box", EpicAddOpts{Repos: []string{"o/glyph"}})
	fe := wantSessionBusy(t, err, "")
	if d, _ := fe.Details.(map[string]any); d["hint"] != nil {
		t.Errorf("no --draft hint on an epic add: %v", d["hint"])
	}
	var box, other *core.Epic
	offGuard(a, func() {
		box, _ = a.EpicAdd("box", EpicAddOpts{Repos: []string{"o/glyph"}})
		other, _ = a.EpicAdd("other", EpicAddOpts{Repos: []string{"o/furrow"}})
	})
	title := "renamed"
	_, _, err = a.EpicSet(box.ID, EpicSetOpts{Title: &title})
	wantSessionBusy(t, err, box.ID)
	_, _, err = a.EpicNote(box.ID, "note")
	wantSessionBusy(t, err, box.ID)
	_, _, _, err = a.EpicActivate(box.ID, "")
	wantSessionBusy(t, err, box.ID)
	_, _, err = a.EpicSet(other.ID, EpicSetOpts{AddRepos: []string{"o/glyph"}})
	wantSessionBusy(t, err, other.ID)
	if _, _, err := a.EpicSet(other.ID, EpicSetOpts{Title: &title}); err != nil {
		t.Fatalf("a box in an unoccupied repo edits freely: %v", err)
	}
}
