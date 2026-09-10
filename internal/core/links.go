package core

import (
	"regexp"
	"strings"
)

// The [[<id>]] wiki-link notation lives here, in ONE place: every feature that
// resolves a link builds its regex with LinkPattern, so no two readers of a
// body can disagree on what counts as a link.

// LinkPattern compiles the regex matching a [[<id>]] reference embedded in body
// prose, for the given id prefix. The single capture group is the referenced id
// (prefix + [0-9a-z]+, the frozen-id shape). Building it from the prefix keeps
// the notation in step with whatever [ids].prefix the board uses.
func LinkPattern(idPrefix string) *regexp.Regexp {
	return regexp.MustCompile(`\[\[(` + regexp.QuoteMeta(idPrefix) + `[0-9a-z]+)\]\]`)
}

// ExtractLinks returns the ids referenced via [[<id>]] in text, in first-seen
// order and de-duplicated. re must be a LinkPattern. A bare id (no brackets) is
// deliberately NOT a link — only the explicit [[...]] notation counts, so an
// agent's id-laden progress log never reads as a mention. Code is stripped
// first (see stripCode), so a [[t-x]] written as a documented EXAMPLE inside
// `backticks` or a ``` fence ``` is not treated as a real link — matching how
// GitHub and Obsidian resolve mentions, and keeping furrow's own bodies (which
// document the notation with [[t-…]] placeholders) from self-flagging. Returns
// nil when there are no links.
func ExtractLinks(text string, re *regexp.Regexp) []string {
	ms := re.FindAllStringSubmatch(stripCode(text), -1)
	if len(ms) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range ms {
		id := m[1]
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// stripCode removes fenced code blocks and inline code spans from markdown so a
// [[id]] written as an example inside code is not mistaken for a live link. It
// is intentionally lightweight, not a full CommonMark parser: good enough to
// keep documented [[t-…]] placeholders from self-flagging, and a pathological
// unbalanced fence just drops the tail, which only ever suppresses links (never
// invents one).
func stripCode(md string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue // drop the fence delimiter line itself
		}
		if inFence {
			continue
		}
		b.WriteString(stripInlineCode(line))
		b.WriteByte('\n')
	}
	return b.String()
}

// stripInlineCode removes backtick-delimited inline code spans from one line: a
// run of N backticks opens, the next run of exactly N backticks closes, and the
// whole span (delimiters included) is dropped. An unterminated opening run drops
// the rest of the line.
func stripInlineCode(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			b.WriteByte(line[i])
			i++
			continue
		}
		n := backtickRun(line, i)
		j := i + n
		for j < len(line) {
			if line[j] == '`' && backtickRun(line, j) == n {
				break
			}
			j++
		}
		if j >= len(line) {
			return b.String() // unterminated span: drop the tail
		}
		i = j + n // skip the span and its closing run
	}
	return b.String()
}

func backtickRun(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

// RewriteLinks applies fn to every LIVE [[id]] link in text — outside fenced
// and inline code, by the same rules ExtractLinks reads by, so a rewrite and
// lint's dangling-link check agree on which links are real and a documented
// [[t-…]] example survives verbatim. fn receives the id inside the brackets
// and returns the replacement for the WHOLE [[id]] token plus whether to
// replace it; false keeps the token as written. Reports how many tokens
// changed. It is the one link-rewriting walker: the v6 migration (an id
// changes) and `furrow rm --force` (an id ceases to exist) both go through it.
func RewriteLinks(text string, re *regexp.Regexp, fn func(id string) (string, bool)) (string, int) {
	n := 0
	lines := strings.Split(text, "\n")
	inFence := false
	for li, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[li] = rewriteLinksOutsideInlineCode(line, re, fn, &n)
	}
	return strings.Join(lines, "\n"), n
}

// UnlinkIDs turns every live [[id]] whose id is in ids into the bare id — the
// prose keeps saying what it said, it just stops pointing at a record that no
// longer exists. A bare id is deliberately not a link (see ExtractLinks), so
// the result is exactly what lint's dangling-link would otherwise flag, minus
// the flag.
func UnlinkIDs(text string, re *regexp.Regexp, ids map[string]bool) (string, int) {
	return RewriteLinks(text, re, func(id string) (string, bool) {
		if ids[id] {
			return id, true
		}
		return "", false
	})
}

// rewriteLinksOutsideInlineCode applies fn to the non-code segments of one
// line, walking inline code spans with the same rules as stripInlineCode (a
// run of N backticks opens, the next run of exactly N closes; an unterminated
// run code-quotes the rest of the line) — but keeping the spans verbatim
// instead of dropping them.
func rewriteLinksOutsideInlineCode(line string, re *regexp.Regexp, fn func(id string) (string, bool), n *int) string {
	rewrite := func(seg string) string {
		return re.ReplaceAllStringFunc(seg, func(m string) string {
			id := m[2 : len(m)-2] // the match is the full [[id]]
			if repl, ok := fn(id); ok {
				*n++
				return repl
			}
			return m
		})
	}
	var b strings.Builder
	start := 0
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		run := backtickRun(line, i)
		j := i + run
		for j < len(line) {
			if line[j] == '`' && backtickRun(line, j) == run {
				break
			}
			j++
		}
		b.WriteString(rewrite(line[start:i]))
		if j >= len(line) {
			b.WriteString(line[i:]) // unterminated span: the tail is code
			return b.String()
		}
		b.WriteString(line[i : j+run]) // the span, verbatim
		i = j + run
		start = i
	}
	b.WriteString(rewrite(line[start:]))
	return b.String()
}
