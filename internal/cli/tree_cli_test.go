package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// groupJSON mirrors `ls --tree --json`: one group per epic, its tasks inside.
type groupJSON struct {
	Epic *struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"epic"`
	Active   bool `json:"active"`
	Progress *struct {
		Done  int `json:"done"`
		Total int `json:"total"`
	} `json:"progress"`
	Stuck bool `json:"stuck"`
	Tasks []struct {
		ID         string   `json:"id"`
		Title      string   `json:"title"`
		Status     string   `json:"status"`
		Actionable bool     `json:"actionable"`
		BlockedBy  []string `json:"blocked_by"`
	} `json:"tasks"`
}

// buildTreeBoard: one epic with a gate and a task waiting on that gate, plus a
// task filed under no epic — the smallest board that exercises grouping, the
// star, a blocker, and the unfiled group.
func buildTreeBoard(t *testing.T) (epic, gate, waiter, loose string) {
	t.Helper()
	initStore(t)
	out, code := run(t, "--json", "epic", "add", "the box")
	if code != 0 {
		t.Fatalf("epic add: exit %d:\n%s", code, out)
	}
	var e struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("parse epic: %v\n%s", err, out)
	}
	epic = e.ID
	gate = addTask(t, "the gate", "-e", epic, "-s", "ready")
	waiter = addTask(t, "waits on the gate", "-e", epic, "-s", "ready")
	loose = addTask(t, "unrelated")
	if _, code := run(t, "dep", waiter, gate); code != 0 {
		t.Fatal("dep")
	}
	return epic, gate, waiter, loose
}

func TestCLITreeHumanGroupsStarAndBlocker(t *testing.T) {
	epic, gate, waiter, loose := buildTreeBoard(t)

	out, code := run(t, "ls", "--tree")
	if code != int(core.CodeOK) {
		t.Fatalf("ls --tree exit=%d:\n%s", code, out)
	}
	// Key each line by the id it IS, not by an id it merely mentions — a blocked
	// task's line names its blocker too.
	byID := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		for _, f := range strings.Fields(l) {
			if strings.HasPrefix(f, "t-") || strings.HasPrefix(f, "e-") {
				if _, seen := byID[f]; !seen {
					byID[f] = l
				}
				break
			}
		}
	}
	for _, id := range []string{epic, gate, waiter, loose} {
		if byID[id] == "" {
			t.Fatalf("every task and its box must appear (%s missing):\n%s", id, out)
		}
	}
	// The epic heads its group; members are indented under it.
	if strings.HasPrefix(byID[epic], "    ") {
		t.Errorf("the epic heads its group and must not be indented like a member: %q", byID[epic])
	}
	if !strings.HasPrefix(byID[gate], "    ") {
		t.Errorf("a member must be indented under its box: %q", byID[gate])
	}
	// The group header carries the roll-up.
	if !strings.Contains(byID[epic], "(0/2)") {
		t.Errorf("the group header must carry progress: %q", byID[epic])
	}
	// The star says "pick this up now" — and only the gate can be.
	if !strings.HasPrefix(strings.TrimSpace(byID[gate]), "★") {
		t.Errorf("the gate is actionable and must be starred: %q", byID[gate])
	}
	if strings.Contains(byID[waiter], "★") {
		t.Errorf("the waiter is blocked and must not be starred: %q", byID[waiter])
	}
	if !strings.Contains(byID[waiter], "blocked by: "+gate) {
		t.Errorf("a blocked task must name its blocker: %q", byID[waiter])
	}
	// The lane is printed as well as the glyph — a glyph is a summary, not a substitute.
	if !strings.Contains(byID[gate], "[ready]") {
		t.Errorf("the lane must be greppable: %q", byID[gate])
	}
	// The unfiled task is drawn, never dropped: it is a lint error the reader has
	// to be able to see in order to fix.
	if !strings.Contains(out, "(no epic)") {
		t.Errorf("an unfiled task must appear under a (no epic) group:\n%s", out)
	}
}

func TestCLITreeJSONGroupsAndCarriesTheDerivedFacts(t *testing.T) {
	epic, gate, waiter, _ := buildTreeBoard(t)

	out, code := run(t, "--json", "ls", "--tree", epic)
	if code != int(core.CodeOK) {
		t.Fatalf("exit=%d:\n%s", code, out)
	}
	var groups []groupJSON
	if err := json.Unmarshal([]byte(out), &groups); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(groups) != 1 || groups[0].Epic == nil || groups[0].Epic.ID != epic {
		t.Fatalf("an epic reference draws just that group: %+v", groups)
	}
	if len(groups[0].Tasks) != 2 {
		t.Fatalf("both members must be in the group: %+v", groups[0].Tasks)
	}
	if groups[0].Progress == nil || groups[0].Progress.Total != 2 {
		t.Errorf("the group must carry its roll-up: %+v", groups[0].Progress)
	}
	// The embedded core.Task must survive beside the sibling fields — this is the
	// regression a MarshalJSON on core.Task would cause (it would empty title/status).
	for _, c := range groups[0].Tasks {
		if c.Title == "" || c.Status == "" {
			t.Errorf("a node must carry the whole task, not just the derived fields: %+v", c)
		}
		switch c.ID {
		case gate:
			if !c.Actionable || len(c.BlockedBy) != 0 {
				t.Errorf("the gate is actionable and unblocked: %+v", c)
			}
		case waiter:
			if c.Actionable || len(c.BlockedBy) != 1 || c.BlockedBy[0] != gate {
				t.Errorf("the waiter is blocked BY the gate: %+v", c)
			}
		}
	}

	// --ndjson streams one whole GROUP per line (a group is the value here;
	// flattening it would destroy the grouping that was asked for).
	out, code = run(t, "--ndjson", "ls", "--tree")
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 { // the box's group, and the unfiled one
		t.Fatalf("want one line per group, got %d:\n%s", len(lines), out)
	}
	for _, l := range lines {
		var g groupJSON
		if err := json.Unmarshal([]byte(l), &g); err != nil {
			t.Errorf("each line must be one compact group: %v", err)
		}
	}
}

func TestCLITreeRootErrors(t *testing.T) {
	epic, _, _, _ := buildTreeBoard(t)

	// An unknown epic reference is exit 2 with candidates — an empty tree would
	// read as "this box has nothing in it", which is a different fact.
	fe, _ := runErr(t, "ls", "--tree", "no-such-box")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("unknown epic reference must be exit 2, got %+v", fe)
	}
	if fe != nil && len(fe.Candidates) == 0 {
		t.Errorf("an unknown epic must carry the board's epic ids as candidates: %+v", fe)
	}
	// A positional id without --tree is still bad usage (ls takes no args).
	if fe, _ := runErr(t, "ls", epic); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("a positional id without --tree must be exit 2, got %+v", fe)
	}
}

// t-n7x4: `ls --tree` is still `ls` — the -l did-you-mean guard (exit 2 +
// candidates when a label matches nothing but names a repo) and the
// hidden-drafts hint must fire under the same conditions, with the same exit
// code and stderr, as the flat listing. The tree branch used to early-return
// past both.
func TestTreeMatchesFlatLsGuards(t *testing.T) {
	initStore(t)
	addTask(t, "in repo", "-r", "akira-toriyama/foo")
	addTask(t, "a draft", "--draft")

	// -l naming a repo: flat and tree agree — exit 2, same candidates.
	feFlat, _ := runErr(t, "ls", "-l", "foo")
	feTree, _ := runErr(t, "ls", "--tree", "-l", "foo")
	if feFlat == nil || feTree == nil {
		t.Fatalf("both reads must refuse -l foo: flat=%v tree=%v", feFlat, feTree)
	}
	if feTree.Code != feFlat.Code || len(feTree.Candidates) != len(feFlat.Candidates) {
		t.Errorf("tree must fail exactly like flat: flat=%+v tree=%+v", feFlat, feTree)
	}
	if len(feTree.Candidates) != 1 || feTree.Candidates[0] != "akira-toriyama/foo" {
		t.Errorf("tree candidates = %v, want the repo steer", feTree.Candidates)
	}

	// A repo scope that hides the draft: both say so on stderr.
	_, seFlat, codeFlat := runSplit(t, "ls", "-r", "akira-toriyama/foo")
	_, seTree, codeTree := runSplit(t, "ls", "--tree", "-r", "akira-toriyama/foo")
	if codeFlat != 0 || codeTree != 0 {
		t.Fatalf("scoped reads must succeed: flat=%d tree=%d", codeFlat, codeTree)
	}
	if !strings.Contains(seFlat, "draft") || !strings.Contains(seTree, "draft") {
		t.Errorf("both must hint the hidden draft:\nflat: %q\ntree: %q", seFlat, seTree)
	}
}
