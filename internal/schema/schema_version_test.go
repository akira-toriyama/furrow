package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/schema"
)

// schemaVersionConst reports the layout version a schema document pins in
// properties.schema_version.const, and whether it declares one at all.
func schemaVersionConst(t *testing.T, name, doc string) (int, bool) {
	t.Helper()
	var parsed struct {
		Properties struct {
			SchemaVersion *struct {
				Const *int `json:"const"`
			} `json:"schema_version"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("%s schema is not valid JSON: %v", name, err)
	}
	if parsed.Properties.SchemaVersion == nil || parsed.Properties.SchemaVersion.Const == nil {
		return 0, false
	}
	return *parsed.Properties.SchemaVersion.Const, true
}

// The published schemas must pin the layout version this binary actually
// writes. Nothing else binds them: check.sh diffs each schema against its
// committed docs/schema/ copy, so two equal-but-wrong values pass, and
// check-readme-parity.sh's {"schema_version": N} sweep reads README.md and
// docs/*.md but never docs/schema/*.json. That gap shipped in v4.0.0 — the
// const said 8 while core.SchemaVersion said 9, so every meta.json furrow wrote
// was invalid against the schema furrow published.
func TestSchemaVersionConstMatchesCoreSchemaVersion(t *testing.T) {
	docs := []struct {
		name string
		doc  string
	}{
		{"task", schema.TaskV2},
		{"meta", schema.MetaV2},
		{"repo", schema.RepoV1},
		{"epic", schema.EpicV2},
	}

	declared := 0
	for _, d := range docs {
		v, ok := schemaVersionConst(t, d.name, d.doc)
		if !ok {
			continue // a shard schema carries no version; meta.json owns it
		}
		declared++
		if v != core.SchemaVersion {
			t.Errorf("%s schema pins schema_version %d, core.SchemaVersion is %d — "+
				"raise the const in internal/schema/schema.go and regenerate the committed copy "+
				"(docs/schema/), in the same change as the bump", d.name, v, core.SchemaVersion)
		}
	}

	if declared == 0 {
		t.Fatal("no published schema declares properties.schema_version.const — this guard is " +
			"watching nothing. If the key moved, follow it here rather than deleting the check")
	}
}

// Pins the reader itself: a guard that silently finds nothing would pass
// forever, which is the failure mode the declared==0 branch above exists for.
func TestSchemaVersionConstReadsTheDocument(t *testing.T) {
	v, ok := schemaVersionConst(t, "fixture", `{"properties":{"schema_version":{"const":4242}}}`)
	if !ok || v != 4242 {
		t.Errorf("got (%d, %v), want (4242, true)", v, ok)
	}

	if _, ok := schemaVersionConst(t, "fixture", `{"properties":{"schema_version":{"type":"integer"}}}`); ok {
		t.Error("a schema_version property with no const must report absent, not zero")
	}
	if _, ok := schemaVersionConst(t, "fixture", `{"properties":{}}`); ok {
		t.Error("a document with no schema_version property must report absent")
	}
}
