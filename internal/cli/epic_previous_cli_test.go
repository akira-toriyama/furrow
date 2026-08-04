package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// epicID runs `epic add --json` and returns the created box's id.
func epicID(t *testing.T, args ...string) string {
	t.Helper()
	out, code := run(t, append([]string{"--json", "epic", "add"}, args...)...)
	if code != 0 {
		t.Fatalf("epic add exit = %d:\n%s", code, out)
	}
	var e struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("parse epic add --json: %v\n%s", err, out)
	}
	return e.ID
}

// epic done/deactivate suggest the previous active box: one human line and a
// `previous` key in --json — computed from the activation log, never executed.
func TestEpicDoneSuggestsPreviousActive(t *testing.T) {
	initStore(t)
	ea := epicID(t, "box a", "-r", "me/r1")
	eb := epicID(t, "box b", "-r", "me/r1")
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
	// The suggestion is display data: nothing got activated.
	st, _ := run(t, "--json", "epic", "show", ea)
	if !strings.Contains(st, `"active": false`) {
		t.Errorf("suggestion must never activate; epic show:\n%s", st)
	}
}

func TestEpicDeactivateSuggestsPreviousActiveJSON(t *testing.T) {
	initStore(t)
	ea := epicID(t, "box a", "-r", "me/r1")
	eb := epicID(t, "box b", "-r", "me/r1")
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
	ea := epicID(t, "box a", "-r", "me/r1")

	got, code := run(t, "epic", "done", ea)
	if code != 0 {
		t.Fatalf("epic done exit = %d:\n%s", code, got)
	}
	if !strings.Contains(got, "previous: unknown") {
		t.Errorf("done with no records should say unknown:\n%s", got)
	}

	eb := epicID(t, "box b", "-r", "me/r1")
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
