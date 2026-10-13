package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// previewBoard seeds the four row shapes a set selection can meet: dated,
// undated, repeating, and a follower of a box with a day. Returns the ids and
// the box.
func previewBoard(t *testing.T) (dated, undated, repeating, follower, box string) {
	t.Helper()
	initStore(t)
	box = addEpicWithAnchor(t, "会場", "2026-11-21")
	dated = addTask(t, "dated", "-e", box, "--due", "2026-10-12")
	undated = addTask(t, "undated", "-e", box)
	repeating = addTask(t, "weekly", "-e", box, "--due", "2026-10-05", "--repeat", "weekly")
	follower = addTask(t, "follower", "-e", box, "--due", "2026-11-07", "--anchor", box)
	return
}

// The preview's contract, as a table: what a selection preview shows at exit
// 0 with no refused row, --yes writes at exit 0; what it marks refused, --yes
// refuses at exit 2; and a flag the apply would refuse on its own (a box that
// does not exist, a rule that does not parse) is refused BEFORE the preview —
// the preview used to say "would set N" for `-e nosuchbox` and `--repeat
// bogus`, and only --yes said no.
func TestSetSelectionPreviewNeverPromisesWhatYesRefuses(t *testing.T) {
	type row struct {
		name  string
		pick  func(d, u, r, f string) string // which ids the selection names
		flags []string
	}
	all := func(d, u, r, f string) string { return d + "," + u + "," + r + "," + f }
	rows := []row{
		{"lane", all, []string{"-s", "ready"}},
		{"value", all, []string{"--value", "3"}},
		{"label", all, []string{"--add-label", "x"}},
		{"due", all, []string{"--due", "2026-12-01"}},
		{"clear-due", all, []string{"--clear-due"}},
		{"shift over dated", func(d, u, r, f string) string { return d + "," + r + "," + f }, []string{"--due-shift", "+7d"}},
		{"shift over undated", all, []string{"--due-shift", "+7d"}},
		{"anchor over dated", func(d, u, r, f string) string { return d }, []string{"--anchor", "会場"}},
		{"anchor over repeating", func(d, u, r, f string) string { return d + "," + r }, []string{"--anchor", "会場"}},
		{"anchor over undated", func(d, u, r, f string) string { return d + "," + u }, []string{"--anchor", "会場"}},
		{"anchor with due over undated", func(d, u, r, f string) string { return d + "," + u }, []string{"--anchor", "会場", "--due", "2026-12-01"}},
		{"repeat over dated", func(d, u, r, f string) string { return d }, []string{"--repeat", "weekly"}},
		{"repeat over follower", func(d, u, r, f string) string { return d + "," + f }, []string{"--repeat", "weekly"}},
		{"repeat over follower with clear-anchor", func(d, u, r, f string) string { return d + "," + f }, []string{"--repeat", "weekly", "--clear-anchor"}},
		{"repeat over undated", func(d, u, r, f string) string { return d + "," + u }, []string{"--repeat", "weekly"}},
		{"shift and anchor over dated", func(d, u, r, f string) string { return d }, []string{"--due-shift", "+7d", "--anchor", "会場"}},
		{"shift and anchor over repeating", func(d, u, r, f string) string { return d + "," + r }, []string{"--due-shift", "+7d", "--anchor", "会場"}},
		{"shift and anchor over undated", func(d, u, r, f string) string { return d + "," + u }, []string{"--due-shift", "+7d", "--anchor", "会場"}},
	}
	for _, rw := range rows {
		d, u, r, f, _ := previewBoard(t)
		sel := "id:" + rw.pick(d, u, r, f)
		args := append([]string{"--json", "set", "-q", sel}, rw.flags...)
		out, code := run(t, args...)
		if code != 0 {
			t.Errorf("%s: the preview itself exited %d:\n%s", rw.name, code, out)
			continue
		}
		var preview struct {
			DryRun  bool     `json:"dry_run"`
			Refused []string `json:"refused"`
		}
		if err := json.Unmarshal([]byte(out), &preview); err != nil || !preview.DryRun {
			t.Errorf("%s: preview = %s (%v)", rw.name, out, err)
			continue
		}
		yesOut, yesCode := run(t, append(args, "--yes")...)
		switch {
		case len(preview.Refused) == 0 && yesCode != 0:
			t.Errorf("%s: the preview promised the write (no refused) but --yes exited %d:\n%s", rw.name, yesCode, yesOut)
		case len(preview.Refused) > 0 && yesCode != 2:
			t.Errorf("%s: the preview refused %v but --yes exited %d:\n%s", rw.name, preview.Refused, yesCode, yesOut)
		}
	}

	// Flag-level refusals come before any preview.
	d, _, _, _, _ := previewBoard(t)
	for _, flags := range [][]string{
		{"-e", "nosuchbox"}, {"--repeat", "bogus rule"}, {"--anchor", "nosuchbox"}, {"--due", "2026-13-45"}, {"-s", "nolane"},
		{"--due-shift", "+7d", "--due", "2026-12-01"}, {"--anchor", "会場", "--repeat", "weekly"},
	} {
		fe, out := runErr(t, append([]string{"set", "-q", "id:" + d}, flags...)...)
		if fe == nil || fe.Code != core.CodeValidation || strings.Contains(out, "would set") {
			t.Errorf("set -q … %v must be refused before the preview, got %+v\n%s", flags, fe, out)
		}
	}
}

