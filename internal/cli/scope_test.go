package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// queryCmd builds a throwaway command carrying the shared --label/--repo flags,
// so a test can toggle whether each flag was "Changed".
func queryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	var label, repo string
	cmd.Flags().StringVarP(&label, "label", "l", "", "")
	cmd.Flags().StringVarP(&repo, "repo", "r", "", "")
	return cmd
}

// With auto_filter on (a pointer, or a board with auto_filter=true) and no
// explicit flags, scopedQuery scopes reads to the board repo — silently.
func TestScopedQuery_AutoFilterAppliesSilently(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd := queryCmd() // neither --label nor --repo changed
	o, err := scopedQuery(cmd, &app.App{DefaultRepo: "me/chord", AutoFilter: true, Dir: "/b/.furrow"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if o.ScopeRepo != "me/chord" || o.Label != "" {
		t.Errorf("scope = %q label = %q, want me/chord + empty", o.ScopeRepo, o.Label)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

// auto_filter = false: reads are NOT scoped by the board repo (the whole board
// shows), even though DefaultRepo is set — the repo still attaches on `add`,
// but read filtering is off.
func TestScopedQuery_AutoFilterFalseDoesNotScope(t *testing.T) {
	cmd := queryCmd()
	o, _ := scopedQuery(cmd, &app.App{DefaultRepo: "me/chord", AutoFilter: false, Dir: "/b/.furrow"}, "", "")
	if o.ScopeRepo != "" {
		t.Errorf("scope = %q, want empty (auto_filter=false shows the whole board)", o.ScopeRepo)
	}
}

// -l/-r orthogonality: an explicit -l is a pure tag filter — it must NOT clear
// the board's repo scope. The tag ANDs with the scope.
func TestScopedQuery_ExplicitLabelKeepsScope(t *testing.T) {
	cmd := queryCmd()
	_ = cmd.Flags().Set("label", "bug") // Changed=true
	o, _ := scopedQuery(cmd, &app.App{DefaultRepo: "me/chord", AutoFilter: true, Dir: "/b/.furrow"}, "bug", "")
	if o.ScopeRepo != "me/chord" {
		t.Errorf("scope = %q, want me/chord (an explicit -l must not clear the scope)", o.ScopeRepo)
	}
	if o.Label != "bug" {
		t.Errorf("label = %q, want bug", o.Label)
	}
}

// A board's literal label never read-filters: only DefaultRepo scopes.
func TestScopedQuery_LiteralBoardLabelDoesNotScope(t *testing.T) {
	cmd := queryCmd()
	o, _ := scopedQuery(cmd, &app.App{DefaultLabel: "tracked", AutoFilter: true, Dir: "/b/.furrow"}, "", "")
	if o.ScopeRepo != "" || o.Label != "" {
		t.Errorf("scope = %q label = %q, want both empty (a literal label is add-time only)", o.ScopeRepo, o.Label)
	}
}

// Scope control is -r only: -r "" escapes to the whole board.
func TestScopedQuery_ExplicitEmptyRepoEscapes(t *testing.T) {
	cmd := queryCmd()
	_ = cmd.Flags().Set("repo", "") // Changed=true, value ""
	o, err := scopedQuery(cmd, &app.App{DefaultRepo: "me/chord", AutoFilter: true, Dir: "/b/.furrow"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if o.ScopeRepo != "" || o.Repo != "" {
		t.Errorf("scope = %q repo = %q, want both empty (whole board)", o.ScopeRepo, o.Repo)
	}
}

func TestScopedQuery_NoDefaultRepo(t *testing.T) {
	cmd := queryCmd()
	o, _ := scopedQuery(cmd, &app.App{DefaultRepo: "", AutoFilter: true, Dir: "/b/.furrow"}, "", "")
	if o.ScopeRepo != "" {
		t.Errorf("scope = %q, want empty", o.ScopeRepo)
	}
}

// pointerRepoLayout builds a central board plus a repo dir whose pointer scopes
// it to me/demo, seeds one out-of-scope task on the board, and chdirs into the
// repo. Returns the app opened via the pointer (seeds scoped tasks) and the
// board-level app (seeds out-of-scope tasks with no repo union).
func pointerRepoLayout(t *testing.T) (scoped, board *app.App) {
	t.Helper()
	t.Setenv(app.EnvDir, "")
	root := t.TempDir()
	central := filepath.Join(root, "central")
	cboard, err := app.Init(central)
	if err != nil {
		t.Fatal(err)
	}
	// A task on the board attached to ANOTHER repo must be scoped out.
	if _, err := cboard.Add("only on board", app.AddOpts{Repos: []string{"me/other"}}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "board = \"../central/.furrow\"\ndefault_repo = \"me/demo\"\n"
	if err := os.WriteFile(filepath.Join(repo, app.PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(repo)
	if err != nil {
		t.Fatal(err)
	}

	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return a, cboard
}

// runLs drives a real command line through cobra and returns (stdout, stderr).
func runLs(t *testing.T, args ...string) (string, string) {
	t.Helper()
	var so, se bytes.Buffer
	out, errOut = &so, &se
	defer func() { out, errOut = os.Stdout, os.Stderr }()
	rootCmd := newRootCmd()
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&so)
	rootCmd.SetErr(&se)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return so.String(), se.String()
}

// TestLs_PointerScopesSilently drives a real `ls` through cobra against a
// pointer layout. It asserts the scope contract end to end: the pointer scopes
// reads to its repo (a task attached to another repo is filtered out), stdout
// stays pure data, and NO scope banner appears on stdout or stderr.
func TestLs_PointerScopesSilently(t *testing.T) {
	a, _ := pointerRepoLayout(t)
	// Seeded via the pointer: withBoardRepo attaches me/demo.
	if _, err := a.Add("hello from repo", app.AddOpts{}); err != nil {
		t.Fatal(err)
	}

	so, se := runLs(t, "ls")
	if strings.Contains(so, "furrow:") {
		t.Errorf("scope banner leaked into stdout:\n%s", so)
	}
	if strings.Contains(se, "scope=") {
		t.Errorf("scope banner must stay OFF, stderr:\n%s", se)
	}
	if !strings.Contains(so, "hello from repo") {
		t.Errorf("scoped task missing from stdout:\n%s", so)
	}
	if strings.Contains(so, "only on board") {
		t.Errorf("pointer scope must filter out the other repo's task:\n%s", so)
	}
}

// TestLs_TagFilterANDsWithScope pins the -l/-r orthogonality end to end: on a
// repo-scoped board, `ls -l tag` returns ONLY the in-scope tasks carrying the
// tag — the explicit -l neither clears the scope nor leaks the other repo's
// tagged task. And `-r ""` escapes to the whole board.
func TestLs_TagFilterANDsWithScope(t *testing.T) {
	a, cboard := pointerRepoLayout(t)
	// Out-of-scope task that CARRIES the tag (seeded board-side, so the pointer
	// repo is not unioned in): must not leak through -l tag.
	if _, err := cboard.Add("other tagged", app.AddOpts{Repos: []string{"me/other"}, Labels: []string{"tag"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("in scope tagged", app.AddOpts{Labels: []string{"tag"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("in scope plain", app.AddOpts{}); err != nil {
		t.Fatal(err)
	}

	got, _ := runLs(t, "ls", "-l", "tag")
	if !strings.Contains(got, "in scope tagged") {
		t.Errorf("-l tag should keep the in-scope tagged task:\n%s", got)
	}
	if strings.Contains(got, "other tagged") {
		t.Errorf("-l tag must AND with the scope, not replace it (out-of-scope task leaked):\n%s", got)
	}
	if strings.Contains(got, "in scope plain") {
		t.Errorf("-l tag must still filter by the tag:\n%s", got)
	}

	// -r '' is the whole-board escape.
	whole, _ := runLs(t, "ls", "-r", "")
	for _, want := range []string{"only on board", "other tagged", "in scope tagged", "in scope plain"} {
		if !strings.Contains(whole, want) {
			t.Errorf("-r '' should show the whole board (missing %q):\n%s", want, whole)
		}
	}
}

// The hidden-drafts stderr hint fires for the board's AUTO repo scope too, not
// just an explicit -r — and the draft stays off stdout.
func TestLs_ScopeHidesDraftsHint(t *testing.T) {
	a, _ := pointerRepoLayout(t)
	if _, err := a.Add("in scope", app.AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("a draft", app.AddOpts{Draft: true}); err != nil {
		t.Fatal(err)
	}

	so, se := runLs(t, "ls")
	if strings.Contains(so, "a draft") {
		t.Errorf("a draft must be hidden from the scoped read:\n%s", so)
	}
	if !strings.Contains(se, "draft(s) hidden") || !strings.Contains(se, "--drafts") {
		t.Errorf("stderr should hint at the hidden draft, got:\n%s", se)
	}
	// The escape hatch shows the draft and mutes the hint.
	so, se = runLs(t, "ls", "--drafts")
	if !strings.Contains(so, "a draft") {
		t.Errorf("--drafts should list the draft:\n%s", so)
	}
	if strings.Contains(se, "draft(s) hidden") {
		t.Errorf("--drafts must not re-hint, stderr:\n%s", se)
	}
}
