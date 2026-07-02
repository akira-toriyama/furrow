package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// runErr executes furrow in-process and returns the structured error (nil on
// success) plus stdout — for tests that assert on Candidates and friends.
func runErr(t *testing.T, args ...string) (*core.Error, string) {
	t.Helper()
	var buf bytes.Buffer
	out = &buf
	defer func() { out = nil }()
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := root.Execute()
	if err == nil {
		return nil, buf.String()
	}
	fe := core.AsError(err)
	if fe == nil {
		fe = &core.Error{Code: core.CodeValidation, Msg: err.Error()}
	}
	return fe, buf.String()
}

func TestCLIAddAndFilterByRepo(t *testing.T) {
	initStore(t)
	fid := addTask(t, "furrow work", "-s", "ready", "-r", "akira-toriyama/furrow")
	cid := addTask(t, "chord work", "-s", "ready", "-r", "akira-toriyama/chord")

	// -r with a full owner/repo filters the repos field.
	out, code := run(t, "--json", "ls", "-r", "akira-toriyama/furrow")
	if code != 0 || !strings.Contains(out, fid) || strings.Contains(out, cid) {
		t.Errorf("ls -r full form should return only the furrow task (code %d):\n%s", code, out)
	}
	// -r with a unique short name resolves (case-insensitively).
	out, code = run(t, "--json", "ls", "-r", "Chord")
	if code != 0 || !strings.Contains(out, cid) || strings.Contains(out, fid) {
		t.Errorf("ls -r short name should resolve to the chord repo (code %d):\n%s", code, out)
	}
	// next honors -r the same way.
	out, code = run(t, "--json", "next", "-r", "furrow")
	if code != 0 || !strings.Contains(out, fid) || strings.Contains(out, cid) {
		t.Errorf("next -r short name (code %d):\n%s", code, out)
	}
	// human ls shows the repos alongside the labels (greppable).
	out, _ = run(t, "ls")
	if !strings.Contains(out, "(akira-toriyama/furrow)") {
		t.Errorf("human ls should show the repos:\n%s", out)
	}
	// human show gets a repos: line.
	out, _ = run(t, "show", fid)
	if !strings.Contains(out, "repos:    akira-toriyama/furrow") {
		t.Errorf("human show should display a repos line:\n%s", out)
	}
}

func TestCLIRepoAmbiguousShortNameExit2WithCandidates(t *testing.T) {
	initStore(t)
	addTask(t, "one", "-s", "ready", "-r", "akira-toriyama/furrow")
	addTask(t, "two", "-s", "ready", "-r", "other-org/furrow")

	fe, _ := runErr(t, "ls", "-r", "furrow")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("ambiguous -r should exit 2, got %+v", fe)
	}
	want := []string{"akira-toriyama/furrow", "other-org/furrow"}
	if !reflect.DeepEqual(fe.Candidates, want) {
		t.Errorf("candidates = %v, want %v", fe.Candidates, want)
	}
	// an unresolvable short name is exit 2 too (strict, never silently empty).
	fe, _ = runErr(t, "ls", "-r", "ghost")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("unresolvable -r short name should exit 2, got %+v", fe)
	}
}

// The error envelope renders candidates (omitempty) beside code/id/message.
func TestRenderErrorCandidatesEnvelope(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	renderError(&core.Error{Code: core.CodeValidation, Msg: "ambiguous", Candidates: []string{"a/x", "b/x"}})
	var env struct {
		Error struct {
			Code       int      `json:"code"`
			Msg        string   `json:"message"`
			Candidates []string `json:"candidates"`
		} `json:"error"`
	}
	if err := json.Unmarshal(se.Bytes(), &env); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, se.String())
	}
	if !reflect.DeepEqual(env.Error.Candidates, []string{"a/x", "b/x"}) {
		t.Errorf("candidates = %v", env.Error.Candidates)
	}

	// omitempty: no candidates key when there are none.
	se.Reset()
	renderError(&core.Error{Code: core.CodeNotFound, Msg: "nope"})
	if strings.Contains(se.String(), "candidates") {
		t.Errorf("empty candidates must be omitted:\n%s", se.String())
	}
}

func TestCLIDidYouMeanGuard(t *testing.T) {
	initStore(t)
	addTask(t, "one", "-s", "ready", "-r", "akira-toriyama/furrow")
	addTask(t, "two", "-s", "ready", "-r", "akira-toriyama/furrow")

	// Arm 1 (fires): -l furrow matches nothing, but furrow names a repo.
	fe, _ := runErr(t, "ls", "-l", "furrow")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("did-you-mean should exit 2, got %+v", fe)
	}
	if !reflect.DeepEqual(fe.Candidates, []string{"akira-toriyama/furrow"}) {
		t.Errorf("candidates = %v", fe.Candidates)
	}
	for _, want := range []string{`label "furrow" matches no tasks`, "2 task(s)", "use -r furrow"} {
		if !strings.Contains(fe.Msg, want) {
			t.Errorf("message %q should contain %q", fe.Msg, want)
		}
	}
	// next and revisit guard the same way.
	if fe, _ := runErr(t, "next", "-l", "furrow"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("next -l should hit the guard, got %+v", fe)
	}
	if fe, _ := runErr(t, "revisit", "-l", "furrow"); fe == nil || fe.Code != core.CodeValidation {
		t.Errorf("revisit -l should hit the guard, got %+v", fe)
	}

	// Arm 2 (unaffected): a pure tag with >0 matches lists normally.
	tagged := addTask(t, "tagged", "-s", "ready", "-l", "furrow")
	out, code := run(t, "--json", "ls", "-l", "furrow")
	if code != 0 || !strings.Contains(out, tagged) {
		t.Errorf("a matching pure tag must be unaffected (code %d):\n%s", code, out)
	}

	// A miss with no repo resolution stays a plain empty result (exit 0 for ls).
	if _, code := run(t, "ls", "-l", "no-such-thing"); code != 0 {
		t.Errorf("an unresolvable label miss should stay exit 0 on ls, got %d", code)
	}
}

