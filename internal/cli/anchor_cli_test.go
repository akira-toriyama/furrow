package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// anchorBoard seeds the reschedule drill in miniature: a box with a day, a
// follower in that box and one in another box (one day serves several boxes),
// a dated task the other side fixed (no pointer), and a done follower whose
// due is history. Returns the box id and the four task ids in that order.
func anchorBoard(t *testing.T) (epic string, follower, crossFollower, fixed, done string) {
	t.Helper()
	initStore(t)
	mustRun(t, "config", "set", "due.timezone", "Asia/Tokyo")
	epic = addEpicWithAnchor(t, "会場", "2026-11-21")
	other := addEpicWithAnchor(t, "献立", "")
	follower = addTask(t, "申込書を出す", "-e", epic, "--due", "2026-11-07T13:20", "--anchor", epic)
	crossFollower = addTask(t, "試食会", "-e", other, "--due", "2026-11-14", "--anchor", epic)
	fixed = addTask(t, "会場の入金期限（先方指定）", "-e", epic, "--due", "2026-10-12")
	done = addTask(t, "済んだやつ", "-e", epic, "--due", "2026-10-01", "--anchor", epic)
	mustRun(t, "done", done)
	return
}

func addEpicWithAnchor(t *testing.T, title, day string) string {
	t.Helper()
	args := []string{"--json", "epic", "add", title, "-r", "o/r"}
	if day != "" {
		args = append(args, "--anchor", day)
	}
	out := mustRun(t, args...)
	var e struct {
		ID     string `json:"id"`
		Anchor string `json:"anchor"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("epic add --json: %v\n%s", err, out)
	}
	if e.Anchor != day {
		t.Fatalf("epic add stored anchor %q, want %q", e.Anchor, day)
	}
	return e.ID
}

type anchorEnvelope struct {
	Changed []string `json:"changed"`
	Anchor  *struct {
		Epic  string `json:"epic"`
		From  string `json:"from"`
		To    string `json:"to"`
		Days  int    `json:"days"`
		Moves []struct {
			ID   string `json:"id"`
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"moves"`
		Kept      []string `json:"kept"`
		Followers []string `json:"followers"`
	} `json:"anchor"`
	DryRun bool `json:"dry_run"`
}

