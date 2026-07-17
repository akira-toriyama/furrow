package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The t-44h4 repro: the task-status bot appends a marker line to a body while
// a local session appends a note to the SAME body — two EOF-adjacent appends,
// textually conflicting on every pull --rebase. The union merge attribute
// (`bodies/*.md merge=union`, scaffolded by furrow init) must let sync fold
// both sides together instead of aborting — the whole point is that an
// append-mostly prose file has no meaningful textual conflict.
func TestSyncUnionMergesConcurrentBodyAppends(t *testing.T) {
	_, cloneA, cloneB := setupClones(t)

	a := openBoard(t, cloneA)
	shared, err := a.Add("shared", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{}); err != nil {
		t.Fatal(err)
	}

	// A appends the bot's marker line and pushes; B appends a closing note,
	// then syncs into the collision.
	if _, err := openBoard(t, cloneA).AddNote(shared.ID, "- 🔗 `furrow#153` merged"); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneA).Sync(context.Background(), SyncOpts{Bodies: []string{shared.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := openBoard(t, cloneB).AddNote(shared.ID, "Shipped in furrow#153: the closing word."); err != nil {
		t.Fatal(err)
	}

	p, err := openBoard(t, cloneB).Sync(context.Background(), SyncOpts{Bodies: []string{shared.ID}})
	if err != nil {
		t.Fatalf("concurrent body appends must union-merge, not conflict: %v", err)
	}
	if !p.Pushed || p.Conflict {
		t.Errorf("progress = %+v; want pushed=true conflict=false", p)
	}

	// Both paragraphs survive, in one clean body (no conflict markers).
	body, err := os.ReadFile(filepath.Join(cloneB, ".furrow", "bodies", shared.ID+".md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- 🔗 `furrow#153` merged", "Shipped in furrow#153: the closing word."} {
		if !strings.Contains(string(body), want) {
			t.Errorf("union-merged body must keep %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "<<<<<<<") {
		t.Errorf("union merge must not leave conflict markers:\n%s", body)
	}
}

// Init scaffolds the union attribute so every NEW board gets conflict-free
// body appends from day one (an existing board adds the same line by hand —
// furrow doctor nags until it does).
func TestInitScaffoldsBodyUnionMergeAttributes(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, DirName, ".gitattributes"))
	if err != nil {
		t.Fatalf("init must scaffold .furrow/.gitattributes: %v", err)
	}
	for _, want := range []string{"bodies/*.md merge=union", "archive/bodies/*.md merge=union"} {
		if !strings.Contains(string(b), want) {
			t.Errorf(".gitattributes must carry %q, got:\n%s", want, b)
		}
	}
}

// A git-backed board that predates the scaffold lacks the union rule — doctor
// names it (warn) with the one-line fix; the scaffolded default stays silent.
func TestDoctorWarnsWhenBodyUnionMergeRuleIsMissing(t *testing.T) {
	_, _, cloneB := setupClones(t)
	boardB := filepath.Join(cloneB, DirName)
	writeGlobalConfig(t, boardEntry(boardB, "auto", filepath.Dir(cloneB)))

	r := mustDoctor(t, "")
	if len(findProblems(r, "no-body-union-merge")) != 0 {
		t.Errorf("a scaffolded board must not warn: %+v", r.Problems)
	}

	if err := os.Remove(filepath.Join(boardB, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	r = mustDoctor(t, "")
	n := findProblems(r, "no-body-union-merge")
	if len(n) != 1 || r.Problems[n[0]].Severity != "warn" {
		t.Fatalf("want one no-body-union-merge warn, got %+v", r.Problems)
	}
	if !strings.Contains(r.Problems[n[0]].Msg, "merge=union") {
		t.Errorf("the fix line must be in the message: %+v", r.Problems[n[0]])
	}
}
