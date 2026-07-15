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
	DefaultLanes    = []string{"inbox", "backlog", "ready", "in-progress", "waiting", "done", "icebox"}
	DefaultLane     = "inbox" // lane assigned by `furrow add`
	DefaultDoneLane = "done"  // lane `furrow done` moves a task into
	// DefaultTerminal lanes are excluded from `furrow next`. "waiting" is the GTD
	// Waiting-For lane (delegated / blocked on someone else): parked like icebox,
	// not finished — so it never stamps `closed` and is never archived.
	DefaultTerminal = []string{"done", "icebox", "waiting"}

	// DefaultNextLanes is the "actionable now" set `furrow next` considers (in
	// addition to the deps-satisfied check). Intake/planning lanes are excluded
	// so next stays focused on what's ready to work. Falls back to all
	// non-terminal lanes for a custom lane scheme that has neither of these.
	DefaultNextLanes = []string{"ready", "in-progress"}

	DefaultPriorityStep    = 10
	DefaultPriorityDefault = 100

	DefaultIDPrefix = "t-"
	DefaultIDWidth  = 5 // number of random suffix chars in a new id (e.g. t-k3m9p)

	DefaultArchiveOlderThanDays = 30
	DefaultUITheme              = "auto"

	// DefaultRevisitStaleDays is how long a task may go without an update before
	// `furrow revisit` flags it stale. A config stale_days of 0 disables the
	// stale signal (the other revisit signals still fire).
	DefaultRevisitStaleDays = 30

	// DefaultReviewStaleAfterDays is how long a repo may go without a human
	// review before `furrow sync` nudges "N days unreviewed" (the per-repo
	// staleness clock a `furrow review <repo>` resets). A config
	// stale_after_days of 0 disables the nudge. The GTD weekly-review cadence
	// motivates the 14-day default (two missed weeks).
	DefaultReviewStaleAfterDays = 14

	// DefaultTypes is the work-item type vocabulary ([types].order): a closed set
	// like the lanes. "task" is an ordinary work item; "epic" is a container (a
	// box that groups children via parent and is itself skipped by `furrow next`).
	DefaultTypes = []string{"task", "epic"}
	// DefaultType is the type an empty shard resolves to ([types].default). It must
	// never be a container (fromRaw enforces the invariant), or every type-less
	// task would vanish from `furrow next`.
	DefaultType = "task"
	// DefaultContainers are the types that are boxes ([types].containers): excluded
	// from `furrow next`, they group work rather than being worked themselves.
	DefaultContainers = []string{"epic"}
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
	NextLanes   []string        // lanes `furrow next` considers (besides deps-done)

	// Types is the work-item type vocabulary ([types].order). DefaultType is the
	// type an empty shard resolves to (never a container). Containers is the set
	// of container types (epics) that `furrow next` skips — the type-side twin of
	// Terminal for lanes.
	Types       []string
	DefaultType string
	Containers  map[string]bool

	PriorityStep    int
	PriorityDefault int

	IDPrefix string
	IDWidth  int

	ArchiveOlderThanDays int
	UITheme              string

	RevisitStaleDays int // days without update before `revisit` flags stale; 0 disables

	// ReviewStaleAfterDays is how many days a repo may go without a human review
	// before `furrow sync` nudges it as unreviewed; 0 disables the nudge.
	ReviewStaleAfterDays int

	LabelsRequired bool // when true, a task with zero labels is rejected/flagged

	// LintArchiveDone is the [lint].archive_done nudge threshold: `furrow lint`
	// warns when at least this many done tasks are older than ArchiveOlderThanDays
	// and ready to archive. 0 (default) disables the nudge.
	LintArchiveDone int

	// Alias is the board-level [alias] table (name -> command string) that
	// `furrow <name> …` expands git-style. Empty when unset; a nil map is fine to
	// range over. Builtin-shadowing entries are inert (expansion checks builtins
	// first) and flagged by lint.
	Alias map[string]string

	// Standalone marks a board used on a single machine with no remote, no
	// `furrow sync`, and no CI. It changes only USER-FACING GUIDANCE — never
	// behavior, the schema gate, or the on-disk format: `furrow upgrade` drops the
	// shared-board flag-day / pinned-CI checklist and the `furrow sync` publish
	// line that misdirect a standalone operator. (The write-block error itself is
	// CI-agnostic for every board; it just points at `furrow upgrade`.) Default
	// false = shared-board behavior. Declared in config.toml (not meta.json), so it
	// needs no schema bump.
	Standalone bool

	idPattern *regexp.Regexp  // compiled from IDPrefix, cached
	nextSet   map[string]bool // membership set built from NextLanes
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
		RevisitStaleDays:     DefaultRevisitStaleDays,
		ReviewStaleAfterDays: DefaultReviewStaleAfterDays,
		Types:                append([]string(nil), DefaultTypes...),
		DefaultType:          DefaultType,
		Containers:           setOf(DefaultContainers),
	}
	c.NextLanes = defaultNextLanes(c.Lanes, c.Terminal)
	c.compile()
	return c
}

// defaultNextLanes is the fallback `next` lane set: ready+in-progress if present,
// else every non-terminal lane (for a custom lane scheme).
func defaultNextLanes(lanes []string, terminal map[string]bool) []string {
	var out []string
	for _, l := range DefaultNextLanes {
		if contains(lanes, l) {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		for _, l := range lanes {
			if !terminal[l] {
				out = append(out, l)
			}
		}
	}
	return out
}

// IsNextLane reports whether tasks in lane are considered by `furrow next`.
func (c *Config) IsNextLane(lane string) bool { return c.nextSet[lane] }

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

// EffectiveType resolves a shard's type: the stored value, or the configured
// default ([types].default) when empty. Every task therefore has a type even
// though most shards store none.
func (c *Config) EffectiveType(typ string) string {
	if typ == "" {
		return c.DefaultType
	}
	return typ
}

// IsContainerType reports whether a task of this (raw, possibly empty) type is a
// container — a box like an epic that `furrow next` skips. Empty resolves to the
// default type first, and the default is never a container (fromRaw enforces it),
// so a type-less task is never a container. The type-side twin of IsTerminal.
func (c *Config) IsContainerType(typ string) bool {
	return c.Containers[c.EffectiveType(typ)]
}

// IsType reports whether typ is in the configured vocabulary ([types].order).
// The empty string is always valid — it means "the default type".
func (c *Config) IsType(typ string) bool {
	return typ == "" || contains(c.Types, typ)
}

// ContainerTypes returns the container type names in vocabulary order — the
// list form of the Containers set, for `furrow board` reporting.
func (c *Config) ContainerTypes() []string {
	var out []string
	for _, ty := range c.Types {
		if c.Containers[ty] {
			out = append(out, ty)
		}
	}
	return out
}

// IDPattern is the regexp a frozen id must match: the configured prefix
// followed by one or more lowercase base32 chars. It is intentionally permissive
// so legacy zero-padded numeric ids (t-0042) and new random ids (t-k3m9p) both
// validate.
func (c *Config) IDPattern() *regexp.Regexp { return c.idPattern }

func (c *Config) compile() {
	c.idPattern = regexp.MustCompile("^" + regexp.QuoteMeta(c.IDPrefix) + `[0-9a-z]+$`)
	c.nextSet = setOf(c.NextLanes)
}

func setOf(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
