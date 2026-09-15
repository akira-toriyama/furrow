// What a sync publishes OUT: the epic activation records the pushed commits
// add, read from the diff so an agent's switch is visible in the sync that
// published it. Display data on the publishedSwitches contract.

package app

import (
	"context"
	"regexp"
	"strings"

	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// EpicSwitch is one published activation record: the box, its title (resolved
// at sync time so the summary is readable without a second command), and the
// timestamp/reason exactly as recordSwitch wrote them.
type EpicSwitch struct {
	Epic   string `json:"epic"`
	Title  string `json:"title"`
	At     string `json:"at"`
	Reason string `json:"reason,omitempty"`
}

// switchLineRe matches recordSwitch's body line: "YYYY-MM-DD HH:MM activated",
// optionally " — <reason>". Anchored both ends so prose that merely mentions
// activation never counts.
var switchLineRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}) activated(?: — (.*))?$`)

// publishedSwitches scans the unpushed commits' added body lines for activation
// records on files that belong to an EPIC. Best-effort display data: any read
// failure returns nil — a summary line must never fail a sync.
func (a *App) publishedSwitches(ctx context.Context, r *gitrepo.Repo, spec string) []EpicSwitch {
	added := r.AddedLines(ctx, spec+"/bodies")
	if len(added) == 0 {
		return nil
	}
	epics, err := a.Store.LoadEpics()
	if err != nil {
		return nil
	}
	titles := make(map[string]string, len(epics))
	for i := range epics {
		titles[epics[i].ID] = epics[i].Title
	}
	prefix := spec + "/bodies/"
	var out []EpicSwitch
	for _, l := range added {
		rest, ok := strings.CutPrefix(l.Path, prefix)
		if !ok || !strings.HasSuffix(rest, ".md") {
			continue
		}
		// Membership in the epic store — not an id-prefix guess — decides whether
		// the body is a box's, so a custom [ids] prefix cannot fool the scan.
		id := strings.TrimSuffix(rest, ".md")
		title, isEpic := titles[id]
		if !isEpic {
			continue
		}
		if m := switchLineRe.FindStringSubmatch(l.Text); m != nil {
			out = append(out, EpicSwitch{Epic: id, Title: title, At: m[1], Reason: m[2]})
		}
	}
	return out
}
