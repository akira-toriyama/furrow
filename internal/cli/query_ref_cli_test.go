package cli

import (
	"fmt"
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
	mustRun(t, "done", retired)
	mustRun(t, "archive", retired, "--yes")

	// Live read: the retired id is carried by `live`, so it resolves and the
	// carrier is the answer; its own deps are not here, so blocks: says so.
	if got := lsIDs(t, "-q", "depends-on:"+retired); len(got) != 1 || got[0] != live {
		t.Errorf("depends-on:<archived> = %v, want [%s]", got, live)
	}
	if got := lsIDs(t, "-q", "descendant-of:"+retired); len(got) != 1 || got[0] != live {
		t.Errorf("descendant-of:<archived> = %v, want [%s]", got, live)
	}
	fe, _ := runErr(t, "ls", "-q", "blocks:"+retired)
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "with --archived") {
		t.Fatalf("blocks:<archived> should be exit 2 pointing at --archived, got %+v", fe)
	}
	if d, _ := fe.Details.(map[string]any); d["outside"] == nil || d["archived"] == nil || d["term"] != "blocks:"+retired {
		t.Errorf("blocks:<archived> details = %+v, want outside + archived + term", d)
	}
	// `live` waits on the retired task alone; in the hot snapshot that dep is
	// not a task, so blocks:<live> is a true empty, not a fault.
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
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "without --archived") {
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

	// stats' window unions both snapshots, so a ref either one holds passes
	// both passes (it used to exit 2 on every board with the graph fields);
	// the distributions are still the hot board's, so a task whose own edges
	// are only in the archive is refused exactly as `ls` refuses it.
	for _, q := range []string{"depends-on:" + upstream, "depends-on:" + retired, "blocks:" + upstream, "descendant-of:" + retired, "ancestor-of:" + live} {
		if fe, out := runErr(t, "stats", "--since", "2020-01-01", "-q", q); fe != nil {
			t.Errorf("stats --since -q %q = %+v\n%s", q, fe, out)
		}
	}
	if fe, _ := runErr(t, "stats", "--since", "2020-01-01", "-q", "blocks:"+retired); fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "with --archived") {
		t.Errorf("stats -q blocks:<archived> should refuse as ls does, got %+v", fe)
	}
	if fe, _ := runErr(t, "stats", "--since", "2020-01-01", "-q", "blocks:t-nope0"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("stats --since -q blocks:t-nope0 should still be exit 2, got %+v", fe)
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
	fe, _ := runErr(t, "ls", "-q", "anchor:2026-11-21")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "names the BOX") {
		t.Errorf("anchor:<day> should say the pointer names the box, got %+v", fe)
	}
}
