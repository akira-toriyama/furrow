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
// Fenced code blocks are skipped, exactly as ExtractLinks does: a body that
// DOCUMENTS what a conflict looks like writes the markers inside a ``` fence, and
// an error-severity rule that fires on that would cry wolf on the very board whose
// notes explain the rule. The cost is a real conflict whose markers land entirely
// inside a fence going unreported — a deliberate trade, and the reason the guard is
// not the only defence (the sync-side stash report is the other half).
func ConflictMarkerLines(text string) []int {
	var lines []int
	inFence := false
	for i, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue // the fence delimiter itself is never a marker
		}
		if inFence {
			continue
		}
		if isConflictMarker(line) {
			lines = append(lines, i+1)
		}
	}
	return lines
}

// HasConflictMarkers reports whether text carries any git conflict marker.
func HasConflictMarkers(text string) bool { return len(ConflictMarkerLines(text)) > 0 }

// isConflictMarker matches one marker line: a run of EXACTLY 7 marker characters
// at column 0, followed by a space (the ours/base/theirs label) or end-of-line
// (the bare "======="). Both halves of that rule earn their keep — column 0 keeps
// an inline `<<<<<<<` quoted in prose from matching, and the exact run of 7 keeps
// a markdown setext underline ("=====", "=========") from matching, which is the
// one shape a body might innocently contain.
func isConflictMarker(line string) bool {
	line = strings.TrimSuffix(line, "\r") // a CRLF body must still match
	for _, c := range conflictMarkers {
		n := 0
		for n < len(line) && line[n] == c {
			n++
		}
		if n == 7 && (len(line) == 7 || line[7] == ' ') {
			return true
		}
	}
	return false
}
