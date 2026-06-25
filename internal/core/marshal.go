package core

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"
)

// Marshal is the ONE path that serializes an Index to bytes. Every writer — CLI,
// TUI, migrate — MUST go through it; never call json.Marshal on an Index
// anywhere else (scripts/check-marshal-singlepath.sh enforces this in CI).
//
// DO NOT regress the determinism contract (ROADMAP §6 / MEMO §3):
//   - key order        = struct field order (encoding/json guarantees this)
//   - indent           = 2 spaces
//   - SetEscapeHTML(false) so CJK and < > & survive verbatim
//   - []  not null     (Canonicalize replaces nil slices with empty ones)
//   - sort             = lane-rank -> priority -> id
//   - timestamps       = UTC, whole seconds (RFC3339 "...Z", no fractional)
//   - trailing newline (Encode appends it)
//
// The payoff: bytes written by `furrow` equal bytes a human or Claude would
// hand-edit, so re-saving an untouched index produces zero git churn.
func Marshal(idx *Index, laneOrder []string) ([]byte, error) {
	Canonicalize(idx, laneOrder)
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(idx); err != nil { // Encode writes the trailing '\n'
		return nil, Internalf("index", "marshal index: %v", err)
	}
	return b.Bytes(), nil
}

// Unmarshal parses index bytes into an Index. A parse failure is a validation
// error (the file is malformed input), not an internal fault.
func Unmarshal(data []byte) (*Index, error) {
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, Validationf("index", "index.json is not valid JSON: %v", err)
	}
	return &idx, nil
}

// Canonicalize enforces the determinism invariants in place: non-nil slices,
// whole-second UTC timestamps, sorted per-task string slices, and the stable
// lane->priority->id task order. Marshal calls it; it is exported so tests and
// the lint command can assert "this is already canonical".
func Canonicalize(idx *Index, laneOrder []string) {
	if idx.SchemaVersion == 0 {
		idx.SchemaVersion = SchemaVersion
	}
	if idx.Tasks == nil {
		idx.Tasks = []Task{}
	}

	rank := laneRank(laneOrder)
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if t.Labels == nil {
			t.Labels = []string{}
		}
		if t.Deps == nil {
			t.Deps = []string{}
		}
		if t.Refs == nil {
			t.Refs = []string{}
		}
		if t.Checklist == nil {
			t.Checklist = []ChecklistItem{}
		}
		// Labels and deps are sets — sort them so reordering inputs can't churn
		// the diff. Refs and checklist are ordered by the user, so leave them.
		sort.Strings(t.Labels)
		sort.Strings(t.Deps)

		t.Created = normTime(t.Created)
		t.Updated = normTime(t.Updated)
		if t.Closed != nil {
			c := normTime(*t.Closed)
			t.Closed = &c
		}
	}

	sort.SliceStable(idx.Tasks, func(a, b int) bool {
		ta, tb := idx.Tasks[a], idx.Tasks[b]
		if ra, rb := laneRankOf(rank, ta.Status), laneRankOf(rank, tb.Status); ra != rb {
			return ra < rb
		}
		if ta.Priority != tb.Priority {
			return ta.Priority < tb.Priority
		}
		return ta.ID < tb.ID
	})
}

// normTime coerces a timestamp to the on-disk contract: UTC, whole seconds. A
// zero time stays zero (encoding/json emits "0001-01-01T00:00:00Z").
func normTime(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// laneRank maps each lane to its position in the configured order.
func laneRank(laneOrder []string) map[string]int {
	rank := make(map[string]int, len(laneOrder))
	for i, l := range laneOrder {
		rank[l] = i
	}
	return rank
}

// laneRankOf returns a lane's rank, or a sentinel past the end so unknown lanes
// sort last (they are also flagged by lint).
func laneRankOf(rank map[string]int, lane string) int {
	if r, ok := rank[lane]; ok {
		return r
	}
	return len(rank) + 1
}
