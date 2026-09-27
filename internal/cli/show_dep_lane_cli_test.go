package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// t-3acv: `show` printed `deps: t-xvh65` and `epic: e-k3m9` — ids alone, with
// no lane and no title — so the one read that gives a WHOLE task was the only
// place a dep's state could not be seen, and six drill runs plus an independent
// refutation agent each retyped `dep --list` to get it. The dep rows are now
// byte-identical to that command's, which is the point: two reads naming the
// same edge must render it the same way.
func TestCLIShowResolvesDepsAndEpicToLaneAndTitle(t *testing.T) {
	initStore(t)
	box := addEpic(t, "demo box", "-r", "o/r")
	upstream := addTask(t, "upstream A", "-r", "o/r", "-s", "icebox")
	settled := addTask(t, "settled B", "-r", "o/r")
	subject := addTask(t, "downstream C", "-r", "o/r", "-e", box)

	if out, code := run(t, "dep", subject, upstream, settled); code != 0 {
		t.Fatalf("dep exit %d:\n%s", code, out)
	}
	if out, code := run(t, "set", settled, "-s", "done"); code != 0 {
		t.Fatalf("set done exit %d:\n%s", code, out)
	}

	human, code := run(t, "show", subject)
	if code != 0 {
		t.Fatalf("show exit %d:\n%s", code, human)
	}
	for _, want := range []string{
		// The tally is the checklist's (t-c7kw): the derived N/M a reader would
		// otherwise count by eye, and here it IS the question — all deps done
		// means the task can move.
		"deps:     1/2 done\n",
		"  " + upstream + "  [icebox]  upstream A\n",
		"  " + settled + "  [done]  settled B\n",
		"epic:     " + box + "  demo box\n",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("show must print %q:\n%s", want, human)
		}
	}

	// The rows must be the ones `dep --list` already prints, character for
	// character — a second spelling of the same edge is the drift this fixes.
	depList, code := run(t, "dep", subject, "--list")
	if code != 0 {
		t.Fatalf("dep --list exit %d:\n%s", code, depList)
	}
	for _, row := range []string{
		"  " + upstream + "  [icebox]  upstream A\n",
		"  " + settled + "  [done]  settled B\n",
	} {
		if !strings.Contains(depList, row) {
			t.Errorf("dep --list must print the same row %q:\n%s", row, depList)
		}
	}
}

// A task with no deps prints no deps block at all, and an unfiled task no epic
// line — the gates are unchanged, so the addition costs a bare task nothing.
func TestCLIShowOmitsDepsAndEpicWhenThereAreNone(t *testing.T) {
	initStore(t)
	bare := addTask(t, "bare", "-r", "o/r")

	human, code := run(t, "show", bare)
	if code != 0 {
		t.Fatalf("show exit %d:\n%s", code, human)
	}
	if strings.Contains(human, "deps:") {
		t.Errorf("a task with no deps must print no deps line:\n%s", human)
	}
	if strings.Contains(human, "epic:") {
		t.Errorf("an unfiled task must print no epic line:\n%s", human)
	}
}

// A CLOSED box is annotated and an open one is not — dueDetail's rule, spending
// the width on the abnormal state. An open box is what every filed task is
// supposed to have, so saying so on every read would be noise.
func TestCLIShowAnnotatesOnlyAClosedEpic(t *testing.T) {
	initStore(t)
	box := addEpic(t, "retired box", "-r", "o/r")
	filed := addTask(t, "member", "-r", "o/r", "-e", box)

	human, _ := run(t, "show", filed)
	if !strings.Contains(human, "epic:     "+box+"  retired box\n") {
		t.Errorf("an open box carries no state annotation:\n%s", human)
	}

	if out, code := run(t, "epic", "done", box); code != 0 {
		t.Fatalf("epic done exit %d:\n%s", code, out)
	}
	human, _ = run(t, "show", filed)
	if !strings.Contains(human, "epic:     "+box+"  retired box  (closed)\n") {
		t.Errorf("a closed box must say so — an open member of one is a defect lint owns:\n%s", human)
	}
}

