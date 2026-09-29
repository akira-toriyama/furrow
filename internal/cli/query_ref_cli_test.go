package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The ref binders of -q read a SNAPSHOT (the hot board, or the archive under
// --archived) and resolve a task ref against what that snapshot knows — its
// tasks and the ids its tasks carry as deps — plus the other store's existence
// (t-5mcm, the refutation pass): a retired dep a live task still carries
// resolves as the literal it is (the match is non-empty by construction), a
// live id resolves under --archived, and the two qualifiers that read the
// NAMED task's own deps (blocks:, ancestor-of:) refuse a task the snapshot
// does not hold, saying where its edges are readable — never a silent 0 rows
// in either direction, and never a false "unknown" for a task that exists.
func TestQueryTaskRefsAcrossStores(t *testing.T) {
	initStore(t)
	upstream := addTask(t, "upstream, done and still live")
	mustRun(t, "done", upstream)
	retired := addTask(t, "retired", "--dep", upstream)
	live := addTask(t, "live, waits on the retired one", "--dep", retired)
	z := addTask(t, "z, waits on live", "--dep", live)
	mustRun(t, "done", retired)
	mustRun(t, "archive", retired, "--yes")

	// Live read: the retired id is carried by `live`, so it resolves and the
	// carrier is the answer; its own deps are not here, so blocks: says so.
	if got := lsIDs(t, "-q", "depends-on:"+retired); len(got) != 1 || got[0] != live {
		t.Errorf("depends-on:<archived> = %v, want [%s]", got, live)
	}
	if got := lsIDs(t, "-q", "descendant-of:"+retired); len(got) != 2 || !slices.Contains(got, live) || !slices.Contains(got, z) {
		t.Errorf("descendant-of:<archived> = %v, want [%s %s]", got, live, z)
	}
	fe, _ := runErr(t, "ls", "-q", "blocks:"+retired)
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "ls --archived -q") {
		t.Fatalf("blocks:<archived> should be exit 2 pointing at ls --archived, got %+v", fe)
	}
	if d, _ := fe.Details.(map[string]any); d["outside"] == nil || d["archived"] == nil || d["term"] != "blocks:"+retired {
		t.Errorf("blocks:<archived> details = %+v, want outside + archived + term", d)
	}
	// `live` waits on the retired task alone: an edge into the archive is
	// retired, hence done, work — nothing live blocks it — so blocks:<live>
	// is a true empty, not a fault (README's "graph" bullet says so).
	if got := lsIDs(t, "-q", "blocks:"+live); len(got) != 0 {
		t.Errorf("blocks:<live> = %v, want [] (its only dep is archived)", got)
	}

	// Archived read: the hot `upstream` is carried by the retired task, so it
	// resolves there; its deps are on the live board, so blocks: says so. A
	// live id nothing archived carries still EXISTS, so its dependents in the
	// archive are a true empty.
	if got := lsIDs(t, "--archived", "-q", "depends-on:"+upstream); len(got) != 1 || got[0] != retired {
		t.Errorf("--archived depends-on:<live> = %v, want [%s]", got, retired)
	}
	fe, _ = runErr(t, "ls", "--archived", "-q", "blocks:"+upstream)
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "furrow ls -q") {
		t.Fatalf("--archived blocks:<live> should be exit 2 pointing at the live board, got %+v", fe)
	}
	if d, _ := fe.Details.(map[string]any); d["live"] == nil {
		t.Errorf("--archived blocks:<live> details = %+v, want live", d)
	}
	if got := lsIDs(t, "--archived", "-q", "depends-on:"+live); len(got) != 0 {
		t.Errorf("--archived depends-on:<live, uncarried> = %v, want [] (a true empty)", got)
	}

	// A miss in EVERY store is still the fault, the misses named.
	for _, args := range [][]string{{"ls", "-q", "depends-on:t-nope0"}, {"ls", "--archived", "-q", "ancestor-of:t-nope0"}} {
		fe, _ := runErr(t, args...)
		if fe == nil || fe.Code != core.CodeValidation {
			t.Fatalf("%v should be exit 2, got %+v", args, fe)
		}
		if d, _ := fe.Details.(map[string]any); d["missing"] == nil {
			t.Errorf("%v details = %+v, want missing", args, d)
		}
	}

	// stats' window unions both snapshots (it used to exit 2 on every board
	// with the graph fields): a ref either one holds passes both passes, and
	// the walk follows the union's edges — the chain upstream ← retired ←
	// live ← z crosses the archive twice and is one DAG to the window. The
	// distributions are still the hot board's, so a task whose own edges are
	// only in the archive is refused exactly as `ls` refuses it.
	window := func(q string) []string {
		t.Helper()
		out, code := run(t, "--json", "stats", "--since", "2020-01-01", "-q", q)
		if code != 0 {
			t.Fatalf("stats --since -q %q exit = %d:\n%s", q, code, out)
		}
		var v struct {
			Window struct {
				Created []string `json:"created_ids"`
				Closed  []string `json:"closed_ids"`
			} `json:"window"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("parse stats --json: %v\n%s", err, out)
		}
		return append(v.Window.Created, v.Window.Closed...)
	}
	for _, c := range []struct {
		q    string
		want []string
	}{
		{"depends-on:" + upstream, []string{retired}},
		{"depends-on:" + retired, []string{live}},
		{"blocks:" + live, []string{retired}},
		{"blocks:" + upstream, nil},
		{"descendant-of:" + upstream, []string{retired, live, z}},
		{"ancestor-of:" + z, []string{live, retired, upstream}},
	} {
		ids := window(c.q)
		for _, id := range c.want {
			if !slices.Contains(ids, id) {
				t.Errorf("stats --since -q %q window = %v, lacks %s", c.q, ids, id)
			}
		}
	}
	if fe, _ := runErr(t, "stats", "--since", "2020-01-01", "-q", "blocks:"+retired); fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "ls --archived -q") {
		t.Errorf("stats -q blocks:<archived> should refuse as ls does, got %+v", fe)
	}
	if fe, _ := runErr(t, "stats", "--since", "2020-01-01", "-q", "blocks:t-nope0"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("stats --since -q blocks:t-nope0 should still be exit 2, got %+v", fe)
	}

	// An id no store holds as a task but the archive still carries as a dep
	// (`rm` does not see archived references) exists to the read: the live
	// board's dependents are a true empty, the archive's are the answer, and
	// stats' hot-only compile no longer refuses what its window returns.
	p := addTask(t, "p, removed after q retired")
	q := addTask(t, "q, waits on p", "--dep", p)
	mustRun(t, "done", q)
	mustRun(t, "archive", q, "--yes")
	mustRun(t, "rm", p, "--yes")
	if got := lsIDs(t, "-q", "depends-on:"+p); len(got) != 0 {
		t.Errorf("depends-on:<rm'd, carried by the archive> = %v, want []", got)
	}
	if got := lsIDs(t, "--archived", "-q", "depends-on:"+p); len(got) != 1 || got[0] != q {
		t.Errorf("--archived depends-on:<rm'd> = %v, want [%s]", got, q)
	}
	if ids := window("depends-on:" + p); !slices.Contains(ids, q) {
		t.Errorf("stats --since -q depends-on:<rm'd> window = %v, lacks %s", ids, q)
	}
	// Its own edges exist nowhere; the refusal says who carries it, and does
	// not point at lint, which judges the live board alone.
	fe, _ = runErr(t, "ls", "-q", "blocks:"+p)
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "archive's tasks") || strings.Contains(fe.Msg, "lint") {
		t.Errorf("blocks:<rm'd, carried by the archive> = %+v, want exit 2 naming the archive's tasks as the carriers", fe)
	}
}

// Every binder fault carries the term and its byte offset, the repo and lane
// values included (README's claim, which the refutation pass measured false
// for repo:/status:), and the anchor: day slip gets the box hint.
func TestQueryRefFaultDetails(t *testing.T) {
	initStore(t)
	addTask(t, "x")
	for _, c := range []struct {
		q, kind string
		offset  int
	}{
		{"status:ready epic:e-nope0", "epic-not-found", 13},
		{"is:open repo:nosuch/none/x", "repo-unknown", 8},
		{"status:nope", "unknown-lane", 0},
		{"label:a depends-on:t-nope0", "validation", 8},
	} {
		fe, _ := runErr(t, "ls", "-q", c.q)
		if fe == nil || fe.Code != core.CodeValidation || fe.Kind != c.kind {
			t.Errorf("ls -q %q = %+v, want exit 2 kind %s", c.q, fe, c.kind)
			continue
		}
		d, _ := fe.Details.(map[string]any)
		if d["term"] == nil || fmt.Sprint(d["offset"]) != strconv.Itoa(c.offset) {
			t.Errorf("ls -q %q details = %+v, want term + offset %v", c.q, d, c.offset)
		}
	}
	// The day slip keeps the resolver's kind and candidates, with the hint
	// appended — a consumer branching on epic-not-found still lands here.
	fe, _ := runErr(t, "ls", "-q", "anchor:2026-11-21")
	if fe == nil || fe.Code != core.CodeValidation || fe.Kind != "epic-not-found" || !strings.Contains(fe.Msg, "names the BOX") {
		t.Errorf("anchor:<day> should be epic-not-found saying the pointer names the box, got %+v", fe)
	}
}
