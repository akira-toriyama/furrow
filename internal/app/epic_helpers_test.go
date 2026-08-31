package app

import "testing"

// mustEpic creates an epic and fails the test on error — the epic twin of
// mustAdd, so a test that only needs "a box to file this under" is one line.
func mustEpic(t *testing.T, a *App, title string, o EpicAddOpts) string {
	t.Helper()
	e, err := a.EpicAdd(title, o)
	if err != nil {
		t.Fatalf("epic add %q: %v", title, err)
	}
	return e.ID
}

func mustActivate(t *testing.T, a *App, id string) {
	t.Helper()
	if _, _, _, err := a.EpicActivate(id, ""); err != nil {
		t.Fatalf("epic activate %s: %v", id, err)
	}
}
