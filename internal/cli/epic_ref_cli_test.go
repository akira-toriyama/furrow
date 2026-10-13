package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

func epicRefBoard(t *testing.T) (boxOne, boxTwo, task string) {
	t.Helper()
	initStore(t)
	boxOne = addEpicWithAnchor(t, "box one", "")
	boxTwo = addEpicWithAnchor(t, "box two", "")
	task = addTask(t, "member", "-e", boxOne)
	return
}

// A task read handed a box ref fails as `epic show` would: an ambiguous title
// is epic-ambiguous with the candidates (it used to fold into "1 of 1 ids not
// found", exit 1, candidates lost); an epic-shaped id naming no box is
// epic-not-found at exit 2 like every other reader; a LIVE box's id says it is
// a box and points at `epic show`; a task miss stays not-found exit 1.
func TestShowReportsABoxRefAsTheBoxResolutionWould(t *testing.T) {
	boxOne, boxTwo, _ := epicRefBoard(t)
	fe, _ := runErr(t, "show", "box")
	if fe == nil || fe.Kind != core.KindEpicAmbiguous || fe.Code != core.CodeValidation || !slices.Contains(fe.Candidates, boxOne) || !slices.Contains(fe.Candidates, boxTwo) {
		t.Errorf("show <ambiguous title> = %+v; want epic-ambiguous exit 2 naming both boxes", fe)
	}
	fe, _ = runErr(t, "show", "e-nope0")
	if fe == nil || fe.Kind != core.KindEpicNotFound || fe.Code != core.CodeValidation {
		t.Errorf("show e-nope0 = %+v; want epic-not-found exit 2", fe)
	}
	if out, code := run(t, "--json", "show", boxOne, "--no-body"); code != 0 || !strings.Contains(out, boxOne) {
		t.Errorf("show <box id> still reads the box: %d\n%s", code, out)
	}
	fe, _ = runErr(t, "show", "t-nope0")
	if fe == nil || fe.Kind != core.KindNotFound || fe.Code != core.CodeNotFound {
		t.Errorf("show t-nope0 = %+v; want not-found exit 1", fe)
	}
}

// One kind, one exit code: epic-not-found is exit 2 on every command that
// resolves a box ref (show used to say exit 1 for the same kind).
func TestEpicNotFoundExitsTheSameEverywhere(t *testing.T) {
	_, _, task := epicRefBoard(t)
	for _, args := range [][]string{
		{"show", "e-nope0"},
		{"epic", "show", "e-nope0"},
		{"epic", "set", "e-nope0", "--goal", "x"},
		{"ls", "-e", "e-nope0"},
		{"ls", "-q", "epic:e-nope0"},
		{"set", task, "-e", "e-nope0"},
	} {
		fe, _ := runErr(t, args...)
		if fe == nil || fe.Kind != core.KindEpicNotFound || fe.Code != core.CodeValidation {
			t.Errorf("%v = %+v; want epic-not-found exit 2", args, fe)
		}
	}
}

// `furrow epic foo` is an unknown subcommand with the known names in
// candidates, like `config foo` and the root (it was a bare validation
// error with no candidates).
func TestEpicUnknownSubcommandNamesTheKnownOnes(t *testing.T) {
	initStore(t)
	fe, _ := runErr(t, "epic", "foo")
	if fe == nil || fe.Kind != core.KindUnknownSubcommand || fe.Code != core.CodeValidation || !slices.Contains(fe.Candidates, "add") || !slices.Contains(fe.Candidates, "activate") {
		t.Errorf("epic foo = %+v; want unknown-subcommand with the subcommands as candidates", fe)
	}
	if out, code := run(t, "epic"); code != 0 || !strings.Contains(out, "Usage") {
		t.Errorf("a bare `epic` still prints help: %d\n%s", code, out)
	}
}

// `ls <epic>` without --tree names both flags that take a box instead of
// "unknown command".
func TestLsPositionalWithoutTreeNamesTheFlags(t *testing.T) {
	boxOne, _, _ := epicRefBoard(t)
	fe, _ := runErr(t, "ls", boxOne)
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "--tree "+boxOne) || !strings.Contains(fe.Msg, "-e "+boxOne) {
		t.Errorf("ls <epic> = %+v; want a refusal naming --tree and -e", fe)
	}
	if out, code := run(t, "ls", "--tree", boxOne); code != 0 {
		t.Errorf("ls --tree <epic> still works: %d\n%s", code, out)
	}
}

// `epic add <task> <epic>` in either order points at the exact `set -e`
// call, by resolution: the reversed spelling used to propose `set <epic> -e
// <task>`, and ids that did not exist got a call too. A title spelling of the
// box resolves as well.
func TestEpicAddRedirectResolvesEitherOrder(t *testing.T) {
	boxOne, _, task := epicRefBoard(t)
	want := "furrow set " + task + " -e " + boxOne
	for _, args := range [][]string{{task, boxOne}, {boxOne, task}, {"box one", task}} {
		fe, _ := runErr(t, append([]string{"epic", "add"}, args...)...)
		if fe == nil || fe.Code != core.CodeValidation || !slices.Equal(fe.Candidates, []string{want}) {
			t.Errorf("epic add %v = %+v; want candidates [%s]", args, fe, want)
		}
	}
	for _, args := range [][]string{{"t-nope0", boxOne}, {task, "e-nope0"}, {task, boxOne, "extra"}} {
		fe, _ := runErr(t, append([]string{"epic", "add"}, args...)...)
		if fe == nil || fe.Code != core.CodeValidation {
			t.Errorf("epic add %v = %+v; want exit 2", args, fe)
		}
		if len(args) == 2 && len(fe.Candidates) != 0 {
			t.Errorf("epic add %v must not propose a call for an id that does not exist: %v", args, fe.Candidates)
		}
	}
}