func TestCLIDraftsFlag(t *testing.T) {
	initStore(t)
	did := addTask(t, "a draft", "-s", "ready", "--draft")
	rid := addTask(t, "attached", "-s", "ready", "-r", "o/r")

	out, code := run(t, "--json", "ls", "--drafts")
	if code != 0 || !strings.Contains(out, did) || strings.Contains(out, rid) {
		t.Errorf("ls --drafts should list only the draft (code %d):\n%s", code, out)
	}
	// --draft conflicts with an explicit -r.
	if _, code := run(t, "add", "x", "--draft", "-r", "o/r"); code != int(core.CodeValidation) {
		t.Errorf("--draft with -r should exit 2, got %d", code)
	}
	// --drafts conflicts with -r (a draft has no repo to filter on).
	if _, code := run(t, "ls", "--drafts", "-r", "o/r"); code != int(core.CodeValidation) {
		t.Errorf("--drafts with -r should exit 2, got %d", code)
	}
}

func TestCLIHiddenDraftsHintOnStderr(t *testing.T) {
	initStore(t)
	addTask(t, "a draft", "-s", "ready")
	addTask(t, "attached", "-s", "ready", "-r", "akira-toriyama/furrow")

	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	// -r hides the draft -> ONE stderr hint line; stdout stays pure data.
	sout, code := run(t, "--json", "ls", "-r", "furrow")
	if code != 0 {
		t.Fatalf("ls -r exit = %d", code)
	}
	if want := "1 draft(s) hidden — furrow ls --drafts\n"; se.String() != want {
		t.Errorf("stderr = %q, want %q", se.String(), want)
	}
	if strings.Contains(sout, "hidden") {
		t.Errorf("the hint must never reach stdout:\n%s", sout)
	}

	// next hints too.
	se.Reset()
	run(t, "--json", "next", "-r", "furrow")
	if !strings.Contains(se.String(), "1 draft(s) hidden") {
		t.Errorf("next -r should hint on stderr, got %q", se.String())
	}

	// no -r, no hint.
	se.Reset()
	run(t, "--json", "ls")
	if se.Len() != 0 {
		t.Errorf("plain ls must not hint, stderr = %q", se.String())
	}
}

func TestCLIRepoCommandMutatesAndReportsChanged(t *testing.T) {
	initStore(t)
	seed := addTask(t, "seed", "-s", "ready", "-r", "akira-toriyama/furrow")
	id := addTask(t, "edit me", "-s", "ready")

	out, code := run(t, "--json", "repo", id, "--add", "furrow")
	if code != 0 {
		t.Fatalf("repo --add exit = %d:\n%s", code, out)
	}
	var res struct {
		Before  *core.Task `json:"before"`
		After   *core.Task `json:"after"`
		Changed []string   `json:"changed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("repo --json should be a mutation object: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(res.After.Repos, []string{"akira-toriyama/furrow"}) {
		t.Errorf("after repos = %v (short name should resolve)", res.After.Repos)
	}
	if !contains(res.Changed, "repos") {
		t.Errorf("changed should include repos, got %v", res.Changed)
	}

	// --rm detaches (short form accepted).
	out, _ = run(t, "--json", "repo", id, "--rm", "furrow")
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.After.Repos) != 0 || !contains(res.Changed, "repos") {
		t.Errorf("rm should empty repos + report the change: %+v %v", res.After.Repos, res.Changed)
	}

	// strict --add: a bare unknown name is exit 2, never a silent new repo.
	if _, code := run(t, "repo", id, "--add", "not-a-repo"); code != int(core.CodeValidation) {
		t.Errorf("strict --add should exit 2 on a bare unknown name, got %d", code)
	}
	// no flags is bad usage.
	if _, code := run(t, "repo", id); code != int(core.CodeValidation) {
		t.Errorf("repo with no flags should exit 2, got %d", code)
	}
	_ = seed
}

func TestCLIAddStdinWithRepo(t *testing.T) {
	initStore(t)
	out, code := runIn(t, "alpha\nbeta\n", "--ndjson", "add", "--stdin", "-s", "ready", "-r", "akira-toriyama/furrow")
	if code != 0 {
		t.Fatalf("add --stdin -r exit = %d:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 created tasks, got %d:\n%s", len(lines), out)
	}
	for _, l := range lines {
		var tk core.Task
		if err := json.Unmarshal([]byte(l), &tk); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tk.Repos, []string{"akira-toriyama/furrow"}) {
			t.Errorf("%s repos = %v; each stdin line must honor -r", tk.Title, tk.Repos)
		}
	}
}

func TestCLIRevisitNoRepoReason(t *testing.T) {
	initStore(t)
	id := addTask(t, "a draft", "-s", "ready", "--value", "3", "--effort", "2")

	out, code := run(t, "--json", "revisit")
	if code != 0 {
		t.Fatalf("revisit exit = %d:\n%s", code, out)
	}
	var rows []revisitRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("the draft should surface, got %+v", rows)
	}
	found := false
	for _, r := range rows[0].Revisit {
		if r.Code == core.RevisitNoRepo {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the no_repo reason, got %+v", rows[0].Revisit)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
