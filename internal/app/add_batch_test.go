package app

import (
	"strings"
	"testing"
)

// AddBatch resolves the batch's own names before the write: a dep may cite a
// key declared on ANY line (order-free), a [[key]] in title/body/checklist
// becomes [[id]], and the stored tasks carry ids only.
func TestAddBatchResolvesKeysToIDs(t *testing.T) {
	a := newApp()
	existing, err := a.Add("already here", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	created, keys, err := a.AddBatch([]BatchSpec{
		{Key: "serve", AddSpec: AddSpec{Title: "serve dinner", AddOpts: AddOpts{Deps: []string{"cook", existing.ID}, Body: "after [[cook]] and [[" + existing.ID + "]]", Checklist: []string{"plates from [[cook]]"}}}},
		{Key: "cook", AddSpec: AddSpec{Title: "cook — see [[serve]]"}},
		{AddSpec: AddSpec{Title: "no key at all"}},
	})
	if err != nil {
		t.Fatalf("AddBatch: %v", err)
	}
	if len(created) != 3 || len(keys) != 3 || keys[0] != "serve" || keys[1] != "cook" || keys[2] != "" {
		t.Fatalf("created %d, keys %v — want spec order with the keys echoed", len(created), keys)
	}
	serve, cook := created[0], created[1]
	if len(serve.Deps) != 2 || serve.Deps[0] != cook.ID && serve.Deps[1] != cook.ID {
		t.Errorf("serve.Deps = %v, want the key resolved to %s beside %s", serve.Deps, cook.ID, existing.ID)
	}
	if !strings.Contains(cook.Title, "[["+serve.ID+"]]") {
		t.Errorf("a forward [[key]] in a title must become the id: %q", cook.Title)
	}
	body, err := a.Store.LoadBody(serve.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "[["+cook.ID+"]]") || !strings.Contains(body, "[["+existing.ID+"]]") || strings.Contains(body, "[[cook]]") {
		t.Errorf("body must carry ids, never keys: %q", body)
	}
	if got := serve.Checklist[0].Text; got != "plates from [["+cook.ID+"]]" {
		t.Errorf("checklist item = %q, want the key rewritten", got)
	}
	if len(keys) == 0 || serve.ID == cook.ID {
		t.Errorf("ids must be distinct: %s %s", serve.ID, cook.ID)
	}
}

// Every refusal is a validation error that writes nothing.
func TestAddBatchRefusesBadReferences(t *testing.T) {
	a := newApp()
	existing, _ := a.Add("already here", AddOpts{})
	cases := []struct {
		name  string
		specs []BatchSpec
		want  string
	}{
		{"duplicate key", []BatchSpec{{Key: "x", AddSpec: AddSpec{Title: "a"}}, {Key: "x", AddSpec: AddSpec{Title: "b"}}}, "already used"},
		{"key shadows an id", []BatchSpec{{Key: existing.ID, AddSpec: AddSpec{Title: "a"}}}, "existing task id"},
		{"unknown dep", []BatchSpec{{AddSpec: AddSpec{Title: "a", AddOpts: AddOpts{Deps: []string{"nope"}}}}}, "neither an existing task id nor a key"},
		{"self dep", []BatchSpec{{Key: "a", AddSpec: AddSpec{Title: "a", AddOpts: AddOpts{Deps: []string{"a"}}}}}, "depend on itself"},
		{"cycle", []BatchSpec{
			{Key: "a", AddSpec: AddSpec{Title: "a", AddOpts: AddOpts{Deps: []string{"b"}}}},
			{Key: "b", AddSpec: AddSpec{Title: "b", AddOpts: AddOpts{Deps: []string{"c"}}}},
			{Key: "c", AddSpec: AddSpec{Title: "c", AddOpts: AddOpts{Deps: []string{"a"}}}},
		}, "cycle inside the batch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, _ := a.List(QueryOpts{})
			_, _, err := a.AddBatch(c.specs)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one naming %q", err, c.want)
			}
			after, _ := a.List(QueryOpts{})
			if len(after) != len(before) {
				t.Errorf("a refused batch must write nothing: %d -> %d tasks", len(before), len(after))
			}
		})
	}
}
