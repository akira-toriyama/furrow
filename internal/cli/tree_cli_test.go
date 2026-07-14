package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// treeNodeJSON mirrors the --tree --json node: the whole task, plus the two derived
// facts, plus nested children.
type treeNodeJSON struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	Actionable bool           `json:"actionable"`
	BlockedBy  []string       `json:"blocked_by"`
	Children   []treeNodeJSON `json:"children"`
}

// buildTreeBoard: an epic with a gate, a task waiting on that gate, and a loose
// task — the smallest board that exercises nesting, the star, and a blocker.
func buildTreeBoard(t *testing.T) (epic, gate, waiter, loose string) {
	t.Helper()
	initStore(t)
	epic = addTask(t, "an epic")
	gate = addTask(t, "the gate")
	waiter = addTask(t, "waits on the gate")
	loose = addTask(t, "unrelated")
	for _, c := range []string{gate, waiter} {
		if _, code := run(t, "parent", c, epic); code != 0 {
			t.Fatalf("parent %s", c)
		}
		if _, code := run(t, "move", c, "ready"); code != 0 {
			t.Fatalf("move %s", c)
		}
	}
	if _, code := run(t, "dep", waiter, gate); code != 0 {
		t.Fatal("dep")
	}
	return epic, gate, waiter, loose
}

func TestCLITreeHumanShowsShapeStarAndBlocker(t *testing.T) {
	epic, gate, waiter, loose := buildTreeBoard(t)

	out, code := run(t, "ls", "--tree")
	if code != int(core.CodeOK) {
		t.Fatalf("ls --tree exit=%d:\n%s", code, out)
	}
	// Key each line by the id it IS, not by an id it merely mentions — a blocked
	// task's line names its blocker too.
	byID := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if f := strings.Fields(l); len(f) >= 2 {
			byID[f[1]] = l
		}
	}
	for _, id := range []string{epic, gate, waiter, loose} {
		if byID[id] == "" {
			t.Fatalf("every task must appear in the tree (%s missing):\n%s", id, out)
		}
	}
	// Structure is carried by indentation: a child is indented, a root is not.
	if strings.HasPrefix(byID[epic], " ") {
		t.Errorf("the epic is a root and must not be indented: %q", byID[epic])
	}
	if !strings.HasPrefix(byID[gate], "   ") {
		t.Errorf("the gate hangs under the epic and must be indented: %q", byID[gate])
	}
	// The star says "pick this up now" — and only the gate can be.
	if !strings.HasPrefix(strings.TrimSpace(byID[gate]), "★") {
		t.Errorf("the gate is actionable and must be starred: %q", byID[gate])
	}
	if strings.Contains(byID[waiter], "★") {
		t.Errorf("the waiter is blocked and must not be starred: %q", byID[waiter])
	}
	// …and the blocked one names what is in its way.
	if !strings.Contains(byID[waiter], "blocked by: "+gate) {
		t.Errorf("a blocked task must name its blocker: %q", byID[waiter])
	}
	// The lane is printed as well as the glyph — a glyph is a summary, not a substitute.
	if !strings.Contains(byID[gate], "[ready]") {
		t.Errorf("the lane must be greppable: %q", byID[gate])
	}
}

func TestCLITreeJSONNestsAndCarriesTheDerivedFacts(t *testing.T) {
	epic, gate, waiter, _ := buildTreeBoard(t)

	out, code := run(t, "--json", "ls", "--tree", epic)
	if code != int(core.CodeOK) {
		t.Fatalf("exit=%d:\n%s", code, out)
	}
	var roots []treeNodeJSON
	if err := json.Unmarshal([]byte(out), &roots); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(roots) != 1 || roots[0].ID != epic {
		t.Fatalf("a root id draws just that subtree: %+v", roots)
	}
	if len(roots[0].Children) != 2 {
		t.Fatalf("both slices must nest under the epic: %+v", roots[0].Children)
	}
	// The embedded core.Task must survive beside the sibling fields — this is the
	// regression a MarshalJSON on core.Task would cause (it would empty title/status).
	for _, c := range roots[0].Children {
		if c.Title == "" || c.Status == "" {
			t.Errorf("a node must carry the whole task, not just the tree fields: %+v", c)
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

	// --ndjson streams one whole TREE per line (a tree is a value; flattening it
	// would destroy the structure that was asked for).
	out, code = run(t, "--ndjson", "ls", "--tree")
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 { // the epic's tree, and the loose task
		t.Fatalf("want one line per root, got %d:\n%s", len(lines), out)
	}
	for _, l := range lines {
		var n treeNodeJSON
		if err := json.Unmarshal([]byte(l), &n); err != nil {
			t.Errorf("each line must be one compact tree: %v", err)
		}
	}
}

func TestCLITreeRootErrors(t *testing.T) {
	epic, _, _, _ := buildTreeBoard(t)

	// An unknown id is a miss (exit 1), like every other "specifically requested id".
	if fe, _ := runErr(t, "ls", "--tree", "t-404"); fe == nil || fe.Code != core.CodeNotFound {
		t.Errorf("unknown root must be exit 1, got %+v", fe)
	}
	// An id that exists but the filters exclude: an empty tree would read as "this
	// task has nothing under it", which is a different fact. Say so instead.
	if fe, _ := runErr(t, "ls", "--tree", epic, "-s", "done"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("a filtered-out root must be exit 2, got %+v", fe)
	}
	// A positional id without --tree is still bad usage (ls takes no args).
	if fe, _ := runErr(t, "ls", epic); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("ls <id> without --tree must be exit 2, got %+v", fe)
	}
}
