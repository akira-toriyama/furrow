package app

import (
	"testing"
)

// groupIDs names the groups a Tree call produced, in order, using "(none)" for
// the trailing unfiled group — so a test can assert the ORDER, which is the one
// property a map-backed implementation would get wrong intermittently.
func groupIDs(gs []TreeGroup) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		if g.Epic == nil {
			out = append(out, "(none)")
			continue
		}
		out = append(out, g.Epic.ID)
	}
	return out
}

func taskTitles(g TreeGroup) []string {
	out := make([]string, 0, len(g.Tasks))
	for _, n := range g.Tasks {
		out = append(out, n.Task.Title)
	}
	return out
}

func findGroup(gs []TreeGroup, id string) *TreeGroup {
	for i := range gs {
		if gs[i].Epic != nil && gs[i].Epic.ID == id {
			return &gs[i]
		}
	}
	return nil
}

// The tree groups tasks by epic and orders the groups: active box first, then the
// other open boxes by id, then closed ones, then the unfiled tasks. That order is
// the reader's attention order, and it must be total — a tie would make two runs
// print differently.
func TestTreeGroupsAndOrder(t *testing.T) {
	a := newApp()
	active := mustEpic(t, a, "active box", EpicAddOpts{Repos: []string{"o/r"}})
	other := mustEpic(t, a, "other box", EpicAddOpts{})
	closed := mustEpic(t, a, "closed box", EpicAddOpts{})
	mustActivate(t, a, active)
	if _, _, err := a.EpicDone(closed); err != nil {
		t.Fatal(err)
	}

	mustAdd(t, a, "in active", AddOpts{Epic: active})
	mustAdd(t, a, "in other", AddOpts{Epic: other})
	mustAdd(t, a, "in closed", AddOpts{Epic: closed})
	mustAdd(t, a, "unfiled", AddOpts{NoEpic: true}) // meant unfiled; inheritance would file it under the active box

	groups, err := a.Tree(QueryOpts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := groupIDs(groups)
	want := []string{active, other, closed, "(none)"}
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("group %d = %s, want %s (active, open, closed, unfiled)", i, got[i], want[i])
		}
	}
	if g := findGroup(groups, active); g == nil || !g.Active {
		t.Error("the active box's group must report Active")
	}
}

// An unfiled task is drawn in a trailing "(none)" group, never dropped. A tree
// that showed fewer tasks than the same flags without --tree would be worse than
// one with an extra group, and unfiled tasks are a lint error the reader has to
// be able to SEE in order to fix.
func TestTreeKeepsUnfiledTasks(t *testing.T) {
	a := newApp()
	mustAdd(t, a, "orphan", AddOpts{})

	groups, err := a.Tree(QueryOpts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Epic != nil {
		t.Fatalf("want a single unfiled group, got %v", groupIDs(groups))
	}
	if titles := taskTitles(groups[0]); len(titles) != 1 || titles[0] != "orphan" {
		t.Errorf("unfiled group = %v, want [orphan]", titles)
	}
	if groups[0].Progress != nil {
		t.Error("the unfiled group must carry no progress — 'not in a box' is not progress toward anything")
	}
}

// Progress is computed over the FULL index, never the filtered set: a read filter
// that hides some members must not make a box look finished. This is the
// regression that matters most about the roll-up, and it is easy to reintroduce
// by counting the rows you are about to draw.
func TestTreeProgressIgnoresTheReadFilter(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "box", EpicAddOpts{})
	mustAdd(t, a, "m1", AddOpts{Epic: box, Status: "ready"})
	m2 := mustAdd(t, a, "m2", AddOpts{Epic: box, Status: "ready"})
	mustAdd(t, a, "m3", AddOpts{Epic: box, Status: "ready"})
	if _, err := a.Done(m2.ID); err != nil {
		t.Fatal(err)
	}

	// -s ready hides the done member from the DRAWING…
	groups, err := a.Tree(QueryOpts{Status: "ready"}, "")
	if err != nil {
		t.Fatal(err)
	}
	g := findGroup(groups, box)
	if g == nil {
		t.Fatal("box group missing")
	}
	if len(g.Tasks) != 2 {
		t.Errorf("drawn tasks = %d, want 2 (the filter applies to the drawing)", len(g.Tasks))
	}
	// …but the roll-up still counts all three.
	if g.Progress == nil || g.Progress.Done != 1 || g.Progress.Total != 3 {
		t.Errorf("progress = %+v, want {Done:1 Total:3} over the FULL index", g.Progress)
	}
}

