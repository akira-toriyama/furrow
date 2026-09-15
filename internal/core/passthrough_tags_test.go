package core

import (
	"reflect"
	"strings"
	"testing"
)

// Every exported field of a persisted type must carry a json tag naming its
// key. knownNames (the passthrough) and TestShardFieldsGolden's fingerprint
// both read the TAG and skip a field without one — the condition that keeps
// the unexported extras carrier off the list — while encoding/json writes an
// untagged exported field under its Go name. So `Sprint string` with no tag
// would be written by the encoder, parked as unknown by the decoder, and
// re-emitted by the encoder: a shard carrying "Sprint" twice, with the golden
// blind to it (t-pf9j). The five types are clean today; this is for the next
// field.
//
// bite-exempt: it pins the CURRENT shape on purpose — no persisted type has an
// untagged exported field, so nothing can make it fail on the tree before it.
func TestEveryPersistedExportedFieldHasAJSONTag(t *testing.T) {
	for _, ty := range []any{Task{}, Meta{}, RepoRecord{}, Epic{}, ChecklistItem{}} {
		rt := reflect.TypeOf(ty)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if !f.IsExported() {
				continue // the extras carrier: structurally invisible to encoding/json
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" {
				t.Errorf("%s.%s is exported with no json tag: encoding/json would write it under its Go name, which knownNames and the shard golden cannot see", rt.Name(), f.Name)
			}
		}
	}
}
