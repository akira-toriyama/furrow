// Package fsstore is the ONLY package that touches the filesystem for furrow's
// store. Everything else reaches it through the core.Store interface. Keeping
// os/filepath confined here is what lets the rest of the code be tested without
// a disk (see internal/store/memstore).
package fsstore

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Store reads and writes a .furrow directory: index.json and bodies/<id>.md. It
// is constructed with the few config-derived values it needs (lane order for the
// marshaller's sort, id prefix/length for NextID) so it never imports config.
type Store struct {
	root      string // absolute path to the .furrow directory
	laneOrder []string
	idPrefix  string
	idLen     int
}

// compile-time proof fsstore satisfies the port.
var _ core.Store = (*Store)(nil)

// New builds a Store rooted at the given .furrow directory.
func New(root string, laneOrder []string, idPrefix string, idLen int) *Store {
	return &Store{root: root, laneOrder: laneOrder, idPrefix: idPrefix, idLen: idLen}
}

// Root returns the .furrow directory path (handy for the CLI to print paths).
func (s *Store) Root() string { return s.root }

func (s *Store) indexPath() string { return filepath.Join(s.root, "index.json") }
func (s *Store) bodiesDir() string { return filepath.Join(s.root, "bodies") }
func (s *Store) bodyPath(id string) string {
	return filepath.Join(s.bodiesDir(), id+".md")
}

// BodyFile returns the absolute path of bodies/<id>.md for the CLI to hand to
// $EDITOR. It does not create the file.
func (s *Store) BodyFile(id string) string {
	if abs, err := filepath.Abs(s.bodyPath(id)); err == nil {
		return abs
	}
	return s.bodyPath(id)
}

// Load reads and parses index.json. A missing index is not an error — a fresh
// .furrow returns an empty, well-formed Index so `furrow add` works day one.
func (s *Store) Load() (*core.Index, error) {
	b, err := os.ReadFile(s.indexPath())
	if os.IsNotExist(err) {
		return &core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{}}, nil
	}
	if err != nil {
		return nil, core.Internalf("index", "read index.json: %v", err)
	}
	return core.Unmarshal(b)
}

// Save serializes via the single core.Marshal path, then writes index.json
// atomically (tmp file in the same dir + rename) so a crash never leaves a
// half-written index.
func (s *Store) Save(idx *core.Index) error {
	data, err := core.Marshal(idx, s.laneOrder)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return core.Internalf("index", "create .furrow: %v", err)
	}
	return s.atomicWrite(s.indexPath(), data)
}

// LoadBody returns bodies/<id>.md, or "" when absent (a task may legitimately
// have an empty body until someone writes one).
func (s *Store) LoadBody(id string) (string, error) {
	b, err := os.ReadFile(s.bodyPath(id))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", core.Internalf(id, "read body: %v", err)
	}
	return string(b), nil
}

// SaveBody writes bodies/<id>.md atomically, creating bodies/ as needed.
func (s *Store) SaveBody(id, content string) error {
	if err := os.MkdirAll(s.bodiesDir(), 0o755); err != nil {
		return core.Internalf(id, "create bodies/: %v", err)
	}
	return s.atomicWrite(s.bodyPath(id), []byte(content))
}

// BodyExists reports whether bodies/<id>.md is present.
func (s *Store) BodyExists(id string) bool {
	_, err := os.Stat(s.bodyPath(id))
	return err == nil
}

// DeleteBody removes bodies/<id>.md (used by archive). Absent is not an error.
func (s *Store) DeleteBody(id string) error {
	err := os.Remove(s.bodyPath(id))
	if err != nil && !os.IsNotExist(err) {
		return core.Internalf(id, "delete body: %v", err)
	}
	return nil
}

// ListBodyIDs returns the ids of every bodies/<id>.md, sorted, for the
// index<->body 1:1 lint check.
func (s *Store) ListBodyIDs() ([]string, error) {
	entries, err := os.ReadDir(s.bodiesDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, core.Internalf("bodies", "read bodies/: %v", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(ids)
	return ids, nil
}

// NextID returns a fresh random id: the configured prefix plus a random
// Crockford-base32 suffix (idLen chars), e.g. "t-k3m9p". There is no shared
// counter, so concurrent `furrow add` from separate operators/worktrees won't
// collide; the app verifies the id isn't already in the index, and `furrow lint`
// flags any duplicate as a backstop. Existing numeric ids (t-0042) stay frozen
// and coexist.
func (s *Store) NextID() (string, error) {
	suffix, err := core.RandomIDSuffix(s.idLen, rand.Reader)
	if err != nil {
		return "", err
	}
	return s.idPrefix + suffix, nil
}

// atomicWrite writes data to a temp file in the destination directory, fsyncs,
// and renames over the target — atomic on a single filesystem.
func (s *Store) atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return core.Internalf("", "create temp in %s: %v", dir, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op once the rename succeeds
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return core.Internalf("", "write temp: %v", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return core.Internalf("", "fsync temp: %v", err)
	}
	if err := f.Close(); err != nil {
		return core.Internalf("", "close temp: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return core.Internalf("", "rename temp -> %s: %v", path, err)
	}
	return nil
}
