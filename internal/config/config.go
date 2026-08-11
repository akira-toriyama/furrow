package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// raw mirrors config.toml's structure for decoding. Every field is optional;
// an absent or invalid value clamps to the package default. Decoding is STRICT
// (DisallowUnknownFields) so a key the parser does not know — a typo'd
// `[lanse]`, a retired section — surfaces as a clamp warning WITH its line
// number instead of being silently ignored: go-toml/v2's default lenient mode
// made `furrow lint` blind to exactly the mistake the template promised it
// would report. The key is still ignored (clamp-don't-reject — a stray key
// never errors); strictness only buys the warning.
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
		Prefix     string `toml:"prefix"`
		EpicPrefix string `toml:"epic_prefix"`
		Width      *int   `toml:"width"`
	} `toml:"ids"`
	Archive struct {
		OlderThanDays *int `toml:"older_than_days"`
	} `toml:"archive"`
	Next struct {
		Lanes []string `toml:"lanes"`
	} `toml:"next"`
	Labels struct {
		Required *bool `toml:"required"`
	} `toml:"labels"`
	Revisit struct {
		StaleDays *int `toml:"stale_days"`
	} `toml:"revisit"`
	Review struct {
		StaleAfterDays *int `toml:"stale_after_days"`
	} `toml:"review"`
	Lint struct {
		ArchiveDone       *int     `toml:"archive_done"`
		IgnoreCodes       []string `toml:"ignore_codes"`
		ProvenanceMarkers []string `toml:"provenance_markers"`
	} `toml:"lint"`
	// Due is the [due] section: which lanes a due date says nothing in. A slice
	// (not a *bool) because the answer is board vocabulary, not a switch — the
	// done lane is skipped structurally, and this names the PARKED lanes on top of
	// it (default: icebox).
	Due struct {
		IgnoreLanes []string `toml:"ignore_lanes"`
	} `toml:"due"`
	// Alias is the board-level [alias] table: name -> a command string that
	// `furrow <name> …` expands to, git-style. A map decodes any [alias] key.
	Alias map[string]string `toml:"alias"`
	// Standalone marks a local single-machine board (no remote / no `furrow
	// sync` / no CI). A pointer so "absent" (the shared-board default) is
	// distinguishable; any bool value is accepted (clamp-don't-reject — a bool
	// has no out-of-range value).
	Standalone *bool `toml:"standalone"`
	// DefaultRepo is the board's OWN repo scope: the owner/repo that `add`
	// attaches and reads filter by when discovery supplied none (a local
	// `.furrow`, or FURROW_DIR). Stored verbatim — shape validation lives in the
	// app layer, which owns core.IsRepoShaped, exactly like [lint].ignore_codes
	// defers the code vocabulary. config stays core-free.
	DefaultRepo string `toml:"default_repo"`
}

// Load reads config.toml at path and returns the effective config plus any
// clamp warnings (each a human-readable string). A missing file is not an
// error: it returns Default() with no warnings. A malformed file IS an error
// (the user wrote broken TOML — that is worth stopping for). An UNKNOWN key is
// neither: it is ignored with a warning naming the file, line, and key.
func Load(path string) (*Config, []string, error) {
	// #nosec G304 -- path is furrow's own config location, resolved by the
	// app layer from the store dir / XDG (never attacker-supplied).
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return LoadBytes(data, path)
}

// LoadBytes is Load over in-memory content — the validation half of `config
// set`, which must know what a document WOULD mean before writing it.
func LoadBytes(data []byte, path string) (*Config, []string, error) {
	var r raw
	unknown, err := decodeSalvaging(data, &r, path)
	if err != nil {
		return nil, nil, fmt.Errorf("config.toml: %w", err)
	}
	c, warn, err := fromRaw(r)
	if err != nil {
		return nil, nil, err
	}
	return c, append(unknown, warn...), nil
}

// decodeSalvaging is decodeStrict with the clamp-don't-reject policy applied to
// WRONG-TYPED values too: one `step = "10"` used to make every board command —
// including `furrow board` (the diagnosis) and `furrow config set` (the repair)
// — exit 2, relaying go-toml's struct-tag prose, while the user-level loader
// salvaged the same class of damage. A wrong-typed KEY now falls back to its
// default with a warning naming file:line and the dotted key (symmetric with
// the unknown-key path), and everything else keeps its written value.
//
// Mechanics: on a type error, blank out exactly the offending LINE (keeping the
// newline, so every later warning still carries the ORIGINAL line number) and
// decode again. A document whose damage cannot be isolated that way — a type
// error without a position, or one inside a multi-line value whose blanked
// first line breaks the TOML syntax — falls back to the original hard error:
// salvage must never guess. Malformed TOML stays fatal as before.
func decodeSalvaging(data []byte, v any, path string) ([]string, error) {
	work := append([]byte(nil), data...)
	var typeWarn []string
	// One iteration per bad key; the bound only stops a pathological document.
	for range 128 {
		unknown, err := decodeStrict(work, v, path)
		if err == nil {
			return append(typeWarn, unknown...), nil
		}
		var de *toml.DecodeError
		if !errors.As(err, &de) {
			return nil, err // malformed TOML, not a value of the wrong type
		}
		row, _ := de.Position()
		key := strings.Join(de.Key(), ".")
		lines := bytes.Split(work, []byte("\n"))
		if key == "" || row < 1 || row > len(lines) {
			return nil, err // cannot isolate the damage — fail honestly
		}
		blanked := append([]byte(nil), lines[row-1]...)
		lines[row-1] = nil
		next := bytes.Join(lines, []byte("\n"))
		if bytes.Equal(next, work) {
			return nil, err // no progress (already-blank line) — fail, don't loop
		}
		work = next
		typeWarn = append(typeWarn, fmt.Sprintf("%s:%d: key %q has the wrong type (%s); using the default", path, row, key, strings.TrimSpace(string(blanked))))
	}
	return nil, fmt.Errorf("%s: too many wrong-typed values to salvage", path)
}

