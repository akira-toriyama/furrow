package app

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// assetWarns counts the warn-severity findings whose message contains all of the
// given substrings — the scan-for-my-finding predicate the app lint tests use.
func assetWarns(ps []core.Problem, subs ...string) int {
	n := 0
	for _, p := range ps {
		if p.Severity != core.SevWarn {
			continue
		}
		all := true
		for _, s := range subs {
			if !strings.Contains(p.Msg, s) {
				all = false
				break
			}
		}
		if all {
			n++
		}
	}
	return n
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{core.DefaultAssetWarnBytes, "5.0 MiB"},
		{core.DefaultAssetWarnBytes + 1, "5.0 MiB"},
		{1 << 30, "1.0 GiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLintReportsOrphanAsset(t *testing.T) {
	a := newApp()
	task, _ := a.Add("has an unused asset", AddOpts{Status: "ready"})
	// Save an asset but never reference it from the body -> orphan.
	name, err := a.Store.SaveAsset(task.ID, "orphan.png", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if got := assetWarns(ps, name, "not referenced"); got != 1 {
		t.Errorf("expected one orphan-asset warn for %q, got %d: %+v", name, got, ps)
	}
	if core.HasErrors(ps) {
		t.Errorf("an orphan asset is warn-only and must not fail lint: %+v", ps)
	}
}

func TestLintReportsDanglingAssetRef(t *testing.T) {
	a := newApp()
	task, _ := a.Add("refs a missing asset", AddOpts{Status: "ready"})
	missing := task.ID + "-missing.png"
	if err := a.Store.SaveBody(task.ID, "# t\n\n![shot]("+core.AssetRef(missing)+")\n"); err != nil {
		t.Fatal(err)
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if got := assetWarns(ps, missing, "is missing"); got != 1 {
		t.Errorf("expected one dangling-asset warn for %q, got %d: %+v", missing, got, ps)
	}
	if core.HasErrors(ps) {
		t.Errorf("a dangling asset ref is warn-only and must not fail lint: %+v", ps)
	}
}

func TestLintDanglingAssetRefInCodeIgnored(t *testing.T) {
	// A ref written as a documented example inside a code fence must not be
	// treated as live — the asset scan reuses ExtractAssetRefs' code stripping.
	a := newApp()
	task, _ := a.Add("documents attach", AddOpts{Status: "ready"})
	body := "# doc\n\n```\n![x](assets/" + task.ID + "-x.png)\n```\n"
	if err := a.Store.SaveBody(task.ID, body); err != nil {
		t.Fatal(err)
	}
	ps, _ := a.Lint()
	if got := assetWarns(ps, "is missing"); got != 0 {
		t.Errorf("an assets/ ref inside a code fence must not dangle, got %d: %+v", got, ps)
	}
}

func TestLintReportsOversizedAsset(t *testing.T) {
	a := newApp()
	task, _ := a.Add("big asset", AddOpts{Status: "ready"})
	big := make([]byte, core.DefaultAssetWarnBytes+1)
	name, err := a.Store.SaveAsset(task.ID, "clip.mp4", big)
	if err != nil {
		t.Fatal(err)
	}
	// Reference it so it is NOT also flagged orphan — isolate the oversized signal.
	if err := a.Store.SaveBody(task.ID, "# t\n\n[clip]("+core.AssetRef(name)+")\n"); err != nil {
		t.Fatal(err)
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if got := assetWarns(ps, name, "warning threshold"); got != 1 {
		t.Errorf("expected one oversized-asset warn for %q, got %d: %+v", name, got, ps)
	}
	if got := assetWarns(ps, name, "not referenced"); got != 0 {
		t.Errorf("a referenced asset must not be reported orphan: %+v", ps)
	}
	if core.HasErrors(ps) {
		t.Errorf("an oversized asset is warn-only and must not fail lint: %+v", ps)
	}
}

func TestLintCleanAssetNoFindings(t *testing.T) {
	// A small, referenced asset (the normal `furrow attach` outcome) yields no
	// asset findings and keeps lint at exit 0.
	a := newApp()
	task, _ := a.Add("clean attach", AddOpts{Status: "ready"})
	if _, err := a.Attach(task.ID, "shot.png", []byte("small")); err != nil {
		t.Fatal(err)
	}
	ps, err := a.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if got := assetWarns(ps, "asset"); got != 0 {
		t.Errorf("a small referenced asset must produce no asset findings, got %d: %+v", got, ps)
	}
	if core.HasErrors(ps) {
		t.Errorf("a clean board must not fail lint: %+v", ps)
	}
}
