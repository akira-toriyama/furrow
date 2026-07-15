package cli

import (
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// TestChangedFieldsTracksType pins the review fix: the `set --json` mutation
// envelope's `changed` array must include "type" when the type actually changes.
// Otherwise a headless agent that branches on `changed` (per the integration
// contract) reads a `set --type epic` as a no-op and thinks the declaration never
// took, even though the shard was written.
func TestChangedFieldsTracksType(t *testing.T) {
	before := &core.Task{ID: "t-1"}
	after := &core.Task{ID: "t-1", Type: "epic"}

	changed := changedFields(before, after)
	found := false
	for _, c := range changed {
		if c == "type" {
			found = true
		}
	}
	if !found {
		t.Errorf(`changedFields must report "type" when it changes; got %v`, changed)
	}

	// An unchanged type must not be reported.
	if got := changedFields(after, after); len(got) != 0 {
		t.Errorf("an all-equal pair must report no changes; got %v", got)
	}
}
