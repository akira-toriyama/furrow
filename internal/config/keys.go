package config

import (
	"reflect"
	"strings"
)

// TopLevelKeys returns every top-level key the board config parser understands
// — the `[section]` tables plus the bare `standalone` switch — in declaration
// order. It is derived by reflection from the raw decode struct's toml tags, so
// it can never disagree with what Load actually reads: add a section to raw and
// this list carries it with no second registration. It backs `furrow vocab
// config-keys`, the source scripts/check-docs-vocab.sh checks the documented
// config enumerations against (the 2026-07 audit found the README example, the
// architecture table, and CLAUDE.md's section list each missing sections the
// parser had gained).
func TopLevelKeys() []string {
	t := reflect.TypeOf(raw{})
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("toml")
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		if tag == "" || tag == "-" {
			continue
		}
		keys = append(keys, tag)
	}
	return keys
}
