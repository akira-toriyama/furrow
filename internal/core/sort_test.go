package core

import (
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"
)

func ptr(n int) *int { return &n }

func day(d int) time.Time { return time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC) }

func idsOf(tasks []Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func TestSortTasksByUpdated(t *testing.T) {
	tasks := []Task{
		{ID: "t-a", Updated: day(3)},
		{ID: "t-b", Updated: day(9)},
		{ID: "t-c", Updated: day(6)},
	}
	SortTasks(tasks, "updated", false) // newest first
	if got, want := idsOf(tasks), []string{"t-b", "t-c", "t-a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("updated desc = %v, want %v", got, want)
	}
	SortTasks(tasks, "updated", true) // oldest first
	if got, want := idsOf(tasks), []string{"t-a", "t-c", "t-b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("updated asc = %v, want %v", got, want)
	}
}

func TestSortTasksByValueUnsetLast(t *testing.T) {
	tasks := []Task{
		{ID: "t-a", Value: ptr(2)},
		{ID: "t-b"}, // unset
		{ID: "t-c", Value: ptr(5)},
		{ID: "t-d"}, // unset
	}
	SortTasks(tasks, "value", false) // highest first, unset last
	if got, want := idsOf(tasks), []string{"t-c", "t-a", "t-b", "t-d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("value desc = %v, want %v (unset last, tie by id)", got, want)
	}
	SortTasks(tasks, "value", true) // lowest first, but unset STILL last
	if got, want := idsOf(tasks), []string{"t-a", "t-c", "t-b", "t-d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("value asc = %v, want %v (unset still last, tie by id)", got, want)
	}
}

func TestSortTasksTieBreakByID(t *testing.T) {
	tasks := []Task{
		{ID: "t-z", Updated: day(5)},
		{ID: "t-a", Updated: day(5)},
		{ID: "t-m", Updated: day(5)},
	}
	SortTasks(tasks, "updated", false)
	if got, want := idsOf(tasks), []string{"t-a", "t-m", "t-z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("equal timestamps should tie-break by id asc, got %v want %v", got, want)
	}
}

func TestSortTasksByEffortAndCreated(t *testing.T) {
	tasks := []Task{
		{ID: "t-a", Effort: ptr(3), Created: day(2)},
		{ID: "t-b", Effort: ptr(1), Created: day(8)},
	}
	SortTasks(tasks, "effort", false)
	if got := idsOf(tasks); got[0] != "t-a" { // highest effort first
		t.Errorf("effort desc should put t-a first, got %v", got)
	}
	SortTasks(tasks, "created", false)
	if got := idsOf(tasks); got[0] != "t-b" { // newest created first
		t.Errorf("created desc should put t-b first, got %v", got)
	}
}

func TestIsSortField(t *testing.T) {
	for _, f := range SortFields {
		if !IsSortField(f) {
			t.Errorf("%q should be a valid sort field", f)
		}
	}
	if IsSortField("priority") || IsSortField("") {
		t.Errorf("unlisted fields should be invalid")
	}
}

// TaskOrder is the tiebreak App.Due leans on once its primary key (the due
// instant) ties, so its TOTALITY is the contract: if two DISTINCT tasks could
// compare 0, the caller would be back to depending on a sort's stability, which
// is exactly the defect it exists to remove. Its second contract is that it
// stays Canonicalize's order — the two are one definition, and a drift would
// quietly reshuffle every band that tiebreaks with it.
func TestTaskOrderIsTotalAndReproducesCanonicalize(t *testing.T) {
	lanes := []string{"inbox", "ready", "done"}
	tasks := []Task{
		{ID: "t-c", Status: "ready", Priority: 20},
		{ID: "t-a", Status: "ready", Priority: 20}, // ties t-c on lane AND priority
		{ID: "t-b", Status: "inbox", Priority: 90},
		{ID: "t-d", Status: "retired", Priority: 10}, // unknown lane sorts last
		{ID: "t-e", Status: "ready", Priority: 10},
	}
	order := TaskOrder(lanes)
	for i := range tasks {
		for j := range tasks {
			c := order(&tasks[i], &tasks[j])
			switch {
			case i == j && c != 0:
				t.Errorf("%s vs itself = %d, want 0", tasks[i].ID, c)
			case i != j && c == 0:
				t.Errorf("distinct tasks %s and %s compare equal — the order is not total, so a caller tie-breaking with it still depends on sort stability",
					tasks[i].ID, tasks[j].ID)
			case i != j && c != -order(&tasks[j], &tasks[i]):
				t.Errorf("%s vs %s = %d but the reverse = %d — not antisymmetric",
					tasks[i].ID, tasks[j].ID, c, order(&tasks[j], &tasks[i]))
			}
		}
	}

	byOrder := slices.Clone(tasks)
	sort.SliceStable(byOrder, func(a, b int) bool { return order(&byOrder[a], &byOrder[b]) < 0 })
	idx := &Index{Tasks: slices.Clone(tasks)}
	Canonicalize(idx, lanes)
	if got, want := idsOf(byOrder), idsOf(idx.Tasks); !reflect.DeepEqual(got, want) {
		t.Errorf("TaskOrder = %v but Canonicalize = %v — the tiebreak no longer reproduces the index order it stands in for", got, want)
	}
}
