package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// raw mirrors config.toml's structure for decoding. Every field is optional;
// an absent or invalid value clamps to the package default. go-toml/v2 ignores
// unknown top-level keys by default, which is exactly the clamp-don't-reject
// behavior we want — a stray key never errors.
type raw struct {
	Lanes struct {
		Order    []string `toml:"order"`
		Default  string   `toml:"default"`
		Done     string   `toml:"done"`
		Terminal []string `toml:"terminal"`
	} `toml:"lanes"`
	Priority struct {
		Step    *int `toml:"step"`
		Default *int `toml:"default"`
	} `toml:"priority"`
	IDs struct {
		Prefix string `toml:"prefix"`
		Width  *int   `toml:"width"`
	} `toml:"ids"`
	Archive struct {
		OlderThanDays *int `toml:"older_than_days"`
	} `toml:"archive"`
	UI struct {
		Theme string `toml:"theme"`
	} `toml:"ui"`
	Next struct {
		Lanes []string `toml:"lanes"`
	} `toml:"next"`
	Labels struct {
		Required *bool `toml:"required"`
	} `toml:"labels"`
	Revisit struct {
		StaleDays *int `toml:"stale_days"`
	} `toml:"revisit"`
	// Alias is the board-level [alias] table: name -> a command string that
	// `furrow <name> …` expands to, git-style. A map decodes any [alias] key.
	Alias map[string]string `toml:"alias"`
}

// Load reads config.toml at path and returns the effective config plus any
// clamp warnings (each a human-readable string). A missing file is not an
// error: it returns Default() with no warnings. A malformed file IS an error
// (the user wrote broken TOML — that is worth stopping for).
func Load(path string) (*Config, []string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var r raw
	if err := toml.Unmarshal(data, &r); err != nil {
		return nil, nil, fmt.Errorf("config.toml: %w", err)
	}
	return fromRaw(r)
}

