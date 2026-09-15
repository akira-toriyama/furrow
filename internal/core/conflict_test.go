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
}

// The false positives an error-severity rule must not have. A body that DOCUMENTS
// a conflict quotes the markers inline or indents them; a fence only shelters
// the bare `=======` (see TestConflictMarkerLinesFencesShelterOnlyTheSeparator).
func TestConflictMarkerLinesIgnoresProseAndFences(t *testing.T) {
	clean := []struct{ name, body string }{
		{"fenced separator", "text\n```\nHeading\n=======\n```\ntail\n"},
		{"tilde-fenced separator", "~~~\n=======\n~~~\n"},
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
	if len(ConflictMarkerLines("prose\n=======\nmore\n")) == 0 {
		t.Error("a bare 7-char ======= separator is git's, and must be flagged")
	}
}

// A conflict is exactly what splits a fence — one side of the diff carrying a
// ``` the other does not — so the ours/base/theirs markers are reported wherever
// they stand, and only the bare `=======` (a legal setext underline) keeps the
// fence's shelter. With the old all-markers skip, the single unpaired ~~~ here
// hid every marker: lint's ERROR and sync's commit refusal went silent together
// on a half-merged body (t-q5fk).
func TestConflictMarkerLinesFencesShelterOnlyTheSeparator(t *testing.T) {
	unpaired := "intro\n~~~\nnot a fence pair\n<<<<<<< ours\nmine\n=======\ntheirs\n>>>>>>> theirs\n"
	if got := ConflictMarkerLines(unpaired); !slices.Equal(got, []int{4, 8}) {
		t.Errorf("unpaired fence: got %v, want [4 8] (the separator alone is sheltered)", got)
	}
	documented := "text\n```\n<<<<<<< HEAD\n=======\n>>>>>>> other\n```\ntail\n"
	if got := ConflictMarkerLines(documented); !slices.Equal(got, []int{3, 5}) {
		t.Errorf("fenced example: got %v, want [3 5] — <<<<<<< and >>>>>>> have no markdown meaning, fenced or not", got)
	}
	if got := ConflictMarkerLines("~~~\n||||||| base\n~~~\n"); !slices.Equal(got, []int{2}) {
		t.Errorf("fenced base marker: got %v, want [2]", got)
	}
}
