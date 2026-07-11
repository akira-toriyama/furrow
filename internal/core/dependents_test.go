package core

import (
	"reflect"
	"testing"
)

func TestDependents(t *testing.T) {
	idx := &Index{Tasks: []Task{
		{ID: "t-a", Deps: []string{"t-c"}},
		{ID: "t-b", Deps: []string{"t-c", "t-a"}},
		{ID: "t-c"},
		{ID: "t-d", Deps: []string{"t-e"}}, // depends on an unrelated id
	}}

	// Tasks naming t-c in their Deps, in index (canonical) order.
	got := idFrom(idx.Dependents("t-c"))
	if want := []string{"t-a", "t-b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dependents(t-c) = %v, want %v", got, want)
	}

	// t-a is depended on only by t-b.
	if got, want := idFrom(idx.Dependents("t-a")), []string{"t-b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dependents(t-a) = %v, want %v", got, want)
	}

	// A task nobody depends on has no dependents (nil/empty).
	if got := idx.Dependents("t-b"); len(got) != 0 {
		t.Errorf("Dependents(t-b) should be empty, got %v", idFrom(got))
	}

	// An id absent from the index has no dependents (never panics).
	if got := idx.Dependents("t-missing"); len(got) != 0 {
		t.Errorf("Dependents(unknown) should be empty, got %v", idFrom(got))
	}
}

func idFrom(tasks []Task) []string {
	ids := []string{}
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}
