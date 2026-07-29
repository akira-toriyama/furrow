package core

import "testing"

// meta.json is the one board-wide schema version, kept out of every task shard.
// MarshalMeta must share the canonical byte recipe (2-space indent, trailing
// newline) so a hand-edit equals a furrow write, exactly like the shards.
func TestMarshalMetaCanonical(t *testing.T) {
	b, err := MarshalMeta(&Meta{SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"schema_version\": 7\n}\n"
	if string(b) != want {
		t.Errorf("MarshalMeta bytes = %q, want %q", b, want)
	}

	m, err := UnmarshalMeta(b)
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != SchemaVersion {
		t.Errorf("round-trip schema_version = %d, want %d", m.SchemaVersion, SchemaVersion)
	}
}

// A malformed meta.json is a validation error (bad input), not an internal fault.
func TestUnmarshalMetaRejectsGarbage(t *testing.T) {
	if _, err := UnmarshalMeta([]byte("{ not json")); err == nil {
		t.Error("expected a validation error on malformed meta.json")
	}
}

// SchemaVersion is 7: epic-to-epic deps — the epic shard gained the required
// `deps` set (the order boxes open in). revisit's epic_dep_done and lint's
// epic-dep-* rules read it, so a v6 binary that merely PRESERVED the field
// would report a clean board while ignoring the ordering it encodes; and being
// a non-omitempty set, every epic shard rewrites on its next save — the gate
// must refuse the layout, not ignore it.
//
// The literal is deliberate (not `!= SchemaVersion`): this test's whole job is to
// make a bump impossible to do by accident, so it has to fail when the const moves
// and force the author to confirm the flag day.
func TestSchemaVersionIsSeven(t *testing.T) {
	if SchemaVersion != 7 {
		t.Errorf("SchemaVersion = %d, want 7 (epic-to-epic deps)", SchemaVersion)
	}
}