// The reschedule: the day moves a week, every open follower's due moves with
// it — the cross-box one too — the fixed date and the done follower stay, and
// nothing lands until --yes. What the preview shows is what --yes writes.
func TestEpicSetAnchorMovesFollowersAfterPreview(t *testing.T) {
	epic, follower, cross, fixed, done := anchorBoard(t)

	preview := mustRun(t, "epic", "set", epic, "--anchor", "2026-11-28")
	for _, want := range []string{
		"would move 2 due(s) that follow " + epic + ": anchor 2026-11-21 → 2026-11-28 (+7d)",
		follower + "  [inbox] 申込書を出す  due 2026-11-07 13:20 → 2026-11-14 13:20",
		cross + "  [inbox] 試食会  due 2026-11-14 23:59 → 2026-11-21 23:59",
		"1 done follower(s) keep their due (history): " + done,
		"re-run with --yes to apply",
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lacks %q:\n%s", want, preview)
		}
	}
	if show := mustRun(t, "show", follower, "--no-body"); !strings.Contains(show, "due:      2026-11-07 13:20") {
		t.Errorf("the preview wrote the due:\n%s", show)
	}
	out := mustRun(t, "--json", "epic", "set", epic, "--anchor", "2026-11-28")
	var dry anchorEnvelope
	if err := json.Unmarshal([]byte(out), &dry); err != nil {
		t.Fatalf("preview --json: %v\n%s", err, out)
	}
	if !dry.DryRun || dry.Anchor == nil || len(dry.Anchor.Moves) != 2 || dry.Anchor.Days != 7 || len(dry.Anchor.Kept) != 1 {
		t.Fatalf("preview object = %s", out)
	}
	for _, m := range dry.Anchor.Moves {
		if !strings.HasSuffix(m.From, "Z") || !strings.HasSuffix(m.To, "Z") {
			t.Errorf("a move's stamps must both be UTC instants: %+v", m)
		}
	}

	out = mustRun(t, "--json", "epic", "set", epic, "--anchor", "2026-11-28", "--yes")
	var env anchorEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("apply --json: %v\n%s", err, out)
	}
	if env.DryRun || strings.Join(env.Changed, ",") != "anchor" || env.Anchor == nil || len(env.Anchor.Moves) != 2 {
		t.Fatalf("apply envelope = %s", out)
	}
	if env.Anchor.Moves[0].To != "2026-11-14T04:20:00Z" {
		t.Errorf("13:20 JST must stay 13:20 JST a week later: %s", env.Anchor.Moves[0].To)
	}
	show := mustRun(t, "show", follower, "--no-body")
	for _, want := range []string{"due:      2026-11-14 13:20 +09:00", "anchor:   " + epic + "  会場  2026-11-28  (due at D-14)"} {
		if !strings.Contains(show, want) {
			t.Errorf("show after the move lacks %q:\n%s", want, show)
		}
	}
	if show := mustRun(t, "show", fixed, "--no-body"); !strings.Contains(show, "due:      2026-10-12 23:59") || strings.Contains(show, "anchor:") {
		t.Errorf("the fixed date moved, or grew a pointer:\n%s", show)
	}
	if show := mustRun(t, "show", done, "--no-body"); !strings.Contains(show, "due:      2026-10-01 23:59") {
		t.Errorf("the done follower's due is history and moved:\n%s", show)
	}

	// The same day again is a no-op: changed is empty and nothing previews.
	out = mustRun(t, "--json", "epic", "set", epic, "--anchor", "2026-11-28")
	var again anchorEnvelope
	if err := json.Unmarshal([]byte(out), &again); err != nil || again.DryRun || len(again.Changed) != 0 {
		t.Errorf("a same-day set must be an applied no-op: %s (%v)", out, err)
	}
	if human := mustRun(t, "epic", "set", epic, "--anchor", "2026-11-28"); !strings.Contains(human, "anchor 2026-11-28 (unchanged)") {
		t.Errorf("the human line should say the day is unchanged:\n%s", human)
	}
}

// A clear keeps the followers' pointers and says so — on stderr, in the
// envelope, and then in lint — never silently unmarks them.
func TestEpicClearAnchorDisclosesFollowers(t *testing.T) {
	epic, follower, cross, _, _ := anchorBoard(t)
	stdout, stderr := mustSplit(t, "epic", "set", epic, "--clear-anchor")
	if !strings.Contains(stdout, "anchor cleared (was 2026-11-21)") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "note: 2 task(s) still follow "+epic) || !strings.Contains(stderr, "--clear-anchor --yes") {
		t.Errorf("stderr should name the followers left behind and the fix:\n%s", stderr)
	}
	out := mustRun(t, "--json", "epic", "set", epic, "--anchor", "2026-11-21")
	var env anchorEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Anchor == nil || env.Anchor.From != "" || len(env.Anchor.Moves) != 0 {
		t.Errorf("re-setting the day is a first set and moves nothing: %s (%v)", out, err)
	}
	out = mustRun(t, "--json", "epic", "set", epic, "--clear-anchor")
	var cleared anchorEnvelope
	if err := json.Unmarshal([]byte(out), &cleared); err != nil || cleared.Anchor == nil {
		t.Fatalf("clear --json: %v\n%s", err, out)
	}
	if cleared.Anchor.From != "2026-11-21" || cleared.Anchor.To != "" || len(cleared.Anchor.Moves) != 0 {
		t.Errorf("a clear reports the day it dropped and moves nothing: %s", out)
	}
	if got := strings.Join(cleared.Anchor.Followers, ","); got != follower+","+cross && got != cross+","+follower {
		t.Errorf("the envelope's followers must name the two tasks left pointing at the box, got %q", got)
	}
	lint, code := run(t, "lint", "--code", "anchor-unset")
	if code != 0 {
		t.Fatalf("anchor-unset is a warn, exit %d:\n%s", code, lint)
	}
	for _, id := range []string{follower, cross} {
		if !strings.Contains(lint, "anchor-unset  "+id) {
			t.Errorf("lint does not name %s:\n%s", id, lint)
		}
	}
	if show := mustRun(t, "show", follower, "--no-body"); !strings.Contains(show, "anchor:   "+epic+"  会場  (no day set on the box)") {
		t.Errorf("show should say the box has no day:\n%s", show)
	}
}

