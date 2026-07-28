package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The v5 -> v6 flag day, end to end against real bytes on disk: `type:"epic"`
// tasks become epic shards, `parent` edges become `epic` membership, bodies
// follow their epic, and live [[t-…]] links are rewritten. The fixture is
// written BY HAND, not by this binary — the whole point is that the retired
// keys arrive as extras a v6 binary has no fields for.

// writeV5Shard writes one raw v5 task shard. The retired keys go in exactly
// where v5's marshaller put them (canonical lowercase). deps is raw JSON
// (`[]`, `["t-x"]`).
func writeV5Shard(t *testing.T, dir, id, title, status, deps, extra string) {
	t.Helper()
	body := `{
  "id": "` + id + `",
  "title": "` + title + `",
  "status": "` + status + `",
  "priority": 100,
  "labels": [],
  "repos": [],
  "deps": ` + deps + `,
  "refs": [],
  "checklist": [],
  "created": "2026-06-01T10:00:00Z",
  "updated": "2026-07-01T10:00:00Z",
  "closed": null,
  "reviewed": null,
  "body": "bodies/` + id + `.md"` + extra + `
}
`
	path := filepath.Join(dir, "tasks", id+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeV5Meta(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{\n  \"schema_version\": 5\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeBody(t *testing.T, dir, id, content string) {
	t.Helper()
	path := filepath.Join(dir, "bodies", id+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// v5Board builds a v5 board with one open epic (with a body), one bodyless
// epic, one member, one task waiting ON the epic, one task on its own, and one
// task filed under a plain task (the hand-fix case).
func v5Board(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	fdir := filepath.Join(dir, DirName)

	writeV5Shard(t, fdir, "t-box1", "the box", "backlog", `[]`, `,
  "type": "epic"`)
	writeV5Shard(t, fdir, "t-box3", "bodyless box", "backlog", `[]`, `,
  "type": "epic"`)
	writeV5Shard(t, fdir, "t-kid1", "child", "backlog", `[]`, `,
  "parent": "t-box1"`)
	writeV5Shard(t, fdir, "t-dep1", "waits on the box", "backlog", `["t-box1"]`, ``)
	writeV5Shard(t, fdir, "t-solo", "solo", "backlog", `[]`, ``)
	writeV5Shard(t, fdir, "t-odd", "under a plain task", "backlog", `[]`, `,
  "parent": "t-kid1"`)

	writeBody(t, fdir, "t-box1", "# the box\n\nplan lives here\n")
	writeBody(t, fdir, "t-kid1", "see [[t-box1]] for the plan\n\n```\na doc example: [[t-box1]]\n```\n")
	writeBody(t, fdir, "t-dep1", "# waits\n")
	writeBody(t, fdir, "t-solo", "# solo\n")
	writeBody(t, fdir, "t-odd", "# odd\n")

	writeV5Meta(t, fdir)
	return a, fdir
}

func TestUpgradeV6PreviewPlansTheConversionAndWritesNothing(t *testing.T) {
	a, fdir := v5Board(t)

	rep, err := a.Upgrade(false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Changed || rep.Applied || rep.From != 5 {
		t.Fatalf("preview = %+v, want changed:true applied:false from:5", rep)
	}
	if len(rep.Stores) != 1 {
		t.Fatalf("stores = %+v, want the one hot store", rep.Stores)
	}
	st := rep.Stores[0]
	if len(st.Epics) != 2 || st.Epics[0].TaskID != "t-box1" || st.Epics[0].EpicID != "e-box1" ||
		st.Epics[0].Title != "the box" || st.Epics[0].Closed || st.Epics[1].TaskID != "t-box3" {
		t.Errorf("epics = %+v, want t-box1 -> e-box1 (open) and t-box3 -> e-box3", st.Epics)
	}
	if st.Rehomed != 1 || st.BodiesRenamed != 1 || st.LinksRewritten != 1 {
		t.Errorf("rehomed/bodies/links = %d/%d/%d, want 1/1/1", st.Rehomed, st.BodiesRenamed, st.LinksRewritten)
	}
	if len(st.Kept) != 1 || st.Kept[0].TaskID != "t-odd" || st.Kept[0].Reason != "parent-not-epic" {
		t.Errorf("kept = %+v, want t-odd's parent kept as parent-not-epic", st.Kept)
	}
	if len(st.DroppedDeps) != 1 || st.DroppedDeps[0].TaskID != "t-dep1" || st.DroppedDeps[0].EpicID != "e-box1" {
		t.Errorf("dropped deps = %+v, want t-dep1's dep on t-box1", st.DroppedDeps)
	}
	if st.Tasks != 4 {
		t.Errorf("tasks = %d, want 4 after the conversion (the two boxes became epics)", st.Tasks)
	}

	// Preview writes NOTHING: the epic does not exist, the shard and body are
	// untouched, the board still declares v5.
	if _, err := os.Stat(filepath.Join(fdir, "epics", "e-box1.json")); !os.IsNotExist(err) {
		t.Error("preview must not create the epic shard")
	}
	if _, err := os.Stat(filepath.Join(fdir, "tasks", "t-box1.json")); err != nil {
		t.Error("preview must not remove the converted task's shard")
	}
	if v, _ := a.Store.BoardVersion(); v != 5 {
		t.Errorf("board version = %d, want 5 untouched", v)
	}
}

func TestUpgradeV6AppliesTheConversion(t *testing.T) {
	a, fdir := v5Board(t)

	rep, err := a.Upgrade(true)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied {
		t.Fatalf("apply = %+v, want applied:true", rep)
	}
	st := rep.Stores[0]
	if st.LinksRewritten != 1 {
		t.Errorf("links rewritten = %d, want the 1 live link", st.LinksRewritten)
	}

	// The epic shard exists, through the single marshaller path, and the task
	// shard is gone.
	e, ok, err := a.Store.LoadEpic("e-box1")
	if err != nil || !ok {
		t.Fatalf("LoadEpic(e-box1) = %v, %v; want the migrated epic", ok, err)
	}
	if e.Title != "the box" || e.Active || !e.IsOpen() || e.Goal != "" {
		t.Errorf("epic = %+v, want open, inactive, goal empty", e)
	}
	if _, err := os.Stat(filepath.Join(fdir, "tasks", "t-box1.json")); !os.IsNotExist(err) {
		t.Error("the converted task's shard must be deleted")
	}

	// Membership landed and the retired keys were CONSUMED — the bytes carry
	// "epic", and neither "parent" nor "type" anywhere.
	kid, err := os.ReadFile(filepath.Join(fdir, "tasks", "t-kid1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kid), `"epic": "e-box1"`) || strings.Contains(string(kid), `"parent"`) {
		t.Errorf("t-kid1 shard = %s, want epic membership and the parent key consumed", kid)
	}

	// The hand-fix case stays parked, exactly as it arrived.
	odd, err := os.ReadFile(filepath.Join(fdir, "tasks", "t-odd.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(odd), `"parent": "t-kid1"`) {
		t.Errorf("t-odd shard = %s, want its non-epic parent preserved for a human", odd)
	}

	// The body followed its epic, and the live link was rewritten — but the
	// documentation example inside the fence was not.
	if _, err := os.Stat(filepath.Join(fdir, "bodies", "t-box1.md")); !os.IsNotExist(err) {
		t.Error("the converted task's body must be renamed away")
	}
	eb, err := os.ReadFile(filepath.Join(fdir, "bodies", "e-box1.md"))
	if err != nil || !strings.Contains(string(eb), "plan lives here") {
		t.Errorf("bodies/e-box1.md = %q, %v; want the task's body carried over", eb, err)
	}
	kb, err := os.ReadFile(filepath.Join(fdir, "bodies", "t-kid1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kb), "see [[e-box1]]") {
		t.Errorf("t-kid1 body = %q, want the live link rewritten to [[e-box1]]", kb)
	}
	if !strings.Contains(string(kb), "a doc example: [[t-box1]]") {
		t.Errorf("t-kid1 body = %q, want the fenced example untouched", kb)
	}

	// The dep on the box is gone from the shard, and the drop is recorded in the
	// task's body, naming the epic.
	dep1, err := os.ReadFile(filepath.Join(fdir, "tasks", "t-dep1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dep1), "t-box1") {
		t.Errorf("t-dep1 shard = %s, want the dep on the box dropped", dep1)
	}
	db, err := os.ReadFile(filepath.Join(fdir, "bodies", "t-dep1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(db), "dropped dep on t-box1") || !strings.Contains(string(db), "[[e-box1]]") {
		t.Errorf("t-dep1 body = %q, want the dropped dep noted with a live [[e-box1]] link", db)
	}

	// The bodyless box got the same seed EpicAdd would have written.
	b3, err := os.ReadFile(filepath.Join(fdir, "bodies", "e-box3.md"))
	if err != nil || string(b3) != "# bodyless box\n" {
		t.Errorf("bodies/e-box3.md = %q, %v; want the seeded heading", b3, err)
	}

	// The board declares v6 and ordinary writes work again.
	if v, _ := a.Store.BoardVersion(); v != core.SchemaVersion {
		t.Errorf("board version = %d, want %d", v, core.SchemaVersion)
	}
	if _, err := a.Add("after the flag day", AddOpts{}); err != nil {
		t.Errorf("post-upgrade write failed: %v", err)
	}

	// The migration must leave no wreckage lint would blame on it: every epic
	// body is owned (no orphan-body), every epic has a body (no missing-body),
	// the rewritten links resolve (no dangling-link), and no dep dangles.
	probs, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probs {
		switch p.Code {
		case "orphan-body", "missing-body", "dangling-link", "dep-missing":
			t.Errorf("post-migration lint: %s %s — %s", p.Code, p.ID, p.Msg)
		}
	}

	// Idempotent: a second run is a clean no-op.
	rep2, err := a.Upgrade(true)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Changed || rep2.Applied {
		t.Errorf("second run = %+v, want a no-op", rep2)
	}
}

// A hot task's parent can point at an epic that was ARCHIVED. The conversion
// map is global across the board's stores, so the edge still becomes
// membership — pointing at the epic now living in archive/epics/.
func TestUpgradeV6RehomesAcrossStores(t *testing.T) {
	a, fdir := v5Board(t)
	arc := filepath.Join(fdir, "archive")
	writeV5Shard(t, arc, "t-box2", "archived box", "done", `[]`, `,
  "type": "epic"`)
	writeV5Meta(t, arc)
	writeV5Shard(t, fdir, "t-kid2", "straggler", "backlog", `[]`, `,
  "parent": "t-box2"`)

	rep, err := a.Upgrade(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Stores) != 2 {
		t.Fatalf("stores = %+v, want hot + archive", rep.Stores)
	}

	// The archived epic landed in the ARCHIVE store, closed (its lane was
	// terminal even without a closed stamp).
	if _, err := os.Stat(filepath.Join(arc, "epics", "e-box2.json")); err != nil {
		t.Errorf("archive epic shard missing: %v", err)
	}
	arcStore := rep.Stores[1]
	if len(arcStore.Epics) != 1 || !arcStore.Epics[0].Closed {
		t.Errorf("archive conversions = %+v, want e-box2 closed", arcStore.Epics)
	}

	// The hot straggler was re-homed onto it, not left behind.
	kid, err := os.ReadFile(filepath.Join(fdir, "tasks", "t-kid2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kid), `"epic": "e-box2"`) || strings.Contains(string(kid), `"parent"`) {
		t.Errorf("t-kid2 shard = %s, want membership of the archived epic", kid)
	}
	if rep.Stores[0].Rehomed != 2 {
		t.Errorf("hot rehomed = %d, want 2 (t-kid1, t-kid2)", rep.Stores[0].Rehomed)
	}
}
