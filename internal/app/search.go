package app

import (
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// snippetRadius is the runes of body context Search keeps on each side of a
// match. A title, checklist item or ref is short and returned whole; a body
// gets a windowed excerpt.
const snippetRadius = 60

// SearchHit is one task matched by Search: the task, which field carried the
// match (core.FindText's title|checklist|refs|body), and a one-line snippet of
// the matched text in context.
type SearchHit struct {
	Task         core.Task
	MatchedField string
	Snippet      string
}

// Search returns the tasks whose text holds term, in canonical order, after
// applying the query's scope filters — the same -s/-l/-r/-n semantics as List.
// term is a case-insensitive substring, or with regex an RE2 pattern
// (core.RegexNeedle). The fields are walked by core.FindText — the walk `-q`
// free text shares — so a title, checklist-item or ref hit reports that field
// with the matched text whole (whitespace collapsed to one line) and never
// reads the body; a body hit reports a windowed excerpt. The body scan is O(board) with no
// index — the same "an index is YAGNI" stance as Backlinks. term is required:
// an empty/blank term is a validation error, not a match-everything. A -s
// naming an unknown lane fails fast (validateLaneFilter, symmetric with List).
// A zero-match result is healthy (exit 0), never a miss.
func (a *App) Search(o QueryOpts, term string, regex bool) ([]SearchHit, error) {
	if strings.TrimSpace(term) == "" {
		return nil, core.Validationf("", "search term must not be empty")
	}
	needle := core.SubstringNeedle(term)
	if regex {
		var err error
		if needle, err = core.RegexNeedle(term); err != nil {
			return nil, err
		}
	}
	if err := a.validateLaneFilter(o.Status); err != nil {
		return nil, err
	}
	// The index AND the body loader must come from the same store. Searching
	// the archive index with the hot store's LoadBody would match on title and
	// then silently miss (or error on) every body hit — the failure would look
	// green in any test that only searches titles.
	idx, loadBody, err := a.searchSource(o)
	if err != nil {
		return nil, err
	}
	// Compile -q once; it ANDs with the term like every other filter, and is
	// evaluated before the term so a query-excluded task never pays for a body
	// read the text match would otherwise do. The compiler's cached body
	// reader replaces the raw loader below, so a body the query already read
	// (body:/free-text terms) is never loaded a second time for the snippet
	// scan — `ls` pins one-load-per-body (TestQueryBodyLoadIsLazy) and search
	// holds the same invariant through the same cache.
	qpred, qbody, err := a.queryPredShared(o.Query, idx, queryRead{staleDays: a.Cfg.RevisitStaleDays, loadBody: loadBody, archived: o.Archived})
	if err != nil {
		return nil, err
	}
	readBody := func(t *core.Task) (string, error) { return loadBody(t.ID) }
	if qbody != nil {
		readBody = qbody
	}
	var out []SearchHit
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) {
			continue
		}
		if qpred != nil {
			ok, err := qpred(t)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		field, text, err := core.FindText(t, needle, func() (string, error) { return readBody(t) })
		if err != nil {
			return nil, err
		}
		if field == "" {
			continue
		}
		// A title, item or ref is shown whole but still on one line: an item or
		// a ref can carry a newline the shard never refused.
		snippet := strings.Join(strings.Fields(text), " ")
		if field == core.FieldBody {
			snippet = needle.Snippet(text, snippetRadius)
		}
		out = append(out, SearchHit{Task: *t, MatchedField: field, Snippet: snippet})
		if o.Limit > 0 && len(out) >= o.Limit {
			break
		}
	}
	return out, nil
}

// searchSource returns the index to scan and the body loader that goes with it.
// They are returned together on purpose: `--archived` has to switch BOTH, and
// switching only the index is the bug this signature makes impossible — a title
// hit would still look right while every body hit read the wrong store.
func (a *App) searchSource(o QueryOpts) (*core.Index, func(string) (string, error), error) {
	if !o.Archived {
		idx, err := a.load()
		return idx, a.Store.LoadBody, err
	}
	arc, err := a.archiveStore()
	if err != nil {
		return nil, nil, err
	}
	idx, err := arc.Load()
	if err != nil {
		return nil, nil, err
	}
	core.Canonicalize(idx, a.Cfg.Lanes)
	return idx, arc.LoadBody, nil
}
