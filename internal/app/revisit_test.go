package app

import (
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/config"
	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// revisitApp builds an app with a mutable clock so tests can age tasks.
func codesByID(items []RevisitItem) map[string][]string {
	m := map[string][]string{}
	for _, it := range items {
		cs := make([]string, len(it.Reasons))
		for i, r := range it.Reasons {
			cs[i] = r.Code
		}
		sort.Strings(cs)
		m[it.Task.ID] = cs
	}
	return m
}

// Three of the epic signals, and the two states that must stay QUIET.
//
// epic_all_done and epic_stuck are mutually exclusive by construction (all-done
// short-circuits). epic_stale is orthogonal and rides along: an ACTIVE box can be
// both blocked and forgotten, and that is worth saying twice.
func TestRevisitEpicSignals(t *testing.T) {
	a, clk := revisitApp()
	mk := func(title string, o AddOpts) string {
		task, err := a.Add(title, o)
		if err != nil {
			t.Fatal(err)
		}
		return task.ID
	}
	allDone := mustEpic(t, a, "box all done", EpicAddOpts{})
	mk("done member", AddOpts{Epic: allDone, Status: "done"})
	stuck := mustEpic(t, a, "box stuck", EpicAddOpts{Repos: []string{"o/s"}})
	mk("backlog member", AddOpts{Epic: stuck, Status: "backlog"}) // open, but backlog is not a next lane
	mustActivate(t, a, stuck)
	parked := mustEpic(t, a, "box parked", EpicAddOpts{Repos: []string{"o/p"}})
	mk("backlog member", AddOpts{Epic: parked, Status: "backlog"})
	empty := mustEpic(t, a, "box empty", EpicAddOpts{})

	sum, err := a.RevisitSummary(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	has := func(ids []string, id string) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}
	if !has(sum.EpicAllDone, allDone) || has(sum.EpicStuck, allDone) {
		t.Errorf("all-done box: want epic_all_done only; done=%v stuck=%v", sum.EpicAllDone, sum.EpicStuck)
	}
	if !has(sum.EpicStuck, stuck) || has(sum.EpicAllDone, stuck) {
		t.Errorf("stuck box: want epic_stuck only; done=%v stuck=%v", sum.EpicAllDone, sum.EpicStuck)
	}
	if has(sum.EpicAllDone, parked) || has(sum.EpicStuck, parked) {
		t.Errorf("a box next does not read must get NEITHER signal — it is waiting its turn; done=%v stuck=%v", sum.EpicAllDone, sum.EpicStuck)
	}
	if has(sum.EpicAllDone, empty) || has(sum.EpicStuck, empty) {
		t.Error("an empty box (zero members) must get NEITHER signal — declaring it first is legitimate")
	}

	// epic_stale fires only for the ACTIVE box: a parked epic is SUPPOSED to sit
	// untouched, so flagging it would make the signal meaningless.
	active := mustEpic(t, a, "the active box", EpicAddOpts{Repos: []string{"o/r"}})
	mk("live member", AddOpts{Epic: active, Status: "ready"})
	mustActivate(t, a, active)
	clk.t = clk.t.AddDate(0, 0, 60)

	sum, err = a.RevisitSummary(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !has(sum.EpicStale, active) {
		t.Errorf("an active box untouched for 60d must be epic_stale; got %v", sum.EpicStale)
	}
	if has(sum.EpicStale, parked) {
		t.Errorf("a NON-active box must never be epic_stale (parked is not forgotten); got %v", sum.EpicStale)
	}
}

// epic_all_done means every member is in the done lane, and epic_stuck means a
// box `next` reads hands out nothing. Members parked outside the done lane are
// neither: they make a box next reads stuck, and leave any other box quiet.
func TestRevisitBoxSignalsFollowWhatNextReads(t *testing.T) {
	a := newApp()
	box := func(title, repo string, lanes ...string) string {
		id := mustEpic(t, a, title, EpicAddOpts{Repos: []string{repo}})
		for _, lane := range lanes {
			mustAdd(t, a, title+" "+lane, AddOpts{Epic: id, Status: lane})
		}
		return id
	}
	activeLeftover := box("active leftover", "o/a", "done", "icebox")
	mustActivate(t, a, activeLeftover)
	parkedLeftover := box("parked leftover", "o/b", "done", "icebox")
	pinned := box("pinned", "o/c", "backlog")
	setFlag(t, a, pinned, nil, ptr(true))
	activeDone := box("active done", "o/d", "done", "done")
	mustActivate(t, a, activeDone)
	closed := box("closed", "o/e", "backlog")
	mustEpicDone(t, a, closed)
	activeLive := box("active live", "o/f", "ready", "icebox")
	mustActivate(t, a, activeLive)

	items, err := a.RevisitEpics(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		for _, r := range it.Reasons {
			got[it.Epic.ID] += r.Code + ": " + r.Detail + "\n"
		}
	}
	want := map[string]string{
		activeLeftover: "epic_stuck: no open members, and 1 of 2 parked rather than done\n",
		pinned:         "epic_stuck: 1 open members but none actionable\n",
		activeDone:     "epic_all_done: all 2 members done — consider closing\n",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: reasons = %q, want %q", id, got[id], w)
		}
	}
	for _, id := range []string{parkedLeftover, closed, activeLive} {
		if got[id] != "" {
			t.Errorf("%s must raise no box signal, got %q", id, got[id])
		}
	}

	// The flag every read prints comes from the same definition.
	for id, wantStuck := range map[string]bool{activeLeftover: true, pinned: true, activeDone: false, parkedLeftover: false, activeLive: false} {
		if row := rowOf(t, a, id); row.Stuck != wantStuck {
			t.Errorf("%s: epic ls stuck = %v, want %v", row.Epic.Title, row.Stuck, wantStuck)
		}
	}
	all, err := a.EpicList(EpicQueryOpts{All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range all {
		if it.Epic.ID == closed && it.Stuck {
			t.Error("a closed box is not read by next, so it must not be stuck")
		}
	}
}

// The summary's stale count skips the members of a box waiting its turn; the
// revisit read lists them all the same.
func TestRevisitSummaryStaleSkipsParkedBoxMembers(t *testing.T) {
	a, clk := revisitApp()
	member := func(title, repo string) (box, task string) {
		box = mustEpic(t, a, title, EpicAddOpts{Repos: []string{repo}})
		return box, mustAdd(t, a, title+" member", AddOpts{Epic: box, Status: "backlog", Repos: []string{repo}}).ID
	}
	_, parkedTask := member("parked", "o/a")
	active, activeTask := member("active", "o/b")
	mustActivate(t, a, active)
	pinned, pinnedTask := member("pinned", "o/c")
	setFlag(t, a, pinned, nil, ptr(true))
	standing, standingTask := member("standing", "o/d")
	setFlag(t, a, standing, ptr(true), nil)
	closed, closedTask := member("closed", "o/e")
	mustEpicDone(t, a, closed)
	unfiled := mustAdd(t, a, "unfiled", AddOpts{Status: "backlog", Repos: []string{"o/f"}}).ID
	clk.t = clk.t.AddDate(0, 0, 60)

	sum, err := a.RevisitSummary(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{activeTask, pinnedTask, standingTask, closedTask, unfiled}
	sort.Strings(want)
	got := slices.Clone(sum.Stale)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Errorf("summary stale = %v, want %v (everything but the parked box's %s)", got, want, parkedTask)
	}

	items, err := a.Revisit(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if codes := codesByID(items)[parkedTask]; !slices.Contains(codes, "stale") {
		t.Errorf("revisit must still list the parked box's member as stale, got %v", codes)
	}
}

func TestRevisitSignalsAndExclusions(t *testing.T) {
	a, clk := revisitApp()

	// At T0: a dependency we will finish, and a stale (but estimated) task.
	dep, _ := a.Add("dep", AddOpts{Status: "ready", Value: ptr(1), Effort: ptr(1)})
	a.Done(dep.ID) // dep -> done lane (terminal)
	stale, _ := a.Add("stale", AddOpts{Status: "ready", Value: ptr(3), Effort: ptr(2), Repos: []string{"o/r"}})
	a.Add("parked", AddOpts{Status: "icebox"}) // terminal; unset estimates

	clk.t = clk.t.AddDate(0, 0, 60)
	fresh, _ := a.Add("fresh-needs-est", AddOpts{Status: "ready", Repos: []string{"o/r"}}) // value+effort unset, fresh
	user, _ := a.Add("dep-user", AddOpts{Status: "ready", Value: ptr(3), Effort: ptr(2), Repos: []string{"o/r"}, Deps: []string{dep.ID}})

	items, err := a.Revisit(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	got := codesByID(items)

	if want := []string{"stale"}; !slices.Equal(got[stale.ID], want) {
		t.Errorf("stale task: codes = %v, want %v", got[stale.ID], want)
	}
	if want := []string{"effort_unset", "value_unset"}; !slices.Equal(got[fresh.ID], want) {
		t.Errorf("fresh-needs-est: codes = %v, want %v", got[fresh.ID], want)
	}
	if want := []string{"dep_done"}; !slices.Equal(got[user.ID], want) {
		t.Errorf("dep-user: codes = %v, want %v", got[user.ID], want)
	}
	// terminal (done dep, icebox parked) must never surface.
	if _, ok := got[dep.ID]; ok {
		t.Errorf("done dep must be excluded, got %v", got[dep.ID])
	}
	if len(items) != 3 {
		t.Errorf("expected exactly 3 items (stale, fresh, dep-user), got %d: %v", len(items), got)
	}
}

func TestRevisitStaleDaysZeroDisablesStale(t *testing.T) {
	a, clk := revisitApp()
	st, _ := a.Add("old-but-estimated", AddOpts{Status: "ready", Value: ptr(3), Effort: ptr(2), Repos: []string{"o/r"}})
	clk.t = clk.t.AddDate(0, 0, 90)

	items, _ := a.Revisit(QueryOpts{}, 0) // staleDays 0 disables stale
	if got := codesByID(items); len(got[st.ID]) != 0 {
		t.Errorf("with staleDays=0 the estimated task should have no reasons, got %v", got[st.ID])
	}
}

func TestRevisitLabelFilterAndLimit(t *testing.T) {
	a, _ := revisitApp()
	a.Add("furrow task", AddOpts{Status: "ready", Labels: []string{"furrow"}})
	a.Add("chord task", AddOpts{Status: "ready", Labels: []string{"chord"}})

	only, _ := a.Revisit(QueryOpts{Label: "furrow"}, 30)
	if len(only) != 1 || only[0].Task.Labels[0] != "furrow" {
		t.Errorf("label filter should keep only furrow, got %+v", only)
	}

	a.Add("furrow task 2", AddOpts{Status: "ready", Labels: []string{"furrow"}})
	limited, _ := a.Revisit(QueryOpts{Label: "furrow", Limit: 1}, 30)
	if len(limited) != 1 {
		t.Errorf("limit=1 should return 1 item, got %d", len(limited))
	}
}

func TestRevisitCanonicalOrderAndLimitIdentity(t *testing.T) {
	a, _ := revisitApp()
	// Added out of canonical order; all unestimated so all surface. Canonical
	// order is lane-rank -> priority -> id: ready (rank 2) sorts before
	// in-progress (rank 3), and within ready the sparse priority orders them.
	ip, _ := a.Add("in progress", AddOpts{Status: "in-progress"})
	r1, _ := a.Add("ready a", AddOpts{Status: "ready"}) // priority 100
	r2, _ := a.Add("ready b", AddOpts{Status: "ready"}) // priority 110

	items, err := a.Revisit(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	gotOrder := make([]string, len(items))
	for i, it := range items {
		gotOrder[i] = it.Task.ID
	}
	if want := []string{r1.ID, r2.ID, ip.ID}; !slices.Equal(gotOrder, want) {
		t.Errorf("revisit order = %v, want canonical %v", gotOrder, want)
	}

	// limit must return the canonical-FIRST item, not just any one.
	one, _ := a.Revisit(QueryOpts{Limit: 1}, 30)
	if len(one) != 1 || one[0].Task.ID != r1.ID {
		t.Errorf("limit=1 should return canonical-first %s, got %+v", r1.ID, one)
	}
}

func TestRevisitSummaryCountsScopedDepDoneAndStale(t *testing.T) {
	a, clk := revisitApp()

	// A dependency we finish, plus a stale in-scope task at T0.
	dep, _ := a.Add("dep", AddOpts{Status: "ready", Value: ptr(1), Effort: ptr(1), Repos: []string{"o/r"}})
	a.Done(dep.ID) // -> done lane (terminal)
	staleIn, _ := a.Add("stale-in", AddOpts{Status: "ready", Value: ptr(3), Effort: ptr(2), Repos: []string{"o/r"}})
	a.Add("stale-other", AddOpts{Status: "ready", Value: ptr(3), Effort: ptr(2), Repos: []string{"x/y"}}) // other repo
	a.Add("stale-draft", AddOpts{Status: "ready", Value: ptr(3), Effort: ptr(2)})                         // draft (no repo)
	a.Add("parked", AddOpts{Status: "icebox", Repos: []string{"o/r"}})                                    // terminal

	// Age everything 60d, then a fresh dependent (in scope) whose dep is done.
	clk.t = clk.t.AddDate(0, 0, 60)
	user, _ := a.Add("dep-user", AddOpts{Status: "ready", Value: ptr(3), Effort: ptr(2), Repos: []string{"o/r"}, Deps: []string{dep.ID}})

	sum, err := a.RevisitSummary(QueryOpts{ScopeRepo: "o/r"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{user.ID}; !slices.Equal(sum.DepDone, want) {
		t.Errorf("DepDone = %v, want %v", sum.DepDone, want)
	}
	// Only the in-scope stale task: other-repo, draft, terminal, and the fresh
	// dependent (updated 0d ago) are all excluded.
	if want := []string{staleIn.ID}; !slices.Equal(sum.Stale, want) {
		t.Errorf("Stale = %v, want %v", sum.Stale, want)
	}
	if sum.Empty() {
		t.Error("summary should not be Empty")
	}
}

func TestRevisitSummaryStaleDaysZeroDisablesStale(t *testing.T) {
	a, clk := revisitApp()
	a.Add("old", AddOpts{Status: "ready", Value: ptr(1), Effort: ptr(1), Repos: []string{"o/r"}})
	clk.t = clk.t.AddDate(0, 0, 90)

	sum, err := a.RevisitSummary(QueryOpts{ScopeRepo: "o/r"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Stale) != 0 || !sum.Empty() {
		t.Errorf("staleDays=0 must disable stale; got %+v", sum)
	}
}

// TestRevisitSummaryMultiAndOverlap covers two gaps in the single-id-per-list
// coverage above: DepDone must return >1 id in canonical (priority then id)
// order, and a task that is BOTH stale and has a done dependency must appear
// in both DepDone and Stale (the two loops in RevisitSummary are independent,
// not mutually exclusive).
func TestRevisitSummaryMultiAndOverlap(t *testing.T) {
	a, clk := revisitApp()

	dep, _ := a.Add("dep", AddOpts{Status: "ready", Value: ptr(1), Effort: ptr(1), Repos: []string{"o/r"}})
	a.Done(dep.ID) // -> done lane (terminal)

	// A dependent added at T0, aged 60d with nothing touching it afterwards:
	// it goes stale AND has a done dep, so it must land in BOTH lists.
	overlap, _ := a.Add("dep-user-overlap", AddOpts{Status: "ready", Priority: ptr(20), Value: ptr(3), Effort: ptr(2), Repos: []string{"o/r"}, Deps: []string{dep.ID}})

	clk.t = clk.t.AddDate(0, 0, 60) // past the 30d threshold

	// Two more dependents added AFTER the age jump (so they stay fresh — NOT
	// stale), out of priority order, to isolate the DepDone-order check from
	// staleness. Canonical order is lane-rank -> priority -> id.
	second, _ := a.Add("dep-user-2", AddOpts{Status: "ready", Priority: ptr(50), Value: ptr(3), Effort: ptr(2), Repos: []string{"o/r"}, Deps: []string{dep.ID}})
	first, _ := a.Add("dep-user-1", AddOpts{Status: "ready", Priority: ptr(10), Value: ptr(3), Effort: ptr(2), Repos: []string{"o/r"}, Deps: []string{dep.ID}})

	sum, err := a.RevisitSummary(QueryOpts{ScopeRepo: "o/r"}, 30)
	if err != nil {
		t.Fatal(err)
	}

	// Canonical order is lane-rank -> priority -> id: first(10) < overlap(20) < second(50).
	if want := []string{first.ID, overlap.ID, second.ID}; !slices.Equal(sum.DepDone, want) {
		t.Errorf("DepDone order = %v, want canonical %v", sum.DepDone, want)
	}
	// Only the aged task is stale — the two fresh dependents are not.
	if want := []string{overlap.ID}; !slices.Equal(sum.Stale, want) {
		t.Errorf("Stale = %v, want %v", sum.Stale, want)
	}

	// The overlap task must be present in BOTH lists (not exclusively one).
	inList := func(ids []string, id string) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}
	if !inList(sum.DepDone, overlap.ID) {
		t.Errorf("overlap task %s missing from DepDone: %v", overlap.ID, sum.DepDone)
	}
	if !inList(sum.Stale, overlap.ID) {
		t.Errorf("overlap task %s missing from Stale: %v", overlap.ID, sum.Stale)
	}
}

// The unreviewed repo clock must obey an EXPLICIT -r, not just the board scope.
// cli/scope.go clears ScopeRepo and sets Repo for an explicit -r, so a summary
// that only tested ScopeRepo reported every repo on the board while scoping its
// task signals — `furrow brief -r X` told you repo Y was overdue for review.
func TestRevisitSummaryUnreviewedHonorsExplicitRepo(t *testing.T) {
	a, clk := revisitApp()
	if a.Cfg.ReviewStaleAfterDays <= 0 {
		t.Skip("review staleness disabled in defaults")
	}
	for _, repo := range []string{"o/r", "x/y"} {
		if _, err := a.ReviewRepo(repo, false); err != nil {
			t.Fatal(err)
		}
	}
	clk.t = clk.t.AddDate(0, 0, a.Cfg.ReviewStaleAfterDays+1)

	repos := func(o QueryOpts) []string {
		t.Helper()
		sum, err := a.RevisitSummary(o, a.Cfg.RevisitStaleDays)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, u := range sum.Unreviewed {
			out = append(out, u.Repo)
		}
		return out
	}

	if got := repos(QueryOpts{}); len(got) != 2 {
		t.Errorf("a board-wide summary must report every stale repo, got %v", got)
	}
	if got := repos(QueryOpts{ScopeRepo: "o/r"}); !slices.Equal(got, []string{"o/r"}) {
		t.Errorf("board scope: got %v, want [o/r]", got)
	}
	if got := repos(QueryOpts{Repo: "o/r"}); !slices.Equal(got, []string{"o/r"}) {
		t.Errorf("explicit -r: got %v, want [o/r] — an explicit -r must scope the repo clock too", got)
	}
}

// watchWaiting is the board that asked revisit to keep reading its waiting
// lane ([revisit].watch_lanes = ["waiting"]).
func watchWaiting() appOpt {
	return withCfg(func(c *config.Config) { c.RevisitWatchLanes = map[string]bool{"waiting": true} })
}

// parkedFixture seeds one task per way a parked task can wait, all last
// touched 60 days before now, plus the two terminal lanes nobody asked about.
// The ids name the shape.
func parkedFixture(t *testing.T, a *App) {
	t.Helper()
	now := a.Clock.Now()
	old := now.AddDate(0, 0, -60)
	ahead, arrived := now.AddDate(0, 0, 10), now.AddDate(0, 0, -3)
	rr := []string{"o/r"}
	for _, task := range []core.Task{
		{ID: "t-event", Status: "waiting", Repos: rr, Updated: old},                              // no due, no dep: the task nothing else brings back
		{ID: "t-noted", Status: "waiting", Repos: rr, Updated: now.AddDate(0, 0, -5)},            // same, but someone looked 5 days ago
		{ID: "t-dated", Status: "waiting", Repos: rr, Updated: old, Due: &ahead},                 // waits on a date still ahead
		{ID: "t-late", Status: "waiting", Repos: rr, Updated: old, Due: &arrived},                // its date has come
		{ID: "t-blocked", Status: "waiting", Repos: rr, Updated: old, Deps: []string{"t-open"}},  // waits on a task not yet done
		{ID: "t-chained", Status: "waiting", Repos: rr, Updated: old, Deps: []string{"t-event"}}, // waits on a task that is itself watched
		{ID: "t-iced", Status: "waiting", Repos: rr, Updated: old, Deps: []string{"t-frozen"}},   // waits on a task frozen where nobody looks
		{ID: "t-freed", Status: "waiting", Repos: rr, Updated: old, Deps: []string{"t-closed"}},  // the task it waited on closed
		{ID: "t-frozen", Status: "icebox", Repos: rr, Updated: old, Deps: []string{"t-closed"}},  // a parked lane nobody named
		{ID: "t-open", Status: "ready", Repos: rr, Value: ptr(1), Effort: ptr(1), Updated: now},  // the open dep
		{ID: "t-closed", Status: "done", Repos: rr, Updated: now.AddDate(0, 0, -1), Closed: ptr(now.AddDate(0, 0, -1))},
	} {
		seedTask(t, a, task, "# "+task.ID+"\n")
	}
}

// A board that never set [revisit].watch_lanes reads as it always did: no
// terminal-lane task surfaces, in the list or in the summary.
func TestRevisitSkipsEveryTerminalLaneByDefault(t *testing.T) {
	a, _ := revisitApp()
	parkedFixture(t, a)

	items, err := a.Revisit(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got := codesByID(items); len(got) != 0 {
		t.Errorf("no terminal-lane task may surface by default, got %v", got)
	}
	sum, err := a.RevisitSummary(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Stale) != 0 || len(sum.DepDone) != 0 {
		t.Errorf("default summary must stay empty: stale=%v dep_done=%v", sum.Stale, sum.DepDone)
	}
}

// With the waiting lane named, its tasks raise the two wait signals and only
// those: stale for the wait with no clock of its own, dep_done when the task it
// waited on closes. The estimate signals stay an open task's (every fixture
// task is unestimated), and the lanes nobody named stay out of sight.
func TestRevisitReadsWatchedLanes(t *testing.T) {
	a, _ := newAppWith(at(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)), watchWaiting())
	parkedFixture(t, a)

	items, err := a.Revisit(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"t-event": {"stale"},
		"t-late":  {"stale"},
		"t-freed": {"dep_done", "stale"},
		"t-iced":  {"stale"}, // its dep will never report back, so it must not hold stale back
	}
	if got := codesByID(items); !reflect.DeepEqual(got, want) {
		t.Errorf("revisit = %v, want %v", got, want)
	}

	sum, err := a.RevisitSummary(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"t-event", "t-freed", "t-iced", "t-late"}; !sameIDSet(sum.Stale, want) {
		t.Errorf("summary stale = %v, want %v", sum.Stale, want)
	}
	if want := []string{"t-freed"}; !sameIDSet(sum.DepDone, want) {
		t.Errorf("summary dep_done = %v, want %v", sum.DepDone, want)
	}

	// --stale-days 0 turns stale off for the parked lane as for any other.
	items, err = a.Revisit(QueryOpts{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := codesByID(items), (map[string][]string{"t-freed": {"dep_done"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("revisit with the stale signal off = %v, want %v", got, want)
	}
}

// "Seen it, still waiting" is the answer to a stale parked task: a note
// advances `updated`, and the task goes quiet until the next window.
func TestRevisitParkedStaleClearsOnANote(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)), watchWaiting())
	parkedFixture(t, a)
	if _, err := a.AddNote("t-event", "still waiting — the upstream fix is not out"); err != nil {
		t.Fatal(err)
	}
	stale := func() bool {
		t.Helper()
		items, err := a.Revisit(QueryOpts{}, 30)
		if err != nil {
			t.Fatal(err)
		}
		return slices.Contains(codesByID(items)["t-event"], "stale")
	}
	if stale() {
		t.Error("a note must clear the stale signal")
	}
	// dep_done is not a clock: a note leaves it standing, as on an open task.
	if _, err := a.AddNote("t-freed", "the dep closed, but the release is still not out"); err != nil {
		t.Fatal(err)
	}
	items, err := a.Revisit(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := codesByID(items)["t-freed"], []string{"dep_done"}; !slices.Equal(got, want) {
		t.Errorf("after a note the freed task carries %v, want %v", got, want)
	}
	clk.t = clk.t.AddDate(0, 0, 29)
	if stale() {
		t.Error("the task must stay quiet inside the window")
	}
	clk.t = clk.t.AddDate(0, 0, 1)
	if !stale() {
		t.Error("the task must come back once the window has passed again")
	}
}

// A due only counts as the parked task's own clock in a lane where its arrival
// raises something. icebox is in [due].ignore_lanes on the shipped config, so a
// board that names it gets stale on a dated task there — the date never speaks.
func TestRevisitParkedDueCountsOnlyWhereDuesAreTracked(t *testing.T) {
	a, _ := newAppWith(at(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)), withCfg(func(c *config.Config) {
		c.RevisitWatchLanes = map[string]bool{"waiting": true, "icebox": true}
	}))
	now := a.Clock.Now()
	old, ahead := now.AddDate(0, 0, -60), now.AddDate(0, 0, 10)
	seedTask(t, a, core.Task{ID: "t-wait", Status: "waiting", Updated: old, Due: &ahead}, "# w\n")
	seedTask(t, a, core.Task{ID: "t-ice", Status: "icebox", Updated: old, Due: &ahead}, "# i\n")

	items, err := a.Revisit(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := codesByID(items), (map[string][]string{"t-ice": {"stale"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("revisit = %v, want %v", got, want)
	}
}

// The summary leaves a box-waiting-its-turn's OPEN members out of the stale
// count (they are unstarted work). A member parked in a lane revisit reads
// waits on something outside the box, so it is counted whatever the box is
// doing — the last waiting member of a box nobody reopens is the task this
// read exists for.
func TestRevisitSummaryCountsParkedLaneMembersOfAnyBox(t *testing.T) {
	a, clk := newAppWith(at(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)), watchWaiting())
	member := func(title, repo, lane string) (box, task string) {
		box = mustEpic(t, a, title, EpicAddOpts{Repos: []string{repo}})
		return box, mustAdd(t, a, title+" member", AddOpts{Epic: box, Status: lane, Repos: []string{repo}}).ID
	}
	turnBox, turnWaiting := member("waits its turn", "o/a", "waiting")
	turnBacklog := mustAdd(t, a, "unstarted", AddOpts{Epic: turnBox, Status: "backlog", Repos: []string{"o/a"}}).ID
	active, activeWaiting := member("active", "o/b", "waiting")
	mustActivate(t, a, active)
	closed, closedWaiting := member("closed", "o/c", "waiting")
	mustEpicDone(t, a, closed)
	unfiled := mustAdd(t, a, "unfiled", AddOpts{Status: "waiting", Repos: []string{"o/d"}}).ID
	clk.t = clk.t.AddDate(0, 0, 60)

	sum, err := a.RevisitSummary(QueryOpts{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{turnWaiting, activeWaiting, closedWaiting, unfiled}; !sameIDSet(sum.Stale, want) {
		t.Errorf("summary stale = %v, want %v (every waiting member; not the unstarted %s)", sum.Stale, want, turnBacklog)
	}

	// A scoped read keeps to its repo, parked lane or not.
	sum, err = a.RevisitSummary(QueryOpts{Repo: "o/a"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{turnWaiting}; !sameIDSet(sum.Stale, want) {
		t.Errorf("scoped summary stale = %v, want %v", sum.Stale, want)
	}
}

// sameIDSet compares two id lists as sets.
func sameIDSet(got, want []string) bool {
	g, w := slices.Clone(got), slices.Clone(want)
	sort.Strings(g)
	sort.Strings(w)
	return slices.Equal(g, w)
}

// A parked draft has no repo, so it is in no scope: revisit lists it under any
// scope, as it lists every draft, with its wait signal and not no_repo. The
// scoped summary leaves it out, as it leaves every draft out.
func TestRevisitListsAWatchedParkedDraftUnderAnyScope(t *testing.T) {
	a, _ := newAppWith(at(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)), watchWaiting())
	old := a.Clock.Now().AddDate(0, 0, -60)
	seedTask(t, a, core.Task{ID: "t-draft", Status: "waiting", Updated: old}, "# d\n")
	seedTask(t, a, core.Task{ID: "t-other", Status: "waiting", Repos: []string{"o/other"}, Updated: old}, "# o\n")

	scoped := QueryOpts{Repo: "o/x"}
	items, err := a.Revisit(scoped, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := codesByID(items), (map[string][]string{"t-draft": {"stale"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("scoped revisit = %v, want %v", got, want)
	}
	sum, err := a.RevisitSummary(scoped, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Stale) != 0 {
		t.Errorf("scoped summary stale = %v, want none", sum.Stale)
	}
}

// is:stale is the age test alone. A watched parked task still waiting on a
// read dep matches it and carries no stale signal: -q narrows which flagged
// rows come back, it does not add a signal.
func TestRevisitQueryIsStaleIsTheAgeTestAlone(t *testing.T) {
	a, _ := newAppWith(at(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)), watchWaiting())
	parkedFixture(t, a)
	seedTask(t, a, core.Task{ID: "t-both", Status: "waiting", Repos: []string{"o/r"},
		Updated: a.Clock.Now().AddDate(0, 0, -60), Deps: []string{"t-closed", "t-open"}}, "# b\n")

	items, err := a.Revisit(QueryOpts{Query: "is:stale status:waiting"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	got := codesByID(items)
	if want := []string{"dep_done"}; !slices.Equal(got["t-both"], want) {
		t.Errorf("t-both = %v, want %v (is:stale matched it; the stale signal is held back)", got["t-both"], want)
	}
	if _, ok := got["t-blocked"]; ok {
		t.Errorf("t-blocked is is:stale but raises no signal, so it must not be listed: %v", got)
	}
}
