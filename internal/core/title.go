package core

import (
	"strings"
	"unicode"
)

// NormalizeTitle folds a task title to a single clean line: every control
// character (CR / LF / TAB and other C0/C1 controls) becomes a space, then
// whitespace runs are collapsed and the ends trimmed. A title is spliced into
// the body's "# " heading and printed in `ls`/`ls --tree`, so an interior
// newline would fabricate a heading/section in the body markdown and split a
// table row. Folding (rather than rejecting) keeps bulk/stdin input forgiving;
// a stray control character that reaches the store some other way is caught by
// lint's control-char check (TitleHasControl).
func NormalizeTitle(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	// strings.Fields splits on every Unicode whitespace run (incl. the spaces
	// just inserted and U+2028/U+2029 line/paragraph separators) and rejoins with
	// a single space, so the result is single-line and single-spaced, trimmed.
	return strings.Join(strings.Fields(b.String()), " ")
}

// TitleHasControl reports whether s contains a character NormalizeTitle would
// strip — an interior control character. lint uses it as the backstop for a
// title that reached the store WITHOUT going through NormalizeTitle: a bulk
// import (AddMany/migrate) or a hand-edited shard.
func TitleHasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}
