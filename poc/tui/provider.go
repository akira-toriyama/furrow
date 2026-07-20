package main

// Provider is the seam between the UI and the task store. The POC ships only
// mockProvider (an in-memory board built from fixture.go), but every mutation
// the UI performs goes through this interface, so a real implementation
// shelling out to `furrow --json` / `furrow set --before` would drop in without
// the UI changing.
//
// Deliberately absent: anything that reads or writes a real .furrow store. This
// POC is read-only with respect to the user's actual data.
type Provider interface {
	// Board returns the current board. The UI treats it as read-mostly and
	// re-reads it after every mutation.
	Board() *Board

	// Move places id into lane at post-removal insertion index idx (see
	// AdjustDropIndex). It returns the ids a lane respace renumbered, which a
	// real client would surface from furrow's `renumbered` envelope key.
	Move(id, lane string, idx int) (renumbered []string, err error)

	// Done closes a task into the done lane.
	Done(id string) error

	// ToggleCheck flips one checklist item.
	ToggleCheck(id string, i int) error

	// SetBody replaces a task's prose and stamps Updated.
	SetBody(id, body string) error

	// Reload re-reads the store. The mock rebuilds from the fixture, which is
	// how `r` discards the session's mutations.
	Reload() error
}

// mockProvider holds the fixture in memory. Nothing here touches a filesystem.
type mockProvider struct{ b *Board }

// newMockProvider builds a provider over the hardcoded fixture.
func newMockProvider() *mockProvider { return &mockProvider{b: NewBoard(fixtureTasks())} }

func (p *mockProvider) Board() *Board { return p.b }

func (p *mockProvider) Move(id, lane string, idx int) ([]string, error) {
	return p.b.MoveTo(id, lane, idx)
}

func (p *mockProvider) Done(id string) error { return p.b.Close(id) }

func (p *mockProvider) ToggleCheck(id string, i int) error { return p.b.ToggleCheck(id, i) }

func (p *mockProvider) SetBody(id, body string) error { return p.b.SetBody(id, body) }

func (p *mockProvider) Reload() error { p.b = NewBoard(fixtureTasks()); return nil }

var _ Provider = (*mockProvider)(nil)
