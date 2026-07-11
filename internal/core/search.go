package core

import (
	"strings"
	"unicode/utf8"
)

// ContainsFold reports whether term occurs in text as a case-insensitive
// substring. An empty term never matches — search requires a needle, so
// "matches everything" is deliberately not representable here (the app rejects
// an empty term before ever calling this).
func ContainsFold(text, term string) bool {
	if term == "" {
		return false
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(term))
}

// Snippet returns a one-line excerpt of text around the first case-insensitive
// occurrence of term. Whitespace (including newlines) is collapsed to single
// spaces so a multi-line body yields a single readable line; at most radius
// runes of context are kept on each side, boundary whitespace is trimmed, and
// an ellipsis (…) marks each truncated end. The original casing is preserved in
// the excerpt. It returns "" when term does not occur in text — callers gate on
// ContainsFold first, so "" is only the defensive path.
func Snippet(text, term string, radius int) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	if term == "" {
		return collapsed
	}
	lower := strings.ToLower(collapsed)
	i := strings.Index(lower, strings.ToLower(term))
	if i < 0 {
		return ""
	}
	if radius < 0 {
		radius = 0
	}
	runes := []rune(collapsed)
	// strings.ToLower maps rune-for-rune, so rune offsets are preserved between
	// collapsed and lower; count runes up to the byte match to get a rune index.
	start := utf8.RuneCountInString(lower[:i])
	end := start + utf8.RuneCountInString(term)
	if start > len(runes) {
		start = len(runes)
	}
	if end > len(runes) {
		end = len(runes)
	}
	lo, hi := start-radius, end+radius
	prefix, suffix := "", ""
	if lo <= 0 {
		lo = 0
	} else {
		prefix = "…"
	}
	if hi >= len(runes) {
		hi = len(runes)
	} else {
		suffix = "…"
	}
	return prefix + strings.TrimSpace(string(runes[lo:hi])) + suffix
}
