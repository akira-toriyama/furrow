// Package memstore is an in-memory core.Store. It is a normal (non-test)
// package so both tests and runtime code (e.g. `migrate --dry-run`, which must
// not touch disk) can use it. Mirrors chord's AdapterTest-as-a-real-target
// convention.
package memstore

import (
	"crypto/rand"
	"fmt"
	"sort"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Store keeps each task as its own map entry, keyed by id — the in-memory twin
// of fsstore's one-shard-per-id layout, so tests exercise the same "every task
// is an independent record" semantics (no shared array to imply ordering). The
// zero value is not ready — use New.
type Store struct {
	tasks    map[string]core.Task // id -> task, one entry per shard
	bodies   map[string]string
	assets   map[string][]byte // basename -> bytes, the in-memory twin of bodies/assets/<name>
	idPrefix string
	idLen    int
	nextID   func() (string, error) // id generator; random by default
	// schemaVersion mirrors fsstore's meta.json so tests can exercise the
	// version gate (Load/Save refuse a board newer than the binary). Defaults
	// to the current core.SchemaVersion via New.
	schemaVersion int
}

// compile-time proof memstore satisfies the port.
var _ core.Store = (*Store)(nil)

// New returns an empty in-memory store with the given id formatting.
func New(idPrefix string, idLen int) *Store {
	s := &Store{
		tasks:         map[string]core.Task{},
		bodies:        map[string]string{},
		assets:        map[string][]byte{},
		idPrefix:      idPrefix,
		idLen:         idLen,
		schemaVersion: core.SchemaVersion,
	}
	s.nextID = s.randomID
	return s
}

// SetSchemaVersion overrides the board's layout version — the in-memory twin of
// hand-editing meta.json. Tests use it to exercise the version gate.
func (s *Store) SetSchemaVersion(v int) { s.schemaVersion = v }

// Load folds the per-id task entries into one Index, in id order (deterministic;
// the app canonicalizes into display order afterward), mirroring fsstore's
// glob-and-fold. The tasks are copied out so callers mutating the result do not
// alter the store until they Save.
func (s *Store) Load() (*core.Index, error) {
	if err := core.CheckSchemaVersion(s.schemaVersion); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(s.tasks))
	for id := range s.tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	tasks := make([]core.Task, 0, len(ids))
	for _, id := range ids {
		tasks = append(tasks, s.tasks[id])
	}
	return &core.Index{SchemaVersion: s.schemaVersion, Tasks: tasks}, nil
}

// Save replaces the task set from idx: every task becomes its own entry and any
// id no longer present is dropped — the in-memory twin of writing one shard per
// task and deleting the shards of removed ids.
func (s *Store) Save(idx *core.Index) error {
	if err := core.CheckSchemaVersion(s.schemaVersion); err != nil {
		return err
	}
	next := make(map[string]core.Task, len(idx.Tasks))
	for _, t := range idx.Tasks {
		next[t.ID] = t
	}
	s.tasks = next
	return nil
}

func (s *Store) LoadBody(id string) (string, error) { return s.bodies[id], nil }

func (s *Store) SaveBody(id, content string) error {
	s.bodies[id] = content
	return nil
}

func (s *Store) BodyExists(id string) bool {
	_, ok := s.bodies[id]
	return ok
}

// SaveAsset stores data under a collision-free basename — the in-memory twin of
// fsstore copying into bodies/assets/<id>-<name>. Bytes are copied so a caller
// mutating its slice afterward cannot alter the store.
func (s *Store) SaveAsset(id, srcName string, data []byte) (string, error) {
	base := id + "-" + core.SanitizeAssetName(srcName)
	name := core.NextAssetName(base, func(cand string) bool {
		_, ok := s.assets[cand]
		return ok
	})
	s.assets[name] = append([]byte(nil), data...)
	return name, nil
}

// BodyFile returns "" — an in-memory store is not file-backed, so $EDITOR
// shell-out (the only caller) is not supported against it.
func (s *Store) BodyFile(id string) string { return "" }

func (s *Store) DeleteBody(id string) error {
	delete(s.bodies, id)
	return nil
}

func (s *Store) ListBodyIDs() ([]string, error) { return sortedKeys(s.bodies), nil }

// ListTaskIDs returns the ids of all task shards, sorted — the twin of
// fsstore.ListTaskIDs (in-memory the "shard filename" is just the map key, so
// it always matches the task's id).
func (s *Store) ListTaskIDs() ([]string, error) { return sortedKeys(s.tasks), nil }

// sortedKeys returns the sorted keys of a string-keyed map (any value type).
func sortedKeys[V any](m map[string]V) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// NextID returns a fresh id via the configured generator (random by default,
// matching fsstore). No shared counter, so it is collision-resistant; the app
// verifies uniqueness against the index.
func (s *Store) NextID() (string, error) { return s.nextID() }

func (s *Store) randomID() (string, error) {
	suffix, err := core.RandomIDSuffix(s.idLen, rand.Reader)
	if err != nil {
		return "", err
	}
	return s.idPrefix + suffix, nil
}

// SeedSequentialIDs switches NextID to deterministic, zero-padded sequential ids
// (t-00001, t-00002, …) so tests can assert on specific ids. Production never
// calls this — real stores keep the random generator.
func (s *Store) SeedSequentialIDs() {
	n := 0
	s.nextID = func() (string, error) {
		n++
		return fmt.Sprintf("%s%0*d", s.idPrefix, s.idLen, n), nil
	}
}
