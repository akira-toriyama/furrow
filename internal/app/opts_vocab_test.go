package app

import (
	"reflect"
	"strings"
	"testing"
)

// The options tables (SetOpts.requested / EpicSetOpts.requested) are the ONE
// source both empty() and the no-op refusal read, so a field added to the
// struct without a table entry is caught here rather than by an operator
// reading a message that forgot their flag (t-8sgn: --repeat/--clear-repeat
// and --standing/--pinned were both missing from the prose).
func TestSetOptsVocabularyCoversEveryField(t *testing.T) {
	for name, tc := range map[string]struct {
		fields int
		flags  []optFlag
	}{
		"SetOpts":     {reflect.TypeOf(SetOpts{}).NumField(), SetOpts{}.requested()},
		"EpicSetOpts": {reflect.TypeOf(EpicSetOpts{}).NumField(), EpicSetOpts{}.requested()},
	} {
		if len(tc.flags) != tc.fields {
			t.Errorf("%s: %d fields but %d entries in requested() — add the new field's flag to the table", name, tc.fields, len(tc.flags))
		}
		seen := map[string]bool{}
		for _, f := range tc.flags {
			if f.set {
				t.Errorf("%s: zero options report %s as requested", name, f.name)
			}
			if seen[f.name] {
				t.Errorf("%s: flag %s listed twice", name, f.name)
			}
			seen[f.name] = true
		}
	}
}

func TestSetRefusalNamesEveryFlag(t *testing.T) {
	a := newApp()
	tk, err := a.Add("t", AddOpts{Repos: []string{"o/r"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a.Set(tk.ID, SetOpts{})
	if err == nil {
		t.Fatal("an empty set must be refused")
	}
	for _, f := range (SetOpts{}).requested() {
		if !strings.Contains(err.Error(), f.name+" ") && !strings.Contains(err.Error(), f.name+")") {
			t.Errorf("set's refusal does not name %s: %v", f.name, err)
		}
	}

	box := mustEpic(t, a, "box", EpicAddOpts{})
	_, _, err = a.EpicSet(box, EpicSetOpts{})
	if err == nil {
		t.Fatal("an empty epic set must be refused")
	}
	for _, f := range (EpicSetOpts{}).requested() {
		if !strings.Contains(err.Error(), f.name+" ") && !strings.Contains(err.Error(), f.name+")") {
			t.Errorf("epic set's refusal does not name %s: %v", f.name, err)
		}
	}
}
