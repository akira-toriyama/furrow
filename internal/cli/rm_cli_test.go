package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// rm previews by default, deletes shard + body with --yes, and its --json is
// the {dry_run, force, tasks, references} report on both.
func TestRmPreviewThenYes(t *testing.T) {
	initStore(t)
	id := addTask(t, "filed too early")
	dir := os.Getenv(app.EnvDir)

	out, code := run(t, "rm", id)
	if code != 0 || !strings.Contains(out, "would remove 1 task(s)") || !strings.Contains(out, "re-run with --yes") {
		t.Fatalf("preview: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", id+".json")); err != nil {
		t.Fatalf("preview deleted the shard: %v", err)
	}

	out, code = run(t, "rm", id, "--yes", "--json")
	if code != 0 {
		t.Fatalf("rm --yes: exit %d\n%s", code, out)
	}
	var rep struct {
		DryRun     bool        `json:"dry_run"`
		Force      bool        `json:"force"`
		Tasks      []core.Task `json:"tasks"`
		References struct {
			Deps  []any `json:"deps"`
			Links []any `json:"links"`
		} `json:"references"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse report: %v\n%s", err, out)
	}
	if rep.DryRun || rep.Force || len(rep.Tasks) != 1 || rep.Tasks[0].ID != id || rep.References.Deps == nil || rep.References.Links == nil {
		t.Errorf("report = %+v", rep)
	}
	for _, p := range []string{filepath.Join("tasks", id+".json"), filepath.Join("bodies", id+".md")} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s survived rm --yes (err %v)", p, err)
		}
	}
	fe, _ := runErr(t, "show", id)
	if fe == nil || fe.Kind != core.KindNotFound {
		t.Errorf("show after rm: %v", fe)
	}
}

// A referenced target is exit 2 kind referenced with details.references;
// --force severs and the human output lists what it severed.
func TestRmReferencedAndForce(t *testing.T) {
	initStore(t)
	target := addTask(t, "target")
	waits := addTask(t, "waits", "--dep", target)

	fe, _ := runErr(t, "rm", target, "--yes")
	if fe == nil || fe.Kind != core.KindReferenced || fe.Code != core.CodeValidation {
		t.Fatalf("want referenced exit 2, got %v", fe)
	}
	d, _ := fe.Details.(map[string]any)
	if d == nil || d["references"] == nil {
		t.Errorf("details = %v", fe.Details)
	}

	out, code := run(t, "rm", target, "--force", "--yes")
	if code != 0 || !strings.Contains(out, "removed 1 task(s)") || !strings.Contains(out, "dep      "+waits+" -> "+target) {
		t.Fatalf("rm --force: exit %d\n%s", code, out)
	}
	out, _ = run(t, "show", waits, "--json")
	if !strings.Contains(out, `"deps": []`) {
		t.Errorf("dep edge survived:\n%s", out)
	}
}

// An archived id is not removable from the hot board: the miss says so and
// points at unarchive.
func TestRmArchivedIdSaysSo(t *testing.T) {
	initStore(t)
	id := archiveOne(t, "retired")
	fe, _ := runErr(t, "rm", id, "--yes")
	if fe == nil || fe.Kind != core.KindNotFound {
		t.Fatalf("want not-found, got %v", fe)
	}
	d, _ := fe.Details.(map[string]any)
	if d == nil || d["archived"] == nil || !strings.Contains(fe.Msg, "unarchive") {
		t.Errorf("no archived enrichment: %v / %s", fe.Details, fe.Msg)
	}
	if _, code := run(t, "show", id, "--archived"); code != 0 {
		t.Error("the archived task was touched")
	}
}

// epic rm: refused on members, --force unfiles them and deletes the shard +
// body; the report carries the box as it was.
func TestEpicRmForce(t *testing.T) {
	initStore(t)
	box := addEpic(t, "box", "-r", "o/r")
	member := addTask(t, "member", "-e", box)
	dir := os.Getenv(app.EnvDir)

	fe, _ := runErr(t, "epic", "rm", box, "--yes")
	if fe == nil || fe.Kind != core.KindReferenced {
		t.Fatalf("want referenced, got %v", fe)
	}
	out, code := run(t, "epic", "rm", box, "--force", "--yes", "--json")
	if code != 0 {
		t.Fatalf("epic rm: exit %d\n%s", code, out)
	}
	var rep struct {
		DryRun     bool       `json:"dry_run"`
		Epic       *core.Epic `json:"epic"`
		References struct {
			Members []struct {
				Task string `json:"task"`
				Epic string `json:"epic"`
			} `json:"members"`
		} `json:"references"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if rep.DryRun || rep.Epic == nil || rep.Epic.ID != box || len(rep.References.Members) != 1 || rep.References.Members[0].Task != member {
		t.Errorf("report = %+v", rep)
	}
	for _, p := range []string{filepath.Join("epics", box+".json"), filepath.Join("bodies", box+".md")} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s survived epic rm (err %v)", p, err)
		}
	}
	out, _ = run(t, "show", member, "--json", "--no-body")
	if strings.Contains(out, box) {
		t.Errorf("member still filed:\n%s", out)
	}
	if fe, _ := runErr(t, "epic", "show", box); fe == nil || fe.Kind != core.KindEpicNotFound {
		t.Errorf("epic show after rm: %v", fe)
	}
}
