// `dep --list` and `epic dep --list`: both directions resolved to id+title+
// lane (task) or id+title+state (epic), one object each.

package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
)

// taskRefView is one resolved edge (JSON shape): the referenced task's id, title,
// lane, and its own blocked_by (the not-yet-done deps of THAT task — the key
// every ls/show row carries, [] when nothing is in the way). A dangling ref (an
// id naming no task) has an empty title/status and blocked_by []. One shape for
// every edge direction, so both halves of `dep --list` read the same; on the
// blocks side, blocked_by == [subject] is "unblocks on this close".
type taskRefView struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	BlockedBy []string `json:"blocked_by"`
}

// depListView is `dep --list`'s JSON object: the subject task plus both
// directions — depends_on (what it waits on) and blocks (what waits on it).
// Both arrays are always present (empty -> [], never null).
type depListView struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	DependsOn []taskRefView `json:"depends_on"`
	Blocks    []taskRefView `json:"blocks"`
}

func toTaskRefViews(refs []app.TaskRef) []taskRefView {
	out := make([]taskRefView, 0, len(refs))
	for _, r := range refs {
		// BlockedBy is non-nil by app's contract (blockedDeps / the dangling
		// branch both build []string{}), as factsOf trusts it in show.
		out = append(out, taskRefView{ID: r.ID, Title: r.Title, Status: r.Status, BlockedBy: r.BlockedBy})
	}
	return out
}

// emitDepList renders `dep --list`. --json/--ndjson emit a single object with
// both directions (the single-object twin of the list emitters); human output
// is two labelled sections, and each blocks row ends with what THIS close does
// to it (blocksNote) — terminal is the board's parked/closed lane set and
// doneLane its done lane (named separately, as stateGlyph does: config does
// not force the done lane into [lanes].terminal), since a row in one of those
// moves on no close. A zero-edge neighborhood is a clean object, never a miss
// (exit 0).
func emitDepList(r app.DepListResult, terminal map[string]bool, doneLane string) error {
	if jsonMode() {
		emitObject(depListView{
			ID:        r.ID,
			Title:     r.Title,
			DependsOn: toTaskRefViews(r.DependsOn),
			Blocks:    toTaskRefViews(r.Blocks),
		})
		return nil
	}
	fmt.Fprintf(out, "%s  %s\n", r.ID, r.Title)
	fmt.Fprintf(out, "depends on (%d):\n", len(r.DependsOn))
	printTaskRefs(r.DependsOn)
	fmt.Fprintf(out, "blocks (%d):\n", len(r.Blocks))
	if len(r.Blocks) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, b := range r.Blocks {
		row := taskRefRow(b)
		if note := blocksNote(r, b, terminal, doneLane); note != "" {
			row += "  ← " + note
		}
		fmt.Fprintln(out, row)
	}
	return nil
}

// blocksNote is the trailing `← …` of one blocks row: the answer to "what
// happens to this dependent when the subject closes", read from the row's own
// BlockedBy. Empty for a parked or closed dependent — nothing moves it, and the
// lane in brackets already says so. While the subject is open: it is the row's
// last open dep ("unblocks on this close") or one of several ("N other open
// deps"). Once the subject is done the same two facts read in the past tense.
func blocksNote(r app.DepListResult, b app.TaskRef, terminal map[string]bool, doneLane string) string {
	if b.Status == doneLane || terminal[b.Status] {
		return ""
	}
	others := 0
	for _, id := range b.BlockedBy {
		if id != r.ID {
			others++
		}
	}
	switch {
	case r.Done && others == 0:
		return "unblocked"
	case r.Done:
		return fmt.Sprintf("%d open %s", others, plural(others, "dep"))
	case others == 0:
		return "unblocks on this close"
	default:
		return fmt.Sprintf("%d other open %s", others, plural(others, "dep"))
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func printTaskRefs(refs []app.TaskRef) {
	if len(refs) == 0 {
		fmt.Fprintln(out, "  (none)")
		return
	}
	for _, r := range refs {
		fmt.Fprintln(out, taskRefRow(r))
	}
}

// taskRefRow is the one row shape every resolved task edge prints in —
// `show`'s deps block and both halves of `dep --list` — so a reader learns it
// once. `[?]` marks a dangling ref: an id naming no task.
func taskRefRow(r app.TaskRef) string {
	st := r.Status
	if st == "" {
		st = "?"
	}
	return fmt.Sprintf("  %s  [%s]  %s", r.ID, st, r.Title)
}

// epicRefView is taskRefView's epic twin: an epic has no lane, so the third
// fact is `state` ("open" | "closed"; "" for a dangling edge).
type epicRefView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// epicDepListView is `epic dep --list`'s JSON object — the same both-directions
// shape as depListView, so a front-end reads one layout whichever entity it
// asked about.
type epicDepListView struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	DependsOn []epicRefView `json:"depends_on"`
	Blocks    []epicRefView `json:"blocks"`
}

func toEpicRefViews(refs []app.EpicRef) []epicRefView {
	out := make([]epicRefView, 0, len(refs))
	for _, r := range refs {
		out = append(out, epicRefView{ID: r.ID, Title: r.Title, State: r.State})
	}
	return out
}

// emitEpicDepList renders `epic dep --list`, mirroring emitDepList.
func emitEpicDepList(r app.EpicDepListResult) error {
	if jsonMode() {
		emitObject(epicDepListView{
			ID:        r.ID,
			Title:     r.Title,
			DependsOn: toEpicRefViews(r.DependsOn),
			Blocks:    toEpicRefViews(r.Blocks),
		})
		return nil
	}
	fmt.Fprintf(out, "%s  %s\n", r.ID, r.Title)
	fmt.Fprintf(out, "waits on (%d):\n", len(r.DependsOn))
	printEpicRefs(r.DependsOn)
	fmt.Fprintf(out, "blocks (%d):\n", len(r.Blocks))
	printEpicRefs(r.Blocks)
	return nil
}

func printEpicRefs(refs []app.EpicRef) {
	if len(refs) == 0 {
		fmt.Fprintln(out, "  (none)")
		return
	}
	for _, r := range refs {
		st := r.State
		if st == "" {
			st = "?" // a dangling edge: an id naming no epic (lint: epic-dep-missing)
		}
		fmt.Fprintf(out, "  %s  [%s]  %s\n", r.ID, st, r.Title)
	}
}