// The task side's refusals, each exit 2 with the ids a fix needs: no due
// (unless the same write promises one), a repeat rule, a day where the box
// was expected, and --yes / an empty value with nothing behind them.
func TestSetAnchorRefusals(t *testing.T) {
	freezeClock(t)
	epic, follower, _, _, _ := anchorBoard(t)
	undated := addTask(t, "undated", "-e", epic)
	repeating := addTask(t, "weekly", "-e", epic, "--due", "2026-10-05", "--repeat", "weekly")

	fe, _ := runErr(t, "set", undated, "--anchor", epic)
	if fe == nil || fe.Code != 2 || fe.Subject != undated {
		t.Fatalf("undated: %v", fe)
	}
	if d, _ := fe.Details.(map[string]any); d == nil || d["undated"] == nil {
		t.Errorf("undated refusal carries no details.undated: %v", fe.Details)
	}
	mustRun(t, "set", undated, "--anchor", epic, "--due", "2026-11-20")
	if show := mustRun(t, "show", undated, "--no-body"); !strings.Contains(show, "(due at D-1)") {
		t.Errorf("--due and --anchor in one write:\n%s", show)
	}

	fe, _ = runErr(t, "set", repeating, "--anchor", epic)
	if fe == nil || fe.Code != 2 {
		t.Fatalf("repeating: %v", fe)
	}
	if d, _ := fe.Details.(map[string]any); d == nil || d["repeating"] == nil {
		t.Errorf("repeating refusal carries no details.repeating: %v", fe.Details)
	}

	fe, _ = runErr(t, "set", follower, "--anchor", "2026-11-28")
	if fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "furrow epic set <epic> --anchor 2026-11-28") {
		t.Errorf("a day where the box goes should say where the day lives: %v", fe)
	}
	bare := addEpicWithAnchor(t, "no day", "")
	fe, _ = runErr(t, "set", follower, "--anchor", bare)
	if fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "has no anchor yet") {
		t.Errorf("a box with no day: %v", fe)
	}
	if fe, _ := runErr(t, "set", follower, "--anchor", ""); fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "--clear-anchor") {
		t.Errorf("an empty --anchor must be exit 2 naming the clear: %v", fe)
	}
	if fe, _ := runErr(t, "epic", "set", epic, "--yes", "--goal", "x"); fe == nil || fe.Code != 2 {
		t.Errorf("--yes without --anchor: %v", fe)
	}
	if fe, _ := runErr(t, "epic", "set", epic, "--anchor", "2026-13-45"); fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "YYYY-MM-DD") {
		t.Errorf("a bad day: %v", fe)
	}

	// The batch is all-or-nothing: the dated follower is not re-marked when
	// the undated one refuses, and a -q preview marks the refused rows.
	fresh := addTask(t, "fresh", "-e", epic)
	fe, _ = runErr(t, "set", follower, fresh, "--anchor", epic)
	if fe == nil || fe.Code != 2 {
		t.Fatalf("batch with an undated id: %v", fe)
	}
	preview := mustRun(t, "set", "-q", "epic:"+epic, "-r", "", "--anchor", epic)
	if !strings.Contains(preview, "would set 0 of") || !strings.Contains(preview, fresh+"  [inbox] fresh  no due — refused") ||
		!strings.Contains(preview, repeating+"  [inbox] weekly  due 2026-10-05 23:59  follows its own rule — refused  repeats") {
		t.Errorf("preview must mark the rows --yes would refuse:\n%s", preview)
	}
}

