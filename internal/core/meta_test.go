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
	want := "{\n  \"schema_version\": 8\n}\n"
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

// SchemaVersion is 8: the per-task `due` stamp — the instant a task is promised
// for. `lint` ERRORS on an overdue task (due-overdue) and `brief` leads with the
// arrived ones, so a v7 binary that merely PRESERVED the field would report a
// clean board and orient a whole session past a missed date. The field is
// omitempty, so no dateless shard rewrites — the gate exists for the reading,
// not for the bytes.
//
// The literal is deliberate (not `!= SchemaVersion`): this test's whole job is to
// make a bump impossible to do by accident, so it has to fail when the const moves
// and force the author to confirm the flag day.
func TestSchemaVersionIsEight(t *testing.T) {
	if SchemaVersion != 8 {
		t.Errorf("SchemaVersion = %d, want 8 (the per-task due stamp)", SchemaVersion)
	}
}
