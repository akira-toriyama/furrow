package core

import "time"

// Ports. These interfaces are the seams between the pure core and the outside
// world. They are declared HERE (in core) and implemented by adapters
// (internal/store/fsstore for the real filesystem, internal/store/memstore for
// an in-memory fake used by tests and dry-runs). The app/CLI/TUI layers depend
// on these interfaces, never on a concrete adapter — that is what keeps core
// testable without touching disk.

// Store persists the index and the per-task body files. Implementations own all
// path construction (callers never assemble ".furrow/bodies/<id>.md" by hand)
// and all atomicity guarantees (Save must not leave a partial index.json).
type Store interface {
	// Load reads and parses index.json. A missing index returns an empty,
	// well-formed Index (SchemaVersion set, Tasks == []) rather than an error,
	// so `furrow add` works in a fresh repo.
	Load() (*Index, error)
	// Save serializes via core.Marshal and writes index.json atomically.
	Save(idx *Index) error

	// LoadBody returns the markdown body for id, or "" if the file is absent.
	LoadBody(id string) (string, error)
	// SaveBody writes the markdown body for id (creating bodies/ as needed).
	SaveBody(id, content string) error
	// BodyExists reports whether bodies/<id>.md is present.
	BodyExists(id string) bool
	// ListBodyIDs returns the ids of all bodies/<id>.md files, for the
	// index<->body 1:1 lint check.
	ListBodyIDs() ([]string, error)

	// NextID reserves and returns the next frozen id (e.g. "t-0043"). It is
	// monotonic and never reuses a retired id — see .furrow/seq.
	NextID() (string, error)
}

// Clock supplies the current time. Injected so tests get deterministic
// timestamps and the marshaller's UTC/whole-second contract is easy to honor.
type Clock interface {
	Now() time.Time
}

// systemClock is the production Clock. UTC with whole seconds keeps RFC3339
// output stable (no fractional component) for clean git diffs.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// SystemClock returns the real-time Clock used outside tests.
func SystemClock() Clock { return systemClock{} }
