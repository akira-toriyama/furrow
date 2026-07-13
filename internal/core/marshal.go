package core

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"
)

// Marshal is the ONE path that serializes an Index to bytes; never call
// json.Marshal on an Index anywhere else (scripts/check-marshal-singlepath.sh
// enforces this in CI). It produces the in-memory aggregate's canonical form —
// used by the determinism golden and for inspection — and is NOT a persistence
// path: the store writes per-task shards via MarshalTask + a MetaPath file via
// MarshalMeta, so these bytes must never be written to .furrow/ (that would
// resurrect the abolished, drift-prone index.json).
//
// DO NOT regress the determinism contract (shared with MarshalTask via
// encodeCanonical + canonicalizeTask):
//   - key order        = struct field order (encoding/json guarantees this)
//   - indent           = 2 spaces
//   - SetEscapeHTML(false) so CJK and < > & survive verbatim
//   - []  not null     (Canonicalize replaces nil slices with empty ones)
//   - sort             = lane-rank -> priority -> id
//   - timestamps       = UTC, whole seconds (RFC3339 "...Z", no fractional)
//   - trailing newline (Encode appends it)
//
// The payoff: shard bytes written by `furrow` equal bytes a human or Claude
// would hand-edit, so re-saving an untouched task produces zero git churn.
func Marshal(idx *Index, laneOrder []string) ([]byte, error) {
	Canonicalize(idx, laneOrder)
	data, err := encodeCanonical(idx)
	if err != nil {
		return nil, Internalf("index", "marshal index: %v", err)
	}
	return data, nil
}

// MarshalTask is the per-task twin of Marshal: the ONE path that serializes a
// single task to its shard bytes (tasks/<id>.json). It shares Marshal's byte
// recipe (encodeCanonical) and per-task normalization (canonicalizeTask) so a
// shard written by furrow equals a hand-edit byte-for-byte, exactly as the index
// does. Unlike the index, a shard carries NO schema_version — the store's
// meta.json owns the one board-wide version, keeping every shard free of a field
// that would otherwise be a needless merge point. canonicalizeTask mutates t in
// place (as Canonicalize does for the index). t must be non-nil — a nil task is
// a programmer error, mirroring Marshal's contract for a nil index.
func MarshalTask(t *Task) ([]byte, error) {
	canonicalizeTask(t)
	data, err := encodeCanonical(t)
	if err != nil {
		return nil, Internalf(t.ID, "marshal task: %v", err)
	}
	return data, nil
}

// encodeCanonical applies the determinism byte-recipe to any value: no HTML
// escaping (CJK and < > & survive verbatim), 2-space indent, and a trailing
// newline (Encode appends it). Marshal (*Index) and MarshalTask (*Task) both go
// through it so the recipe lives in exactly one place; regressing it here would
// silently reintroduce git churn for both.
func encodeCanonical(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil { // Encode writes the trailing '\n'
		return nil, err
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

// UnmarshalTask parses one shard's bytes into a Task, the per-task twin of
// Unmarshal. A parse failure is a validation error (malformed input), not an
// internal fault.
func UnmarshalTask(data []byte) (*Task, error) {
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, Validationf("task", "task shard is not valid JSON: %v", err)
	}
	return &t, nil
}

// MarshalRepo is the per-repo twin of MarshalTask: the ONE path that serializes
// a RepoRecord to its shard bytes (repos/<owner>__<repo>.json). It shares the
// byte recipe (encodeCanonical) and normalizes its timestamps (canonicalizeRepo)
// so a repo shard written by furrow equals a hand-edit byte-for-byte, exactly as
// task shards do, and — like them — carries no schema_version. r must be
// non-nil.
func MarshalRepo(r *RepoRecord) ([]byte, error) {
	canonicalizeRepo(r)
	data, err := encodeCanonical(r)
	if err != nil {
		return nil, Internalf(r.Repo, "marshal repo: %v", err)
	}
	return data, nil
}

// canonicalizeRepo enforces the per-repo determinism invariants in place:
// whole-second UTC timestamps, nil-guarded so an unset clock stays nil (-> null).
func canonicalizeRepo(r *RepoRecord) {
	if r.LastReviewed != nil {
		t := normTime(*r.LastReviewed)
		r.LastReviewed = &t
	}
	if r.LastAgentReviewed != nil {
		t := normTime(*r.LastAgentReviewed)
		r.LastAgentReviewed = &t
	}
}

// UnmarshalRepo parses one repo shard's bytes into a RepoRecord, the per-repo
// twin of UnmarshalTask. A parse failure is a validation error (malformed
// input), not an internal fault.
func UnmarshalRepo(data []byte) (*RepoRecord, error) {
	var r RepoRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, Validationf("repo", "repo shard is not valid JSON: %v", err)
	}
	return &r, nil
}

