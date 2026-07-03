package core

import (
	"regexp"
	"strings"
)

// The [[<id>]] wiki-link notation lives here, in ONE place, so the two features
// that read it never drift: `show --backlinks` (which tasks mention this one)
// and `furrow lint`'s dangling-link check (a [[id]] pointing at nothing). Both
// call LinkPattern once and feed it to ExtractLinks.

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
// is intentionally lightweight, not a full CommonMark parser: a line whose
// trimmed text starts with ``` or ~~~ toggles a fenced block (fence lines and
// their contents are dropped), and on the remaining lines a run of N backticks
// opens an inline span that the next run of exactly N backticks closes (the span
// is dropped). Good enough to keep documented [[t-…]] placeholders from
// self-flagging; a pathological unbalanced fence just drops the tail, which only
// ever suppresses links (never invents one).
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

// backtickRun returns the length of the run of backticks in s starting at i.
func backtickRun(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}