// --clear-due takes the pointer with it (nothing left to follow);
// --clear-anchor leaves the due; the envelope's changed names both.
func TestClearDueClearsAnchor(t *testing.T) {
	epic, follower, cross, _, _ := anchorBoard(t)
	out := mustRun(t, "--json", "set", follower, "--clear-due")
	var envs []struct {
		Changed []string `json:"changed"`
		After   struct {
			Anchor string `json:"anchor"`
			Due    string `json:"due"`
		} `json:"after"`
	}
	if err := json.Unmarshal([]byte(out), &envs); err != nil || len(envs) != 1 {
		t.Fatalf("set --json: %v\n%s", err, out)
	}
	if strings.Join(envs[0].Changed, ",") != "due,anchor" || envs[0].After.Anchor != "" {
		t.Errorf("clear-due envelope = %+v", envs[0])
	}
	out = mustRun(t, "--json", "set", cross, "--clear-anchor")
	if err := json.Unmarshal([]byte(out), &envs); err != nil || len(envs) != 1 {
		t.Fatalf("set --json: %v\n%s", err, out)
	}
	if strings.Join(envs[0].Changed, ",") != "anchor" || envs[0].After.Due == "" {
		t.Errorf("clear-anchor envelope = %+v", envs[0])
	}
	if ls := mustRun(t, "ls", "-q", "anchor:"+epic, "-r", "", "-n", "0"); strings.Contains(ls, follower) || strings.Contains(ls, cross) {
		t.Errorf("both pointers should be gone:\n%s", ls)
	}
}

// The selectors and the rows: anchor:<epic> and has:anchor are the moving
// side, has:due no:anchor the fixed side, and the box's day rides the epic
// rows of ls and brief.
func TestAnchorSelectorsAndRows(t *testing.T) {
	epic, follower, cross, fixed, done := anchorBoard(t)
	mustRun(t, "epic", "activate", epic)
	following := mustRun(t, "ls", "-q", "anchor:"+epic, "-r", "", "-n", "0")
	for _, id := range []string{follower, cross, done} {
		if !strings.Contains(following, id) {
			t.Errorf("anchor:%s lacks %s:\n%s", epic, id, following)
		}
	}
	if strings.Contains(following, fixed) {
		t.Errorf("anchor: selected the fixed date:\n%s", following)
	}
	// The drill's exact miss (t-5mcm): a unique title substring resolves as -e
	// resolves it — the same rows, not 0 rows at exit 0.
	if bySub := mustRun(t, "ls", "-q", "anchor:会場", "-r", "", "-n", "0"); bySub != following {
		t.Errorf("anchor:会場 should list what anchor:%s lists:\n%s\n---\n%s", epic, bySub, following)
	}
	if fe, _ := runErr(t, "ls", "-q", "anchor:e-nope0"); fe == nil || fe.Code != 2 || fe.Kind != "epic-not-found" || len(fe.Candidates) == 0 {
		t.Errorf("anchor: on an unknown box is exit 2 epic-not-found with candidates: %+v", fe)
	}
	fixedRows := mustRun(t, "ls", "-q", "has:due no:anchor", "-r", "", "-n", "0")
	if !strings.Contains(fixedRows, fixed) || strings.Contains(fixedRows, follower) {
		t.Errorf("has:due no:anchor is the fixed side:\n%s", fixedRows)
	}
	if rows := mustRun(t, "ls", "-q", "has:anchor", "-r", "", "-n", "0"); !strings.Contains(rows, cross) || strings.Contains(rows, fixed) {
		t.Errorf("has:anchor:\n%s", rows)
	}
	if fe, _ := runErr(t, "ls", "-q", "anchor:>=2026"); fe == nil || fe.Code != 2 {
		t.Errorf("anchor: is equality-only: %v", fe)
	}
	if brief := mustRun(t, "brief"); !strings.Contains(brief, "epic: ▶ "+epic+"  1/3  会場  anchor 2026-11-21") {
		t.Errorf("brief's epic row should carry the day:\n%s", brief)
	}
	if ls := mustRun(t, "epic", "ls"); !strings.Contains(ls, "会場  ⚠ stuck  anchor 2026-11-21") && !strings.Contains(ls, "会場  anchor 2026-11-21") {
		t.Errorf("epic ls row should carry the day:\n%s", ls)
	}
	if show := mustRun(t, "epic", "show", epic, "--no-body"); !strings.Contains(show, "anchor:   2026-11-21") {
		t.Errorf("epic show:\n%s", show)
	}
	if help := mustRun(t, "set", "--help"); !strings.Contains(help, "--anchor e-v0zdq") || !strings.Contains(help, "has:due no:anchor") {
		t.Errorf("set --help lost its anchor example:\n%s", help)
	}
}

