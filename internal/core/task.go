// Package core is furrow's pure domain. It models the task index and owns the
// single deterministic serialization path for .furrow/index.json.
//
// PURITY RULE (the spine — see docs/architecture.md): this package imports only
// the standard library (encoding/json, sort, time, fmt, errors, regexp). It
// must NOT import cobra, bubbletea, os, or filepath. Filesystem access lives in
// internal/store; presentation lives in internal/cli and internal/tui; they
// reach the filesystem through the ports declared here (ports.go). Crossing a
// layer means a port is missing, not that core should grow an import.
package core

import "time"

// SchemaVersion is the current on-disk schema version written into index.json.
// Bump only on a breaking layout change, and update docs/schema/ + goldens in
// the same change.
const SchemaVersion = 1

// Index is the whole of .furrow/index.json. The struct field order IS the JSON
// key order — encoding/json emits fields in declaration order — so reordering
// these fields changes every diff. Do not reorder without a schema bump and a
// golden-file update.
type Index struct {
	SchemaVersion int    `json:"schema_version"`
	Tasks         []Task `json:"tasks"`
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
	ID        string          `json:"id"`       // frozen, == bodies/<id>.md stem; never reused or renumbered
	Title     string          `json:"title"`    // one-line summary
	Status    string          `json:"status"`   // a lane value from config.toml [lanes].order
	Priority  int             `json:"priority"` // sparse integer (10-step); reorder = edit this one field
	Labels    []string        `json:"labels"`
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
