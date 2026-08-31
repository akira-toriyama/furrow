package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// `show <epic-id>` renders the BOX view — byte-for-byte the one `epic show`
// prints, since both go through the same renderer. That is the point of the
// routing: an id names its own entity kind, so a reader holding one never has
// to know which command to reach for.
func TestCLIShowEpicMatchesEpicShow(t *testing.T) {
	initStore(t)
	epic := addEpic(t, "a box", "--goal", "curry is served", "-r", "o/r")
	task := addTask(t, "a member", "-e", epic)

	viaShow, code := run(t, "show", epic)
	if code != 0 {
		t.Fatalf("show <epic-id> exit = %d:\n%s", code, viaShow)
	}
	viaEpicShow, code := run(t, "epic", "show", epic)
	if code != 0 {
		t.Fatalf("epic show exit = %d:\n%s", code, viaEpicShow)
	}
	if viaShow != viaEpicShow {
		t.Errorf("show and epic show diverged:\n--- show ---\n%s\n--- epic show ---\n%s", viaShow, viaEpicShow)
	}
	if !strings.Contains(viaShow, task) {
		t.Errorf("the box view should list its member %s:\n%s", task, viaShow)
	}

	// --json is the box object (progress/tasks/goal), not a task view.
	out, code := run(t, "--json", "show", epic)
	if code != 0 {
		t.Fatalf("show --json exit = %d:\n%s", code, out)
	}
	var box struct {
		ID       string `json:"id"`
		Goal     string `json:"goal"`
		Progress struct {
			Total int `json:"total"`
		} `json:"progress"`
		Tasks    []map[string]any `json:"tasks"`
		BodyText string           `json:"body_text"`
		Status   string           `json:"status"` // a box has no lane
	}
	if err := json.Unmarshal(showOne(t, out), &box); err != nil {
		t.Fatalf("parse show --json: %v\n%s", err, out)
	}
	if box.ID != epic || box.Goal != "curry is served" || box.Progress.Total != 1 || len(box.Tasks) != 1 {
		t.Errorf("not the box view: %+v\n%s", box, out)
	}
	if box.Status != "" {
		t.Errorf("a box has no lane, got status %q", box.Status)
	}

	// --no-body OMITS the key, exactly as it does for a task — an empty string
	// is indistinguishable from a box whose body really is empty.
	out, _ = run(t, "--json", "show", epic, "--no-body")
	var raw map[string]any
	if err := json.Unmarshal(showOne(t, out), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["body_text"]; ok {
		t.Errorf("--no-body must omit body_text on a box entry:\n%s", out)
	}
	if _, ok := raw["progress"]; !ok {
		t.Errorf("--no-body dropped more than the body:\n%s", out)
	}
	// `epic show` keeps emitting the key, empty body or not: the FLAG decides
	// whether it exists, never the content.
	out, _ = run(t, "--json", "epic", "show", epic)
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["body_text"]; !ok {
		t.Errorf("epic show must keep body_text:\n%s", out)
	}
}

// A corrupt epic shard is a STORE failure, not "this id names nothing". Both
// errors are CodeValidation, so a per-ref "any error means not-a-box" read
// would report a box that is on disk as missing — while `epic show`, `note`,
// `edit` and `lint` all report the unreadable shard. `show` must agree with
// them.
func TestCLIShowSurfacesCorruptEpicShard(t *testing.T) {
	initStore(t)
	healthy := addEpic(t, "healthy box", "-r", "o/r")
	sick := addEpic(t, "sick box", "-r", "o/r")
	shard := filepath.Join(os.Getenv("FURROW_DIR"), "epics", sick+".json")
	if err := os.WriteFile(shard, []byte("<<<<<<< HEAD\n{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fe, _ := runErr(t, "show", healthy)
	if fe == nil || int(fe.Code) != 2 {
		t.Fatalf("a corrupt epic shard should surface as exit 2, got %v", fe)
	}
	if !strings.Contains(fe.Msg, "not valid JSON") {
		t.Errorf("the error should name the corruption, got %q", fe.Msg)
	}
	// The sibling commands' verdict, which `show` must not contradict.
	if peer, _ := runErr(t, "epic", "show", healthy); peer == nil || int(peer.Code) != int(fe.Code) {
		t.Errorf("show and epic show disagree about the same board: %v vs %v", fe, peer)
	}
}

// A mixed batch keeps input order and gives each entry its own shape — what an
// id naming its entity kind implies for the array.
func TestCLIShowMixedBatch(t *testing.T) {
	initStore(t)
	epic := addEpic(t, "a box", "-r", "o/r")
	task := addTask(t, "a task")

	out, code := run(t, "--json", "show", task, epic, "--no-body")
	if code != 0 {
		t.Fatalf("mixed show exit = %d:\n%s", code, out)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("a batch must be an array: %v\n%s", err, out)
	}
	if len(arr) != 2 || arr[0]["id"] != task || arr[1]["id"] != epic {
		t.Fatalf("input order lost: %v", arr)
	}
	if _, ok := arr[0]["status"]; !ok {
		t.Errorf("the task entry lost its lane: %v", arr[0])
	}
	if _, ok := arr[1]["progress"]; !ok {
		t.Errorf("the box entry lost its roll-up: %v", arr[1])
	}

	// --ndjson is one entry per line at any arity, same shapes.
	out, code = run(t, "--ndjson", "show", task, epic, "--no-body")
	if code != 0 {
		t.Fatalf("ndjson show exit = %d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 ndjson lines, got %d:\n%s", len(lines), out)
	}
	for i, want := range []string{task, epic} {
		var v map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &v); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if v["id"] != want {
			t.Errorf("line %d id = %v, want %s", i, v["id"], want)
		}
	}

	// --backlinks is a TASK relation ([[id]] links carry the task prefix), so a
	// box simply has none — and asking for it must not break the batch.
	if out, code := run(t, "--json", "show", task, epic, "--backlinks"); code != 0 {
		t.Fatalf("backlinks over a mixed batch exit = %d:\n%s", code, out)
	} else if !strings.Contains(out, "mentioned_by") {
		t.Errorf("the task entry should still carry mentioned_by:\n%s", out)
	}
}

func TestCLIShowEpicMisses(t *testing.T) {
	initStore(t)
	epic := addEpic(t, "a box", "-r", "o/r")

	// An epic-shaped miss says EPIC — "task not found" would send the reader to
	// the wrong store.
	fe, _ := runErr(t, "show", "e-nope0")
	if fe == nil || int(fe.Code) != 1 {
		t.Fatalf("unknown box want exit 1, got %v", fe)
	}
	if !strings.Contains(fe.Msg, "epic not found") {
		t.Errorf("message should name the entity: %q", fe.Msg)
	}

	out, code := run(t, "--json", "show", epic, "t-nope0", "--no-body")
	if code != 1 {
		t.Errorf("partial miss want exit 1, got %d", code)
	}
	if !strings.Contains(out, epic) {
		t.Errorf("the found box should still be emitted:\n%s", out)
	}

	// --archived reads the task archive only: boxes are never archived. The box
	// EXISTS on the board, so neither the kind nor the exit may claim otherwise:
	// this is bad usage (drop the flag), kind validation, exit 2 — a not-found
	// kind here would send a kind-branching consumer looking for a box that is
	// right there on the board.
	fe, _ = runErr(t, "show", epic, "--archived")
	if fe == nil || fe.Code != core.CodeValidation || fe.Kind != core.KindValidation {
		t.Fatalf("an epic id under --archived want kind validation exit 2, got %+v", fe)
	}
	if !strings.Contains(fe.Msg, "is an epic") || !strings.Contains(fe.Msg, "archive") {
		t.Errorf("the message should blame the flag, not the board: %q", fe.Msg)
	}
}

// `edit <epic-id>` hands back the box's body file — the same bodies/<id>.md the
// task path returns, created on demand.
func TestCLIEditEpicBodyPath(t *testing.T) {
	initStore(t)
	epic := addEpic(t, "a box", "-r", "o/r")

	out, code := run(t, "edit", epic)
	if code != 0 {
		t.Fatalf("edit <epic-id> exit = %d:\n%s", code, out)
	}
	got := strings.TrimSpace(out)
	want := filepath.Join(os.Getenv("FURROW_DIR"), "bodies", epic+".md")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("the body file should exist: %v", err)
	}

	// An unknown box keeps the epic resolver's exit 2 + candidates.
	fe, _ := runErr(t, "edit", "e-nope0")
	if fe == nil || int(fe.Code) != 2 || len(fe.Candidates) == 0 {
		t.Errorf("unknown box want exit 2 with candidates, got %v", fe)
	}
}