// fromRaw applies the clamp-don't-reject policy, collecting a warning for every
// value it had to correct.
func fromRaw(r raw) (*Config, []string, error) {
	c := Default()
	var warn []string

	if len(r.Lanes.Order) > 0 {
		c.Lanes = dedupeNonEmpty(r.Lanes.Order)
		if len(c.Lanes) == 0 {
			c.Lanes = append([]string(nil), DefaultLanes...)
			warn = append(warn, "lanes.order was empty after cleaning; using defaults")
		}
	}

	// default lane must exist in the order. When the global default lane isn't
	// part of a custom order, fall back to that order's first lane.
	defaultFallback := DefaultLane
	if !contains(c.Lanes, defaultFallback) {
		defaultFallback = c.Lanes[0]
	}
	c.DefaultLane = clampLane(r.Lanes.Default, defaultFallback, c.Lanes, "lanes.default", &warn)
	// done lane: prefer config, else "done" if present, else the last lane.
	doneFallback := DefaultDoneLane
	if !contains(c.Lanes, doneFallback) {
		doneFallback = c.Lanes[len(c.Lanes)-1]
	}
	c.DoneLane = clampLane(r.Lanes.Done, doneFallback, c.Lanes, "lanes.done", &warn)

	// terminal lanes: keep only those that are real lanes.
	if r.Lanes.Terminal != nil {
		var keep []string
		for _, l := range r.Lanes.Terminal {
			if contains(c.Lanes, l) {
				keep = append(keep, l)
			} else {
				warn = append(warn, fmt.Sprintf("lanes.terminal entry %q is not a lane; ignored", l))
			}
		}
		c.Terminal = setOf(keep)
	} else {
		// default terminal set, intersected with the configured lanes.
		var keep []string
		for _, l := range DefaultTerminal {
			if contains(c.Lanes, l) {
				keep = append(keep, l)
			}
		}
		c.Terminal = setOf(keep)
	}

	// next lanes: keep only real lanes; empty/absent -> sensible default.
	if r.Next.Lanes != nil {
		var keep []string
		for _, l := range r.Next.Lanes {
			if contains(c.Lanes, l) {
				keep = append(keep, l)
			} else {
				warn = append(warn, fmt.Sprintf("next.lanes entry %q is not a lane; ignored", l))
			}
		}
		if len(keep) == 0 {
			keep = defaultNextLanes(c.Lanes, c.Terminal)
			warn = append(warn, "next.lanes was empty after cleaning; using the default actionable lanes")
		}
		c.NextLanes = keep
	} else {
		c.NextLanes = defaultNextLanes(c.Lanes, c.Terminal)
	}

	c.PriorityStep = clampPositive(r.Priority.Step, DefaultPriorityStep, "priority.step", &warn)
	c.PriorityDefault = clampPositive(r.Priority.Default, DefaultPriorityDefault, "priority.default", &warn)

	if r.IDs.Prefix != "" {
		c.IDPrefix = r.IDs.Prefix
	}
	c.IDWidth = clampPositive(r.IDs.Width, DefaultIDWidth, "ids.width", &warn)

	if r.Archive.OlderThanDays != nil {
		if *r.Archive.OlderThanDays < 0 {
			warn = append(warn, fmt.Sprintf("archive.older_than_days %d < 0; using %d", *r.Archive.OlderThanDays, DefaultArchiveOlderThanDays))
		} else {
			c.ArchiveOlderThanDays = *r.Archive.OlderThanDays
		}
	}

	if r.Labels.Required != nil {
		c.LabelsRequired = *r.Labels.Required
	}

	// stale_days: a "days" knob like archive.older_than_days — 0 is valid (it
	// disables the stale signal); only a negative value clamps to the default.
	if r.Revisit.StaleDays != nil {
		if *r.Revisit.StaleDays < 0 {
			warn = append(warn, fmt.Sprintf("revisit.stale_days %d < 0; using %d", *r.Revisit.StaleDays, DefaultRevisitStaleDays))
		} else {
			c.RevisitStaleDays = *r.Revisit.StaleDays
		}
	}

	if r.UI.Theme != "" {
		if validThemes[r.UI.Theme] {
			c.UITheme = r.UI.Theme
		} else {
			warn = append(warn, fmt.Sprintf("ui.theme %q is not auto|dark|light; using %q", r.UI.Theme, DefaultUITheme))
		}
	}

	// [alias]: keep only entries with a non-blank name AND a non-blank command
	// (clamp-don't-reject — a half-written alias never breaks furrow, just drops
	// with a warning `furrow lint` surfaces). Builtin-shadow refusal is the CLI's
	// job (it owns the command set): a shadowing alias is inert because expansion
	// checks builtins first, and lint warns about it there.
	for name, cmd := range r.Alias {
		if strings.TrimSpace(name) == "" {
			warn = append(warn, "alias with an empty name; ignored")
			continue
		}
		if strings.TrimSpace(cmd) == "" {
			warn = append(warn, fmt.Sprintf("alias %q has an empty command; ignored", name))
			continue
		}
		if c.Alias == nil {
			c.Alias = map[string]string{}
		}
		c.Alias[name] = cmd
	}

	c.compile()
	return c, warn, nil
}

func clampLane(v, fallback string, lanes []string, key string, warn *[]string) string {
	if v == "" {
		return fallback
	}
	if contains(lanes, v) {
		return v
	}
	*warn = append(*warn, fmt.Sprintf("%s %q is not in lanes.order; using %q", key, v, fallback))
	return fallback
}

func clampPositive(v *int, fallback int, key string, warn *[]string) int {
	if v == nil {
		return fallback
	}
	if *v <= 0 {
		*warn = append(*warn, fmt.Sprintf("%s %d must be > 0; using %d", key, *v, fallback))
		return fallback
	}
	return *v
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// dedupeNonEmpty drops empty strings and later duplicates, preserving order.
func dedupeNonEmpty(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
