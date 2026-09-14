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

// rm is the ONE path that ends a recurrence: the rule is held by exactly one
// live task and only a close hands it on, so deleting that task destroys the
// series with nothing left behind to say so (lint stays clean). The preview
// must disclose it, and so must the --yes apply — an operator who passes --yes
// straight away never sees a preview. It must also stay QUIET on an ordinary
// task: a note that cries wolf teaches the reader to skip it.
func TestRmRepeatingSaysSeriesEnds(t *testing.T) {
	initStore(t)
	id := addTask(t, "water the plants", "--due", "2030-05-01", "--repeat", "daily")
	plain := addTask(t, "filed too early")

	out, code := run(t, "rm", id)
	if code != 0 {
		t.Fatalf("preview: exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"repeat: " + id + " carries a series",
		"FREQ=DAILY",
		"due 2030-0",
		"removing it ends the series; no successor is minted",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview does not say %q:\n%s", want, out)
		}
	}

	out, code = run(t, "rm", id, "--yes")
	if code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"repeat: " + id + " carried a series",
		"FREQ=DAILY",
		"the series ended; no successor was minted",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("apply does not say %q:\n%s", want, out)
		}
	}

	for _, args := range [][]string{{"rm", plain}, {"rm", plain, "--yes"}} {
		out, code = run(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
		if strings.Contains(out, "repeat:") {
			t.Errorf("%v cried wolf on a task carrying no rule:\n%s", args, out)
		}
	}
}

// An asset another body still shows survives rm and the report says so: the
// human output names the holder, --json carries assets.kept (t-7hhb).
func TestRmKeepsAssetAnotherBodyShows(t *testing.T) {
	initStore(t)
	owner := addTask(t, "owner")
	reader := addTask(t, "reader")
	src := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := run(t, "attach", owner, src, "--json")
	if code != 0 {
		t.Fatalf("attach: %s", out)
	}
	var att struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal([]byte(out), &att); err != nil {
		t.Fatal(err)
	}
	if out, code := run(t, "note", reader, "![shot]("+att.Ref+")"); code != 0 {
		t.Fatalf("note: %s", out)
	}
	out, code = run(t, "rm", owner, "--yes")
	if code != 0 || !strings.Contains(out, "asset kept in the store: bodies/"+att.Ref+" — still held by "+reader) {
		t.Fatalf("rm must say what it kept and for whom: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv(app.EnvDir), "bodies", att.Ref)); err != nil {
		t.Fatalf("the reader's file must survive: %v", err)
	}
	out, code = run(t, "rm", reader, "--json")
	var rep struct {
		Assets struct {
			Deleted []string `json:"deleted"`
			Kept    []any    `json:"kept"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || code != 0 {
		t.Fatalf("preview: exit %d %v\n%s", code, err, out)
	}
	if len(rep.Assets.Deleted) != 1 || len(rep.Assets.Kept) != 0 {
		t.Errorf("the last holder's preview must show the file going: %+v", rep.Assets)
	}
}
