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
	want := "{\n  \"schema_version\": 3\n}\n"
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

// SchemaVersion is 3: the repos pivot made repos a required first-class field,
// and the flag-day bumped every board so pre-repos binaries refuse it (the
// version gate) instead of lenient-parsing repos away.
func TestSchemaVersionIsThree(t *testing.T) {
	if SchemaVersion != 3 {
		t.Errorf("SchemaVersion = %d, want 3 (repos-required shards)", SchemaVersion)
	}
}
