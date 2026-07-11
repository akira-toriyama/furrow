package app

import (
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// snippetRadius is the runes of body context Search keeps on each side of a
// match. Titles are short and returned whole; bodies get a windowed excerpt.
const snippetRadius = 60

// SearchHit is one task matched by Search: the task, which field carried the
// match (title|body), and a one-line snippet of the matched text in context.
type SearchHit struct {
	Task         core.Task
	MatchedField string
	Snippet      string
}

// Search returns the tasks whose title or body contains term (a case-insensitive
// substring), in canonical order, after applying the query's scope filters — the
// same -s/-l/-r/-n semantics as List. Title is matched first: a title hit reports
// matched_field "title" (snippet = the whole title) and never pays to load the
// body; otherwise the body is loaded on demand and a hit reports "body" with a
// windowed excerpt. The body scan is O(board) with no index — the same "an index
// is YAGNI" stance as Backlinks. term is required: an empty/blank term is a
// validation error, not a match-everything. A -s naming an unknown lane fails
// fast (validateLaneFilter, symmetric with List). A zero-match result is healthy
// (exit 0), never a miss.
func (a *App) Search(o QueryOpts, term string) ([]SearchHit, error) {
	if strings.TrimSpace(term) == "" {
		return nil, core.Validationf("", "search term must not be empty")
	}
	if err := a.validateLaneFilter(o.Status); err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	var out []SearchHit
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) {
			continue
		}
		switch {
		case core.ContainsFold(t.Title, term):
			out = append(out, SearchHit{Task: *t, MatchedField: "title", Snippet: t.Title})
		default:
			body, err := a.Store.LoadBody(t.ID)
			if err != nil {
				return nil, err
			}
			if !core.ContainsFold(body, term) {
				continue
			}
			out = append(out, SearchHit{Task: *t, MatchedField: "body", Snippet: core.Snippet(body, term, snippetRadius)})
		}
		if o.Limit > 0 && len(out) >= o.Limit {
			break
		}
	}
	return out, nil
}
