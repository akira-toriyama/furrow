package app

import "testing"

// layoutOf is the whole LAYOUT axis: a fact derived from how discovery reached
// the board, never a config key. SourceLocal is the only arm that sits inside
// the tree it serves; every other arm was reached by configuration, and a new
// arm added later must decide deliberately rather than fall through by accident
// — which is why this table names all four sources explicitly.
func TestLayoutOfEverySource(t *testing.T) {
	cases := map[string]string{
		SourceLocal:      LayoutRepoLocal,
		SourceEnv:        LayoutCentral,
		SourcePointer:    LayoutCentral,
		SourceUserConfig: LayoutCentral,
	}
	for source, want := range cases {
		if got := layoutOf(source); got != want {
			t.Errorf("layoutOf(%q) = %q, want %q", source, got, want)
		}
	}
	if len(cases) != len(sourceVocabulary()) {
		t.Errorf("a discovery source was added without a layout: %v vs %v", cases, sourceVocabulary())
	}
}

// sourceVocabulary is the test's own copy of the Source* set — deliberately a
// copy, so adding a constant without deciding its layout fails the test above
// instead of silently defaulting to central.
func sourceVocabulary() []string {
	return []string{SourceEnv, SourceLocal, SourcePointer, SourceUserConfig}
}

// Layouts is what `furrow vocab layouts` prints, so it must carry every member
// layoutOf can return — a vocabulary that under-reports is worse than none.
func TestLayoutsCoversEveryReturn(t *testing.T) {
	got := map[string]bool{}
	for _, l := range Layouts() {
		got[l] = true
	}
	for _, s := range sourceVocabulary() {
		if !got[layoutOf(s)] {
			t.Errorf("layoutOf(%q) = %q is not in Layouts() = %v", s, layoutOf(s), Layouts())
		}
	}
}
