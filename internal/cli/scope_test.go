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
// explicit flags, scopedQuery scopes reads to the default label — silently.
func TestScopedQuery_AutoFilterAppliesSilently(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd := queryCmd() // neither --label nor --repo changed
	o, err := scopedQuery(cmd, &app.App{DefaultLabel: "chord", AutoFilter: true, Dir: "/b/.furrow"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if o.ScopeLabel != "chord" || o.Label != "" {
		t.Errorf("scope = %q label = %q, want chord + empty", o.ScopeLabel, o.Label)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

// auto_filter = false: reads are NOT scoped by the board label (the whole board
// shows), even though DefaultLabel is set — the label still tags `add`, but read
// filtering is off.
func TestScopedQuery_AutoFilterFalseDoesNotScope(t *testing.T) {
	cmd := queryCmd()
	o, _ := scopedQuery(cmd, &app.App{DefaultLabel: "chord", AutoFilter: false, Dir: "/b/.furrow"}, "", "")
	if o.ScopeLabel != "" {
		t.Errorf("scope = %q, want empty (auto_filter=false shows the whole board)", o.ScopeLabel)
	}
}

// -l/-r orthogonality: an explicit -l is a pure tag filter now — it must NOT
// clear the board scope. The tag ANDs with the scope.
func TestScopedQuery_ExplicitLabelKeepsScope(t *testing.T) {
	cmd := queryCmd()
	_ = cmd.Flags().Set("label", "bug") // Changed=true
	o, _ := scopedQuery(cmd, &app.App{DefaultLabel: "chord", AutoFilter: true, Dir: "/b/.furrow"}, "bug", "")
	if o.ScopeLabel != "chord" {
		t.Errorf("scope = %q, want chord (an explicit -l must not clear the scope)", o.ScopeLabel)
	}
	if o.Label != "bug" {
		t.Errorf("label = %q, want bug", o.Label)
	}
}

// Scope control is -r only: -r "" escapes to the whole board.
func TestScopedQuery_ExplicitEmptyRepoEscapes(t *testing.T) {
	cmd := queryCmd()
	_ = cmd.Flags().Set("repo", "") // Changed=true, value ""
	o, err := scopedQuery(cmd, &app.App{DefaultLabel: "chord", AutoFilter: true, Dir: "/b/.furrow"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if o.ScopeLabel != "" || o.Repo != "" {
		t.Errorf("scope = %q repo = %q, want both empty (whole board)", o.ScopeLabel, o.Repo)
	}
}

func TestScopedQuery_NoDefaultLabel(t *testing.T) {
	cmd := queryCmd()
	o, _ := scopedQuery(cmd, &app.App{DefaultLabel: "", AutoFilter: true, Dir: "/b/.furrow"}, "", "")
	if o.ScopeLabel != "" {
		t.Errorf("scope = %q, want empty", o.ScopeLabel)
	}
}

// TestLs_PointerScopesSilently drives a real `ls` through cobra against a pointer
// layout. It asserts the scope contract end to end: the pointer still scopes reads
// to its label (a differently-labeled task on the same board is filtered out),
// stdout stays pure data, and NO scope banner appears on stdout or stderr.
func TestLs_PointerScopesSilently(t *testing.T) {
	t.Setenv(app.EnvDir, "") // do not let FURROW_DIR override pointer discovery
	root := t.TempDir()
	central := filepath.Join(root, "central")
	cboard, err := app.Init(central)
	if err != nil {
		t.Fatal(err)
	}
	// A task on the board NOT carrying the pointer label must be scoped out.
	if _, err := cboard.Add("only on board", app.AddOpts{Labels: []string{"other"}}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "board = \"../central/.furrow\"\ndefault_label = \"demo\"\n"
	if err := os.WriteFile(filepath.Join(repo, app.PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed one task via the pointer (Open takes an explicit start dir, no chdir);
	// withDefaultLabel tags it "demo".
	a, err := app.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("hello from repo", app.AddOpts{}); err != nil {
		t.Fatal(err)
	}

	// `ls` resolves the store from cwd, so run it from the repo dir.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	var so, se bytes.Buffer
	out, errOut = &so, &se
	defer func() { out, errOut = os.Stdout, os.Stderr }()
	rootCmd := newRootCmd()
	rootCmd.SetArgs([]string{"ls"})
	rootCmd.SetOut(&so)
	rootCmd.SetErr(&se)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("ls: %v", err)
	}

	if strings.Contains(so.String(), "furrow:") {
		t.Errorf("scope banner leaked into stdout:\n%s", so.String())
	}
	if strings.Contains(se.String(), "scope=label=") {
		t.Errorf("scope banner must stay OFF, stderr:\n%s", se.String())
	}
	if !strings.Contains(so.String(), "hello from repo") {
		t.Errorf("scoped task missing from stdout:\n%s", so.String())
	}
	if strings.Contains(so.String(), "only on board") {
		t.Errorf("pointer scope must filter out the differently-labeled task:\n%s", so.String())
	}
}

// TestLs_TagFilterANDsWithScope pins the -l/-r orthogonality end to end (the
// design's required test): on a pointer-scoped board, `ls -l tag` returns ONLY
// the in-scope tasks carrying the tag — the explicit -l neither clears the
// scope nor leaks the other repo's tagged task. And `-r ""` escapes to the
// whole board.
func TestLs_TagFilterANDsWithScope(t *testing.T) {
	t.Setenv(app.EnvDir, "")
	root := t.TempDir()
	central := filepath.Join(root, "central")
	cboard, err := app.Init(central)
	if err != nil {
		t.Fatal(err)
	}
	// Out-of-scope task that CARRIES the tag: must not leak through -l tag.
	if _, err := cboard.Add("other tagged", app.AddOpts{Labels: []string{"other", "tag"}}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "board = \"../central/.furrow\"\ndefault_label = \"demo\"\n"
	if err := os.WriteFile(filepath.Join(repo, app.PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("in scope tagged", app.AddOpts{Labels: []string{"tag"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("in scope plain", app.AddOpts{}); err != nil {
		t.Fatal(err)
	}

	prev, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	lsWith := func(args ...string) string {
		var so, se bytes.Buffer
		out, errOut = &so, &se
		defer func() { out, errOut = os.Stdout, os.Stderr }()
		rootCmd := newRootCmd()
		rootCmd.SetArgs(append([]string{"ls"}, args...))
		rootCmd.SetOut(&so)
		rootCmd.SetErr(&se)
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("ls %v: %v", args, err)
		}
		return so.String()
	}

	got := lsWith("-l", "tag")
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
	whole := lsWith("-r", "")
	for _, want := range []string{"other tagged", "in scope tagged", "in scope plain"} {
		if !strings.Contains(whole, want) {
			t.Errorf("-r '' should show the whole board (missing %q):\n%s", want, whole)
		}
	}
}
