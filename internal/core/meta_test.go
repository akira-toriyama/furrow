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
	want := "{\n  \"schema_version\": 11\n}\n"
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

// SchemaVersion is 11: the anchor pair — an epic's calendar day and the tasks
// whose dues follow it. A v10 binary that merely PRESERVED the two fields would
// carry them faithfully and then leave every follower's due behind when the
// box's day moved through it — `epic set --anchor` reads the pointer, `-q
// anchor:` selects on it — while reporting the board clean. Both fields are
// omitempty, so no existing shard rewrites: the gate exists for the BEHAVIOUR,
// not for the bytes.
//
// The literal is deliberate (not `!= SchemaVersion`): this test's whole job is to
// make a bump impossible to do by accident, so it has to fail when the const moves
// and force the author to confirm the flag day.
func TestSchemaVersionIsEleven(t *testing.T) {
	if SchemaVersion != 11 {
		t.Errorf("SchemaVersion = %d, want 11 (the anchor pair: a box's day and the dues that follow it)", SchemaVersion)
	}
}
