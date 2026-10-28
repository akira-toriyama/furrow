package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The previous line shows the UTC activation stamp in local time with its
// offset (humanTime), while --json keeps the stamp as recorded: printed bare,
// a JST operator's 17:51 switch read as 08:51 (t-8gmb).
func TestEpicPreviousLineShowsLocalTime(t *testing.T) {
	pinLocalTZ(t, 9*3600)
	initStore(t)
	ea := addEpic(t, "box a", "-r", "me/r1")
	eb := addEpic(t, "box b", "-r", "me/r1")
	mustRun(t, "epic", "activate", ea)
	mustRun(t, "epic", "deactivate", ea)
	mustRun(t, "epic", "activate", eb)

	jgot, code := run(t, "--json", "epic", "deactivate", eb)
	if code != 0 {
		t.Fatalf("epic deactivate --json exit = %d:\n%s", code, jgot)
	}
	var env struct {
		Previous *struct {
			At string `json:"at"`
		} `json:"previous"`
	}
	if err := json.Unmarshal([]byte(jgot), &env); err != nil || env.Previous == nil {
		t.Fatalf("parse previous: %v\n%s", err, jgot)
	}
	stamp, err := time.ParseInLocation("2006-01-02 15:04", env.Previous.At, time.UTC)
	if err != nil {
		t.Fatalf("--json at = %q, want the recorded UTC stamp (YYYY-MM-DD HH:MM)", env.Previous.At)
	}

	mustRun(t, "epic", "activate", eb)
	got, code := run(t, "epic", "deactivate", eb)
	if code != 0 {
		t.Fatalf("epic deactivate exit = %d:\n%s", code, got)
	}
	want := "(activated " + stamp.In(time.FixedZone("TST", 9*3600)).Format("2006-01-02 15:04 -07:00") + ")"
	if !strings.Contains(got, want) {
		t.Errorf("previous line should carry %q (local time with offset):\n%s", want, got)
	}
}

// epic done/deactivate suggest the previous active box: one human line and a
// `previous` key in --json — computed from the activation log, never executed.
func TestEpicDoneSuggestsPreviousActive(t *testing.T) {
	initStore(t)
	ea := addEpic(t, "box a", "-r", "me/r1")
	eb := addEpic(t, "box b", "-r", "me/r1")
	mustRun(t, "epic", "activate", ea)
	mustRun(t, "epic", "deactivate", ea)
	mustRun(t, "epic", "activate", eb)

	got, code := run(t, "epic", "done", eb)
	if code != 0 {
		t.Fatalf("epic done exit = %d:\n%s", code, got)
	}
	if !strings.Contains(got, "previous: "+ea) || !strings.Contains(got, "furrow epic activate "+ea) {
		t.Errorf("done should suggest %s with the activate command:\n%s", ea, got)
	}
	st, _ := run(t, "--json", "epic", "show", ea)
	if !strings.Contains(st, `"active": false`) {
		t.Errorf("suggestion must never activate; epic show:\n%s", st)
	}
}

func TestEpicDeactivateSuggestsPreviousActiveJSON(t *testing.T) {
	initStore(t)
	ea := addEpic(t, "box a", "-r", "me/r1")
	eb := addEpic(t, "box b", "-r", "me/r1")
	mustRun(t, "epic", "activate", ea)
	mustRun(t, "epic", "deactivate", ea)
	mustRun(t, "epic", "activate", eb)

	got, code := run(t, "--json", "epic", "deactivate", eb)
	if code != 0 {
		t.Fatalf("epic deactivate exit = %d:\n%s", code, got)
	}
	var env struct {
		Previous *struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			At    string `json:"at"`
		} `json:"previous"`
	}
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, got)
	}
	if env.Previous == nil || env.Previous.ID != ea || env.Previous.Title != "box a" || env.Previous.At == "" {
		t.Errorf("previous = %+v, want %s 'box a' with a stamp", env.Previous, ea)
	}
}

// No record anywhere: the human line says unknown, the JSON key is null (still
// PRESENT, so "computed, no answer" differs from an older binary's absence).
func TestEpicDonePreviousUnknown(t *testing.T) {
	initStore(t)
	ea := addEpic(t, "box a", "-r", "me/r1")

	got, code := run(t, "epic", "done", ea)
	if code != 0 {
		t.Fatalf("epic done exit = %d:\n%s", code, got)
	}
	if !strings.Contains(got, "previous: unknown") {
		t.Errorf("done with no records should say unknown:\n%s", got)
	}

	eb := addEpic(t, "box b", "-r", "me/r1")
	jgot, _ := run(t, "--json", "epic", "done", eb)
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jgot), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, jgot)
	}
	raw, ok := env["previous"]
	if !ok {
		t.Fatalf("previous key must be present:\n%s", jgot)
	}
	if string(raw) != "null" {
		t.Errorf("previous = %s, want null", raw)
	}
}

// Closing a parked box while another box holds its repo says none and names
// the holder (that repo's focus never left); a box with no repo says why it
// is unknown. --json keeps previous null for both.
func TestEpicDonePreviousSaysWhyThereIsNone(t *testing.T) {
	initStore(t)
	ea := addEpic(t, "box a", "-r", "me/r1")
	eb := addEpic(t, "box b", "-r", "me/r1")
	ec := addEpic(t, "box c", "-r", "me/r1")
	mustRun(t, "epic", "activate", ea)
	mustRun(t, "epic", "deactivate", ea)
	mustRun(t, "epic", "activate", eb)

	got, code := run(t, "epic", "done", ec)
	if code != 0 {
		t.Fatalf("epic done exit = %d:\n%s", code, got)
	}
	if want := "previous: none (still held: me/r1 by " + eb + " — nothing to return to)"; !strings.Contains(got, want) {
		t.Errorf("done on a parked box beside an active one should say %q:\n%s", want, got)
	}
	jgot, code := run(t, "--json", "epic", "deactivate", ea)
	if code != 0 {
		t.Fatalf("epic deactivate exit = %d:\n%s", code, jgot)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jgot), &env); err != nil {
		t.Fatalf("parse envelope: %v\n%s", err, jgot)
	}
	if raw, ok := env["previous"]; !ok || string(raw) != "null" {
		t.Errorf("previous = %s (present %v), want null", raw, ok)
	}

	en := addEpic(t, "box n")
	var shown struct {
		Repos []string `json:"repos"`
	}
	if out, _ := run(t, "--json", "epic", "show", en); json.Unmarshal([]byte(out), &shown) != nil || len(shown.Repos) != 0 {
		t.Fatalf("fixture: box n should have no repo:\n%s", out)
	}
	got, code = run(t, "epic", "done", en)
	if code != 0 {
		t.Fatalf("epic done exit = %d:\n%s", code, got)
	}
	if !strings.Contains(got, "previous: unknown (this box has no repo") {
		t.Errorf("done on a repo-less box should say it has no repo:\n%s", got)
	}
}
