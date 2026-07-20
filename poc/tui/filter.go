package main

import (
	"fmt"
	"sort"
	"strings"
)

// A typed query over the board, modelled on GitHub Projects' filter bar.
//
// Grammar (deliberately small):
//
//	token       := ["-"] (key ":" values | word)
//	values      := value ["," value]...
//	key         := lane|status|repo|label|type|is|no|has|id|parent
//
// Comma inside ONE token is OR; separate tokens AND; a leading "-" negates the
// whole token; a bare word is a case-insensitive substring of the title or id.
//
// This is the one place a TUI beats a browser outright: the entire filter UI is
// a text input, so the full grammar is available with no menus to build.

// Term is one parsed token.
type Term struct {
	Neg  bool
	Key  string // "" = bare word
	Vals []string
}

// Query is a parsed filter expression.
type Query struct {
	Raw      string
	Terms    []Term
	Problems []string // unparseable tokens, reported but not fatal
}

// Empty reports a query that matches everything.
func (q Query) Empty() bool { return len(q.Terms) == 0 }

var queryKeys = map[string]bool{
	"lane": true, "status": true, "repo": true, "label": true,
	"type": true, "is": true, "no": true, "has": true,
	"id": true, "parent": true,
}

var isValues = map[string]bool{
	"blocked": true, "actionable": true, "done": true, "open": true,
	"epic": true, "container": true, "stuck": true, "draft": true,
}

var noHasValues = map[string]bool{
	"repo": true, "label": true, "dep": true, "parent": true,
	"body": true, "checklist": true, "value": true, "effort": true,
}

// ParseQuery parses a filter expression. It never fails outright: an
// unrecognised token is recorded in Problems and skipped, so the rest of the
// query still applies while you are mid-type.
func ParseQuery(s string) Query {
	q := Query{Raw: s}
	for _, tok := range strings.Fields(s) {
		t := Term{}
		if strings.HasPrefix(tok, "-") && len(tok) > 1 {
			t.Neg = true
			tok = tok[1:]
		}
		k, v, isPair := strings.Cut(tok, ":")
		if !isPair {
			t.Vals = []string{strings.ToLower(tok)}
			q.Terms = append(q.Terms, t)
			continue
		}
		k = strings.ToLower(k)
		if !queryKeys[k] {
			q.Problems = append(q.Problems, fmt.Sprintf("unknown key %q", k))
			continue
		}
		if v == "" {
			q.Problems = append(q.Problems, fmt.Sprintf("%s: needs a value", k))
			continue
		}
		if k == "status" {
			k = "lane"
		}
		t.Key = k
		for _, part := range strings.Split(v, ",") {
			if part == "" {
				continue
			}
			t.Vals = append(t.Vals, strings.ToLower(part))
		}
		// An unrecognised VALUE is dropped, not kept. Keeping it made the term
		// match nothing at all, so `is:bogus` emptied the whole board while the
		// contract above promises "reported but not fatal" — the loudest possible
		// way to be non-fatal.
		switch k {
		case "is":
			t.Vals, q.Problems = keepKnown(t.Vals, isValues, q.Problems,
				func(v string) string {
					return fmt.Sprintf("is:%s is not a thing (%s) — ignored", v, keyList(isValues))
				})
		case "no", "has":
			t.Vals, q.Problems = keepKnown(t.Vals, noHasValues, q.Problems,
				func(v string) string {
					return fmt.Sprintf("%s:%s is not a field (%s) — ignored", k, v, keyList(noHasValues))
				})
		}
		if len(t.Vals) > 0 {
			q.Terms = append(q.Terms, t)
		}
	}
	return q
}

// keepKnown filters a term's values down to the ones the vocabulary knows,
// recording a problem for each one it dropped.
func keepKnown(vals []string, vocab map[string]bool, problems []string,
	msg func(string) string) ([]string, []string) {

	var keep []string
	for _, v := range vals {
		if vocab[v] {
			keep = append(keep, v)
			continue
		}
		problems = append(problems, msg(v))
	}
	return keep, problems
}

func keyList(m map[string]bool) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

// Match reports whether a task satisfies the whole query: every term must hold
// (a negated term must NOT hold), and within a term any value may.
func (q Query) Match(t *Task, g *Graph) bool {
	for _, term := range q.Terms {
		got := term.matches(t, g)
		if got == term.Neg {
			return false
		}
	}
	return true
}

func (t Term) matches(task *Task, g *Graph) bool {
	for _, v := range t.Vals {
		if t.matchOne(task, g, v) {
			return true
		}
	}
	return false
}

func (t Term) matchOne(task *Task, g *Graph, v string) bool {
	switch t.Key {
	case "":
		return strings.Contains(strings.ToLower(task.Title), v) ||
			strings.Contains(strings.ToLower(task.ID), v)
	case "lane":
		return strings.ToLower(task.Status) == v
	case "id":
		return strings.ToLower(task.ID) == v
	case "parent":
		return strings.ToLower(task.Parent) == v
	case "type":
		return strings.ToLower(task.EffectiveType()) == v
	case "repo":
		return containsFold(task.Repos, v)
	case "label":
		return containsFold(task.Labels, v)
	case "is":
		return t.matchIs(task, g, v)
	case "no":
		return !hasField(task, v)
	case "has":
		return hasField(task, v)
	}
	return false
}

func (t Term) matchIs(task *Task, g *Graph, v string) bool {
	switch v {
	case "blocked":
		return len(g.BlockedBy(task.ID)) > 0
	case "actionable":
		return g.Actionable(task.ID)
	case "done":
		return g.IsDone(task.ID)
	case "open":
		return !g.IsDone(task.ID)
	case "epic", "container":
		return g.IsContainer(task.ID)
	case "stuck":
		return g.Stuck(task.ID)
	case "draft":
		return len(task.Repos) == 0
	}
	return false
}

func hasField(t *Task, field string) bool {
	switch field {
	case "repo":
		return len(t.Repos) > 0
	case "label":
		return len(t.Labels) > 0
	case "dep":
		return len(t.Deps) > 0
	case "parent":
		return t.Parent != ""
	case "body":
		return strings.TrimSpace(t.Body) != ""
	case "checklist":
		return len(t.Checklist) > 0
	case "value":
		return t.Value > 0
	case "effort":
		return t.Effort > 0
	}
	return false
}

// containsFold reports a case-insensitive substring hit in any element, so
// "repo:vista" matches "akira-toriyama/vista" the way GitHub's does.
func containsFold(hay []string, needle string) bool {
	for _, h := range hay {
		if strings.Contains(strings.ToLower(h), needle) {
			return true
		}
	}
	return false
}
