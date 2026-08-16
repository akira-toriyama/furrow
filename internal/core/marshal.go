package core

import (
	"encoding/json"
	"sort"
	"time"
)

// MarshalTask is the ONE path that serializes a single task to its shard bytes
// (tasks/<id>.json); never call json.Marshal on a Task anywhere else
// (scripts/check-marshal-singlepath.sh enforces this in CI).
//
// DO NOT regress the determinism contract. Every persisted value runs
// encodeCanonicalWithExtras (passthrough.go), the one encoder, whose recipe is:
//   - key order        = struct field order (encoding/json guarantees this),
//     with any unknown keys re-emitted sorted after the known ones
//   - indent           = 2 spaces
//   - SetEscapeHTML(false) so CJK and < > & survive verbatim
//   - []  not null     (canonicalizeTask replaces nil slices with empty ones)
//   - timestamps       = UTC, whole seconds (RFC3339 "...Z", no fractional)
//   - trailing newline
//
// The payoff: shard bytes written by `furrow` equal bytes a human or Claude
// would hand-edit, so re-saving an untouched task produces zero git churn.
// (An Index-level marshaller once lived beside this one — the in-memory
// aggregate's canonical form. Nothing in production ever called it, so it was
// removed (t-eb6a); the index has no serialized form, only per-entity shards.)
//
// A shard carries NO schema_version — the store's meta.json owns the one
// board-wide version, keeping every shard free of a field that would otherwise
// be a needless merge point. canonicalizeTask mutates t in place. t must be
// non-nil — a nil task is a programmer error.
func MarshalTask(t *Task) ([]byte, error) {
	canonicalizeTask(t)
	// …WithExtras: any key this binary does not know came off disk in t.extras and
	// goes back out, sorted, after the known ones. Dropping it would destroy a
	// field a newer furrow wrote — see passthrough.go.
	data, err := encodeCanonicalWithExtras(t, t.extras)
	if err != nil {
		return nil, Internalf(t.ID, "marshal task: %v", err)
	}
	return data, nil
}

// UnmarshalTask parses one shard's bytes into a Task. A parse failure is a
// validation error (malformed input), not an internal fault.
func UnmarshalTask(data []byte) (*Task, error) {
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, Validationf("task", "task shard is not valid JSON: %v", err)
	}
	// Park the keys we do not know instead of dropping them: this is what makes a
	// round-trip through an older binary lossless (passthrough.go).
	extra, err := splitExtras(data, taskKnownKeys)
	if err != nil {
		return nil, Validationf("task", "task shard is not valid JSON: %v", err)
	}
	t.extras = extra
	return &t, nil
}

// MarshalRepo is the per-repo twin of MarshalTask: the ONE path that serializes
// a RepoRecord to its shard bytes (repos/<owner>__<repo>.json). It shares the
// byte recipe (encodeCanonicalWithExtras) and normalizes its timestamps (canonicalizeRepo)
// so a repo shard written by furrow equals a hand-edit byte-for-byte, exactly as
// task shards do, and — like them — carries no schema_version. r must be
// non-nil.
func MarshalRepo(r *RepoRecord) ([]byte, error) {
	canonicalizeRepo(r)
	data, err := encodeCanonicalWithExtras(r, r.extras)
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
	extra, err := splitExtras(data, repoKnownKeys)
	if err != nil {
		return nil, Validationf("repo", "repo shard is not valid JSON: %v", err)
	}
	r.extras = extra
	return &r, nil
}

// MarshalEpic is the per-epic twin of MarshalTask: the ONE path that serializes
// an Epic to its shard bytes (epics/<id>.json). It shares the byte recipe
// (encodeCanonicalWithExtras) and per-epic normalization (canonicalizeEpic) so an
// epic shard written by furrow equals a hand-edit byte-for-byte, exactly as task
// and repo shards do, and — like them — carries no schema_version. e must be
// non-nil.
func MarshalEpic(e *Epic) ([]byte, error) {
	canonicalizeEpic(e)
	data, err := encodeCanonicalWithExtras(e, e.extras)
	if err != nil {
		return nil, Internalf(e.ID, "marshal epic: %v", err)
	}
	return data, nil
}

// canonicalizeEpic enforces the per-epic determinism invariants in place:
// non-nil collections, sorted+deduped sets, whole-second UTC timestamps.
//
// Meta is emptied to {} rather than left nil for the same reason Labels becomes
// []: the on-disk shape must not flip between null and an empty container
// depending on how the value reached the marshaller. Its KEYS need no sorting —
// encoding/json already emits a map's keys in sorted order, which is precisely
// why Meta is a flat map[string]string and not an arbitrary nested value.
func canonicalizeEpic(e *Epic) {
	if e.Labels == nil {
		e.Labels = []string{}
	}
	if e.Repos == nil {
		e.Repos = []string{}
	}
	if e.Meta == nil {
		e.Meta = map[string]string{}
	}
	if e.Deps == nil {
		e.Deps = []string{}
	}
	e.Labels = sortDedup(e.Labels)
	e.Repos = sortDedup(e.Repos)
	e.Deps = sortDedup(e.Deps)

	e.Created = normTime(e.Created)
	e.Updated = normTime(e.Updated)
	if e.Closed != nil {
		c := normTime(*e.Closed)
		e.Closed = &c
	}
	if e.Reviewed != nil {
		r := normTime(*e.Reviewed)
		e.Reviewed = &r
	}
}

// UnmarshalEpic parses one epic shard's bytes into an Epic, the per-epic twin of
// UnmarshalTask. A parse failure is a validation error (malformed input), not an
// internal fault.
func UnmarshalEpic(data []byte) (*Epic, error) {
	var e Epic
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, Validationf("epic", "epic shard is not valid JSON: %v", err)
	}
	extra, err := splitExtras(data, epicKnownKeys)
	if err != nil {
		return nil, Validationf("epic", "epic shard is not valid JSON: %v", err)
	}
	e.extras = extra
	return &e, nil
}

// MarshalMeta serializes the board-wide Meta (schema version) to its meta.json
// bytes. It shares encodeCanonicalWithExtras so meta.json obeys the same byte recipe as
// the shards (2-space indent, no HTML escaping, trailing newline) — a hand-edit
// equals a furrow write. This is the ONE path that serializes Meta.
func MarshalMeta(m *Meta) ([]byte, error) {
	data, err := encodeCanonicalWithExtras(m, m.extras)
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
	extra, err := splitExtras(data, metaKnownKeys)
	if err != nil {
		return nil, Validationf("meta", "meta.json is not valid JSON: %v", err)
	}
	m.extras = extra
	return &m, nil
}

// Canonicalize enforces the determinism invariants in place: non-nil slices,
// whole-second UTC timestamps, sorted per-task string slices, and the stable
// lane->priority->id task order. It is the in-memory index's normal form —
// list reads canonicalize before rendering, and tests and the lint command
// assert "this is already canonical" against it.
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
	if t.Due != nil {
		d := normTime(*t.Due)
		t.Due = &d
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