// `show --json` gained the two keys `ls --json` has always carried, computed by
// the same app helper. The inverse of t-pqme: there the machine readers were
// already served and only the human rows moved; here `show --json` named a
// task's deps as bare ids and nothing else, so "can this move?" had no answer on
// the JSON side of the one read that gives a whole task.
func TestCLIShowJSONCarriesTheDerivedFactsLsAlreadyHad(t *testing.T) {
	initStore(t)
	upstream := addTask(t, "upstream", "-r", "o/r", "-s", "icebox")
	subject := addTask(t, "downstream", "-r", "o/r", "-s", "ready")
	if out, code := run(t, "dep", subject, upstream); code != 0 {
		t.Fatalf("dep exit %d:\n%s", code, out)
	}

	type facts struct {
		ID         string   `json:"id"`
		Actionable bool     `json:"actionable"`
		BlockedBy  []string `json:"blocked_by"`
	}
	// Every --json shape of `show` carries them: with a body, without one, and
	// with backlinks. A shape that dropped them would be the divergence this
	// change exists to close.
	for _, args := range [][]string{
		{"--json", "show", subject},
		{"--json", "show", subject, "--no-body"},
		{"--json", "show", subject, "--backlinks"},
		{"--json", "show", subject, "--no-body", "--backlinks"},
	} {
		out, code := run(t, args...)
		if code != 0 {
			t.Fatalf("%v exit %d:\n%s", args, code, out)
		}
		var got []facts
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("parse %v: %v\n%s", args, err, out)
		}
		if len(got) != 1 {
			t.Fatalf("%v must be a one-element array, got %d:\n%s", args, len(got), out)
		}
		if got[0].Actionable {
			t.Errorf("%v: a task waiting on an icebox dep is not actionable:\n%s", args, out)
		}
		if len(got[0].BlockedBy) != 1 || got[0].BlockedBy[0] != upstream {
			t.Errorf("%v: blocked_by must name the undone dep, got %v:\n%s", args, got[0].BlockedBy, out)
		}
	}

	// And it must AGREE with `ls --json` on the same task — one helper, so the
	// two reads cannot drift.
	lsOut, _ := run(t, "--json", "ls", "-q", "id:"+subject)
	var rows []facts
	if err := json.Unmarshal([]byte(lsOut), &rows); err != nil {
		t.Fatalf("parse ls --json: %v\n%s", err, lsOut)
	}
	if len(rows) != 1 || rows[0].Actionable || len(rows[0].BlockedBy) != 1 {
		t.Fatalf("ls --json disagrees with show --json:\n%s", lsOut)
	}

	// A task with no deps carries blocked_by [] — never null, the house rule
	// every other view's derived-facts slice already follows.
	clean := addTask(t, "clean", "-r", "o/r", "-s", "ready")
	out, _ := run(t, "--json", "show", clean, "--no-body")
	if !strings.Contains(out, `"blocked_by": []`) {
		t.Errorf("blocked_by must be [] not null:\n%s", out)
	}
	if !strings.Contains(out, `"actionable": true`) {
		t.Errorf("a ready task with no deps is actionable:\n%s", out)
	}
}

// A dep the hot store cannot resolve keeps `dep --list`'s `[?]`: the edge is
// reported rather than dropped, and lint's dep-missing is what calls it a
// defect. `dep` refuses to CREATE one, so the reachable way in is to retire the
// target — archiving it leaves the edge behind, pointing out of the store.
func TestCLIShowReportsADanglingDepRatherThanDroppingIt(t *testing.T) {
	initStore(t)
	subject := addTask(t, "downstream", "-r", "o/r")
	real := addTask(t, "upstream", "-r", "o/r")
	if out, code := run(t, "dep", subject, real); code != 0 {
		t.Fatalf("dep exit %d:\n%s", code, out)
	}
	// Retire the dep out of the hot store: the edge survives, its target does
	// not — a dangling id without hand-editing a shard.
	if out, code := run(t, "set", real, "-s", "done"); code != 0 {
		t.Fatalf("set done exit %d:\n%s", code, out)
	}
	if out, code := run(t, "archive", real, "--yes"); code != 0 {
		t.Fatalf("archive exit %d:\n%s", code, out)
	}

	human, code := run(t, "show", subject)
	if code != 0 {
		t.Fatalf("show exit %d:\n%s", code, human)
	}
	if !strings.Contains(human, "  "+real+"  [?]  \n") {
		t.Errorf("a dep naming nothing in the hot store keeps dep --list's [?]:\n%s", human)
	}
	depList, _ := run(t, "dep", subject, "--list")
	if !strings.Contains(depList, "  "+real+"  [?]  \n") {
		t.Errorf("dep --list must render the same dangling row:\n%s", depList)
	}
}

