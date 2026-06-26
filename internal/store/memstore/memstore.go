// Package memstore is an in-memory core.Store. It is a normal (non-test)
// package so both tests and runtime code (e.g. `migrate --dry-run`, which must
// not touch disk) can use it. Mirrors chord's AdapterTest-as-a-real-target
// convention.
package memstore

import (
	"crypto/rand"
	"fmt"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Store keeps the index and bodies in memory. The zero value is not ready —
// use New.
type Store struct {
	idx      *core.Index
	bodies   map[string]string
	idPrefix string
	idLen    int
	nextID   func() (string, error) // id generator; random by default
}

// compile-time proof memstore satisfies the port.
var _ core.Store = (*Store)(nil)

// New returns an empty in-memory store with the given id formatting.
func New(idPrefix string, idLen int) *Store {
	s := &Store{
		idx:      &core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{}},
		bodies:   map[string]string{},
		idPrefix: idPrefix,
		idLen:    idLen,
	}
	s.nextID = s.randomID
	return s
}

// Load returns a deep-enough copy so callers mutating the result do not alter
// the store until they Save (matches fsstore, which re-reads from disk).
func (s *Store) Load() (*core.Index, error) {
	cp := &core.Index{SchemaVersion: s.idx.SchemaVersion, Tasks: append([]core.Task(nil), s.idx.Tasks...)}
	return cp, nil
}

func (s *Store) Save(idx *core.Index) error {
	s.idx = &core.Index{SchemaVersion: idx.SchemaVersion, Tasks: append([]core.Task(nil), idx.Tasks...)}
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

// BodyFile returns "" — an in-memory store is not file-backed, so $EDITOR
// shell-out (the only caller) is not supported against it.
func (s *Store) BodyFile(id string) string { return "" }

func (s *Store) DeleteBody(id string) error {
	delete(s.bodies, id)
	return nil
}

func (s *Store) ListBodyIDs() ([]string, error) {
	ids := make([]string, 0, len(s.bodies))
	for id := range s.bodies {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
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

// Dump returns the current canonical index bytes — convenient for tests that
// want to assert on serialized output without a filesystem.
func (s *Store) Dump(laneOrder []string) string {
	b, _ := core.Marshal(s.idx, laneOrder)
	return strings.TrimRight(string(b), "\n")
}
