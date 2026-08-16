package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// tidy through the CLI: a bare run previews both classes and writes nothing,
// --yes without a selector is the guarded exit 2 (naming what to apply is the
// deliberate act), and --yes with a selector prunes exactly that class.
func TestCLITidy(t *testing.T) {
	initStore(t)
	out, _ := run(t, "--json", "add", "shipped")
	var dep struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &dep); err != nil {
		t.Fatal(err)
	}
	out, _ = run(t, "--json", "add", "anchor")
	var holder struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &holder); err != nil {
		t.Fatal(err)
	}
	run(t, "dep", holder.ID, dep.ID)
	run(t, "done", dep.ID)

	out, code := run(t, "--json", "tidy")
	if code != 0 {
		t.Fatalf("tidy preview exit %d\n%s", code, out)
	}
	var rep app.TidyReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse tidy --json: %v\n%s", err, out)
	}
	if rep.Applied || !rep.Changed || len(rep.DoneDeps) != 1 || rep.DoneDeps[0].ID != holder.ID {
		t.Fatalf("preview = %+v, want the %s edge unapplied", rep, holder.ID)
	}

	if fe, _ := runErr(t, "tidy", "--yes"); fe == nil || !strings.Contains(fe.Msg, "selector") {
		t.Fatalf("tidy --yes without a selector = %+v, want the exit-2 refusal", fe)
	}

	out, code = run(t, "--json", "tidy", "--done-deps", "--yes")
	if code != 0 {
		t.Fatalf("tidy --done-deps --yes exit %d\n%s", code, out)
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || len(rep.DoneDeps) != 1 {
		t.Fatalf("apply = %+v, want applied with the one edge", rep)
	}

	out, _ = run(t, "--json", "show", holder.ID)
	if strings.Contains(out, dep.ID) {
		t.Errorf("satisfied edge survived the prune:\n%s", out)
	}
}
