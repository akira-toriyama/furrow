package core

import "strings"

// Git conflict markers in a task body, detected in ONE place so the two features
// that care never drift: `furrow lint`'s conflict-marker rule (a half-merged body
// already on the board) and `furrow sync`'s pre-commit guard (a half-merged body
// about to be PUT on the board).
//
// Why a body is worth guarding at all: furrow's progress record IS the body
// (docs/architecture.md), so a body carrying markers is half a record — and the
// half that is missing is usually the half someone just wrote.

// conflictMarkers are git's four marker characters. Each is written as a run of
// exactly 7 at column 0: "<<<<<<< ours", "||||||| base" (diff3/zdiff3), "=======",
// ">>>>>>> theirs".
var conflictMarkers = []byte{'<', '|', '=', '>'}

// ConflictMarkerLines returns the 1-based line numbers (as an editor counts them)
// of git conflict markers in text, in order. nil when the text is clean.
//
// `<<<<<<< `, `||||||| ` and `>>>>>>> ` are reported WHEREVER they stand, fence
// or not: no markdown has a legitimate use for a run of seven at column 0, and
// a conflict is exactly the thing that splits a fence — one side of the diff
// carrying a ``` the other does not — so a rule that skipped fences went silent
// on the real conflict it exists for (t-q5fk: one unpaired ~~~ above the
// markers hid all three, and lint's ERROR and sync's commit refusal fell
// together). Only the bare `=======` keeps the fence skip: it is the one shape
// prose can innocently hold (a setext underline), so inside a fence it is
// documentation. The cost of the stronger rule is that a body documenting what
// a conflict looks like must indent its `<<<<<<<`/`>>>>>>>` or quote them
// inline — the corpus held none at column 0 when the trade was reversed.
func ConflictMarkerLines(text string) []int {
	prose := map[int]bool{}
	eachProseLine(text, func(i int, _ string) { prose[i] = true })
	var lines []int
	for i, line := range strings.Split(text, "\n") {
		c, ok := conflictMarkerKind(line)
		if !ok || (c == '=' && !prose[i]) {
			continue
		}
		lines = append(lines, i+1)
	}
	return lines
}

// conflictMarkerKind matches one marker line and names WHICH marker it is
// (the bare `=======` is the one that answers to a fence): a run of EXACTLY 7
// marker characters at column 0, followed by a space (the ours/base/theirs
// label) or end-of-line (the bare "======="). Both halves of that rule earn
// their keep — column 0 keeps an inline `<<<<<<<` quoted in prose from
// matching, and the exact run of 7 keeps a markdown setext underline ("=====",
// "=========") from matching, which is the one shape a body might innocently
// contain.
func conflictMarkerKind(line string) (byte, bool) {
	line = strings.TrimSuffix(line, "\r") // a CRLF body must still match
	for _, c := range conflictMarkers {
		n := 0
		for n < len(line) && line[n] == c {
			n++
		}
		if n == 7 && (len(line) == 7 || line[7] == ' ') {
			return c, true
		}
	}
	return 0, false
}
