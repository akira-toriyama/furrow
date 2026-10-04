package core

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode"
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
	// strings.ToLower maps rune-for-rune, so rune offsets are preserved between
	// collapsed and lower; count runes up to the byte match to get a rune index.
	start := utf8.RuneCountInString(lower[:i])
	return window([]rune(collapsed), start, start+utf8.RuneCountInString(term), radius)
}

// window renders runes[start:end] with at most radius runes of context on each
// side as one line: whitespace runs collapse to one space, boundary whitespace
// is trimmed, and an ellipsis marks each truncated end.
func window(runes []rune, start, end, radius int) string {
	if radius < 0 {
		radius = 0
	}
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
	return prefix + strings.Join(strings.Fields(string(runes[lo:hi])), " ") + suffix
}

// Needle is what a text search looks for: a case-insensitive substring (the
// default, and the only form `-q` free text takes) or an RE2 pattern
// (`search --regex`). Build one with SubstringNeedle or RegexNeedle; neither
// matches the empty string.
type Needle struct {
	term string
	re   *regexp.Regexp
}

// SubstringNeedle looks for term as a case-insensitive substring
// (ContainsFold). An empty term matches nothing.
func SubstringNeedle(term string) Needle { return Needle{term: term} }

// RegexNeedle compiles pattern as RE2, case-insensitive unless the pattern
// clears the flag itself (`(?-i)`), so --regex folds case as the substring
// default does. Matching runs on the raw field text: `.` stops at a newline and
// `(?m)` makes ^/$ line anchors. A pattern that matches the empty string is
// refused — it would match every task, which search never answers (the same
// contract as the empty term).
func RegexNeedle(pattern string) (Needle, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		// The parser quotes the flag-prefixed expression; the caller wrote the
		// pattern without it, so the message names the fault on their spelling.
		msg := err.Error()
		var se *syntax.Error
		if errors.As(err, &se) {
			msg = fmt.Sprintf("%s: `%s`", se.Code, strings.TrimPrefix(se.Expr, "(?i)"))
		}
		return Needle{}, Validationf("", "--regex %q is not an RE2 pattern: %s", pattern, msg)
	}
	if re.MatchString("") {
		return Needle{}, Validationf("", "--regex %q matches the empty string, so it would match every task — anchor it to at least one character", pattern)
	}
	return Needle{re: re}, nil
}

// In reports whether the needle occurs in text.
func (n Needle) In(text string) bool {
	if n.re != nil {
		return n.re.MatchString(text)
	}
	return ContainsFold(text, n.term)
}

// Snippet is the one-line excerpt around the needle's first occurrence in text,
// shaped as the package Snippet shapes a substring hit: context is counted in
// the whitespace-collapsed text, so a regex hit and a substring hit on the same
// body read alike. A regex match is located on the RAW text (a line anchor
// needs the newlines), mapped into the collapsed runes, and a match longer than
// the two context windows is cut at that length so `.*` cannot turn the
// excerpt into the whole body. "" when the needle does not occur.
func (n Needle) Snippet(text string, radius int) string {
	if n.re == nil {
		return Snippet(text, n.term, radius)
	}
	loc := n.re.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	runes, at := collapseIndexed(text)
	start, end := at[loc[0]], at[loc[1]]
	if limit := 2 * max(radius, 0); end-start > limit {
		end = start + limit
	}
	return window(runes, start, end, radius)
}

// collapseIndexed is strings.Join(strings.Fields(text), " ") as runes, plus,
// for every rune-start byte offset of text and for len(text), the index in
// those runes where that byte's content lands (a whitespace byte maps to where
// the run's single space, if any, sits).
func collapseIndexed(text string) ([]rune, []int) {
	runes := make([]rune, 0, len(text))
	at := make([]int, len(text)+1)
	gap := false
	for i, r := range text {
		if unicode.IsSpace(r) {
			at[i] = len(runes)
			gap = len(runes) > 0
			continue
		}
		if gap {
			runes = append(runes, ' ')
			gap = false
		}
		at[i] = len(runes)
		runes = append(runes, r)
	}
	at[len(text)] = len(runes)
	return runes, at
}

// The fields a text search reads, named by their shard JSON keys — the values
// search reports as matched_field.
const (
	FieldTitle     = "title"
	FieldChecklist = "checklist"
	FieldRefs      = "refs"
	FieldBody      = "body"
)

// FindText returns the first field of t that holds n, in the order title, each
// checklist item, each ref, body, together with the text that matched: the
// title, the one item or ref, or the whole body. The three shard fields are
// tried first so a hit there never calls body. field "" means no match. This is
// the one walk behind both `search` and `-q` free text, so the two can never
// disagree about which tasks a term reaches.
func FindText(t *Task, n Needle, body func() (string, error)) (field, text string, err error) {
	if n.In(t.Title) {
		return FieldTitle, t.Title, nil
	}
	for _, it := range t.Checklist {
		if n.In(it.Text) {
			return FieldChecklist, it.Text, nil
		}
	}
	for _, r := range t.Refs {
		if n.In(r) {
			return FieldRefs, r, nil
		}
	}
	b, err := body()
	if err != nil {
		return "", "", err
	}
	if n.In(b) {
		return FieldBody, b, nil
	}
	return "", "", nil
}