// `show --archived` answers from the ARCHIVE SNAPSHOT ALONE, so it agrees with
// `ls --archived` about the same task. Resolving dep edges across both stores
// was built first and measured: with an archived task whose only dep was done
// and still hot, `ls --archived` said blocked_by [that dep] while
// `show --archived` said [], and the human block printed `deps: 1/1 done`
// where the archive alone says 0/1. Two reads disagreeing about one task is the
// defect this change exists to close, so `[?]` is the accepted cost — the
// honest answer for a snapshot read, and the one `dep --list` gives too (it
// will not resolve an archived id at all).
func TestCLIShowArchivedAgreesWithLsArchived(t *testing.T) {
	initStore(t)
	hotDep := addTask(t, "still hot", "-r", "o/r")
	retired := addTask(t, "retired", "-r", "o/r")
	if out, code := run(t, "dep", retired, hotDep); code != 0 {
		t.Fatalf("dep exit %d:\n%s", code, out)
	}
	if out, code := run(t, "set", retired, "-s", "done"); code != 0 {
		t.Fatalf("set done exit %d:\n%s", code, out)
	}
	if out, code := run(t, "archive", retired, "--yes"); code != 0 {
		t.Fatalf("archive exit %d:\n%s", code, out)
	}
	// The dep stays hot AND becomes done — the shape that split the two reads.
	if out, code := run(t, "set", hotDep, "-s", "done"); code != 0 {
		t.Fatalf("set dep done exit %d:\n%s", code, out)
	}

	type facts struct {
		ID         string   `json:"id"`
		Actionable bool     `json:"actionable"`
		BlockedBy  []string `json:"blocked_by"`
	}
	lsOut, code := run(t, "--json", "ls", "--archived")
	if code != 0 {
		t.Fatalf("ls --archived exit %d:\n%s", code, lsOut)
	}
	var rows []facts
	if err := json.Unmarshal([]byte(lsOut), &rows); err != nil {
		t.Fatalf("parse ls --archived: %v\n%s", err, lsOut)
	}
	var want *facts
	for i := range rows {
		if rows[i].ID == retired {
			want = &rows[i]
		}
	}
	if want == nil {
		t.Fatalf("the archived task must appear in ls --archived:\n%s", lsOut)
	}

	showOut, code := run(t, "--json", "show", retired, "--archived", "--no-body")
	if code != 0 {
		t.Fatalf("show --archived exit %d:\n%s", code, showOut)
	}
	var got []facts
	if err := json.Unmarshal([]byte(showOut), &got); err != nil {
		t.Fatalf("parse show --archived: %v\n%s", err, showOut)
	}
	if len(got) != 1 {
		t.Fatalf("show --archived must be a one-element array:\n%s", showOut)
	}
	if got[0].Actionable != want.Actionable || !slices.Equal(got[0].BlockedBy, want.BlockedBy) {
		t.Errorf("show --archived and ls --archived disagree about %s: show %+v vs ls %+v", retired, got[0], *want)
	}

	// And the human block's tally is the same answer, not a second one: the dep
	// is outside this snapshot, so it is unresolved and unsatisfied.
	human, _ := run(t, "show", retired, "--archived")
	if !strings.Contains(human, "deps:     0/1 done\n") {
		t.Errorf("the tally counts from the archive snapshot, like blocked_by:\n%s", human)
	}
	if !strings.Contains(human, "  "+hotDep+"  [?]  \n") {
		t.Errorf("a dep outside the snapshot reads [?], as it does in dep --list:\n%s", human)
	}
	// A live id is still NOT findable through --archived.
	if _, code := run(t, "show", hotDep, "--archived"); code == 0 {
		t.Errorf("--archived must not hand back a live task")
	}
}

