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
	want := "{\n  \"schema_version\": 12\n}\n"
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

// SchemaVersion is 12: the per-epic `successor` — the box declared to open
// after this one closes. A v11 binary that merely PRESERVED the field would
// carry it faithfully and then, on `epic done`, point the session back at the
// computed previous while the human's declared plan rode along unread — and
// its lint would call the board clean. The field is omitempty, so no existing
// shard rewrites: the gate exists for the BEHAVIOUR, not for the bytes.
//
// The literal is deliberate (not `!= SchemaVersion`): this test's whole job is to
// make a bump impossible to do by accident, so it has to fail when the const moves
// and force the author to confirm the flag day.
func TestSchemaVersionIsTwelve(t *testing.T) {
	if SchemaVersion != 12 {
		t.Errorf("SchemaVersion = %d, want 12 (the per-epic successor: the box declared to open after this one closes)", SchemaVersion)
	}
}
