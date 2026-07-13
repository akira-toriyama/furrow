package app

import "github.com/akira-toriyama/furrow/internal/core"

// BoardInfo is the machine-readable snapshot `furrow board` prints: where writes
// land (Store), how the board was discovered (Source), what scope filters reads
// (ScopeRepo/AutoFilter), and the full lane vocabulary — so an agent learns the
// lanes and the effective scope in one call, without provoking an error (the old
// only-way-to-discover-lanes was to fail a `move` on purpose). Every field is a
// copy or scalar, so a caller can never mutate the live config through it.
type BoardInfo struct {
	Store                string   `json:"store"`         // absolute .furrow path (where writes land)
	Source               string   `json:"source"`        // env|local|pointer|user-config
	ScopeRepo            string   `json:"scope_repo"`    // board-scope repo ("" = whole board)
	AutoFilter           bool     `json:"auto_filter"`   // reads auto-filter by scope_repo (meaningful only when set)
	DefaultLabel         string   `json:"default_label"` // literal add-time tag ("" = none)
	Lanes                []string `json:"lanes"`         // the closed lane vocabulary, in order
	NextLanes            []string `json:"next_lanes"`    // lanes `furrow next` considers
	DefaultLane          string   `json:"default_lane"`  // lane `add` assigns
	DoneLane             string   `json:"done_lane"`     // lane `done` moves into
	Terminal             []string `json:"terminal"`      // lanes excluded from `next`, in lane order
	StaleDays            int      `json:"stale_days"`    // revisit staleness window (0 disables)
	ArchiveOlderThanDays int      `json:"archive_older_than_days"`
	LabelsRequired       bool     `json:"labels_required"` // add/lint reject a label-less task

	// The schema triple: what the BOARD declares, what this BINARY writes, and
	// whether the two agree (only then are writes allowed). This is the one place
	// an agent can ask "can I write here, and if not, which side is stale?"
	// WITHOUT provoking an error — `board` answers even on a board no other
	// command can open, which is what makes it usable as a CI pre-flight.
	SchemaVersion       int    `json:"schema_version"`        // the board's declared layout (0 = absent or unreadable)
	BinarySchemaVersion int    `json:"binary_schema_version"` // the layout this furrow writes
	SchemaState         string `json:"schema_state"`          // current|outdated|too-new|unreadable
	Writable            bool   `json:"writable"`              // == (schema_state == "current")
}

// Board schema states. Stable kebab-case tokens — branch on these, not on the
// two integers, so the remediation ("upgrade the board" vs "update the binary")
// is never derived by accident.
const (
	SchemaCurrent    = "current"    // board == binary; writes allowed
	SchemaOutdated   = "outdated"   // board < binary; readable, read-only until `furrow upgrade`
	SchemaTooNew     = "too-new"    // board > binary; refused both ways — update furrow
	SchemaUnreadable = "unreadable" // meta.json is corrupt; restore it from git
)

// Board returns the active board's introspection snapshot. It reads the config
// (already resolved) plus one small meta.json probe, and it NEVER fails: a
// too-new or corrupt board is REPORTED here, not raised. That contract is
// load-bearing — `furrow board --json` is the last thing that still works when
// the board and the binary disagree, so it is what CI reads to diagnose the
// mismatch instead of watching every task read fail with "task not found".
func (a *App) Board() BoardInfo {
	// Terminal lanes in canonical (lane-order) form, not the config membership
	// map's random iteration order, so the output is deterministic.
	terminal := []string{}
	for _, l := range a.Cfg.Lanes {
		if a.Cfg.IsTerminal(l) {
			terminal = append(terminal, l)
		}
	}
	// The schema state of the WHOLE board — hot store and, when it exists, the
	// sibling archive store, which carries its own meta.json and its own write
	// gate. Folding them is schemaState's job (schema_state.go); doing it here
	// would mean lint and upgrade each re-derive the rule and drift from it.
	ver, state, writable := a.schemaState()
	return BoardInfo{
		Store:                a.Dir,
		Source:               a.Source,
		ScopeRepo:            a.DefaultRepo,
		AutoFilter:           a.AutoFilter,
		DefaultLabel:         a.DefaultLabel,
		Lanes:                append([]string(nil), a.Cfg.Lanes...),
		NextLanes:            append([]string(nil), a.Cfg.NextLanes...),
		DefaultLane:          a.Cfg.DefaultLane,
		DoneLane:             a.Cfg.DoneLane,
		Terminal:             terminal,
		StaleDays:            a.Cfg.RevisitStaleDays,
		ArchiveOlderThanDays: a.Cfg.ArchiveOlderThanDays,
		LabelsRequired:       a.Cfg.LabelsRequired,
		SchemaVersion:        ver,
		BinarySchemaVersion:  core.SchemaVersion,
		SchemaState:          state,
		Writable:             writable,
	}
}
