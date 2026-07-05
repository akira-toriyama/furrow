package app

import (
	"reflect"
	"testing"
)

// ids returns just the task ids of a GetBatch result, in order.
func itemIDs(items []ShowItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Task.ID)
	}
	return out
}

func TestGetBatchInputOrderAndDedupe(t *testing.T) {
	a := newApp()
	t1, _ := a.Add("first", AddOpts{})
	t2, _ := a.Add("second", AddOpts{})
	t3, _ := a.Add("third", AddOpts{})

	// Output follows input order, not board order; duplicates keep the first
	// occurrence only.
	items, missing, err := a.GetBatch([]string{t3.ID, t1.ID, t3.ID, t2.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := itemIDs(items), []string{t3.ID, t1.ID, t2.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none", missing)
	}
}

func TestGetBatchPartialMiss(t *testing.T) {
	a := newApp()
	t1, _ := a.Add("first", AddOpts{})
	t2, _ := a.Add("second", AddOpts{})

	// A missing id is data, not an error: found tasks come back alongside the
	// misses (input order on both sides, misses deduped too).
	items, missing, err := a.GetBatch([]string{t1.ID, "t-nope1", t2.ID, "t-nope2", "t-nope1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := itemIDs(items), []string{t1.ID, t2.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
	if want := []string{"t-nope1", "t-nope2"}; !reflect.DeepEqual(missing, want) {
		t.Errorf("missing = %v, want %v", missing, want)
	}
}

func TestGetBatchBodyLoading(t *testing.T) {
	a := newApp()
	t1, _ := a.Add("first", AddOpts{Body: "long prose"})

	// withBody=false: metadata only, Body stays empty.
	items, _, err := a.GetBatch([]string{t1.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Body != "" {
		t.Errorf("withBody=false should not load the body, got %q", items[0].Body)
	}

	// withBody=true: the stored body rides along.
	items, _, err = a.GetBatch([]string{t1.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Body != "long prose\n" && items[0].Body != "long prose" {
		t.Errorf("withBody=true should load the body, got %q", items[0].Body)
	}
}

func TestGetBatchEmptyInput(t *testing.T) {
	a := newApp()
	items, missing, err := a.GetBatch(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || len(missing) != 0 {
		t.Errorf("empty input should yield empty results, got %v / %v", items, missing)
	}
}
