package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/store/memstore"
)

// proseFailStore is a store whose body writes fail on demand — the IO failure
// a per-id loop turned into a half-annotated batch (t-5n2x).
type proseFailStore struct {
	*memstore.Store
	failBodies bool // every SaveBodies fails
	failBody   bool // every SaveBody fails
	// bodyBudget > 0 lets that many SaveBody calls succeed before the rest
	// fail — the third-of-three failure a per-id loop cannot survive.
	bodyBudget int
	bodyCalls  int
}

func (s *proseFailStore) SaveBodies(bodies map[string]string) error {
	if s.failBodies {
		return errors.New("disk full")
	}
	return s.Store.SaveBodies(bodies)
}

func (s *proseFailStore) SaveBody(id, content string) error {
	s.bodyCalls++
	if s.failBody || (s.bodyBudget > 0 && s.bodyCalls > s.bodyBudget) {
		return errors.New("disk full")
	}
	return s.Store.SaveBody(id, content)
}

func newProseFailApp() (*App, *proseFailStore) {
	cfg := config.Default()
	st := &proseFailStore{Store: memstore.New(cfg.IDPrefix, "e-", cfg.IDWidth)}
	clk := &fixedClock{t: time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)}
	return NewWithStore(st, cfg, clk), st
}

// A `done <id>... --note` whose prose write fails closes nothing AND annotates
// nothing: the notes are composed first and land in one store write, so the
// retry does not duplicate a note that half-landed.
func TestDoneManyNoteFailedProseLeavesEveryBodyAndLaneAlone(t *testing.T) {
	a, st := newProseFailApp()
	var ids []string
	for _, title := range []string{"one", "two", "three"} {
		tk, err := a.Add(title, AddOpts{Repos: []string{"o/r"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
	}
	st.failBodies, st.bodyBudget, st.bodyCalls = true, 1, 0 // one body's write succeeds, the next fails
	if _, err := a.DoneManyNote(ids, "superseded"); err == nil {
		t.Fatal("a failed prose write must fail the close")
	}
	st.failBodies, st.bodyBudget = false, 0
	for _, id := range ids {
		tk, body, err := a.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if tk.Status == a.Cfg.DoneLane {
			t.Errorf("%s closed although the batch failed", id)
		}
		if strings.Contains(body, "superseded") {
			t.Errorf("%s carries the note of a batch that failed: %q", id, body)
		}
	}
	// The retry lands exactly one note per task.
	if _, err := a.DoneManyNote(ids, "superseded"); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		_, body, _ := a.Get(id)
		if n := strings.Count(body, "superseded"); n != 1 {
			t.Errorf("%s carries the note %d times after one successful close", id, n)
		}
	}
}

// An activation whose record cannot be written does not activate: the record
// is the only place PreviousActiveSuggest and sync's publishedSwitches read,
// so a box open without one has lost its "previous" for good.
func TestEpicActivateFailedRecordLeavesTheBoxInactive(t *testing.T) {
	a, st := newProseFailApp()
	box := mustEpic(t, a, "box", EpicAddOpts{Repos: []string{"o/r"}})
	st.failBody = true
	if _, _, _, err := a.EpicActivate(box, "go"); err == nil {
		t.Fatal("a failed activation record must fail the activation")
	}
	st.failBody = false
	e, ok, err := a.Store.LoadEpic(box)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if e.Active {
		t.Error("the box is active although its activation record was never written")
	}
	body, _ := a.Store.LoadBody(box)
	if strings.Contains(body, "activated") {
		t.Errorf("a record landed for a failed activation: %q", body)
	}
	if _, _, _, err := a.EpicActivate(box, "go"); err != nil {
		t.Fatal(err)
	}
	e, _, _ = a.Store.LoadEpic(box)
	body, _ = a.Store.LoadBody(box)
	if !e.Active || strings.Count(body, "activated") != 1 {
		t.Errorf("retry: active=%v records=%d", e.Active, strings.Count(body, "activated"))
	}
}