// A batch line takes the pointer as `anchor`, under the same two invariants
// as the flag, and epic rm counts followers as references.
func TestAnchorBatchAndRm(t *testing.T) {
	epic, _, _, _, _ := anchorBoard(t)
	lines := `{"title": "batch one", "due": "2026-11-10", "anchor": "` + epic + `"}` + "\n"
	out, code := runIn(t, lines, "--json", "add", "--batch", "-", "-e", epic)
	if code != 0 || !strings.Contains(out, `"anchor": "`+epic+`"`) {
		t.Fatalf("batch add exit %d:\n%s", code, out)
	}
	bad := `{"title": "batch bad", "anchor": "` + epic + `"}` + "\n"
	if fe, _ := runErrIn(t, bad, "add", "--batch", "-", "-e", epic); fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "--anchor needs a --due") {
		t.Errorf("an undated batch line must be refused: %v", fe)
	}
	fe, _ := runErr(t, "epic", "rm", epic)
	if fe == nil || fe.Kind != "referenced" || !strings.Contains(fe.Msg, "follower(s) of its anchor") {
		t.Errorf("epic rm should count followers: %v", fe)
	}
	other := addEpicWithAnchor(t, "elsewhere", "2026-12-01")
	mover := addTask(t, "moves boxes", "-e", other, "--due", "2026-11-30", "--anchor", other)
	mustRun(t, "epic", "rm", other, "--force", "--yes")
	if show := mustRun(t, "show", mover, "--no-body"); strings.Contains(show, "anchor:") || !strings.Contains(show, "due:      2026-11-30") {
		t.Errorf("--force must unpoint the follower and keep its due:\n%s", show)
	}
}

// detailIDs is a refusal's details.<key> as the ids it names.
func detailIDs(fe *core.Error, key string) []string {
	d, _ := fe.Details.(map[string]any)
	ids, _ := d[key].([]string)
	return ids
}

// taskAnchor is the box a task's due follows, as the shard holds it.
func taskAnchor(t *testing.T, id string) string {
	t.Helper()
	var tasks []struct {
		Anchor string `json:"anchor"`
	}
	if out := mustRun(t, "--json", "show", id); json.Unmarshal([]byte(out), &tasks) != nil || len(tasks) != 1 {
		t.Fatalf("show --json %s:\n%s", id, out)
	}
	return tasks[0].Anchor
}

