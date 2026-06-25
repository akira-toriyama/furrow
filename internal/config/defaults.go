// Package config loads .furrow/config.toml — the one human-edited file in the
// store. It is read-only from furrow's side (the app never writes it) and
// follows a clamp-don't-reject policy: unknown keys are ignored and
// out-of-range values fall back to a safe default with a warning, so a typo can
// never break the tool. `furrow lint`/`validate` surfaces the warnings.
package config

import "regexp"

// Defaults mirror config.toml's shipped template and GitHub Projects #5's lanes.
// Editing the template changes a user's config; editing these changes the
// fallback when a key is absent or invalid.
var (
	DefaultLanes    = []string{"inbox", "backlog", "ready", "in-progress", "done", "icebox"}
	DefaultLane     = "inbox"                    // lane assigned by `furrow add`
	DefaultDoneLane = "done"                     // lane `furrow done` moves a task into
	DefaultTerminal = []string{"done", "icebox"} // lanes a task in is not "actionable" for `next`

	DefaultPriorityStep    = 10
	DefaultPriorityDefault = 100

	DefaultIDPrefix = "t-"
	DefaultIDWidth  = 4

	DefaultArchiveOlderThanDays = 30
	DefaultUITheme              = "auto"
)

// validThemes is the closed set for [ui].theme.
var validThemes = map[string]bool{"auto": true, "dark": true, "light": true}

// Config is the effective, validated configuration the rest of furrow reads.
// Construct it only via Load (or Default); never hand-build one, so the
// invariants (DefaultLane is in Lanes, etc.) always hold.
type Config struct {
	Lanes       []string
	DefaultLane string
	DoneLane    string
	Terminal    map[string]bool // membership set built from the terminal lane list

	PriorityStep    int
	PriorityDefault int

	IDPrefix string
	IDWidth  int

	ArchiveOlderThanDays int
	UITheme              string

	idPattern *regexp.Regexp // compiled from IDPrefix, cached
}

// Default returns the built-in configuration used when .furrow/config.toml is
// absent. It is what `furrow init` writes as a template, too.
func Default() *Config {
	c := &Config{
		Lanes:                append([]string(nil), DefaultLanes...),
		DefaultLane:          DefaultLane,
		DoneLane:             DefaultDoneLane,
		Terminal:             setOf(DefaultTerminal),
		PriorityStep:         DefaultPriorityStep,
		PriorityDefault:      DefaultPriorityDefault,
		IDPrefix:             DefaultIDPrefix,
		IDWidth:              DefaultIDWidth,
		ArchiveOlderThanDays: DefaultArchiveOlderThanDays,
		UITheme:              DefaultUITheme,
	}
	c.compile()
	return c
}

// LaneRank returns a lane's position in the order, or false if it is unknown.
func (c *Config) LaneRank(lane string) (int, bool) {
	for i, l := range c.Lanes {
		if l == lane {
			return i, true
		}
	}
	return 0, false
}

// IsLane reports whether lane is a configured status.
func (c *Config) IsLane(lane string) bool {
	_, ok := c.LaneRank(lane)
	return ok
}

// IsTerminal reports whether tasks in lane are excluded from `next`.
func (c *Config) IsTerminal(lane string) bool { return c.Terminal[lane] }

// IDPattern is the regexp a frozen id must match: the configured prefix
// followed by one or more digits.
func (c *Config) IDPattern() *regexp.Regexp { return c.idPattern }

func (c *Config) compile() {
	c.idPattern = regexp.MustCompile("^" + regexp.QuoteMeta(c.IDPrefix) + `[0-9]+$`)
}

func setOf(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
