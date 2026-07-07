package core

import "time"

// Ports. These interfaces are the seams between the pure core and the outside
// world. They are declared HERE (in core) and implemented by adapters
// (internal/store/fsstore for the real filesystem, internal/store/memstore for
// an in-memory fake used by tests and dry-runs). The app/CLI/TUI layers depend
// on these interfaces, never on a concrete adapter — that is what keeps core
// testable without touching disk.

// Store persists the tasks (one metadata shard per id) and the per-task body
// files. Implementations own all path construction (callers never assemble
// ".furrow/tasks/<id>.json" by hand) and all atomicity guarantees (Save must
// never leave a half-written shard).
type Store interface {
	// Load folds every task shard into one Index. A fresh store returns an
	// empty, well-formed Index (SchemaVersion set, Tasks == []) rather than an
	// error, so `furrow add` works in a fresh repo.
	Load() (*Index, error)
	// Save writes each task as its own shard (via core.MarshalTask), writes the
	// board-wide meta, and deletes shards for ids no longer present — all
	// atomically and writing only what changed.
	Save(idx *Index) error

	// LoadBody returns the markdown body for id, or "" if the file is absent.
	LoadBody(id string) (string, error)
	// SaveBody writes the markdown body for id (creating bodies/ as needed).
	SaveBody(id, content string) error
	// BodyExists reports whether bodies/<id>.md is present.
	BodyExists(id string) bool
	// ListBodyIDs returns the ids of all bodies/<id>.md files, for the
	// tasks<->body 1:1 lint check.
	ListBodyIDs() ([]string, error)
	// ListTaskIDs returns the ids of all task shards (tasks/<id>.json), for the
	// tasks<->body 1:1 lint check and shard filename/id integrity.
	ListTaskIDs() ([]string, error)

	// SaveAsset copies data into the task's asset area (bodies/assets/<id>-<name>),
	// choosing a collision-free basename (via NextAssetName) so an existing asset
	// is never overwritten, and returns the final basename it stored. srcName is
	// the caller's file name; the store sanitizes it (SanitizeAssetName) and
	// prefixes the id. The write is atomic. This is the store half of `furrow
	// attach`; the body's markdown reference is added by the app layer.
	SaveAsset(id, srcName string, data []byte) (name string, err error)

	// NextID returns a fresh, random, collision-resistant id (e.g. "t-k3m9p":
	// prefix + a random Crockford-base32 suffix). There is no shared counter, so
	// concurrent adds in separate worktrees won't collide. The caller (app) is
	// responsible for ensuring the id is not already present in the index.
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
