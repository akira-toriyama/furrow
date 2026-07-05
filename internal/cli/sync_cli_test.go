package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// `furrow sync` outside a git repo: validation exit (2), and the progress
// object still lands on stdout — the "emitted on success AND failure" half of
// the contract that a plain error path would silently drop.
func TestSyncOutsideGitPrintsProgressAndExits2(t *testing.T) {
	initStore(t) // t.TempDir board — not a git repo

	out, code := run(t, "--json", "sync")
	if code != int(core.CodeValidation) {
		t.Errorf("exit = %d, want %d", code, core.CodeValidation)
	}
	for _, key := range []string{`"committed": false`, `"pulled": false`, `"pushed": false`, `"conflict": false`} {
		if !strings.Contains(out, key) {
			t.Errorf("progress object missing %s on failure:\n%s", key, out)
		}
	}

	// Human mode prints the terse one-liner instead.
	hout, hcode := run(t, "sync")
	if hcode != int(core.CodeValidation) {
		t.Errorf("human exit = %d, want %d", hcode, core.CodeValidation)
	}
	if !strings.Contains(hout, "sync: committed=false") {
		t.Errorf("human summary missing:\n%s", hout)
	}
}

func TestRevisitLineRendering(t *testing.T) {
	// Non-empty: counts, scope tag, and the call-to-action.
	got := revisitLine(app.RevisitSummary{DepDone: []string{"t-1", "t-2"}, Stale: []string{"t-3"}}, "furrow")
	want := "revisit: 2 dep_done, 1 stale (furrow) — furrow revisit"
	if got != want {
		t.Errorf("revisitLine = %q, want %q", got, want)
	}
	// Empty summary renders nothing (clean board stays quiet).
	if got := revisitLine(app.RevisitSummary{}, "furrow"); got != "" {
		t.Errorf("empty revisitLine = %q, want \"\"", got)
	}
	// Board-wide fallback tag.
	if got := revisitLine(app.RevisitSummary{Stale: []string{"t-3"}}, "board"); !strings.Contains(got, "(board)") {
		t.Errorf("board tag missing: %q", got)
	}
}

func TestSyncOutputJSONShape(t *testing.T) {
	prog := &app.SyncProgress{Pulled: true, Pushed: true}
	// With a summary: revisit object carries the ids.
	withSum := mustJSON(syncOutput{prog, &app.RevisitSummary{DepDone: []string{"t-0046"}}})
	for _, want := range []string{`"pulled": true`, `"revisit"`, `"dep_done"`, `"t-0046"`} {
		if !strings.Contains(string(withSum), want) {
			t.Errorf("json missing %s:\n%s", want, withSum)
		}
	}
	// Without a summary: no revisit key at all (omitempty via nil pointer).
	noSum := mustJSON(syncOutput{prog, nil})
	if strings.Contains(string(noSum), "revisit") {
		t.Errorf("empty summary must omit revisit key:\n%s", noSum)
	}
}
