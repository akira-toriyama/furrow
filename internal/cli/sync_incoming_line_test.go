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
		{ID: "t-6", Kind: "removed"},
		{ID: "t-5", Kind: "archived"},
	})
	want := "incoming: 2 created (t-1, t-2), 1 moved (t-3 backlog→ready), " +
		"1 refiled (t-4 unfiled→e-9), 1 archived (t-5), 1 removed (t-6)"
	if got != want {
		t.Errorf("incomingLine = %q, want %q", got, want)
	}
}

// Every kind the classifier can assign renders: the render order IS
// app.IncomingKindList(), so a kind added to the vocabulary cannot fall out of
// the human line the way `removed` did between #360 and t-31r8.
func TestIncomingLineRendersEveryKind(t *testing.T) {
	for _, kind := range app.IncomingKindList() {
		want := fmt.Sprintf("incoming: 1 %s (t-x)", kind)
		if got := incomingLine([]app.IncomingChange{{ID: "t-x", Kind: kind}}); got != want {
			t.Errorf("incomingLine(%s) = %q, want %q", kind, got, want)
		}
	}
}

// A pull whose only change is unrenderable stays quiet. Before t-31r8 a
// `removed` change built zero groups and still printed the bare prefix
// "incoming: " — a line with nothing after it.
func TestIncomingLineQuietWhenNoGroupRenders(t *testing.T) {
	if got := incomingLine([]app.IncomingChange{{ID: "t-x", Kind: "not-a-kind"}}); got != "" {
		t.Errorf("incomingLine(unknown kind) = %q, want \"\"", got)
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
