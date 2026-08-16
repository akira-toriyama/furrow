package cli

import (
	"fmt"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
)

// incomingLine: grouped by kind in classifier order, moves carry from→to,
// refiled maps "" to the word unfiled, and nothing incoming renders nothing.
func TestIncomingLineRendering(t *testing.T) {
	if got := incomingLine(nil); got != "" {
		t.Errorf("empty incomingLine = %q, want \"\"", got)
	}

	got := incomingLine([]app.IncomingChange{
		{ID: "t-3", Kind: "moved", From: "backlog", To: "ready"},
		{ID: "t-1", Kind: "created"},
		{ID: "t-4", Kind: "refiled", From: "", To: "e-9"},
		{ID: "t-2", Kind: "created"},
	})
	want := "incoming: 2 created (t-1, t-2), 1 moved (t-3 backlog→ready), 1 refiled (t-4 unfiled→e-9)"
	if got != want {
		t.Errorf("incomingLine = %q, want %q", got, want)
	}
}

// A CI-heavy pull must stay one legible line: at most three ids are named per
// kind, with an exact +N more remainder.
func TestIncomingLineCapsNamedIDs(t *testing.T) {
	var changes []app.IncomingChange
	for i := 0; i < 5; i++ {
		changes = append(changes, app.IncomingChange{ID: fmt.Sprintf("t-%d", i), Kind: "closed"})
	}
	want := "incoming: 5 closed (t-0, t-1, t-2, +2 more)"
	if got := incomingLine(changes); got != want {
		t.Errorf("incomingLine = %q, want %q", got, want)
	}
}
