package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The per-repo single-active invariant, from both sides. This is the rule the
// whole feature rests on — if two boxes can be active for one repo, `next` has no
// answer to "which one" and the scoping means nothing.
//
// It went untested in the first draft of this change, which is exactly how
// `next --containers` shipped with zero coverage. Mutation-testing it (deleting
// the check and watching nothing fail) is what surfaced the gap.
func TestEpicActivateEnforcesOnePerRepo(t *testing.T) {
	a := newApp()
	first := mustEpic(t, a, "first box", EpicAddOpts{Repos: []string{"o/r"}})
	second := mustEpic(t, a, "second box", EpicAddOpts{Repos: []string{"o/r"}})
	elsewhere := mustEpic(t, a, "other repo box", EpicAddOpts{Repos: []string{"o/other"}})

	mustActivate(t, a, first)

	// A second box for the SAME repo is refused, and says which box holds the slot
	// — an agent has to be able to act on the answer without parsing prose.
	_, _, _, err := a.EpicActivate(second, "")
	if err == nil {
		t.Fatal("a second active epic for the same repo must be refused")
	}
	var fe *core.Error
	if !errors.As(err, &fe) || fe.Code != core.CodeValidation {
		t.Fatalf("want a validation error, got %v", err)
	}
	d, _ := fe.Details.(map[string]any)
	held, _ := d["held"].(map[string]any)
	if held["o/r"] != first {
		t.Errorf("details must name the incumbent per repo, got %v", fe.Details)
	}

	// A box for a DIFFERENT repo is unaffected: the limit is per repo, not per
	// board, because reads are repo-scoped by default.
	if _, _, _, err := a.EpicActivate(elsewhere, ""); err != nil {
		t.Errorf("a box for another repo must still be activatable: %v", err)
	}
}

// An epic naming several repos consumes a slot in EVERY one of them: a cross-repo
// box really is "the current core" on both sides, and holding only one slot would
// let a second box open behind it.
func TestEpicActivateConsumesEveryNamedRepo(t *testing.T) {
	a := newApp()
	fleet := mustEpic(t, a, "fleet box", EpicAddOpts{Repos: []string{"o/a", "o/b"}})
	justB := mustEpic(t, a, "b box", EpicAddOpts{Repos: []string{"o/b"}})

	mustActivate(t, a, fleet)
	if _, _, _, err := a.EpicActivate(justB, ""); err == nil {
		t.Error("a multi-repo active box must hold the slot in each of its repos")
	}
}

// A repo-less epic cannot be activated: with no slot to consume it would slip
// past the per-repo count entirely, and you could hold arbitrarily many "current"
// boxes at once.
func TestEpicActivateRefusesARepolessBox(t *testing.T) {
	a := newApp()
	draft := mustEpic(t, a, "draft box", EpicAddOpts{})
	if _, _, _, err := a.EpicActivate(draft, ""); err == nil {
		t.Error("a box naming no repo must not be activatable")
	}
}

// Re-activating the box that is already active is a no-op, not an error: an
// idempotent verb is safe to run from a script that cannot see current state.
func TestEpicActivateIsIdempotent(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "box", EpicAddOpts{Repos: []string{"o/r"}})
	mustActivate(t, a, box)
	if _, _, _, err := a.EpicActivate(box, ""); err != nil {
		t.Errorf("re-activating the active box must be a no-op, got %v", err)
	}
}

// `epic done` clears Active in the SAME write. Without that a closed box keeps
// its repos' slots forever and nothing can ever be opened after it — the board
// wedges, and the only symptom is a confusing refusal on an unrelated command.
func TestEpicDoneClearsActiveAndFreesTheSlot(t *testing.T) {
	a := newApp()
	first := mustEpic(t, a, "first box", EpicAddOpts{Repos: []string{"o/r"}})
	next := mustEpic(t, a, "next box", EpicAddOpts{Repos: []string{"o/r"}})
	mustActivate(t, a, first)

	_, after, err := a.EpicDone(first)
	if err != nil {
		t.Fatal(err)
	}
	if after.Active {
		t.Error("closing a box must clear its active flag in the same write")
	}
	if after.IsOpen() {
		t.Error("closing a box must stamp closed")
	}
	// …and the slot is genuinely free.
	if _, _, _, err := a.EpicActivate(next, ""); err != nil {
		t.Errorf("the closed box must have released its repo's slot: %v", err)
	}
	// furrow does NOT pick the successor itself — that judgement is the human's.
	// Between the close and the human's choice the repo has no active box, which is
	// what epic-no-active exists to nag about.
}

// A closed box is not activatable: reopening is a deliberate act, and silently
// re-opening one from `activate` would lose the record that it was ever finished.
func TestEpicActivateRefusesAClosedBox(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "box", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, err := a.EpicDone(box); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.EpicActivate(box, ""); err == nil {
		t.Error("a closed box must not be activatable")
	}
}

// Activating records the switch in the epic's body. furrow cannot police WHO
// switches — a CLI has no caller identity — so the record is what makes a
// misread instruction visible in the same session instead of weeks later.
func TestEpicActivateRecordsTheSwitch(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "box", EpicAddOpts{Repos: []string{"o/r"}})
	if _, _, _, err := a.EpicActivate(box, "asked for by the human at standup"); err != nil {
		t.Fatal(err)
	}
	body, err := a.Store.LoadBody(box)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(body, "activated") {
		t.Errorf("the switch must be recorded in the body, got:\n%s", body)
	}
	if !containsSubstring(body, "asked for by the human at standup") {
		t.Errorf("the reason must be recorded verbatim, got:\n%s", body)
	}
}