// An undated box is the one --anchor refusal whose remedy is a write to
// another object, so a set reports it after every refusal of the task's own:
// its not-found, its missing due, its repeat rule. Reported first, it sent the
// operator to date the box, and the retry then refused on the task (t-91gm).
func TestSetAnchorUndatedBoxComesLast(t *testing.T) {
	freezeClock(t)
	initStore(t)
	box := addEpicWithAnchor(t, "no day", "")
	repeating := addTask(t, "weekly", "-e", box, "--due", "2026-10-05", "--repeat", "weekly")
	plain := addTask(t, "plain", "-e", box, "--due", "2026-10-06")
	plain2 := addTask(t, "plain two", "-e", box, "--due", "2026-10-07")
	undated := addTask(t, "undated", "-e", box)

	for _, ids := range [][]string{{repeating}, {repeating, plain}} {
		fe, _ := runErr(t, append(append([]string{"set"}, ids...), "--anchor", box)...)
		if fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "--clear-repeat") {
			t.Fatalf("set %v --anchor <undated box> must refuse the repeat first: %v", ids, fe)
		}
		if got := detailIDs(fe, "repeating"); !slices.Equal(got, []string{repeating}) {
			t.Errorf("set %v: details.repeating = %v, want [%s]", ids, fe.Details, repeating)
		}
		for _, id := range ids {
			if a := taskAnchor(t, id); a != "" {
				t.Errorf("set %v refused but %s now follows %q", ids, id, a)
			}
		}
	}
	fe, _ := runErr(t, "set", undated, "--anchor", box)
	if fe == nil || fe.Code != 2 || !slices.Equal(detailIDs(fe, "undated"), []string{undated}) {
		t.Errorf("an undated task must be refused for its due first: %v", fe)
	}
	fe, _ = runErr(t, "set", "t-zzzzz", "--anchor", box)
	if fe == nil || fe.Code != 1 || fe.Kind != core.KindNotFound {
		t.Errorf("a missing task must be not-found before the box: %v", fe)
	}

	// The control: once the task side holds (or this write cures it), the
	// box's missing day is what is left, on both paths.
	for _, args := range [][]string{
		{plain},
		{plain, plain2},
		{repeating, "--clear-repeat"},
		{undated, "--due", "2026-11-20"},
	} {
		fe, _ := runErr(t, append(append([]string{"set"}, args...), "--anchor", box)...)
		if fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "epic "+box+" has no anchor yet") {
			t.Errorf("set %v --anchor <undated box>: %v", args, fe)
		}
	}
}

// add and a --batch line share the order: the spec's own due and repeat
// refusals first, the box's missing day last.
func TestAddAnchorUndatedBoxComesLast(t *testing.T) {
	initStore(t)
	box := addEpicWithAnchor(t, "no day", "")
	if fe, _ := runErr(t, "add", "x", "-e", box, "--anchor", box); fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "--anchor needs a --due") {
		t.Errorf("add --anchor without --due: %v", fe)
	}
	line := `{"title": "batch", "due": "2026-11-10", "repeat": "weekly", "anchor": "` + box + `"}` + "\n"
	if fe, _ := runErrIn(t, line, "add", "--batch", "-", "-e", box); fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "cannot ride together") {
		t.Errorf("a batch line with anchor+repeat+due: %v", fe)
	}
	if fe, _ := runErr(t, "add", "x", "-e", box, "--due", "2026-11-10", "--anchor", box); fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "epic "+box+" has no anchor yet") {
		t.Errorf("add --due --anchor <undated box>: %v", fe)
	}
}

