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

import (
	"fmt"
	"time"
)

// SchemaVersion is the layout this BINARY writes. The board's own version lives
// in exactly one file, .furrow/meta.json (see Meta) — never in a task shard, so a
// version bump is a single-file change and no shard becomes a cross-write merge
// point. The two are not the same number, and that distinction is the whole
// point: a binary may only write a board that already declares this exact
// version (CheckWritable), and nothing but `furrow upgrade` may raise a board.
//
// Bumping this const is therefore a FLAG DAY, not a code change: it makes every
// board still on the old layout read-only, including for anyone running a pinned
// release (the fleet's task-status CI). Order matters — release furrow, bump
// every caller's pin, THEN `furrow upgrade --yes` the board. Bump only on a
// read-breaking layout change, and update docs/schema/ + goldens in the same
// change. v5 = adds the per-task `type` field (the work-item type; empty == the
// config default, normally "task"). `next` reads it to skip containers (epics),
// so an older binary that merely PRESERVED it would still hand you an epic as
// work — a query reads the field, so by the bump rule it is a schema field, not
// a label, and the layout goes up. v4 = adds the per-task `reviewed` timestamp
// AND the per-repo review shards (.furrow/repos/<owner>__<repo>.json); a v3-only
// binary must refuse it, or its lenient unmarshal would strip `reviewed` and
// write the loss back. v3 = shards whose tasks carry the required first-class
// repos set (the repos pivot). v2 = per-task shards (tasks/<id>.json) +
// meta.json (v1 was the monolithic index.json).
const SchemaVersion = 5

// Index is the in-memory aggregate of every task: the store folds the per-task
// shards (tasks/<id>.json) into one of these on Load, and splits it back into
// shards on Save. It is NOT an on-disk file — .furrow/index.json is abolished.
// The struct field order IS the JSON key order for Marshal (an in-memory,
// test/inspection-only canonical form; the store never persists these bytes), so
// reordering fields changes the determinism golden — don't reorder without a
// schema bump and a golden-file update.
//
// SchemaVersion here is INFORMATIONAL — what the board declared when Load read
// it. Save ignores it and consults the board on disk (Store.BoardVersion),
// because an in-memory field defaults to the binary's version at marshal time
// (Canonicalize) and trusting it is exactly how a routine write once migrated a
// shared board behind its owner's back.
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

	// extras holds keys this binary does not know — a field written by a NEWER
	// furrow that did not bump SchemaVersion, so no version gate fired. Without it,
	// one ordinary write would silently destroy that field (see passthrough.go).
	// nil when there were none, which is the normal case.
	//
	// UNEXPORTED on purpose, and it is structural, not stylistic: encoding/json
	// cannot see it, so it can never surface as a literal "extras" key, and it can
	// never leak into internal/cli/output.go's --json views. Which leads to the
	// rule that must not be broken:
	//
	//   *** Task must NEVER grow a MarshalJSON method. ***
	//
	// internal/cli's views EMBED core.Task to put body_text / reason / revisit /
	// snippet / mentioned_by beside it. A MarshalJSON on Task would be PROMOTED to
	// those outer structs, Go would call it for the whole view, and every sibling
	// field would vanish — with no compile error. The splice happens on the store's
	// write path instead (core.MarshalTask).
	extras Extras
}

// CheckSchemaVersion is the READ half of the version gate: it rejects a board
// whose meta.json declares a layout NEWER than this binary knows. It guards
// against MISREADING such a board — a v3-only binary would happily load a v4
// shard and then act as if `reviewed` did not exist. (It no longer guards against
// DESTROYING the fields it doesn't know: passthrough.go now preserves those. But
// preserving is not understanding, which is exactly why this gate stays.) Both
// stores call it on Load; the CLI surfaces it as exit 3 (internal —
// the fix is updating the binary, not the input). An OLDER board loads fine:
// forward-compat is the store's normal lenient read. Writing one is a different
// question — see CheckWritable.
func CheckSchemaVersion(v int) error {
	if v > SchemaVersion {
		return &Error{
			Code: CodeInternal,
			ID:   "schema-too-new",
			// Message is deliberately CI-agnostic: core is pure and must not name a
			// specific CI workflow. The presentation layer (which can read config)
			// adds any shared-board / pinned-CI guidance.
			Msg: fmt.Sprintf("board is schema v%d; this furrow knows only v%d — update furrow",
				v, SchemaVersion),
			Details: map[string]any{"board_schema": v, "binary_schema": SchemaVersion},
		}
	}
	return nil
}

