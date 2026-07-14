package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

func mkParentTasks(t *testing.T, a *App, titles ...string) []string {
	t.Helper()
	ids := make([]string, len(titles))
	for i, title := range titles {
		task, err := a.Add(title, AddOpts{})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = task.ID
	}
	return ids
}

// The whole point of the command: a parent set at `add` can now be CHANGED.
// Before this, the only way was to hand-edit a machine-written shard.
func TestReparentMovesATaskBetweenParents(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "epic one", "epic two", "a slice")
	epic1, epic2, slice := ids[0], ids[1], ids[2]

	if _, err := a.Reparent(slice, epic1); err != nil {
		t.Fatalf("initial parent: %v", err)
	}
	got, err := a.Reparent(slice, epic2)
	if err != nil {
		t.Fatalf("re-parent: %v", err)
	}
	if got.Parent != epic2 {
		t.Errorf("parent = %q, want %q", got.Parent, epic2)
	}
	// And the old parent no longer claims it.
	res, err := a.ParentList(epic1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Children) != 0 {
		t.Errorf("the old parent must have let go: %+v", res.Children)
	}
}

func TestReparentRmDetachesToTopLevel(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "epic", "slice")
	if _, err := a.Reparent(ids[1], ids[0]); err != nil {
		t.Fatal(err)
	}
	got, err := a.Reparent(ids[1], "")
	if err != nil {
		t.Fatalf("--rm: %v", err)
	}
	if got.Parent != "" {
		t.Errorf("parent = %q, want empty (top-level)", got.Parent)
	}
}

// A cycle in the hierarchy has no root, so every task in it belongs to no tree and
// shows up under nothing. Refuse the edge that would close one — the same
// discipline AddDeps applies to deps.
func TestReparentRefusesCycles(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "a", "b", "c")
	x, y, z := ids[0], ids[1], ids[2]
	if _, err := a.Reparent(y, x); err != nil { // y under x
		t.Fatal(err)
	}
	if _, err := a.Reparent(z, y); err != nil { // z under y under x
		t.Fatal(err)
	}

	cases := []struct {
		name, id, parent string
	}{
		{"self", x, x},
		{"direct loop", x, y},     // x under y, but y is already under x
		{"transitive loop", x, z}, // x under z, but z is a descendant of x
		{"missing parent", x, "t-404"},
	}
	for _, c := range cases {
		_, err := a.Reparent(c.id, c.parent)
		fe := core.AsError(err)
		if fe == nil || fe.Code != core.CodeValidation {
			t.Errorf("%s: want a validation error (exit 2), got %v", c.name, err)
		}
	}
	// …and nothing was written: the refusals are pre-flight, not partial.
	tx, _, err := a.Get(x)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Parent != "" {
		t.Errorf("a refused re-parent must not have changed anything: parent=%q", tx.Parent)
	}
}

// Re-filing a leftover under the epic it actually came from is legitimate, even
// after that epic closed. Refusing it would make the correct record
// unrepresentable — lint's parent-done warn is where the state surfaces instead.
func TestReparentAllowsADoneParentAndLintWarns(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "epic that shipped", "the tail nobody finished")
	epic, tail := ids[0], ids[1]
	if _, err := a.Done(epic); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reparent(tail, epic); err != nil {
		t.Fatalf("a done parent must be allowed: %v", err)
	}

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ps {
		if p.Code == "parent-done" {
			found = true
			if p.Severity != core.SevWarn || p.ID != tail {
				t.Errorf("parent-done must WARN on the open child: %+v", p)
			}
		}
	}
	if !found {
		t.Errorf("an open task under a done parent must surface — that is the state nobody could see: %+v", ps)
	}
	if core.HasErrors(ps) {
		t.Errorf("parent-done is advisory; closing an epic ahead of its tail is untidy, not broken: %+v", ps)
	}
}

// Both directions in one read: what am I under, and what is still under me?
func TestParentListReadsBothDirections(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "epic", "slice one", "slice two")
	epic := ids[0]
	for _, child := range ids[1:] {
		if _, err := a.Reparent(child, epic); err != nil {
			t.Fatal(err)
		}
	}

	res, err := a.ParentList(epic)
	if err != nil {
		t.Fatal(err)
	}
	if res.Parent != nil {
		t.Errorf("the epic is top-level; parent must be nil, got %+v", res.Parent)
	}
	if len(res.Children) != 2 {
		t.Fatalf("want both slices under the epic, got %+v", res.Children)
	}
	if res.Children[0].Title == "" || res.Children[0].Status == "" {
		t.Errorf("children must resolve to id+title+lane, got %+v", res.Children[0])
	}

	child, err := a.ParentList(ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if child.Parent == nil || child.Parent.ID != epic || child.Parent.Title == "" {
		t.Errorf("the child must resolve its parent to id+title+lane, got %+v", child.Parent)
	}
	if len(child.Children) != 0 {
		t.Errorf("a leaf has no children, and the array must be [] not null: %+v", child.Children)
	}

	if _, err := a.ParentList("t-404"); core.AsError(err) == nil || core.AsError(err).Code != core.CodeNotFound {
		t.Errorf("an unknown id is a miss (exit 1), got %v", err)
	}
}

// A parent cycle cannot be made through the app — but two operators CAN commit the
// two half-edges on separate shards and let git merge them. lint is the backstop,
// and every walker must survive the result rather than hang.
func TestLintFlagsParentCycleMergedInBehindUs(t *testing.T) {
	a := newApp()
	ids := mkParentTasks(t, a, "a", "b")
	x, y := ids[0], ids[1]

	// Plant the cycle BELOW the app layer, exactly as a git merge would.
	idx, err := a.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := idx.Find(x)
	ty, _ := idx.Find(y)
	tx.Parent, ty.Parent = y, x
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ps {
		if p.Code == "parent-cycle" {
			found = true
			if p.Severity != core.SevError {
				t.Errorf("a rootless hierarchy is broken, not suspicious: want error, got %q", p.Severity)
			}
		}
	}
	if !found {
		t.Errorf("lint must flag a parent cycle: %+v", ps)
	}
	// The walkers must not hang on it — this is the reason Ancestors carries a
	// visited set at all.
	if got := len(idx.Ancestors(x)); got == 0 {
		t.Errorf("Ancestors must still walk a cyclic chain (and stop), got %d", got)
	}
	if _, err := a.ParentList(x); err != nil {
		t.Errorf("parent --list must survive a cycle: %v", err)
	}
}
