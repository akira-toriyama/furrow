package app

import (
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

func reviewDueFires(items []EpicRevisitItem, id string) bool {
	for _, it := range items {
		if it.Epic.ID != id {
			continue
		}
		for _, r := range it.Reasons {
			if r.Code == core.RevisitEpicReviewDue {
				return true
			}
		}
	}
	return false
}

// ReviewEpic stamps `reviewed` without touching `updated` — a review changes
// no content, so staleness must not reset.
func TestReviewEpicStampsReviewedNotUpdated(t *testing.T) {
	a := newApp()
	e, err := a.EpicAdd("mandate", EpicAddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	before, after, err := a.ReviewEpic(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Reviewed != nil {
		t.Errorf("a fresh box must start unreviewed, got %v", before.Reviewed)
	}
	if after.Reviewed == nil || !after.Reviewed.Equal(a.Clock.Now()) {
		t.Errorf("reviewed = %v, want the clock's now", after.Reviewed)
	}
	if !after.Updated.Equal(before.Updated) {
		t.Errorf("a review must not advance updated: %v -> %v", before.Updated, after.Updated)
	}
	// The epic-ref contract applies: a unique title substring resolves too.
	if _, _, err := a.ReviewEpic("mand"); err != nil {
		t.Errorf("unique title substring must resolve: %v", err)
	}
	if _, _, err := a.ReviewEpic("e-nope"); err == nil {
		t.Error("an unknown epic ref must fail")
	}
}

// The cadence: a STANDING box whose last review is past the
// [review].stale_after_days clock raises epic_review_due; a never-reviewed box
// stays quiet (the first review opts it in); a non-standing box never fires it;
// a fresh review silences it.
func TestEpicReviewDueCadence(t *testing.T) {
	a := newApp()
	standing, _ := a.EpicAdd("standing box", EpicAddOpts{})
	plain, _ := a.EpicAdd("ordinary box", EpicAddOpts{})
	yes := true
	if _, _, err := a.EpicSet(standing.ID, EpicSetOpts{Standing: &yes}); err != nil {
		t.Fatal(err)
	}

	old := a.Clock.Now().Add(-time.Duration(a.Cfg.ReviewStaleAfterDays+1) * 24 * time.Hour)
	seedEpicReviewed := func(id string, ts *time.Time) {
		t.Helper()
		e, ok, err := a.Store.LoadEpic(id)
		if err != nil || !ok {
			t.Fatalf("load %s: %v", id, err)
		}
		e.Reviewed = ts
		if err := a.Store.SaveEpic(e); err != nil {
			t.Fatal(err)
		}
	}

	items, err := a.RevisitEpics(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reviewDueFires(items, standing.ID) {
		t.Errorf("a never-reviewed standing box must stay quiet: %+v", items)
	}

	seedEpicReviewed(standing.ID, &old)
	seedEpicReviewed(plain.ID, &old)
	items, err = a.RevisitEpics(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reviewDueFires(items, standing.ID) {
		t.Errorf("a standing box past the review clock must fire: %+v", items)
	}
	if reviewDueFires(items, plain.ID) {
		t.Errorf("a non-standing box must not carry the review cadence: %+v", items)
	}

	sum, err := a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.EpicReviewDue) != 1 || sum.EpicReviewDue[0] != standing.ID {
		t.Errorf("summary.epic_review_due = %v, want [%s]", sum.EpicReviewDue, standing.ID)
	}

	if _, _, err := a.ReviewEpic(standing.ID); err != nil {
		t.Fatal(err)
	}
	items, err = a.RevisitEpics(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reviewDueFires(items, standing.ID) {
		t.Errorf("a just-reviewed box must be quiet again: %+v", items)
	}
}