// MarshalMeta serializes the board-wide Meta (schema version) to its meta.json
// bytes. It shares encodeCanonical so meta.json obeys the same byte recipe as
// the shards (2-space indent, no HTML escaping, trailing newline) — a hand-edit
// equals a furrow write. This is the ONE path that serializes Meta.
func MarshalMeta(m *Meta) ([]byte, error) {
	data, err := encodeCanonical(m)
	if err != nil {
		return nil, Internalf("meta", "marshal meta: %v", err)
	}
	return data, nil
}

// UnmarshalMeta parses meta.json bytes into a Meta. A parse failure is a
// validation error (malformed input), not an internal fault.
func UnmarshalMeta(data []byte) (*Meta, error) {
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, Validationf("meta", "meta.json is not valid JSON: %v", err)
	}
	return &m, nil
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
		canonicalizeTask(&idx.Tasks[i])
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

// canonicalizeTask enforces the per-task determinism invariants in place:
// non-nil slices, sorted+deduped sets, whole-second UTC timestamps, and clamped
// estimates. It is the single per-task recipe — the index's Canonicalize loop
// and MarshalTask both call it — so a shard and a task-inside-the-index
// normalize identically and the rules never drift between the two paths.
func canonicalizeTask(t *Task) {
	if t.Labels == nil {
		t.Labels = []string{}
	}
	if t.Repos == nil {
		t.Repos = []string{}
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
	// Labels, repos, and deps are sets — sort AND dedupe them so reordering or
	// repeating inputs (e.g. `add -l x -l x`) can't churn the diff and a
	// furrow-written set equals a hand-written one byte-for-byte. Refs and
	// checklist are user-ordered sequences, so leave them.
	t.Labels = sortDedup(t.Labels)
	t.Repos = sortDedup(t.Repos)
	t.Deps = sortDedup(t.Deps)

	t.Created = normTime(t.Created)
	t.Updated = normTime(t.Updated)
	if t.Closed != nil {
		c := normTime(*t.Closed)
		t.Closed = &c
	}
	if t.Reviewed != nil {
		r := normTime(*t.Reviewed)
		t.Reviewed = &r
	}

	// value/effort are clamp-don't-reject: an out-of-range estimate (from a
	// hand-edit) is rounded into 1..5 so furrow never writes a stray. lint
	// (EstimateProblems, run on the pre-clamp bytes) surfaces it first.
	clampEstimate(t.Value)
	clampEstimate(t.Effort)
}

// normTime coerces a timestamp to the on-disk contract: UTC, whole seconds. A
// zero time stays zero (encoding/json emits "0001-01-01T00:00:00Z").
func normTime(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// clampEstimate rounds a non-nil value/effort into [EstimateMin, EstimateMax]
// in place. A nil pointer (unset) is left untouched so absent stays absent.
func clampEstimate(p *int) {
	if p == nil {
		return
	}
	if *p < EstimateMin {
		*p = EstimateMin
	} else if *p > EstimateMax {
		*p = EstimateMax
	}
}

// laneRank maps each lane to its rank by FIRST occurrence (0,1,2,…), not by raw
// slice index. This keeps ranks contiguous in 0..len(unique)-1 even if laneOrder
// contains duplicates, so the unknown-lane sentinel (len(rank)+1 in laneRankOf)
// can never collide with a real lane's rank.
func laneRank(laneOrder []string) map[string]int {
	rank := make(map[string]int, len(laneOrder))
	next := 0
	for _, l := range laneOrder {
		if _, ok := rank[l]; !ok {
			rank[l] = next
			next++
		}
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

// sortDedup returns the input sorted with duplicates removed. nil-safe; returns
// a non-nil empty slice for an empty/nil input so the marshaller's [] invariant
// holds.
func sortDedup(ss []string) []string {
	if len(ss) == 0 {
		return []string{}
	}
	sort.Strings(ss)
	out := ss[:1]
	for _, s := range ss[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
