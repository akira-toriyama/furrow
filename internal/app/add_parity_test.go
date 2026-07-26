package app

import (
	"strings"
	"testing"
)

// `add` and `add --stdin` are the same command with a different input shape, so
// every field the flags carry must land identically. Each row runs the SAME
// AddOpts through both paths and compares the stored task — the divergence class
// that produced t-adx9 (--value/--effort dropped), t-ek9y (--type dropped) and,
// found by this test, --check dropped plus an unfolded title. A new AddOpts field
// belongs here, not in a single-path test.
func TestAddSingleAndBulkStoreTheSameFields(t *testing.T) {
	cases := []struct {
		name  string
		title string
		opts  AddOpts
	}{
		{
			name:  "checklist seeds",
			title: "with checks",
			opts:  AddOpts{Checklist: []string{"step one", "  ", "step two"}},
		},
		{
			name:  "estimates and type",
			title: "estimated epic",
			opts:  AddOpts{Value: intp(4), Effort: intp(2), Type: "epic"},
		},
		{
			name:  "labels, refs, lane",
			title: "tagged",
			opts:  AddOpts{Status: "ready", Labels: []string{"cli", "dx"}, Refs: []string{"https://example.test/1"}},
		},
		{
			// A control character must be folded on BOTH paths: the title is
			// spliced into the body's "# " heading and printed by ls, so the bulk
			// path storing it raw was the body-injection vector NormalizeTitle
			// closes for single add.
			name:  "control characters in the title",
			title: "ctrl\x1b[31m\tred\u2028title",
			opts:  AddOpts{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			single, err := newApp().Add(tc.title, tc.opts)
			if err != nil {
				t.Fatalf("single add: %v", err)
			}
			bulk, err := newApp().AddMany([]AddSpec{{Title: tc.title, AddOpts: tc.opts}})
			if err != nil {
				t.Fatalf("bulk add: %v", err)
			}
			if len(bulk) != 1 {
				t.Fatalf("bulk add created %d tasks, want 1", len(bulk))
			}
			b := bulk[0]

			if b.Title != single.Title {
				t.Errorf("title: bulk %q != single %q", b.Title, single.Title)
			}
			if len(b.Checklist) != len(single.Checklist) {
				t.Fatalf("checklist: bulk %v != single %v", b.Checklist, single.Checklist)
			}
			for i := range single.Checklist {
				if b.Checklist[i] != single.Checklist[i] {
					t.Errorf("checklist[%d]: bulk %+v != single %+v", i, b.Checklist[i], single.Checklist[i])
				}
			}
			if b.Type != single.Type {
				t.Errorf("type: bulk %q != single %q", b.Type, single.Type)
			}
			if !samePtr(b.Value, single.Value) {
				t.Errorf("value: bulk %v != single %v", b.Value, single.Value)
			}
			if !samePtr(b.Effort, single.Effort) {
				t.Errorf("effort: bulk %v != single %v", b.Effort, single.Effort)
			}
			if b.Status != single.Status {
				t.Errorf("status: bulk %q != single %q", b.Status, single.Status)
			}
			if !equalStrings(b.Labels, single.Labels) {
				t.Errorf("labels: bulk %v != single %v", b.Labels, single.Labels)
			}
			if !equalStrings(b.Refs, single.Refs) {
				t.Errorf("refs: bulk %v != single %v", b.Refs, single.Refs)
			}
		})
	}
}

// A folded title must reach BOTH the shard and the body heading the bulk path
// seeds from it — storing the raw string is what let an escape sequence into
// `ls` output and a fabricated line into the markdown.
func TestAddManyFoldsTitleIntoShardAndBody(t *testing.T) {
	a := newApp()
	created, err := a.AddMany([]AddSpec{{Title: "esc\x1b[31m and \u2028break"}})
	if err != nil {
		t.Fatal(err)
	}
	got := created[0]
	if want := "esc [31m and break"; got.Title != want {
		t.Errorf("stored title = %q, want %q", got.Title, want)
	}
	body, err := a.Store.LoadBody(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(body, "\x1b\u2028") {
		t.Errorf("seeded body kept a control character: %q", body)
	}
	if !strings.HasPrefix(body, "# "+got.Title) {
		t.Errorf("body heading = %q, want it seeded from the folded title %q", body, got.Title)
	}
}

// A title that is ONLY control characters folds to empty, so it must be
// rejected as an empty title rather than stored as a blank-looking task.
func TestAddManyRejectsControlOnlyTitle(t *testing.T) {
	if _, err := newApp().AddMany([]AddSpec{{Title: "\x1b\t\r"}}); err == nil {
		t.Error("a title that folds to empty must be rejected, like single add")
	}
}

func samePtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
