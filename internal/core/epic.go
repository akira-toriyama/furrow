package core

import "time"

// Epic is one box of work: the object in a single .furrow/epics/<id>.json shard.
// It is NOT a task and deliberately does not reuse Task — a box is not a work
// item, and the fields a work item needs (lane, priority, value/effort, deps)
// are exactly the ones a box must not have, because furrow would then have to
// answer "what is the ROI of a container?" for every query it owns.
//
// Like a task shard it is one entity per file (concurrent edits to different
// epics touch disjoint files), self-describing, and carries NO schema_version —
// that lives in meta.json.
//
// Field order == JSON key order (see Task); new fields go at the END, or a
// binary that parks them as extras would re-emit them there anyway and the two
// writers would churn a one-line move on every alternating save.
//
// The state model is deliberately two-valued: an epic is open (Closed == nil) or
// closed. It does NOT live in a lane. Lanes describe how work is progressing;
// a box's progress is derived from its members, so giving it a lane would create
// a second, hand-maintained copy of a number furrow can always compute.
type Epic struct {
	// ID is frozen and never reused, like a task's. It carries its own prefix
	// ("e-") so an id names its entity kind on sight: `furrow show <id>` can tell
	// a task from an epic without loading either, and the shared bodies/ dir below
	// is unambiguous.
	ID string `json:"id"`
	// Title is the one-line name of the box.
	Title string `json:"title"`
	// Goal is the closing condition in one line: what has to be true for this box
	// to be done. OPTIONAL — an epic whose title already says it ("make curry")
	// gains nothing from restating it, so furrow neither requires it nor lints its
	// absence. Its value is that `epic ls` can show it, which is what you read when
	// deciding which box to open next.
	Goal string `json:"goal"`
	// Active marks the ONE box currently being worked. `furrow next` scopes to it,
	// which is the whole point of the entity: without it, every session picks the
	// juiciest single task from the whole board and the work scatters. At most one
	// epic may be active board-wide; the app enforces that on the write path and
	// `furrow lint` reports a violation merged in from another machine.
	//
	// furrow does NOT police WHO flips it — it cannot; a CLI has no caller
	// identity. That rule lives in the operator's own docs, and if it must be
	// enforced, in the agent harness's permission layer.
	Active bool `json:"active"`
	// Labels are pure free-form tags, exactly as on a task.
	Labels []string `json:"labels"`
	// Repos are the owner/repo identifiers this box spans, same shape and
	// semantics as Task.Repos.
	Repos []string `json:"repos"`
	// Meta is the deliberate escape hatch: free-form key/value that furrow stores,
	// round-trips, and NEVER interprets. It is flat (map[string]string) rather than
	// arbitrary nested JSON so the determinism recipe still holds for free —
	// encoding/json sorts map keys, and a string value cannot reintroduce the
	// float-precision churn an `any` would.
	//
	// It is not queryable, and that is a scoping decision, not an oversight: a
	// board carries a handful of epics, so `furrow epic ls --json | jq` is the
	// access path, and a query grammar for it would be machinery in service of a
	// dozen rows. Promoting a meta key to a first-class field later is a schema
	// bump — so anything load-bearing belongs in a real field from the start.
	Meta map[string]string `json:"meta"`

	Created time.Time  `json:"created"`
	Updated time.Time  `json:"updated"`
	Closed  *time.Time `json:"closed"` // nil (-> null) while open

	// Body is the relative path to the long-form prose, e.g. "bodies/e-k3m9.md".
	// It shares the TASK body directory on purpose: ids are prefix-disjoint, so
	// `furrow edit`, `furrow sync -b <id>`, the bodies/*.md union-merge
	// .gitattributes rule, and the orphan-body lint all keep working unchanged. A
	// separate epics/bodies/ dir would have duplicated every one of them.
	Body string `json:"body"`

	// extras holds keys this binary does not know — a field written by a NEWER
	// furrow that did not bump SchemaVersion, so no version gate fired. Without it,
	// one ordinary write would silently destroy that field (see passthrough.go).
	// nil when the shard had no unknown keys, which is the normal case.
	//
	// UNEXPORTED, like Task's, Meta's and RepoRecord's: encoding/json cannot see
	// it, so it never becomes a key of its own, and no MarshalJSON re-emits it —
	// the splice happens on the store's write path (core.MarshalEpic).
	extras Extras
}

// EpicPath returns the canonical relative shard path for an epic: epics/<id>.json.
// The store owns this path; callers never assemble it.
func EpicPath(id string) string { return "epics/" + id + ".json" }

// IsOpen reports whether the epic has not been closed. The single predicate for
// "open", so no caller re-derives it as `Closed == nil` and drifts if the state
// model ever grows.
func (e *Epic) IsOpen() bool { return e.Closed == nil }
