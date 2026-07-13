package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// unknownKeyFindings returns the unknown-shard-key problems, keyed by the id each
// one blames — the field a --json consumer branches on.
func unknownKeyFindings(t *testing.T, a *App) map[string]core.Problem {
	t.Helper()
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]core.Problem{}
	for _, p := range ps {
		if p.Code == "unknown-shard-key" {
			if p.Severity != core.SevWarn {
				t.Errorf("unknown-shard-key must be a WARNING, not %q: a preserved key breaks nothing and must not red a CI", p.Severity)
			}
			found[p.ID] = p
		}
	}
	return found
}

// All THREE machine-written file kinds park unknown top-level keys now — and all
// three had to flip their published schema to additionalProperties:true to stay
// honest about that. That flip removed the ONLY thing that ever rejected a typo in
// a repo review shard or in meta.json. So lint has to cover all three, or the
// data-preservation fix ships a detection regression: nothing ever deletes an
// extra, so a key no tool reports is a key wrong forever.
func TestLintWarnsUnknownKeysInEveryShardKind(t *testing.T) {
	dir := t.TempDir()
	a, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("a task", AddOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReviewRepo("owner/app", false); err != nil {
		t.Fatal(err)
	}
	if got := unknownKeyFindings(t, a); len(got) != 0 {
		t.Fatalf("a board furrow wrote itself must be clean, got %v", got)
	}

	// Plant one unknown key in each file kind, exactly as a hand-edit typo or a
	// newer furrow's unbumped field would leave it.
	plant := func(path, key, val string) {
		t.Helper()
		b, err := os.ReadFile(path) // #nosec G304 -- test-owned temp path
		if err != nil {
			t.Fatal(err)
		}
		s := strings.TrimRight(string(b), "\n")
		s = strings.TrimSuffix(s, "}") + ",\n  " + key + ": " + val + "\n}\n"
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(dir, DirName)
	plant(filepath.Join(root, core.TaskPath(task.ID)), `"lables"`, `["typo"]`)
	plant(filepath.Join(root, core.RepoRecordPath("owner/app")), `"cadence_days"`, `14`)
	plant(filepath.Join(root, "meta.json"), `"min_reader"`, `3`)

	got := unknownKeyFindings(t, a)
	for id, wantKey := range map[string]string{
		task.ID:     "lables",       // a task shard: a hand-edit typo, blamed on the task id
		"owner/app": "cadence_days", // a repo review shard: a newer furrow's unbumped field
		"meta":      "min_reader",   // meta.json, which belongs to no task
	} {
		p, ok := got[id]
		if !ok {
			t.Errorf("no unknown-shard-key warning for %q — its typo is preserved forever and reported by nothing", id)
			continue
		}
		if !strings.Contains(p.Msg, wantKey) {
			t.Errorf("the %q warning must name the offending key %q, got: %s", id, wantKey, p.Msg)
		}
	}
	if len(got) != 3 {
		t.Errorf("want exactly 3 unknown-shard-key findings (one per file kind), got %d: %v", len(got), got)
	}
}
