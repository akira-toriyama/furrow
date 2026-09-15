package core

import (
	"fmt"
	"strings"
	"unicode"
)

// NormalizeTitle folds a task title to a single clean line: every control
// character (CR / LF / TAB and other C0/C1 controls) becomes a space, then
// whitespace runs are collapsed and the ends trimmed. A title is spliced into
// the body's "# " heading and printed in `ls`/`ls --tree`, so an interior
// newline would fabricate a heading/section in the body markdown and split a
// table row, and an escape sequence would reach the reader's terminal.
// Folding (rather than rejecting) keeps bulk/stdin input forgiving;
// a stray control character that reaches the store some other way is caught by
// lint's control-char check (TitleHasControl).
//
// A FORMAT character (Unicode Cf — U+202E RIGHT-TO-LEFT OVERRIDE, zero-width
// joiners, soft hyphens) is dropped outright: it renders nothing and steers
// the terminal instead, so an override in one title flipped every row after
// it in `ls` (t-awr6). Length is the writer's refusal (MaxTitleLen), not a
// fold: a 10,000-character title is not a title with a typo.
func NormalizeTitle(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.IsControl(r):
			b.WriteByte(' ')
		case unicode.Is(unicode.Cf, r):
			// dropped
		default:
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
// title that reached the store WITHOUT going through NormalizeTitle: a
// HAND-EDITED shard, or a writer that forgets to fold.
func TitleHasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
}

// MaxTitleLen is the longest title a writer accepts, in characters. A title
// is one row's last column in every human view and the body's H1; there is no
// truncation on the way out (the JSON is the record), so the cap is the one
// bound between a shard and a wrecked terminal.
const MaxTitleLen = 500

// TitleTooLong is the writer's refusal for a title past MaxTitleLen, "" when
// it fits. Counted in characters, not bytes: a CJK title is not shorter for
// being wider.
func TitleTooLong(title string) string {
	if n := len([]rune(title)); n > MaxTitleLen {
		return fmt.Sprintf("title is %d characters; the limit is %d — put the rest in the body", n, MaxTitleLen)
	}
	return ""
}