// `brief --json`'s next[] rows embed show's taskView. Widening THAT type to
// carry show's derived facts silently added actionable/blocked_by to brief —
// built without the facts, so every pick read `false` and `null` although a
// next pick is actionable by construction and the house rule is [] never null.
// `show` owns showTaskView; taskView stays the shared two-field shape.
func TestCLIBriefNextRowsDidNotInheritShowsDerivedFacts(t *testing.T) {
	initStore(t)
	box := addEpic(t, "focus box", "-r", "o/r")
	if out, code := run(t, "epic", "activate", box); code != 0 {
		t.Fatalf("epic activate exit %d:\n%s", code, out)
	}
	addTask(t, "pick me", "-r", "o/r", "-s", "ready", "-e", box)

	out, code := run(t, "--json", "brief")
	if code != 0 {
		t.Fatalf("brief exit %d:\n%s", code, out)
	}
	var v struct {
		Next []map[string]any `json:"next"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("parse brief --json: %v\n%s", err, out)
	}
	if len(v.Next) == 0 {
		t.Fatalf("the fixture must produce a next pick:\n%s", out)
	}
	for _, key := range []string{"actionable", "blocked_by"} {
		if _, ok := v.Next[0][key]; ok {
			t.Errorf("brief's next row must not carry %q — that is show's shape, not brief's:\n%s", key, out)
		}
	}
	if _, ok := v.Next[0]["body_text"]; !ok {
		t.Errorf("brief's next row must still carry body_text:\n%s", out)
	}
}

// `show --archived` resolves the box title too. Boxes are never archived, so the
// membership resolves against the one live epic store — without it the archived
// read printed a bare id while the hot read printed the title, which is the
// two-shapes-of-one-command split this change exists to remove.
func TestCLIShowArchivedResolvesTheEpicTitle(t *testing.T) {
	initStore(t)
	box := addEpic(t, "shipped box", "-r", "o/r")
	retired := addTask(t, "retired member", "-r", "o/r", "-e", box)
	if out, code := run(t, "set", retired, "-s", "done"); code != 0 {
		t.Fatalf("set done exit %d:\n%s", code, out)
	}
	if out, code := run(t, "archive", retired, "--yes"); code != 0 {
		t.Fatalf("archive exit %d:\n%s", code, out)
	}

	human, code := run(t, "show", retired, "--archived")
	if code != 0 {
		t.Fatalf("show --archived exit %d:\n%s", code, human)
	}
	if !strings.Contains(human, "epic:     "+box+"  shipped box\n") {
		t.Errorf("the archived read resolves the box title like the hot read:\n%s", human)
	}
}

// Reading a TASK never touched epics/ before the box title was added to the
// `epic:` line — so an unreadable box store must not turn `show <task>`, the
// most-used read furrow has, into an exit 2. It degrades to the bare id it
// printed for nine schema versions. The reads whose ANSWER is the box store
// still fail: `epic ls`, and `show` given a box ref.
func TestCLIShowDegradesToABareEpicIdWhenTheBoxStoreIsUnreadable(t *testing.T) {
	initStore(t)
	box := addEpic(t, "readable box", "-r", "o/r")
	filed := addTask(t, "member", "-r", "o/r", "-e", box)

	if human, _ := run(t, "show", filed); !strings.Contains(human, "epic:     "+box+"  readable box\n") {
		t.Fatalf("fixture: the title must resolve before the shard is corrupted:\n%s", human)
	}

	corrupt := filepath.Join(os.Getenv(app.EnvDir), "epics", "e-bad1.json")
	if err := os.WriteFile(corrupt, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	human, code := run(t, "show", filed)
	if code != 0 {
		t.Errorf("a corrupt box shard must not fail a task read (exit %d):\n%s", code, human)
	}
	if !strings.Contains(human, "epic:     "+box+"\n") {
		t.Errorf("the epic line falls back to the bare id:\n%s", human)
	}

	// The reads the box store IS the answer to still surface it.
	if _, code := run(t, "epic", "ls"); code == 0 {
		t.Errorf("epic ls must still report an unreadable shard")
	}
	if _, code := run(t, "show", box); code == 0 {
		t.Errorf("show given a BOX ref must still report an unreadable shard")
	}
}

// The tally's word is the board's DONE LANE, not the literal "done". Measured
// on a board that renamed it: `deps: 1/2 done` printed above a `[shipped]` row,
// so the tally disagreed with the rows it was counting. stateGlyph reads
// Cfg.DoneLane for the same reason, and blocked_by is computed against it.
func TestCLIShowDepsTallyNamesTheBoardsDoneLane(t *testing.T) {
	initStore(t)
	// `config set` is STRICT — it refuses a write the reader would clamp — so
	// the lane is renamed in the order that keeps every other key valid at each
	// step: widen the order, move done, move terminal, then narrow the order.
	for _, kv := range [][2]string{
		{"lanes.order", "inbox,backlog,ready,in-progress,waiting,done,shipped,icebox"},
		{"lanes.done", "shipped"},
		{"lanes.terminal", "shipped,icebox,waiting"},
		{"lanes.order", "inbox,backlog,ready,in-progress,waiting,shipped,icebox"},
	} {
		if out, code := run(t, "config", "set", kv[0], kv[1]); code != 0 {
			t.Fatalf("config set %s exit %d:\n%s", kv[0], code, out)
		}
	}
	settled := addTask(t, "settled", "-r", "o/r")
	pending := addTask(t, "pending", "-r", "o/r")
	subject := addTask(t, "subject", "-r", "o/r")
	if out, code := run(t, "dep", subject, settled, pending); code != 0 {
		t.Fatalf("dep exit %d:\n%s", code, out)
	}
	if out, code := run(t, "set", settled, "-s", "shipped"); code != 0 {
		t.Fatalf("set shipped exit %d:\n%s", code, out)
	}

	human, code := run(t, "show", subject)
	if code != 0 {
		t.Fatalf("show exit %d:\n%s", code, human)
	}
	if !strings.Contains(human, "deps:     1/2 shipped\n") {
		t.Errorf("the tally must name the board's done lane, not the word \"done\":\n%s", human)
	}
	if strings.Contains(human, "1/2 done") {
		t.Errorf("the tally must not hardcode \"done\" over a renamed lane:\n%s", human)
	}
}

// The claim the docs and the cobra Long both make: `show --no-body --json` is
// key-for-key an `ls --json` row. Pinned on the KEY LIST, not just the values,
// because that is what lets a caller use the lean batch read in place of a
// listing. The new keys APPEND in every other shape, so `body_text` keeps the
// position it had.
func TestCLIShowNoBodyJSONIsKeyForKeyAnLsRow(t *testing.T) {
	initStore(t)
	dep := addTask(t, "dep", "-r", "o/r")
	subject := addTask(t, "subject", "-r", "o/r", "-s", "ready")
	if out, code := run(t, "dep", subject, dep); code != 0 {
		t.Fatalf("dep exit %d:\n%s", code, out)
	}

	keys := func(raw string, pick func(map[string]any) bool) []string {
		t.Helper()
		var rows []map[string]any
		if err := json.Unmarshal([]byte(raw), &rows); err != nil {
			t.Fatalf("parse: %v\n%s", err, raw)
		}
		for _, r := range rows {
			if pick(r) {
				out := make([]string, 0, len(r))
				for k := range r {
					out = append(out, k)
				}
				sort.Strings(out)
				return out
			}
		}
		t.Fatalf("no matching row:\n%s", raw)
		return nil
	}
	isSubject := func(r map[string]any) bool { return r["id"] == subject }

	lsOut, _ := run(t, "--json", "ls")
	showOut, _ := run(t, "--json", "show", subject, "--no-body")
	if got, want := keys(showOut, isSubject), keys(lsOut, isSubject); !slices.Equal(got, want) {
		t.Errorf("show --no-body --json is not key-for-key an ls row:\n show %v\n ls   %v", got, want)
	}

	// With a body, the derived keys append: body_text keeps its place.
	full, _ := run(t, "--json", "show", subject)
	bodyAt := strings.Index(full, `"body_text"`)
	factAt := strings.Index(full, `"actionable"`)
	if bodyAt < 0 || factAt < 0 || bodyAt > factAt {
		t.Errorf("the derived keys must come after body_text, not displace it:\n%s", full)
	}
}
