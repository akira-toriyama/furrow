package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/fsstore"
)

// pastClock stamps writes at a fixed past instant so tests can seed aged tasks.
type pastClock struct{ t time.Time }

func (c pastClock) Now() time.Time { return c.t.UTC().Truncate(time.Second) }

type revisitRow struct {
	ID      string `json:"id"`
	Revisit []struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"revisit"`
}

func TestCLIRevisitJSONReasons(t *testing.T) {
	initStore(t)
	id := addTask(t, "needs estimates", "-s", "ready") // fresh, no value/effort

	out, code := run(t, "--json", "revisit")
	if code != 0 {
		t.Fatalf("revisit --json exit = %d:\n%s", code, out)
	}
	var rows []revisitRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("revisit --json should be an array with reasons, got %v:\n%s", err, out)
	}
	var got *revisitRow
	for i := range rows {
		if rows[i].ID == id {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatalf("unestimated task %s should surface for revisit:\n%s", id, out)
	}
	codes := map[string]bool{}
	for _, r := range got.Revisit {
		codes[r.Code] = true
	}
	if !codes[core.RevisitValueUnset] || !codes[core.RevisitEffortUnset] {
		t.Errorf("expected value_unset + effort_unset, got %+v", got.Revisit)
	}
}

func TestCLIRevisitEmptyExit0(t *testing.T) {
	initStore(t)
	// nothing to revisit is the healthy state: exit 0 (diverges from `next`),
	// and --json is [] not null.
	out, code := run(t, "--json", "revisit")
	if code != 0 {
		t.Errorf("empty revisit should exit 0, got %d:\n%s", code, out)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty revisit --json should be [], got %q", out)
	}
}

func TestCLIRevisitStaleDaysFlagParses(t *testing.T) {
	initStore(t)
	addTask(t, "estimated", "-s", "ready", "--value", "3", "--effort", "2")
	// estimated + fresh + no deps + stale disabled -> nothing surfaces.
	out, code := run(t, "--json", "revisit", "--stale-days", "0")
	if code != 0 {
		t.Fatalf("revisit --stale-days exit = %d:\n%s", code, out)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("estimated fresh task should not surface with stale disabled, got %q", out)
	}
}

func TestCLIRevisitNDJSON(t *testing.T) {
	initStore(t)
	id := addTask(t, "needs estimates", "-s", "ready")

	out, code := run(t, "--ndjson", "revisit")
	if code != 0 {
		t.Fatalf("revisit --ndjson exit = %d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 ndjson line, got %d:\n%s", len(lines), out)
	}
	var row revisitRow
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("ndjson line is not valid JSON: %v\n%s", err, lines[0])
	}
	if row.ID != id || len(row.Revisit) == 0 {
		t.Errorf("ndjson row should carry id %s and a non-empty revisit array, got %+v", id, row)
	}

	// empty store: zero lines, no stray blank line, still exit 0.
	initStore(t)
	out, code = run(t, "--ndjson", "revisit")
	if code != 0 {
		t.Errorf("empty revisit --ndjson should exit 0, got %d", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("empty revisit --ndjson should print nothing, got %q", out)
	}
}

// TestCLIRevisitConfigStaleDaysDefault pins the precedence at the CLI seam: with
// no --stale-days flag, the bare command must drive staleness from
// [revisit].stale_days in config. A task aged past the configured window surfaces
// as `stale`. (Aged via a past-clock app over the same store, since the CLI uses
// the real system clock.)
func TestCLIRevisitConfigStaleDaysDefault(t *testing.T) {
	dir := t.TempDir()
	if _, err := app.Init(dir); err != nil {
		t.Fatal(err)
	}
	fdir := filepath.Join(dir, app.DirName)
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte("[revisit]\nstale_days = 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(app.EnvDir, fdir)

	// Seed an estimated task whose Updated is 30 days ago (so only `stale` can fire).
	cfg, _, err := config.Load(filepath.Join(fdir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	st := fsstore.New(fdir, cfg.Lanes, cfg.IDPrefix, cfg.IDWidth)
	aged := app.NewWithStore(st, cfg, pastClock{t: time.Now().AddDate(0, 0, -30)})
	v, e := 3, 2
	old, err := aged.Add("aged task", app.AddOpts{Status: "ready", Value: &v, Effort: &e})
	if err != nil {
		t.Fatal(err)
	}

	// Bare `revisit` (no flag) must use config stale_days=7 -> 30d-old task is stale.
	out, code := run(t, "--json", "revisit")
	if code != 0 {
		t.Fatalf("revisit exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, old.ID) || !strings.Contains(out, core.RevisitStale) {
		t.Errorf("config [revisit].stale_days should surface the aged task as stale:\n%s", out)
	}
}
