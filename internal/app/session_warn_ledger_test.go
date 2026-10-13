package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// ledgerApp is an App under a fake self session with the registry snapshot
// already read (what any guarded write leaves behind), and an isolated state
// dir for the ledger.
func ledgerApp(t *testing.T, selfID string, live ...string) *App {
	t.Helper()
	t.Setenv(EnvStateHome, t.TempDir())
	a, _ := newAppWith(at(time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)))
	a.Self = SessionRef{PID: 1, ID: selfID}
	a.sessionRead = true
	a.sessionOthers = nil
	for _, id := range append([]string{selfID}, live...) {
		a.sessionOthers = append(a.sessionOthers, core.Session{ID: id})
	}
	return a
}

func clash(occupant, repo string, active time.Time) core.SessionClash {
	c := core.SessionClash{Repo: repo, PID: 7, SessionID: occupant}
	if !active.IsZero() {
		a := active.UTC().Truncate(time.Second)
		c.LastActive = &a
	}
	return c
}

func ids(cs []core.SessionClash) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.SessionID+"@"+c.Repo)
	}
	return out
}

func TestUnheardClashesReportsOncePerSessionUntilOccupantMoves(t *testing.T) {
	t0 := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	a := ledgerApp(t, "self", "occ")
	c := clash("occ", "o/r", t0)

	if got := ids(a.UnheardClashes([]core.SessionClash{c})); len(got) != 1 {
		t.Fatalf("first report must be news: %v", got)
	}
	// A new process (fresh App) of the SAME session has heard it: the ledger
	// is what remembers.
	b := ledgerApp2(t, a)
	if got := b.UnheardClashes([]core.SessionClash{c}); len(got) != 0 {
		t.Errorf("same occupant, same activity: not news again, got %v", ids(got))
	}
	// The occupant did something since: news again.
	moved := clash("occ", "o/r", t0.Add(time.Minute))
	if got := ledgerApp2(t, a).UnheardClashes([]core.SessionClash{moved}); len(got) != 1 {
		t.Errorf("an occupant active since the last report is news, got %v", ids(got))
	}
	if got := ledgerApp2(t, a).UnheardClashes([]core.SessionClash{moved}); len(got) != 0 {
		t.Errorf("…and then heard, got %v", ids(got))
	}
	// Another repo of the same occupant, and another occupant, are each news.
	more := []core.SessionClash{moved, clash("occ", "o/s", t0), clash("occ2", "o/r", t0)}
	if got := ids(ledgerApp2(t, a).UnheardClashes(more)); len(got) != 2 || got[0] != "occ@o/s" || got[1] != "occ2@o/r" {
		t.Errorf("new pairs are news, the heard one is not: %v", got)
	}
}

// ledgerApp2 is a second process of the same session: same self and
// snapshot, new App (same state dir — the env is still set).
func ledgerApp2(t *testing.T, like *App) *App {
	t.Helper()
	a, _ := newAppWith(at(time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC)))
	a.Self, a.sessionRead, a.sessionOthers = like.Self, true, like.sessionOthers
	return a
}

func TestUnheardClashesIsPerSelfSession(t *testing.T) {
	c := clash("occ", "o/r", time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC))
	a := ledgerApp(t, "self-1", "occ", "self-2")
	a.UnheardClashes([]core.SessionClash{c})
	b := ledgerApp2(t, a)
	b.Self = SessionRef{PID: 2, ID: "self-2"}
	if got := b.UnheardClashes([]core.SessionClash{c}); len(got) != 1 {
		t.Errorf("a different session has not heard it: got %v", ids(got))
	}
}

func TestUnheardClashesUnknownActivityIsReportedOnce(t *testing.T) {
	a := ledgerApp(t, "self", "occ")
	c := clash("occ", "o/r", time.Time{}) // no transcript: activity unknown
	if got := a.UnheardClashes([]core.SessionClash{c}); len(got) != 1 {
		t.Fatalf("first: %v", ids(got))
	}
	if got := ledgerApp2(t, a).UnheardClashes([]core.SessionClash{c}); len(got) != 0 {
		t.Errorf("unknown activity cannot be 'active since': not news, got %v", ids(got))
	}
}

func TestUnheardClashesWithoutSelfIDIsEveryClash(t *testing.T) {
	a := ledgerApp(t, "", "occ")
	c := clash("occ", "o/r", time.Now())
	for i := 0; i < 2; i++ {
		if got := a.UnheardClashes([]core.SessionClash{c}); len(got) != 1 {
			t.Errorf("no self id: nothing to key on, every clash is news (round %d: %v)", i, ids(got))
		}
	}
	if p, _ := warnLedgerPath(); fileExists(p) {
		t.Errorf("no ledger must be written without a self id")
	}
}

func TestUnheardClashesPrunesDeadSessionsAndSurvivesCorruption(t *testing.T) {
	t0 := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	a := ledgerApp(t, "self", "occ", "gone")
	a.UnheardClashes([]core.SessionClash{clash("occ", "o/r", t0), clash("gone", "o/r", t0)})
	path, _ := warnLedgerPath()
	if l := readWarnLedger(path); len(l.Warned) != 2 {
		t.Fatalf("ledger = %+v", l.Warned)
	}
	// "gone" is no longer live: its entry is pruned on the next write.
	b := ledgerApp2(t, a)
	b.sessionOthers = []core.Session{{ID: "self"}, {ID: "occ"}}
	b.UnheardClashes([]core.SessionClash{clash("occ", "o/r", t0)})
	l := readWarnLedger(path)
	if len(l.Warned) != 1 || l.Warned[0].Occupant != "occ" {
		t.Errorf("dead occupant must be pruned: %+v", l.Warned)
	}
	// A corrupt ledger is an empty one: the clash is news, the file is replaced.
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ledgerApp2(t, a).UnheardClashes([]core.SessionClash{clash("occ", "o/r", t0)}); len(got) != 1 {
		t.Errorf("corrupt ledger starts over: got %v", ids(got))
	}
	if l := readWarnLedger(path); len(l.Warned) != 1 {
		t.Errorf("ledger rewritten after corruption: %+v", l.Warned)
	}
	// An unwritable state dir queues one stand-down note; the clash is still news.
	t.Setenv(EnvStateHome, filepath.Join(t.TempDir(), "file-not-dir"))
	if err := os.WriteFile(os.Getenv(EnvStateHome), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := ledgerApp2(t, a)
	if got := c.UnheardClashes([]core.SessionClash{clash("occ", "o/r", t0)}); len(got) != 1 {
		t.Errorf("unwritable ledger: still news, got %v", ids(got))
	}
	if n := c.TakeSessionNotes(); len(n) != 1 {
		t.Errorf("want one stand-down note about the ledger, got %q", n)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
