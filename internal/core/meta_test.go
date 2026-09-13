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
	want := "{\n  \"schema_version\": 10\n}\n"
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

// SchemaVersion is 10: the per-task `repeat` rule and its `repeat_anchor` —
// closing a repeating task generates the next occurrence. A v9 binary that
// merely PRESERVED the two fields would carry them faithfully and then close
// the task like any other: the series would stop dead, with the rule still on
// disk saying it had not. Both fields are omitempty, so no existing shard
// rewrites — the gate exists for the BEHAVIOUR, not for the bytes.
//
// The literal is deliberate (not `!= SchemaVersion`): this test's whole job is to
// make a bump impossible to do by accident, so it has to fail when the const moves
// and force the author to confirm the flag day.
func TestSchemaVersionIsTen(t *testing.T) {
	if SchemaVersion != 10 {
		t.Errorf("SchemaVersion = %d, want 10 (the per-task repeat rule and anchor)", SchemaVersion)
	}
}