func containsSubstring(hay, needle string) bool { return strings.Contains(hay, needle) }

// ResolveEpic accepts the same spellings ResolveRepo does, and fails LOUDLY with
// candidates rather than silently matching nothing — `-e` has to be as typo-safe
// as `-r` or a mis-typed box files work into the void.
func TestResolveEpicSpellingsAndAmbiguity(t *testing.T) {
	a := newApp()
	travel := mustEpic(t, a, "travel prep", EpicAddOpts{})
	curry := mustEpic(t, a, "make curry", EpicAddOpts{})

	if got, err := a.ResolveEpic(travel); err != nil || got != travel {
		t.Errorf("exact id: got %q, %v", got, err)
	}
	// Ids are random, so a fixed travel[:4] keeps only 2 suffix chars (1024
	// draws) and curry collides with it about once in a thousand runs — extend
	// the prefix until it is unique by construction, never by luck.
	prefix := travel[:4]
	for strings.HasPrefix(curry, prefix) {
		prefix = travel[:len(prefix)+1]
	}
	if got, err := a.ResolveEpic(prefix); err != nil || got != travel {
		t.Errorf("id prefix: got %q, %v", got, err)
	}
	if got, err := a.ResolveEpic("curry"); err != nil || got != curry {
		t.Errorf("title substring: got %q, %v", got, err)
	}
	if got, err := a.ResolveEpic("PREP"); err != nil || got != travel {
		t.Errorf("title match must fold case: got %q, %v", got, err)
	}

	// An ambiguous title names every candidate.
	mustEpic(t, a, "travel booking", EpicAddOpts{})
	_, err := a.ResolveEpic("travel")
	var fe *core.Error
	if !errors.As(err, &fe) || len(fe.Candidates) != 2 {
		t.Errorf("an ambiguous reference must list both candidates, got %v", err)
	}

	// A miss lists the board's boxes, so the caller can pick one.
	_, err = a.ResolveEpic("no-such-box")
	if !errors.As(err, &fe) || len(fe.Candidates) == 0 {
		t.Errorf("an unknown reference must carry candidates, got %v", err)
	}
}

// EpicSet edits metadata and refuses a no-op, so a caller never gets a silent
// `updated` bump it did not ask for.
func TestEpicSetEditsAndRefusesNoOp(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "box", EpicAddOpts{Meta: map[string]string{"place": "here"}})

	goal := "done when the pamphlet is printed"
	_, after, err := a.EpicSet(box, EpicSetOpts{
		Goal:    &goal,
		SetMeta: map[string]string{"span": "aug"},
		RmMeta:  []string{"place"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Goal != goal {
		t.Errorf("goal = %q", after.Goal)
	}
	if after.Meta["span"] != "aug" {
		t.Errorf("meta not set: %v", after.Meta)
	}
	if _, ok := after.Meta["place"]; ok {
		t.Errorf("meta key not removed: %v", after.Meta)
	}
	if _, _, err := a.EpicSet(box, EpicSetOpts{}); err == nil {
		t.Error("an empty set must be refused, not a silent updated bump")
	}
}

// `epic add` never opens the box. Creating one must not change what `next` hands
// out — opening is a separate, deliberate act.
func TestEpicAddNeverActivates(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "box", EpicAddOpts{Repos: []string{"o/r"}})
	e, ok, err := a.Store.LoadEpic(box)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if e.Active {
		t.Error("a freshly created box must not be active")
	}
}

// A bare Add on a board with exactly ONE active epic files the task under it —
// the epic mirror of withBoardRepo's union (t-mzek: plain captures used to land
// unfiled, each one a fresh epic-required lint error the pre-push gate then
// rejects). Zero or two actives, an explicit -e, and an explicit "unfiled"
// (NoEpic, the CLI's `-e ”`) all inherit nothing.
func TestAddInheritsSingleActiveEpic(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "focus box", EpicAddOpts{Repos: []string{"o/r"}})

	// No active epic yet: nothing to inherit.
	t0, err := a.Add("before activate", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if t0.Epic != "" {
		t.Errorf("no active epic, task must stay unfiled, got %q", t0.Epic)
	}

	if _, _, _, err := a.EpicActivate(box, ""); err != nil {
		t.Fatal(err)
	}
	t1, err := a.Add("during focus", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if t1.Epic != box {
		t.Errorf("bare add must inherit the single active epic %s, got %q", box, t1.Epic)
	}

	// Explicit unfiled (-e '') wins over inheritance.
	t2, err := a.Add("deliberately unfiled", AddOpts{NoEpic: true})
	if err != nil {
		t.Fatal(err)
	}
	if t2.Epic != "" {
		t.Errorf("NoEpic must suppress inheritance, got %q", t2.Epic)
	}

	// An explicit -e wins over inheritance.
	other := mustEpic(t, a, "other box", EpicAddOpts{})
	t3, err := a.Add("explicitly filed", AddOpts{Epic: other})
	if err != nil {
		t.Fatal(err)
	}
	if t3.Epic != other {
		t.Errorf("explicit epic must win, got %q", t3.Epic)
	}

	// Two active epics: furrow never guesses between focuses.
	box2 := mustEpic(t, a, "second focus", EpicAddOpts{Repos: []string{"o/r2"}})
	if _, _, _, err := a.EpicActivate(box2, ""); err != nil {
		t.Fatal(err)
	}
	t4, err := a.Add("ambiguous focus", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if t4.Epic != "" {
		t.Errorf("two actives must inherit nothing, got %q", t4.Epic)
	}
}
