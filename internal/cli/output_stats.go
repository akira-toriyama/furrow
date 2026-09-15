// `stats`: the histogram views and the created/closed flow window. Counting
// is the app's; this file only shapes and prints.

package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/app"
)

// laneCountView / repoCountView / labelCountView are one distribution row each,
// named for their category so an agent reads by_lane[].lane, by_repo[].repo,
// by_label[].label rather than a generic "key".
type laneCountView struct {
	Lane  string `json:"lane"`
	Count int    `json:"count"`
}

type repoCountView struct {
	Repo  string `json:"repo"`
	Count int    `json:"count"`
}

type labelCountView struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// statsView is `stats`'s JSON object: totals plus the lane/repo/label
// distributions within the active scope, and — only when --since/--until set
// one — the window's created/closed flow.
type statsView struct {
	Total   int              `json:"total"`
	Drafts  int              `json:"drafts"`
	ByLane  []laneCountView  `json:"by_lane"`
	ByRepo  []repoCountView  `json:"by_repo"`
	ByLabel []labelCountView `json:"by_label"`
	Window  *statsWindowView `json:"window,omitempty"`
}

// statsWindowView is the flow section: counts AND the ids behind them, so a
// consumer can verify each count with `furrow show` instead of trusting it. A
// nil bound is omitted (an open side of the window).
type statsWindowView struct {
	Since      *time.Time `json:"since,omitempty"`
	Until      *time.Time `json:"until,omitempty"`
	Created    int        `json:"created"`
	Closed     int        `json:"closed"`
	CreatedIDs []string   `json:"created_ids"`
	ClosedIDs  []string   `json:"closed_ids"`
}

func toStatsView(s app.Stats) statsView {
	v := statsView{Total: s.Total, Drafts: s.Drafts}
	if s.Window != nil {
		v.Window = &statsWindowView{
			Since:      s.Window.Since,
			Until:      s.Window.Until,
			Created:    len(s.Window.Created),
			Closed:     len(s.Window.Closed),
			CreatedIDs: s.Window.Created,
			ClosedIDs:  s.Window.Closed,
		}
	}
	v.ByLane = make([]laneCountView, 0, len(s.ByLane))
	for _, c := range s.ByLane {
		v.ByLane = append(v.ByLane, laneCountView{Lane: c.Key, Count: c.Count})
	}
	v.ByRepo = make([]repoCountView, 0, len(s.ByRepo))
	for _, c := range s.ByRepo {
		v.ByRepo = append(v.ByRepo, repoCountView{Repo: c.Key, Count: c.Count})
	}
	v.ByLabel = make([]labelCountView, 0, len(s.ByLabel))
	for _, c := range s.ByLabel {
		v.ByLabel = append(v.ByLabel, labelCountView{Label: c.Key, Count: c.Count})
	}
	return v
}

// emitStats renders `stats`. --json/--ndjson emit one object (the single-object
// twin of the list emitters); human output is a labelled summary. An all-zero
// board is a clean object (exit 0), never a miss.
func emitStats(s app.Stats) error {
	if jsonMode() {
		emitObject(toStatsView(s))
		return nil
	}
	fmt.Fprintf(out, "total: %d  (drafts: %d)\n", s.Total, s.Drafts)
	fmt.Fprintln(out, "lanes:")
	printCounts(s.ByLane)
	fmt.Fprintf(out, "repos (%d):\n", len(s.ByRepo))
	printCounts(s.ByRepo)
	fmt.Fprintf(out, "labels (%d):\n", len(s.ByLabel))
	printCounts(s.ByLabel)
	if w := s.Window; w != nil {
		fmt.Fprintf(out, "window %s .. %s:\n", boundLabel(w.Since), boundLabel(w.Until))
		fmt.Fprintf(out, "  created %d%s\n", len(w.Created), idsSuffix(w.Created))
		fmt.Fprintf(out, "  closed  %d%s\n", len(w.Closed), idsSuffix(w.Closed))
	}
	return nil
}

// boundLabel renders one side of the window ("…" for an open side).
func boundLabel(t *time.Time) string {
	if t == nil {
		return "…"
	}
	return t.UTC().Format(time.RFC3339)
}

// idsSuffix renders a flow list's ids beside its count, empty when there are
// none (the count already says 0).
func idsSuffix(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "  (" + strings.Join(ids, ", ") + ")"
}

// printCounts prints one `count  key` row per entry, right-aligned counts, plain
// so it greps and copies cleanly. An empty category prints "(none)".
func printCounts(counts []app.StatCount) {
	if len(counts) == 0 {
		fmt.Fprintln(out, "  (none)")
		return
	}
	for _, c := range counts {
		fmt.Fprintf(out, "  %5d  %s\n", c.Count, c.Key)
	}
}
