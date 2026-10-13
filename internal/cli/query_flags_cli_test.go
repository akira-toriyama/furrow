package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// A repeated -q ANDs the queries (the grammar's own "repeat = AND"); a second
// -q used to replace the first in silence, which on a write selector widened
// the target set. A repeated -r or -e is refused, naming the -q spelling of an
// OR — one repo and one box is what a read scopes to.
func TestRepeatedQueryFlagsAndOrRefuse(t *testing.T) {
	initStore(t)
	both := addTask(t, "both", "-l", "bug", "-s", "backlog", "-r", "o/a")
	addTask(t, "label only", "-l", "bug", "-s", "ready", "-r", "o/a")
	addTask(t, "lane only", "-s", "backlog", "-r", "o/b")

	if ids := qIDs2(t, "ls", "-q", "label:bug", "-q", "status:backlog"); strings.Join(ids, ",") != both {
		t.Errorf("ls -q a -q b must AND the queries, got %v", ids)
	}
	// The write selector previews the SAME set.
	out, code := run(t, "--json", "done", "-r", "", "-q", "label:bug", "-q", "status:backlog")
	if code != 0 || strings.Count(out, `"id": "`) != 1 || !strings.Contains(out, both) {
		t.Errorf("done -q a -q b must preview the AND set only (exit %d):\n%s", code, out)
	}

	for _, flag := range []string{"-r", "-e"} {
		fe, _ := runErr(t, "ls", flag, "o/a", flag, "o/b")
		if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "-q '") {
			t.Errorf("ls %s x %s y must be refused naming the -q OR spelling, got %+v", flag, flag, fe)
		}
	}
	if fe, _ := runErr(t, "done", "-r", "o/a", "-r", "o/b", "-q", "label:bug"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("a write selector with -r twice must be refused, got %+v", fe)
	}
	if ids := qIDs2(t, "ls", "-r", "o/b"); len(ids) != 1 {
		t.Errorf("a single -r still scopes: %v", ids)
	}
}

// An ideographic space (U+3000, an IME's word separator) inside -q used to
// make one term that matched nothing — [] at exit 0 for a typo. It is a parse
// fault with the offset, like every other.
func TestQueryRefusesIdeographicSpace(t *testing.T) {
	initStore(t)
	addTask(t, "x", "-l", "ui")
	fe, _ := runErr(t, "ls", "-q", "label:ui\u3000is:open")
	if fe == nil || fe.Kind != core.KindQueryParse {
		t.Fatalf("want query-parse, got %+v", fe)
	}
	d, _ := fe.Details.(map[string]any)
	if d == nil || d["term"] != "\u3000" || d["offset"] != 8 {
		t.Errorf("details = %+v; want the U+3000 at offset 8", fe.Details)
	}
}

// qIDs2 runs a read with --json and returns the ids.
func qIDs2(t *testing.T, args ...string) []string {
	t.Helper()
	out, code := run(t, append([]string{"--json"}, args...)...)
	if code != 0 {
		t.Fatalf("%v exit = %d:\n%s", args, code, out)
	}
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, `"id": "`); i >= 0 && strings.HasPrefix(strings.TrimSpace(line), `"id"`) {
			rest := line[i+len(`"id": "`):]
			ids = append(ids, rest[:strings.IndexByte(rest, '"')])
		}
	}
	return ids
}