// The JSON preview of a shift names where each row would land, as the human
// arrow does: `moves` {id, from, to}.
func TestSetDueShiftPreviewJSONNamesTheLanding(t *testing.T) {
	d, u, _, _, _ := previewBoard(t)
	out := mustRun(t, "--json", "set", "-q", "id:"+d+","+u, "--due-shift", "+7d")
	var preview struct {
		DryRun  bool     `json:"dry_run"`
		Refused []string `json:"refused"`
		Moves   []struct {
			ID   string `json:"id"`
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"moves"`
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil || !preview.DryRun {
		t.Fatalf("preview = %s (%v)", out, err)
	}
	if len(preview.Moves) != 1 || preview.Moves[0].ID != d || !strings.HasPrefix(preview.Moves[0].From, "2026-10-12") || !strings.HasPrefix(preview.Moves[0].To, "2026-10-19") {
		t.Errorf("moves = %+v; want the dated row from 2026-10-12 to 2026-10-19", preview.Moves)
	}
	if len(preview.Refused) != 1 || preview.Refused[0] != u {
		t.Errorf("refused = %v; want the undated row", preview.Refused)
	}
}

// A rule bound onto a follower is refused naming the POINTER as the remedy
// (--clear-anchor); the message used to say --clear-repeat, the rule the
// caller had just asked for. The batch names every follower up front.
func TestSetRepeatOnAFollowerNamesThePointer(t *testing.T) {
	d, _, _, f, _ := previewBoard(t)
	fe, _ := runErr(t, "set", f, "--repeat", "weekly")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "--clear-anchor") || strings.Contains(fe.Msg, "--clear-repeat") {
		t.Errorf("single follower: %+v", fe)
	}
	fe, _ = runErr(t, "set", d, f, "--repeat", "weekly")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "1 of 2") {
		t.Fatalf("batch with a follower: %+v", fe)
	}
	if details, _ := fe.Details.(map[string]any); details == nil || details["anchored"] == nil {
		t.Errorf("batch refusal must name the followers in details.anchored: %+v", fe.Details)
	}
	// Dropping the pointer in the same write is the remedy the message names.
	if out, code := run(t, "set", f, "--repeat", "weekly", "--clear-anchor"); code != 0 {
		t.Errorf("--repeat with --clear-anchor on a follower: %d\n%s", code, out)
	}
}