// CheckWritable is the WRITE half of the gate, and the fix for the 2026-07-13
// outage: a binary may write ONLY a board that already declares exactly its own
// layout. Reading an older board is fine (lenient, above); silently REWRITING it
// is not, because the shards would then carry fields the board never promised —
// which is precisely how one routine `furrow sync` from a source build migrated
// the shared tracker 3->4 and locked out every release the fleet's CI pinned.
//
// So: newer board -> schema-too-new (exit 3, "I am stale"); older board, or one
// with shards but no meta at all (v == 0) -> schema-upgrade-required (exit 2,
// "the BOARD is stale — an explicit command fixes it"). The two are told apart by
// exit code alone, and both carry {board_schema, binary_schema} for a machine.
// Raising a board's version is never a side effect: `furrow upgrade` is the only
// caller that may do it, and it is a deliberate flag day.
func CheckWritable(v int) error {
	if err := CheckSchemaVersion(v); err != nil {
		return err
	}
	if v < SchemaVersion {
		return &Error{
			Code: CodeValidation,
			ID:   "schema-upgrade-required",
			// Message is deliberately CI-agnostic: core is pure and must not name a
			// specific CI workflow. It just points at `furrow upgrade`, which is
			// where the standalone-vs-shared guidance (flag-day checklist vs a plain
			// apply) actually differs — the presentation layer reads config there.
			Msg: fmt.Sprintf("board is schema v%d but this furrow writes v%d; an ordinary write never raises a board's layout — run `furrow upgrade`",
				v, SchemaVersion),
			Details: map[string]any{"board_schema": v, "binary_schema": SchemaVersion},
		}
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
	// Reviewed is when a human last reviewed this task (a `furrow review <id>`
	// stamp), tracked SEPARATELY from Updated: reviewing changes no content, so
	// it must not bump `updated` and disturb staleness/`--sort updated`. A
	// pointer so "never reviewed" serializes to explicit null, like Closed.
	Reviewed *time.Time `json:"reviewed"`
	Body     string     `json:"body"` // relative path, e.g. "bodies/t-0042.md"

	// Type is the work-item TYPE — the DECLARATION that a task is a container (an
	// epic), not an inference from whether it happens to have children. `next`
	// reads it to skip containers, so it is a schema field, not a free-form label:
	// a typo'd `epci` must not silently produce a workable task. Empty (omitempty)
	// == the default type from config ([types].default, normally "task"), so every
	// pre-v5 shard is byte-unchanged on disk. It lives at the END of the struct per
	// the shard-shape rule — a mid-struct field churns key order against an old
	// binary's extras re-emit (see the extras note below).
	Type string `json:"type,omitempty"`

	// extras holds keys this binary does not know — a field written by a NEWER
	// furrow that did not bump SchemaVersion, so no version gate fired. Without it,
	// one ordinary write would silently destroy that field (see passthrough.go).
	// nil when the shard had no unknown keys, which is the normal case.
	//
	// UNEXPORTED, and structurally so — the same rule Meta.extras spells out above,
	// and it binds hardest HERE: encoding/json cannot see this field, so it can
	// never surface as a literal "extras" key, and *** Task must NEVER grow a
	// MarshalJSON method *** to re-emit it. Go would PROMOTE that method to
	// internal/cli's --json views (they embed core.Task to put body_text / reason /
	// revisit / snippet / mentioned_by beside it), call it for the whole view, and
	// drop every sibling field with no compile error. The splice happens on the
	// store's write path instead — core.MarshalTask -> encodeCanonicalWithExtras.
	// Read it back with ExtraKeys().
	extras Extras
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