// decodeStrict decodes data into v with unknown keys DISALLOWED, then converts
// the unknown-key hits back into clamp warnings ("path:line: unknown key ...")
// and reports every other decode failure as the error it is. On a pure
// unknown-key miss v is still fully populated — go-toml collects the misses and
// finishes the decode — so the caller can proceed exactly as if the keys were
// absent, which is what clamp-don't-reject promises.
func decodeStrict(data []byte, v any, path string) ([]string, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		return nil, nil
	}
	var missing *toml.StrictMissingError
	if !errors.As(err, &missing) {
		return nil, err
	}
	warn := make([]string, 0, len(missing.Errors))
	for i := range missing.Errors {
		e := &missing.Errors[i]
		row, _ := e.Position()
		warn = append(warn, fmt.Sprintf("%s:%d: unknown key %q; ignored", path, row, strings.Join(e.Key(), ".")))
	}
	return warn, nil
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

	// [due].ignore_lanes: the lanes where a due date raises no signal, kept to
	// real lanes exactly like terminal. An EXPLICIT empty list is honored (it
	// means "nag everywhere"), which is why the nil check is on the raw slice —
	// absent falls back to the default parked lane.
	if r.Due.IgnoreLanes != nil {
		var keep []string
		for _, l := range r.Due.IgnoreLanes {
			if contains(c.Lanes, l) {
				keep = append(keep, l)
			} else {
				warn = append(warn, fmt.Sprintf("due.ignore_lanes entry %q is not a lane; ignored", l))
			}
		}
		c.DueIgnoreLanes = setOf(keep)
	} else {
		var keep []string
		for _, l := range DefaultDueIgnoreLanes {
			if contains(c.Lanes, l) {
				keep = append(keep, l)
			}
		}
		c.DueIgnoreLanes = setOf(keep)
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
	if r.IDs.EpicPrefix != "" {
		c.EpicIDPrefix = r.IDs.EpicPrefix
	}
	// The two prefixes must differ, or ids stop naming their entity kind: epics
	// share the task bodies/ directory, so a collision would make bodies/<id>.md
	// ambiguous and the orphan-body lint unable to say which store owns a file.
	// Clamp rather than reject, like every other config value.
	if c.EpicIDPrefix == c.IDPrefix {
		warn = append(warn, fmt.Sprintf("ids.epic_prefix %q equals ids.prefix; using %q", c.EpicIDPrefix, DefaultEpicIDPrefix))
		c.EpicIDPrefix = DefaultEpicIDPrefix
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

	// [review].stale_after_days: a "days" knob like revisit.stale_days — 0 is
	// valid (it disables the per-repo unreviewed nudge); only a negative value
	// clamps to the default.
	if r.Review.StaleAfterDays != nil {
		if *r.Review.StaleAfterDays < 0 {
			warn = append(warn, fmt.Sprintf("review.stale_after_days %d < 0; using %d", *r.Review.StaleAfterDays, DefaultReviewStaleAfterDays))
		} else {
			c.ReviewStaleAfterDays = *r.Review.StaleAfterDays
		}
	}

	// [lint].archive_done: a count knob — 0 (default) disables the archive nudge;
	// a negative value clamps to 0 (disabled) with a warning.
	if r.Lint.ArchiveDone != nil {
		if *r.Lint.ArchiveDone < 0 {
			warn = append(warn, fmt.Sprintf("lint.archive_done %d < 0; disabling (0)", *r.Lint.ArchiveDone))
		} else {
			c.LintArchiveDone = *r.Lint.ArchiveDone
		}
	}

	// [lint].ignore_codes: lint codes to suppress everywhere `furrow lint` runs.
	// Trimmed + deduped only — config stays core-free, so it cannot know the code
	// vocabulary; an entry naming no real code is a harmless no-op that app.Lint
	// warns about (clamp-don't-reject, deferred to the layer that knows the codes).
	if len(r.Lint.IgnoreCodes) > 0 {
		var codes []string
		for _, code := range r.Lint.IgnoreCodes {
			if c := strings.TrimSpace(code); c != "" {
				codes = append(codes, c)
			}
		}
		c.LintIgnoreCodes = dedupeNonEmpty(codes)
	}

	// [lint].provenance_markers: the board's own provenance vocabulary. Trimmed +
	// deduped like ignore_codes; empty (the default) keeps the check off, so a
	// board that never opts in sees no new warning.
	if len(r.Lint.ProvenanceMarkers) > 0 {
		var markers []string
		for _, m := range r.Lint.ProvenanceMarkers {
			if t := strings.TrimSpace(m); t != "" {
				markers = append(markers, t)
			}
		}
		c.LintProvenanceMarkers = dedupeNonEmpty(markers)
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

	// standalone: absent -> false (shared board, the default). A bool has no
	// out-of-range value, so there is nothing to clamp or warn about.
	if r.Standalone != nil {
		c.Standalone = *r.Standalone
	}

	// default_repo: trimmed and stored verbatim. Whether it is owner/repo-shaped
	// is the app layer's call (core.IsRepoShaped lives there, and config is
	// core-free); an unusable value is clamped away with a warning THERE, on the
	// same clamp-don't-reject terms as every value clamped here.
	c.DefaultRepo = strings.TrimSpace(r.DefaultRepo)

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
