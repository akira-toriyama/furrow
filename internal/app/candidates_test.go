package app

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// A closed vocabulary (lanes) — and any almost-resolving reference (an epic) —
// must ALWAYS fail with the candidates in
// Candidates — CLAUDE.md tells agents to branch on that array and never regex
// the prose, so a gate that omits it is a silent contract break, not a cosmetic
// one. Before this test, unknownTypeErr had no coverage at all and AddMany
// rebuilt both messages with Validationf, so `add --stdin -s ghots` exited 2
// with NO candidates while `add -s ghots` had them (the bulk-vs-single
// divergence class of t-adx9 / t-ek9y, this time in the error shape).
func TestClosedVocabularyGatesCarryCandidates(t *testing.T) {
	lanes := []string{"inbox", "backlog", "ready", "in-progress", "waiting", "done", "icebox"}

	// The epic cases need a box to suggest: candidates are the board's epic ids,
	// not a compiled-in vocabulary, so a helper seeds one and reports its id.
	seedEpic := func(a *App) []string { return []string{mustEpic(t, a, "seed box", EpicAddOpts{})} }

	cases := []struct {
		name string
		call func(a *App) error
		want []string
		// wantFn seeds the board and returns the expected candidates, for the cases
		// whose vocabulary is DATA (epic ids) rather than a compiled-in list.
		wantFn func(a *App) []string
		inMsg  string // a substring the message must keep (bulk gates say WHICH spec)
	}{
		{
			name: "add --status",
			call: func(a *App) error { _, err := a.Add("x", AddOpts{Status: "ghots"}); return err },
			want: lanes,
		},
		{
			name:   "add -e",
			call:   func(a *App) error { _, err := a.Add("x", AddOpts{Epic: "no-such-box"}); return err },
			wantFn: seedEpic,
		},
		{
			name: "add --stdin --status",
			call: func(a *App) error {
				_, err := a.AddMany([]AddSpec{{Title: "bulk", AddOpts: AddOpts{Status: "ghots"}}})
				return err
			},
			want:  lanes,
			inMsg: `spec 0 ("bulk")`,
		},
		{
			name: "add --stdin -e",
			call: func(a *App) error {
				_, err := a.AddMany([]AddSpec{{Title: "bulk", AddOpts: AddOpts{Epic: "no-such-box"}}})
				return err
			},
			wantFn: seedEpic,
			inMsg:  `spec 0 ("bulk")`,
		},
		{
			name: "set -e",
			call: func(a *App) error {
				task, err := a.Add("x", AddOpts{})
				if err != nil {
					return err
				}
				bad := "no-such-box"
				_, _, err = a.Set(task.ID, SetOpts{Epic: &bad})
				return err
			},
			wantFn: seedEpic,
		},
		{
			name: "ls -s",
			call: func(a *App) error { _, err := a.List(QueryOpts{Status: "ghots"}); return err },
			want: lanes,
		},
		{
			name: "move",
			call: func(a *App) error {
				task, err := a.Add("x", AddOpts{})
				if err != nil {
					return err
				}
				_, err = a.Move(task.ID, "ghots")
				return err
			},
			want: lanes,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp()
			want := tc.want
			if tc.wantFn != nil {
				want = tc.wantFn(a) // seeds the board AND names the expected candidates
			}
			err := tc.call(a)
			if err == nil {
				t.Fatal("a value outside the closed vocabulary must fail")
			}
			fe := core.AsError(err)
			if fe == nil {
				t.Fatalf("want a *core.Error, got %T: %v", err, err)
			}
			if fe.Code != core.CodeValidation {
				t.Errorf("code = %d, want %d (bad input, do not retry)", fe.Code, core.CodeValidation)
			}
			if !equalStrings(fe.Candidates, want) {
				t.Errorf("candidates = %v, want %v — an agent branches on this array", fe.Candidates, want)
			}
			if tc.inMsg != "" && !strings.Contains(fe.Msg, tc.inMsg) {
				t.Errorf("message %q lost the spec context %q — a bulk failure must say which input", fe.Msg, tc.inMsg)
			}
		})
	}
}

// A prefixed error must be a COPY: prefixing per spec inside a loop may not
// mutate the shared value the next iteration reads.
func TestWithPrefixfCopiesAndKeepsCandidates(t *testing.T) {
	base := &core.Error{Code: core.CodeValidation, ID: "t-1", Msg: "unknown lane", Candidates: []string{"inbox"}}
	got := base.WithPrefixf("spec %d (%q): ", 2, "title")

	if base.Msg != "unknown lane" {
		t.Errorf("WithPrefixf mutated the receiver: %q", base.Msg)
	}
	if got.Msg != `spec 2 ("title"): unknown lane` {
		t.Errorf("Msg = %q", got.Msg)
	}
	if got.Code != base.Code || got.ID != base.ID || !equalStrings(got.Candidates, base.Candidates) {
		t.Errorf("WithPrefixf dropped a field: %+v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
