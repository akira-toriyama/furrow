// `search`'s hits: the one task view whose row is a snippet, not a title cell
// — which is why it does not carry the repeat tag the other rows do.

package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// searchHitView is one `search` result: the task plus which field carried the
// match and a one-line snippet with the term in context (JSON/NDJSON only).
type searchHitView struct {
	core.Task
	MatchedField string `json:"matched_field"`
	Snippet      string `json:"snippet"`
}

// emitSearch renders `search` results in canonical order. --json is an array
// (empty -> [], never null), --ndjson one hit per line, human a plain table
// with the snippet. A zero-match result is a healthy empty result (exit 0),
// never a miss — the same contract as ls/next/revisit.
func emitSearch(hits []app.SearchHit) error {
	views := make([]searchHitView, 0, len(hits))
	for _, h := range hits {
		views = append(views, searchHitView{Task: h.Task, MatchedField: h.MatchedField, Snippet: h.Snippet})
	}
	emitList(views, func() { printSearchTable(hits) })
	return nil
}

// printSearchTable renders search hits as a plain aligned table: id, matched
// field, and the match text. For a body hit the title is shown before the
// snippet (so the id is never the only clue to which task matched); a title hit
// shows the title alone. Deliberately plain (no box drawing) so it greps and
// copies cleanly, like printTaskTable.
func printSearchTable(hits []app.SearchHit) {
	if len(hits) == 0 {
		fmt.Fprintln(out, "(no matches)")
		return
	}
	wID, wField := len("ID"), len("FIELD")
	for _, h := range hits {
		if len(h.Task.ID) > wID {
			wID = len(h.Task.ID)
		}
		if len(h.MatchedField) > wField {
			wField = len(h.MatchedField)
		}
	}
	fmt.Fprintf(out, "%-*s  %-*s  %s\n", wID, "ID", wField, "FIELD", "MATCH")
	for _, h := range hits {
		match := h.Snippet
		if h.MatchedField == "body" {
			match = h.Task.Title + "  ·  " + h.Snippet
		}
		fmt.Fprintf(out, "%-*s  %-*s  %s\n", wID, h.Task.ID, wField, h.MatchedField, match)
	}
}
