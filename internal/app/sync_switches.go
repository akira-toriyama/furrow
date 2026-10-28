// What a sync publishes OUT: the epic activation records the pushed commits
// add, read from the diff so an agent's switch is visible in the sync that
// published it. Display data on the publishedSwitches contract.

package app

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/v9/internal/gitrepo"
)

// EpicSwitch is one published activation record: the box, its title (resolved
// at sync time so the summary is readable without a second command), and the
// timestamp/reason exactly as recordSwitch wrote them (At in UTC, the
// switchStampLayout spelling).
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

// switchStampLayout is recordSwitch's stamp: minute precision, in UTC, with no
// zone suffix. UTC on every writer is what lets stamps from different machines
// compare directly, and the zero-padded fixed width makes string order time
// order. The previous and sync lines convert it (SwitchStampTime) rather than
// print it bare, since a bare UTC wall clock reads as local time. The body copy
// is shown as written; `epic activate --help` names its zone instead.
const switchStampLayout = "2006-01-02 15:04"

// SwitchStampTime is the instant a recordSwitch stamp names. ok is false for a
// stamp that has switchLineRe's digits but is no real time (a hand-edited
// "2026-13-01 25:00"); the caller then shows it as written.
func SwitchStampTime(at string) (time.Time, bool) {
	t, err := time.ParseInLocation(switchStampLayout, at, time.UTC)
	return t, err == nil
}

// publishedSwitches scans the unpushed commits' added body lines for activation
// records on files that belong to an EPIC. Best-effort display data: any read
// failure returns nil — a summary line must never fail a sync.
func (a *App) publishedSwitches(ctx context.Context, r *gitrepo.Repo, spec string) []EpicSwitch {
	added := r.AddedLines(ctx, specJoin(spec, "bodies"))
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
	prefix := specJoin(spec, "bodies/")
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
