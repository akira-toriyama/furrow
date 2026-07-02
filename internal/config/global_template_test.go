package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The home template must declare the array form with auto_filter spelled out, so
// the file itself documents the v2 central-board model (no retired [board]).
func TestGlobalTemplate_DeclaresBoardArrayWithExplicitAutoFilter(t *testing.T) {
	if !strings.Contains(GlobalTemplate, "[[board]]") {
		t.Errorf("GlobalTemplate must use the [[board]] array form; got:\n%s", GlobalTemplate)
	}
	if !strings.Contains(GlobalTemplate, "auto_filter = true") {
		t.Errorf("GlobalTemplate must state auto_filter = true explicitly; got:\n%s", GlobalTemplate)
	}
	if !strings.Contains(GlobalTemplate, "repo = \"auto\"") {
		t.Errorf("GlobalTemplate must declare repo = \"auto\" (the repos-pivot scope key); got:\n%s", GlobalTemplate)
	}
	if strings.Contains(GlobalTemplate, "label = \"auto\"") {
		t.Errorf("GlobalTemplate must not carry the retired label = \"auto\" mode")
	}
	if strings.Contains(GlobalTemplate, "\n[board]\n") || strings.Contains(GlobalTemplate, "\n[board] ") {
		t.Errorf("GlobalTemplate must not use the retired singular [board] table")
	}
}

// With nothing to derive, the render IS the placeholder template — the exact
// bytes mirrored at repo-root config.global.toml and checked by check.sh.
func TestRenderGlobalConfig_PlaceholderIdentity(t *testing.T) {
	if got := RenderGlobalConfig("", nil); got != GlobalTemplate {
		t.Errorf("RenderGlobalConfig(\"\", nil) must equal GlobalTemplate (the placeholder mirror); got:\n%s", got)
	}
}

// A derived render substitutes path+scopes and stays valid TOML that the real
// loader accepts with no clamp warnings — proves the written file is usable.
func TestRenderGlobalConfig_SubstitutesAndRoundTrips(t *testing.T) {
	board := "/ws/org/proj/.furrow"
	rendered := RenderGlobalConfig(board, []string{"/ws/org"})
	if strings.Contains(rendered, "/path/to/central/.furrow") {
		t.Errorf("rendered config still carries the placeholder board path:\n%s", rendered)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	boards, warn, err := LoadGlobalBoards(p)
	if err != nil {
		t.Fatalf("rendered config did not parse: %v", err)
	}
	if len(warn) != 0 {
		t.Errorf("rendered config produced clamp warnings: %v", warn)
	}
	if len(boards) != 1 {
		t.Fatalf("want 1 board, got %d (%v)", len(boards), boards)
	}
	b := boards[0]
	if b.Path != board {
		t.Errorf("board path = %q, want %q", b.Path, board)
	}
	if len(b.Scopes) != 1 || b.Scopes[0] != "/ws/org" {
		t.Errorf("board scopes = %v, want [/ws/org]", b.Scopes)
	}
	if !b.AutoFilter {
		t.Errorf("auto_filter should default-render to true")
	}
	if b.Repo != "auto" {
		t.Errorf("repo = %q, want \"auto\"", b.Repo)
	}
	if b.Label != "" {
		t.Errorf("label = %q, want \"\" (a literal tag, opt-in)", b.Label)
	}
}
