package core

import (
	"slices"
	"testing"
)

// The three shapes git actually writes, plus diff3's base marker.
func TestConflictMarkerLinesFindsEveryMarker(t *testing.T) {
	body := "# task\n" +
		"<<<<<<< Updated upstream\n" +
		"theirs\n" +
		"||||||| base\n" +
		"common\n" +
		"=======\n" +
		"ours\n" +
		">>>>>>> Stashed changes\n" +
		"tail\n"
	got := ConflictMarkerLines(body)
	want := []int{2, 4, 6, 8}
	if !slices.Equal(got, want) {
		t.Errorf("ConflictMarkerLines = %v, want %v (1-based, as an editor counts)", got, want)
	}
	if !HasConflictMarkers(body) {
		t.Error("HasConflictMarkers must agree with ConflictMarkerLines")
	}
}

// The false positives an error-severity rule must not have. A body that DOCUMENTS
// a conflict (furrow's own notes do) writes the markers inside a fence or inline —
// flagging that would cry wolf on the board that explains the rule.
func TestConflictMarkerLinesIgnoresProseAndFences(t *testing.T) {
	clean := []struct{ name, body string }{
		{"fenced example", "text\n```\n<<<<<<< HEAD\n=======\n>>>>>>> other\n```\ntail\n"},
		{"tilde-fenced example", "~~~\n<<<<<<< HEAD\n~~~\n"},
		{"inline code", "the markers (`<<<<<<< Updated upstream` / `=======`) are git's\n"},
		{"setext underline", "Heading\n=====\n\nOther\n=========\n"},
		{"indented marker", "  <<<<<<< not at column 0\n"},
		{"eight chars", "========\n<<<<<<<<\n"},
		{"empty", ""},
	}
	for _, c := range clean {
		if lines := ConflictMarkerLines(c.body); lines != nil {
			t.Errorf("%s: must not be flagged, got lines %v", c.name, lines)
		}
	}
}

// A body written on Windows still merges (and still conflicts) — the trailing \r
// must not hide the marker.
func TestConflictMarkerLinesHandlesCRLF(t *testing.T) {
	if lines := ConflictMarkerLines("a\r\n=======\r\nb\r\n"); !slices.Equal(lines, []int{2}) {
		t.Errorf("CRLF body: got %v, want [2]", lines)
	}
}

// A bare "=======" (7 exactly) IS a marker: git writes the separator with no
// label, and a half-resolved body can be left with only that line. The setext case
// above is what keeps this from over-matching — the run must be exactly 7.
func TestConflictMarkerLinesBareSeparator(t *testing.T) {
	if !HasConflictMarkers("prose\n=======\nmore\n") {
		t.Error("a bare 7-char ======= separator is git's, and must be flagged")
	}
}
