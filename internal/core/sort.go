package core

import (
	"cmp"
	"sort"
	"strings"
	"time"
)

// SortFields are the fields `ls --sort` accepts, in help/candidate order.
var SortFields = []string{"updated", "created", "value", "effort"}

// IsSortField reports whether field is a recognized --sort key.
func IsSortField(field string) bool {
	for _, f := range SortFields {
		if field == f {
			return true
		}
	}
	return false
}

// TaskOrder returns the canonical task comparator for a board whose lanes are
// laneOrder: lane rank, then priority, then id. It is a TOTAL order on any board
// lint calls clean (`duplicate-id` is an error), and that is the point. Canonicalize sorts the index with
// it, so any OTHER sort over those tasks can tiebreak with it and reproduce the
// index order it would otherwise inherit through a stable sort's back door.
// That back door is a real trap: App.Due sorts by the due instant, and ParseDue
// normalizes a bare-day due to that day's 23:59:59, so same-day tasks carry a
// byte-identical time.Time and nothing but sort.SliceStable's stability decided
// their order. Swapping that one call for an unstable sort scrambled `furrow
// brief`'s overdue band from its first row while the test suite stayed green.
func TaskOrder(laneOrder []string) func(a, b *Task) int {
	rank := laneRank(laneOrder)
	return func(a, b *Task) int {
		if c := cmp.Compare(laneRankOf(rank, a.Status), laneRankOf(rank, b.Status)); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Priority, b.Priority); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	}
}

// SortTasks orders tasks in place by field. The default (reverse=false)
// direction is "most first": newest updated/created, highest value/effort. An
// unset value/effort always sorts LAST — it is unranked, not zero — in BOTH
// directions. Ties break by id ascending for a deterministic order. field must
// be one of SortFields; an unrecognized field leaves the slice in id order.
func SortTasks(tasks []Task, field string, reverse bool) {
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := &tasks[i], &tasks[j]
		switch field {
		case "value", "effort":
			av, bv := estimate(a, field), estimate(b, field)
			if (av == nil) != (bv == nil) {
				return av != nil // the set one comes first, regardless of reverse
			}
			if av != nil && *av != *bv {
				if reverse {
					return *av < *bv
				}
				return *av > *bv
			}
		case "updated", "created":
			at, bt := stamp(a, field), stamp(b, field)
			if !at.Equal(bt) {
				if reverse {
					return at.Before(bt)
				}
				return at.After(bt)
			}
		}
		return a.ID < b.ID
	})
}

func estimate(t *Task, field string) *int {
	if field == "effort" {
		return t.Effort
	}
	return t.Value
}

func stamp(t *Task, field string) time.Time {
	if field == "created" {
		return t.Created
	}
	return t.Updated
}
