package app

import (
	"reflect"
	"strings"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
)

// Vocabularies returns furrow's closed vocabularies by name — the machine
// source behind the hidden `furrow vocab` command, which
// scripts/check-docs-vocab.sh diffs the prose enumerations against. Each entry
// delegates to (or reflects over) the code that OWNS the vocabulary, so a list
// here can never be a second copy that itself drifts.
//
// The `commands` vocabulary is deliberately absent: only internal/cli can walk
// the cobra tree, so the vocab command adds it there. To register a NEW
// vocabulary, add an entry here (pointing at the owning registry, never a
// literal list) and a claim line in scripts/check-docs-vocab.sh.
func Vocabularies() map[string][]string {
	return map[string][]string{
		"config-keys":          config.TopLevelKeys(),
		"doctor-codes":         DoctorCodeList(),
		"error-kinds":          core.ErrorKindList(),
		"lint-codes":           core.LintCodeList(),
		"revisit-codes":        core.RevisitCodeList(),
		"revisit-summary-keys": revisitSummaryKeys(),
		"sync-progress-keys":   jsonKeys(reflect.TypeOf(SyncProgress{})),
		"query-qualifiers":     append([]string(nil), qualifierVocab...),
		"query-presence":       append([]string(nil), presenceVocab...),
		"query-is":             append([]string(nil), stateVocab...),
	}
}

// revisitSummaryKeys returns RevisitSummary's json keys in struct order — the
// `revisit` summary shape `furrow sync`/`brief` emit.
func revisitSummaryKeys() []string { return jsonKeys(reflect.TypeOf(RevisitSummary{})) }

// jsonKeys returns a struct's json tag names in field order. Reflection, not a
// hand-list, so a field added to the struct is in the vocabulary (and therefore
// required of the docs that enumerate the shape) automatically — the mechanism
// behind revisit-summary-keys and sync-progress-keys.
func jsonKeys(t reflect.Type) []string {
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
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
