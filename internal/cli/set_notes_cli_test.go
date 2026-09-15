package cli

import (
	"strings"
	"testing"
)

// t-hs4a: `set` owes the human reader the same disclosures `value`/`reorder`
// give — a clamped estimate and a respaced lane are said on stderr in every
// mode, never only under --json. Both notes were computed inside the batch
// emitter's JSON branch, so the human run rounded 9 to 5 in silence and
// respaced a lane without a word; runCLI captured only stdout, so no test
// could see the silence.

func TestCLISetClampNoteInHumanMode(t *testing.T) {
	initStore(t)
	id := addTask(t, "a", "-r", "o/r")

	stdout, stderr, code := runSplit(t, "set", id, "--value", "9")
	if code != 0 {
		t.Fatalf("set exit = %d:\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "value 9 clamped to 5") {
		t.Errorf("human set --value 9 must say it clamped on stderr, got %q", stderr)
	}
	if strings.Contains(stdout, "clamped") {
		t.Errorf("the note belongs on stderr, not stdout:\n%s", stdout)
	}

	// The batch arm says it ONCE for the whole write.
	id2 := addTask(t, "b", "-r", "o/r")
	_, stderr, code = runSplit(t, "set", id, id2, "--effort", "0")
	if code != 0 {
		t.Fatalf("batch set exit = %d", code)
	}
	if n := strings.Count(stderr, "effort 0 clamped to 1"); n != 1 {
		t.Errorf("batch set must print the clamp note exactly once, got %d in %q", n, stderr)
	}
}

func TestCLISetRespaceNoteInHumanMode(t *testing.T) {
	initStore(t)
	a := addTask(t, "a")
	b := addTask(t, "b")
	c := addTask(t, "c")
	// Exhaust the gap between a and b, as the reorder test does.
	for _, seed := range [][]string{{"reorder", a, "10"}, {"reorder", b, "11"}} {
		if _, code := run(t, seed...); code != 0 {
			t.Fatalf("seed %v failed", seed)
		}
	}
	stdout, stderr, code := runSplit(t, "set", c, "--before", b)
	if code != 0 {
		t.Fatalf("set --before exit = %d:\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "respaced") {
		t.Errorf("human set --before that respaced the lane must say so on stderr, got %q", stderr)
	}
}
