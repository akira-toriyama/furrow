package core

import "regexp"

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
// agent's id-laden progress log never reads as a mention. Returns nil when there
// are no links.
func ExtractLinks(text string, re *regexp.Regexp) []string {
	ms := re.FindAllStringSubmatch(text, -1)
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
