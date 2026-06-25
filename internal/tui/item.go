package tui

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/core"
)

// taskItem adapts a core.Task to bubbles/list. It implements list.DefaultItem
// (Title/Description/FilterValue) so the stock delegate renders it.
type taskItem struct{ t core.Task }

func (i taskItem) Title() string { return i.t.Title }

func (i taskItem) Description() string {
	d := fmt.Sprintf("%s · %s · p%d", i.t.ID, i.t.Status, i.t.Priority)
	if len(i.t.Labels) > 0 {
		d += " · " + join(i.t.Labels)
	}
	if n := len(i.t.Checklist); n > 0 {
		done := 0
		for _, c := range i.t.Checklist {
			if c.Done {
				done++
			}
		}
		d += fmt.Sprintf(" · ✓%d/%d", done, n)
	}
	return d
}

// FilterValue feeds the fuzzy filter: title + id + labels.
func (i taskItem) FilterValue() string {
	return i.t.Title + " " + i.t.ID + " " + join(i.t.Labels)
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