// A box with open members but not one actionable is stuck — the state `next`
// structurally cannot show (it would just return empty, which reads as "nothing
// to do" rather than "everything here is blocked").
func TestTreeStuck(t *testing.T) {
	a := newApp()
	stuck := mustEpic(t, a, "stuck box", EpicAddOpts{})
	gate := mustAdd(t, a, "gate", AddOpts{Status: "backlog"}) // backlog is not a next lane
	mustAdd(t, a, "blocked member", AddOpts{Epic: stuck, Status: "ready", Deps: []string{gate.ID}})

	live := mustEpic(t, a, "live box", EpicAddOpts{})
	mustAdd(t, a, "ready member", AddOpts{Epic: live, Status: "ready"})

	empty := mustEpic(t, a, "empty box", EpicAddOpts{})

	groups, err := a.Tree(QueryOpts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if g := findGroup(groups, stuck); g == nil || !g.Stuck {
		t.Error("a box whose only open member is blocked must be stuck")
	}
	if g := findGroup(groups, live); g == nil || g.Stuck {
		t.Error("a box with an actionable member must NOT be stuck")
	}
	if g := findGroup(groups, empty); g == nil || g.Stuck {
		t.Error("an empty box must NOT be stuck — declaring it before filling it is legitimate")
	}
}

// ls --tree <epic> narrows to one group, and drops the unfiled group: the reader
// asked about one box, not about the backlog of unfiled work.
func TestTreeSingleEpic(t *testing.T) {
	a := newApp()
	box := mustEpic(t, a, "the box", EpicAddOpts{})
	other := mustEpic(t, a, "another box", EpicAddOpts{})
	mustAdd(t, a, "member", AddOpts{Epic: box})
	mustAdd(t, a, "elsewhere", AddOpts{Epic: other})
	mustAdd(t, a, "unfiled", AddOpts{})

	groups, err := a.Tree(QueryOpts{}, box)
	if err != nil {
		t.Fatal(err)
	}
	if got := groupIDs(groups); len(got) != 1 || got[0] != box {
		t.Fatalf("groups = %v, want just %s", got, box)
	}

	// The reference resolves the same ways -e does: a title substring works too.
	byTitle, err := a.Tree(QueryOpts{}, "the box")
	if err != nil {
		t.Fatal(err)
	}
	if got := groupIDs(byTitle); len(got) != 1 || got[0] != box {
		t.Errorf("title-substring reference = %v, want just %s", got, box)
	}

	// An unknown reference is an error, never an empty tree that reads like "this
	// box has nothing in it".
	if _, err := a.Tree(QueryOpts{}, "no-such-box"); err == nil {
		t.Error("an unknown epic reference must fail, not draw an empty tree")
	}
}

// -n caps the number of GROUPS, never the tasks: a limit that truncated
// mid-group would silently amputate members from a box it did show.
func TestTreeLimitCapsGroups(t *testing.T) {
	a := newApp()
	for _, name := range []string{"box a", "box b", "box c"} {
		id := mustEpic(t, a, name, EpicAddOpts{})
		mustAdd(t, a, name+" m1", AddOpts{Epic: id})
		mustAdd(t, a, name+" m2", AddOpts{Epic: id})
	}
	groups, err := a.Tree(QueryOpts{Limit: 2}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	for _, g := range groups {
		if len(g.Tasks) != 2 {
			t.Errorf("group %s drew %d tasks, want both members (the limit caps groups, not tasks)", g.Epic.ID, len(g.Tasks))
		}
	}
}

// ★ is the TASK-level predicate and is deliberately NOT epic-scoped, so it stays
// a strict superset of what `next` hands you. A glyph whose meaning shifted with
// whichever box happens to be open could not be read at a glance.
func TestTreeStarIsNotEpicScoped(t *testing.T) {
	a := newApp()
	active := mustEpic(t, a, "active box", EpicAddOpts{Repos: []string{"o/r"}})
	other := mustEpic(t, a, "other box", EpicAddOpts{})
	mustActivate(t, a, active)
	mustAdd(t, a, "in active", AddOpts{Epic: active, Status: "ready"})
	mustAdd(t, a, "in other", AddOpts{Epic: other, Status: "ready"})

	groups, err := a.Tree(QueryOpts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	g := findGroup(groups, other)
	if g == nil || len(g.Tasks) != 1 {
		t.Fatal("other box's member missing")
	}
	if !g.Tasks[0].Actionable {
		t.Error("a ready task outside the active box must still be ★ — ls is the board-wide view")
	}
}