// A -q preview shows the rows --yes would refuse (exit 0) before it says the
// box has no day; with no refused row, and on an empty selection, the box is
// the refusal (exit 2).
func TestSetSelectAnchorUndatedBoxPreviewsRefusedRows(t *testing.T) {
	freezeClock(t)
	initStore(t)
	box := addEpicWithAnchor(t, "no day", "")
	repeating := addTask(t, "weekly", "-e", box, "--due", "2026-10-05", "--repeat", "weekly")
	addTask(t, "plain", "-e", box, "--due", "2026-10-06")

	out, code := run(t, "set", "-q", "has:repeat", "-r", "", "--anchor", box)
	if code != 0 || !strings.Contains(out, "would set 0 of 1") || !strings.Contains(out, repeating+"  [inbox] weekly  due 2026-10-05 23:59  follows its own rule — refused") {
		t.Errorf("preview exit %d must mark the repeating row:\n%s", code, out)
	}
	fe, _ := runErr(t, "set", "-q", "has:repeat", "-r", "", "--anchor", box, "--yes")
	if fe == nil || fe.Code != 2 || !slices.Equal(detailIDs(fe, "repeating"), []string{repeating}) {
		t.Errorf("--yes must refuse the row the preview marked: %v", fe)
	}
	for _, q := range []string{"no:repeat", "has:anchor"} {
		if fe, _ := runErr(t, "set", "-q", q, "-r", "", "--anchor", box); fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "epic "+box+" has no anchor yet") {
			t.Errorf("set -q %s --anchor <undated box>: %v", q, fe)
		}
	}
}

// One id or several, the box's missing day comes after every refusal of a
// task's own — including the per-row ones a bulk set judges inside its loop
// (a --due-shift past year 9999, an unknown --add-repo). Judged before that
// loop, a bulk set reported the box while one id reported the row, and a
// selection previewed the row as refused while --yes refused on the box.
func TestSetAnchorUndatedBoxComesAfterEveryRow(t *testing.T) {
	freezeClock(t)
	initStore(t)
	box := addEpicWithAnchor(t, "no day", "")
	far := addTask(t, "far", "-e", box, "--due", "9999-12-25", "-l", "edge")
	near := addTask(t, "near", "-e", box, "--due", "2026-12-10", "-l", "edge")

	for _, ids := range [][]string{{far}, {far, near}} {
		fe, _ := runErr(t, append(append([]string{"set"}, ids...), "--due-shift", "+30d", "--anchor", box)...)
		if fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "year 10000") {
			t.Errorf("set %v --due-shift +30d --anchor <undated box>: want the shift's own refusal, got %v", ids, fe)
		}
		fe, _ = runErr(t, append(append([]string{"set"}, ids...), "--add-repo", "nosuchrepo", "--anchor", box)...)
		if fe == nil || fe.Kind == core.KindValidation && strings.Contains(fe.Msg, "has no anchor yet") {
			t.Errorf("set %v --add-repo nosuchrepo --anchor <undated box>: want the repo's own refusal, got %v", ids, fe)
		}
	}
	// The preview marks the row, and --yes refuses on that row, not the box.
	if out, code := run(t, "set", "-q", "label:edge", "--due-shift", "+30d", "--anchor", box); code != 0 || !strings.Contains(out, far) {
		t.Errorf("the preview should show the refused row at exit 0, got exit %d:\n%s", code, out)
	}
	fe, _ := runErr(t, "set", "-q", "label:edge", "--due-shift", "+30d", "--anchor", box, "--yes")
	if fe == nil || !strings.Contains(fe.Msg, "year 10000") {
		t.Errorf("--yes should refuse on the row the preview marked, got %v", fe)
	}
	// With nothing refused on the task side, the box is what is left, for a
	// bulk set too, and for a selection that matches nothing.
	fe, _ = runErr(t, "set", far, near, "--due", "2026-12-20", "--anchor", box)
	if fe == nil || fe.Code != 2 || !strings.Contains(fe.Msg, "has no anchor yet") {
		t.Errorf("a bulk set with every row clean should refuse the box, got %v", fe)
	}
	if fe, _ = runErr(t, "set", "-q", "label:nosuchlabel", "--due", "2026-12-20", "--anchor", box, "--yes"); fe == nil || fe.Code != 2 {
		t.Errorf("an empty --yes selection should still refuse the undated box, got %v", fe)
	}
	for _, id := range []string{far, near} {
		if a := taskAnchor(t, id); a != "" {
			t.Errorf("%s follows %q after refused writes", id, a)
		}
	}
}
