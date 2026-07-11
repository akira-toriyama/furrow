package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// lsIDs runs `ls --json ...` and returns the ids in output order.
func lsIDs(t *testing.T, args ...string) []string {
	t.Helper()
	full := append([]string{"--json", "ls"}, args...)
	out, code := run(t, full...)
	if code != 0 {
		t.Fatalf("ls exit=%d:\n%s", code, out)
	}
	return jsonIDs(t, out)
}

func TestLsSortByValue(t *testing.T) {
	initStore(t)
	lo := addTask(t, "lo", "--value", "1")
	hi := addTask(t, "hi", "--value", "5")
	mid := addTask(t, "mid", "--value", "3")
	un := addTask(t, "unset") // no value

	// --sort value: highest first, unset last.
	got := lsIDs(t, "--sort", "value")
	want := []string{hi, mid, lo, un}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("--sort value = %v, want %v", got, want)
	}

	// --reverse flips ranked order but keeps the unset task last.
	got = lsIDs(t, "--sort", "value", "--reverse")
	want = []string{lo, mid, hi, un}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("--sort value --reverse = %v, want %v", got, want)
	}
}

func TestLsSortValueTopN(t *testing.T) {
	initStore(t)
	addTask(t, "lo", "--value", "1")
	hi := addTask(t, "hi", "--value", "5")
	mid := addTask(t, "mid", "--value", "3")

	// -n with --sort takes the top N of the SORTED set, not canonical first-N.
	got := lsIDs(t, "--sort", "value", "-n", "2")
	if len(got) != 2 || got[0] != hi || got[1] != mid {
		t.Errorf("--sort value -n2 should be top 2 [hi,mid], got %v", got)
	}
}

func TestLsUnknownSortExit2(t *testing.T) {
	initStore(t)
	addTask(t, "x")

	fe, _ := runErr(t, "--json", "ls", "--sort", "bogus")
	if fe == nil || fe.Code != core.CodeValidation {
		t.Fatalf("unknown --sort should exit 2, got %+v", fe)
	}
	if len(fe.Candidates) == 0 {
		t.Errorf("unknown --sort should carry valid fields in candidates")
	}
}

func TestLsInvalidDateExit2(t *testing.T) {
	initStore(t)
	addTask(t, "x")

	_, code := run(t, "ls", "--since", "not-a-date")
	if code != int(core.CodeValidation) {
		t.Fatalf("an invalid --since should exit 2, got %d", code)
	}
}

func TestLsSinceUntilAcceptDates(t *testing.T) {
	initStore(t)
	addTask(t, "x")

	// A well-formed date window is accepted (exit 0); all tasks share the fixed
	// test clock, so this asserts parsing/plumbing, not the boundary itself
	// (covered at the app layer with seeded timestamps).
	_, code := run(t, "ls", "--since", "2000-01-01", "--until", "2100-12-31")
	if code != 0 {
		t.Fatalf("a valid date window should exit 0, got %d", code)
	}
	// A future-only window excludes everything but is still a clean empty result.
	out, code := run(t, "--json", "ls", "--since", "2099-01-01")
	if code != 0 {
		t.Fatalf("empty date window should exit 0, got %d:\n%s", code, out)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("a window matching nothing should print [], got:\n%s", out)
	}
}
