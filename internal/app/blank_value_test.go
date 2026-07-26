package app

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// ONE rule for every mutating flag that takes a value list: a blank entry is
// exit 2, never a stored empty string and never a silent drop. Before t-3gy1
// the rule was applied per command and had drifted three ways — `check --add ""`
// was exit 2, `add --check ""` was silently dropped, and `set --add-label ""`
// wrote `"labels": [""]` into the shard at exit 0. A blank sorts first, so it
// also led every rendered label list. Every row below writes to a DIFFERENT
// funnel; a new list-valued flag belongs here.
func TestBlankValuesAreRejectedEverywhere(t *testing.T) {
	cases := []struct {
		name string
		call func(a *App, id string) error
	}{
		{"add -l", func(a *App, _ string) error { _, err := a.Add("x", AddOpts{Labels: []string{"bug", ""}}); return err }},
		{"add --ref", func(a *App, _ string) error { _, err := a.Add("x", AddOpts{Refs: []string{" "}}); return err }},
		{"add --check", func(a *App, _ string) error { _, err := a.Add("x", AddOpts{Checklist: []string{""}}); return err }},
		{"add --dep", func(a *App, _ string) error { _, err := a.Add("x", AddOpts{Deps: []string{""}}); return err }},
		{"add -r", func(a *App, _ string) error { _, err := a.Add("x", AddOpts{Repos: []string{""}}); return err }},
		{"add --stdin -l", func(a *App, _ string) error {
			_, err := a.AddMany([]AddSpec{{Title: "bulk", AddOpts: AddOpts{Labels: []string{""}}}})
			return err
		}},
		{"label --add", func(a *App, id string) error { _, err := a.Relabel(id, []string{"bug", ""}, nil); return err }},
		{"label --remove", func(a *App, id string) error { _, err := a.Relabel(id, nil, []string{""}); return err }},
		{"ref --add", func(a *App, id string) error { _, err := a.Reref(id, []string{""}, nil); return err }},
		{"ref --rm", func(a *App, id string) error { _, err := a.Reref(id, nil, []string{"  "}); return err }},
		{"repo --add", func(a *App, id string) error { _, err := a.Rerepo(id, []string{""}, nil); return err }},
		{"repo --rm", func(a *App, id string) error { _, err := a.Rerepo(id, nil, []string{""}); return err }},
		{"dep --add", func(a *App, id string) error { _, err := a.AddDeps(id, []string{""}); return err }},
		{"dep --rm", func(a *App, id string) error { _, err := a.RemoveDeps(id, []string{""}); return err }},
		{"set --add-label", func(a *App, id string) error {
			_, _, err := a.Set(id, SetOpts{AddLabels: []string{""}})
			return err
		}},
		{"set --rm-label", func(a *App, id string) error {
			_, _, err := a.Set(id, SetOpts{RmLabels: []string{" "}})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp()
			seed, err := a.Add("seed", AddOpts{Labels: []string{"keep"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.call(a, seed.ID); core.ExitCode(err) != int(core.CodeValidation) {
				t.Fatalf("a blank value must be exit 2, got %v", err)
			}
			// The rejection must be total: nothing may have been written.
			got, _, err := a.Get(seed.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range got.Labels {
				if l == "" {
					t.Errorf("a blank label reached the shard: %+v", got.Labels)
				}
			}
			for _, r := range got.Refs {
				if r == "" {
					t.Errorf("a blank ref reached the shard: %+v", got.Refs)
				}
			}
		})
	}
}

// lint is the backstop for shards that already carry a blank entry — written by
// an older binary before the rule above, or by hand. Refusing at the door does
// nothing for what is already on disk.
func TestLintFlagsBlankListEntry(t *testing.T) {
	a := newApp()
	idx, _ := a.Store.Load()
	idx.Add(core.Task{
		ID: "t-blank1", Title: "carries blanks", Status: "inbox", Priority: 100,
		Labels: []string{"", "bug"}, Refs: []string{""}, Body: core.BodyPath("t-blank1"),
	})
	if err := a.Store.Save(idx); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.SaveBody("t-blank1", "# carries blanks\n"); err != nil {
		t.Fatal(err)
	}

	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	var got []core.Problem
	for _, p := range ps {
		if p.Code == "blank-entry" && p.ID == "t-blank1" {
			got = append(got, p)
		}
	}
	if len(got) == 0 {
		t.Fatalf("lint must flag a blank list entry, got %+v", ps)
	}
	for _, p := range got {
		if p.Severity != core.SevWarn {
			t.Errorf("blank-entry must be a warning (it is fixable, not corrupting), got %q", p.Severity)
		}
	}
}
