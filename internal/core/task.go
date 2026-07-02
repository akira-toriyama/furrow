// Package core is furrow's pure domain. It models the task index and owns the
// single deterministic serialization path for the store's per-task shards.
//
// PURITY RULE (the spine — see docs/architecture.md): this package imports only
// the standard library (encoding/json, sort, time, fmt, errors, regexp). It
// must NOT import cobra, bubbletea, os, or filepath. Filesystem access lives in
// internal/store; presentation lives in internal/cli and internal/tui; they
// reach the filesystem through the ports declared here (ports.go). Crossing a
// layer means a port is missing, not that core should grow an import.
package core

import "time"

// SchemaVersion is the current on-disk layout version. It lives in exactly one
// file, .furrow/meta.json (see Meta) — never in a task shard, so a version bump
// is a single-file change and no shard becomes a cross-write merge point. Bump
// only on a breaking layout change, and update docs/schema/ + goldens in the
// same change. v2 = per-task shards (tasks/<id>.json) + meta.json (v1 was the
// monolithic index.json).
const SchemaVersion = 2

// Index is the in-memory aggregate of every task: the store folds the per-task
// shards (tasks/<id>.json) into one of these on Load, and splits it back into
// shards on Save. It is NOT an on-disk file — .furrow/index.json is abolished.
// The struct field order IS the JSON key order for Marshal (an in-memory,
// test/inspection-only canonical form; the store never persists these bytes), so
// reordering fields changes the determinism golden — don't reorder without a
// schema bump and a golden-file update.
type Index struct {
	SchemaVersion int    `json:"schema_version"`
	Tasks         []Task `json:"tasks"`
}

// Meta is .furrow/meta.json: the one board-wide schema version, deliberately
// held in its own file so a version bump touches a single file and no task shard
// ever carries a schema_version field that separate operators would rewrite at
// once (turning it into a git-conflict point). MetaPath names it.
type Meta struct {
	SchemaVersion int `json:"schema_version"`
}

// CheckSchemaVersion is the version gate: it rejects a board whose meta.json
// declares a layout NEWER than this binary knows. Without it, an old binary's
// lenient json.Unmarshal would load such a board, silently drop every field it
// doesn't know (e.g. repos), and write the loss back on the next Save. Both
// stores call this on Load AND Save; the CLI surfaces it as exit 3 (internal —
// the fix is updating the binary, not the input). Older versions load fine:
// forward-compat is the store's normal lenient read.
func CheckSchemaVersion(v int) error {
	if v > SchemaVersion {
		return Internalf("meta", "board is schema v%d; this furrow is too old (it knows v%d) — update the binary", v, SchemaVersion)
	}
	return nil
}

// Task is one tracked item. Metadata only: the long-form prose lives in
// .furrow/bodies/<id>.md and is addressed by Body (a relative path, never the
// content). This split is the whole point of the hybrid store.
//
// Field order == JSON key order (see Index). `closed` is a pointer so it
// serializes to null while a task is open.
// `parent` is omitempty because most tasks have no parent and an empty string
// key would be noise; both states (absent / present) are deterministic.
type Task struct {
	ID       string `json:"id"`       // frozen, == bodies/<id>.md stem; never reused or renumbered
	Title    string `json:"title"`    // one-line summary
	Status   string `json:"status"`   // a lane value from config.toml [lanes].order
	Priority int    `json:"priority"` // sparse integer (10-step); reorder = edit this one field
	// Value and Effort are an optional, coarse 1..5 estimate (importance and
	// cost) an agent or human records at triage. Both are pointers so "unset"
	// (nil -> key absent) is distinct from any score, keeping intake friction
	// zero. Out-of-range inputs are clamped to 1..5 by Canonicalize (lint warns
	// on a hand-edited stray); ROI = Value/Effort is derived, never stored.
	Value  *int     `json:"value,omitempty"`
	Effort *int     `json:"effort,omitempty"`
	Labels []string `json:"labels"`
	// Repos is the set of repositories (owner/repo form) this task relates to —
	// a first-class concept, not a label convention. 0..N entries; an empty set
	// means the task is a draft (attached to no repo yet), the issue-draft
	// analogue. Same set semantics as Labels: sorted+deduped, [] never null.
	Repos     []string        `json:"repos"`
	Parent    string          `json:"parent,omitempty"`
	Deps      []string        `json:"deps"` // ids this task waits on; `next` treats a task ready when all are done
	Refs      []string        `json:"refs"` // file:line or URL pointers
	Checklist []ChecklistItem `json:"checklist"`
	Created   time.Time       `json:"created"`
	Updated   time.Time       `json:"updated"`
	Closed    *time.Time      `json:"closed"` // nil (-> null) while open; set when moved to a terminal lane
	Body      string          `json:"body"`   // relative path, e.g. "bodies/t-0042.md"
}

// ChecklistItem mirrors a GitHub "Sub-issues progress" line: a piece of work
// inside a task that is checkable without spawning a whole task.
type ChecklistItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// BodyPath returns the canonical relative body path for an id. Both the store
// and the marshaller use this so the Body field is never hand-assembled.
func BodyPath(id string) string { return "bodies/" + id + ".md" }

// TaskPath returns the canonical relative metadata-shard path for an id — the
// per-task twin of BodyPath. Under the sharded store, one <id>.json under tasks/
// holds a task's metadata, 1:1 with bodies/<id>.md, so the whole record for an
// id is exactly {tasks/<id>.json, bodies/<id>.md} and never a slice of a shared
// file. Callers must not hand-assemble this path.
func TaskPath(id string) string { return "tasks/" + id + ".json" }

// MetaPath returns the relative path of the board-wide meta file. It holds only
// the schema version (see Meta); keeping it out of every shard is what stops a
// version field from becoming a merge point. Callers must not hand-assemble it.
func MetaPath() string { return "meta.json" }

// EstimateMin and EstimateMax bound the coarse value/effort scale. Inputs
// outside the range are clamped to it (see Canonicalize).
const (
	EstimateMin = 1
	EstimateMax = 5
)

// ROI is the derived return-on-investment, Value/Effort — the signal an agent
// uses to pick the next task. It is computed, never stored, so editing value or
// effort always yields a current ROI with no stale number to reconcile. ROI is
// undefined (0) when either estimate is unset or Effort is non-positive.
func (t *Task) ROI() float64 {
	if t.Value == nil || t.Effort == nil || *t.Effort <= 0 {
		return 0
	}
	return float64(*t.Value) / float64(*t.Effort)
}
