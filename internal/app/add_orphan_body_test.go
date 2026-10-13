package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/config"
	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// A batch add whose later spec is refused writes NOTHING — not the earlier
// specs' bodies, which a per-spec saveBody used to leave behind as orphans the
// next sync published (t-t2fv). The refusal names the spec.
func TestAddManyRefusedSpecWritesNoBody(t *testing.T) {
	cfg := config.Default()
	st := newStore(cfg)
	a := NewWithStore(st, cfg, &fixedClock{t: defaultNow})
	_, err := a.AddMany([]AddSpec{
		{Title: "one"},
		{Title: "two", AddOpts: AddOpts{Repeat: "daily"}}, // a repeat needs a due
	})
	if err == nil {
		t.Fatal("a repeat with no due must be refused")
	}
	if !strings.Contains(err.Error(), "spec 1") {
		t.Fatalf("the refusal must name the spec: %v", err)
	}
	ids, _ := st.ListBodyIDs()
	if len(ids) != 0 {
		t.Fatalf("bodies written by a refused batch: %v", ids)
	}
	idx, _ := st.Load()
	if len(idx.Tasks) != 0 {
		t.Fatalf("tasks written by a refused batch: %d", len(idx.Tasks))
	}
}

// refusingIndex is a store whose index save fails after the bodies landed —
// the duplicate-id and marshal refusals fsstore raises at Save time.
type refusingIndex struct{ Store }

func (refusingIndex) Save(*core.Index) error { return errors.New("index refused") }

func TestAddTakesBodiesBackWhenTheIndexSaveIsRefused(t *testing.T) {
	cfg := config.Default()
	st := newStore(cfg)
	a := NewWithStore(refusingIndex{st}, cfg, &fixedClock{t: defaultNow})
	if _, err := a.Add("x", AddOpts{}); err == nil {
		t.Fatal("the refused index save must surface")
	}
	ids, _ := st.ListBodyIDs()
	if len(ids) != 0 {
		t.Fatalf("a refused add left bodies behind: %v", ids)
	}
}
