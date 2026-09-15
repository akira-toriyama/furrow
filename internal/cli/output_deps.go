// `dep --list` and `epic dep --list`: both directions resolved to id+title+
// lane (task) or id+title+state (epic), one object each.

package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
)

// taskRefView is one resolved edge (JSON shape): the referenced task's id, title,
// and lane. A dangling ref (an id naming no task) has an empty title/status. One
// shape for every edge direction, so both halves of `dep --list` read the same.
type taskRefView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
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
		out = append(out, taskRefView{ID: r.ID, Title: r.Title, Status: r.Status})
	}
	return out
}

// emitDepList renders `dep --list`. --json/--ndjson emit a single object with
// both directions (the single-object twin of the list emitters); human output
// is two labelled sections. A zero-edge neighborhood is a clean object, never a
// miss (exit 0).
func emitDepList(r app.DepListResult) error {
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
	printTaskRefs(r.Blocks)
	return nil
}

func printTaskRefs(refs []app.TaskRef) {
	if len(refs) == 0 {
		fmt.Fprintln(out, "  (none)")
		return
	}
	for _, r := range refs {
		st := r.Status
		if st == "" {
			st = "?" // a dangling ref: an id naming no task
		}
		fmt.Fprintf(out, "  %s  [%s]  %s\n", r.ID, st, r.Title)
	}
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
