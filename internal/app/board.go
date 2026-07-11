package app

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
}

// Board returns the active board's introspection snapshot. It reads only the
// already-resolved App/Config — no store load — so it is cheap and never fails.
func (a *App) Board() BoardInfo {
	// Terminal lanes in canonical (lane-order) form, not the config membership
	// map's random iteration order, so the output is deterministic.
	terminal := []string{}
	for _, l := range a.Cfg.Lanes {
		if a.Cfg.IsTerminal(l) {
			terminal = append(terminal, l)
		}
	}
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
	}
}
