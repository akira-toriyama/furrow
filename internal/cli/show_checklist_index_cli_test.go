package cli

import (
	"strings"
	"testing"
)

// t-fsvt: `check <id> <i>` takes a zero-based position, and `show` printed
// the rows with no position — so every session counted rows by hand and then
// opened `check --help` to learn whether the count starts at 0 or 1 (three
// drills in a row). Each row now leads with the index `check` takes; the
// proof is that ticking by the number `show` printed ticks that row.
func TestCLIShowChecklistRowsCarryTheIndexCheckTakes(t *testing.T) {
	initStore(t)
	id := addTask(t, "three steps", "-r", "o/r", "--check", "write tests", "--check", "update docs", "--check", "ship")

	if out, code := run(t, "check", id, "1"); code != 0 {
		t.Fatalf("check exit = %d:\n%s", code, out)
	}
	human, code := run(t, "show", id)
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, human)
	}
	for _, want := range []string{
		"checklist: 1/3\n",
		"  0  [ ] write tests\n",
		"  1  [x] update docs\n",
		"  2  [ ] ship\n",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("show must print %q — the row led by the index check takes:\n%s", want, human)
		}
	}

	// Past ten rows the column widens to the last index, so the boxes stay
	// aligned and a two-digit index cannot be misread as a one-digit one.
	args := []string{"eleven steps", "-r", "o/r"}
	for i := 0; i < 11; i++ {
		args = append(args, "--check", "step")
	}
	long := addTask(t, args...)
	human, _ = run(t, "show", long)
	if !strings.Contains(human, "   0  [ ] step\n") || !strings.Contains(human, "  10  [ ] step\n") {
		t.Errorf("an eleven-row list must right-align its indexes to two columns:\n%s", human)
	}

	// JSON carries no index key: the array position is the index, and a
	// derived number stored beside it would be one more field to drift.
	out, _ := run(t, "--json", "show", id)
	if strings.Contains(out, `"index"`) {
		t.Errorf("show --json must not grow an index field:\n%s", out)
	}
}
