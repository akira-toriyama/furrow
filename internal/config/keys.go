package config

import (
	"reflect"
	"strings"
)

// TopLevelKeys returns every top-level key the board config parser understands
// — the `[section]` tables plus the bare `mode` and `default_repo` keys — in
// declaration order. It is derived from BoardKeys (reflection over the raw
// decode struct's toml tags), so it can never disagree with what Load actually
// reads: add a section to raw and this list carries it with no second
// registration. It backs `furrow vocab config-keys`, the source
// scripts/check-docs-vocab.sh checks the documented config enumerations against
// (the 2026-07 audit found the README example, the architecture table, and
// CLAUDE.md's section list each missing sections the parser had gained).
func TopLevelKeys() []string {
	var keys []string
	for _, k := range BoardKeys() {
		name := k.Name
		if k.Section != "" {
			name, _, _ = strings.Cut(k.Section, ".")
		}
		// A section's keys are contiguous in BoardKeys, so a repeat is always the
		// previous entry.
		if len(keys) > 0 && keys[len(keys)-1] == name {
			continue
		}
		keys = append(keys, name)
	}
	return keys
}

// tomlName is a field's toml key: its tag cut at the first option, ok false
// for an untagged or `toml:"-"` field.
func tomlName(f reflect.StructField) (string, bool) {
	tag, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
	if tag == "" || tag == "-" {
		return "", false
	}
	return tag, true
}
