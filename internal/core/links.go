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
	eachProseLine(md, func(_ int, line string) {
		b.WriteString(stripInlineCode(line))
		b.WriteByte('\n')
	})
	return b.String()
}

// eachProseLine calls fn with every line of md that is OUTSIDE a fenced code
// block, with its 0-based index; the fence delimiter lines themselves are not
// prose either. It is the ONE fence scanner: ExtractLinks, RewriteLinks and
// ConflictMarkerLines all read fences through it, so no two readers of a body
// can disagree on what a fence hides (three hand-copied toggles did, t-q5fk).
// A ``` or ~~~ line toggles; an unclosed fence runs to the end, as CommonMark
// says it does.
func eachProseLine(md string, fn func(i int, line string)) {
	inFence := false
	for i, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		fn(i, line)
	}
}

// stripInlineCode removes the inline code spans of one line, delimiters
// included; the prose between them is kept.
func stripInlineCode(line string) string {
	var b strings.Builder
	start := 0
	for _, sp := range inlineCodeSpans(line) {
		b.WriteString(line[start:sp[0]])
		start = sp[1]
	}
	b.WriteString(line[start:])
	return b.String()
}

// inlineCodeSpans returns the [start, end) byte ranges of one line's inline
// code spans, delimiters included, by CommonMark's rule: a run of N backticks
// opens a span, and the next run of EXACTLY N closes it. A run with no closer
// is ordinary text — not the start of a span reaching to the end of the line —
// and scanning resumes after it. It is the one inline-code walker: the strip
// (ExtractLinks) and the keep (RewriteLinks) sides both read spans from it.
// Two copies used to treat an unclosed run as code to the end of the line, so
// a stray backtick — `x“ or “x` — hid every [[id]] after it and `furrow rm`
// deleted a task the body still linked (t-q5fk).
func inlineCodeSpans(line string) [][2]int {
	var spans [][2]int
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		n := backtickRun(line, i)
		j := i + n
		for j < len(line) {
			if line[j] == '`' {
				m := backtickRun(line, j)
				if m == n {
					break
				}
				j += m
				continue
			}
			j++
		}
		if j >= len(line) {
			i += n // no closer: the run is literal text
			continue
		}
		spans = append(spans, [2]int{i, j + n})
		i = j + n
	}
	return spans
}

func backtickRun(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

// rewriteLinks applies fn to every LIVE [[id]] link in text — outside fenced
// and inline code, by the same rules ExtractLinks reads by, so a rewrite and
// lint's dangling-link check agree on which links are real and a documented
// [[t-…]] example survives verbatim. fn receives the id inside the brackets
// and returns the replacement for the WHOLE [[id]] token plus whether to
// replace it; false keeps the token as written. Reports how many tokens
// changed. It is the one link-rewriting walker: the v6 migration (an id
// changes) and `furrow rm --force` (an id ceases to exist) both go through it.
func rewriteLinks(text string, re *regexp.Regexp, fn func(id string) (string, bool)) (string, int) {
	n := 0
	lines := strings.Split(text, "\n")
	eachProseLine(text, func(i int, line string) {
		lines[i] = rewriteLinksOutsideInlineCode(line, re, fn, &n)
	})
	return strings.Join(lines, "\n"), n
}

// UnlinkIDs turns every live [[id]] whose id is in ids into the bare id — the
// prose keeps saying what it said, it just stops pointing at a record that no
// longer exists. A bare id is deliberately not a link (see ExtractLinks), so
// the result is exactly what lint's dangling-link would otherwise flag, minus
// the flag.
func UnlinkIDs(text string, re *regexp.Regexp, ids map[string]bool) (string, int) {
	return rewriteLinks(text, re, func(id string) (string, bool) {
		if ids[id] {
			return id, true
		}
		return "", false
	})
}

// rewriteLinksOutsideInlineCode applies fn to the non-code segments of one
// line — the spans inlineCodeSpans finds are kept verbatim, exactly the spans
// stripInlineCode drops, so the two sides agree on which links are real.
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
	for _, sp := range inlineCodeSpans(line) {
		b.WriteString(rewrite(line[start:sp[0]]))
		b.WriteString(line[sp[0]:sp[1]])
		start = sp[1]
	}
	b.WriteString(rewrite(line[start:]))
	return b.String()
}
