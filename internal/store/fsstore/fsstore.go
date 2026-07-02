// Package fsstore is the ONLY package that touches the filesystem for furrow's
// store. Everything else reaches it through the core.Store interface. Keeping
// os/filepath confined here is what lets the rest of the code be tested without
// a disk (see internal/store/memstore).
package fsstore

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Store reads and writes a .furrow directory laid out as per-task shards:
// tasks/<id>.json (one metadata file per task), bodies/<id>.md (its prose), and
// meta.json (the one board-wide schema version). There is NO index.json — that
// monolithic array was abolished so two operators adding tasks on separate
// worktrees touch disjoint files and never git-conflict. The Store is
// constructed with the few config-derived values it needs (lane order for the
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

func (s *Store) tasksDir() string          { return filepath.Join(s.root, "tasks") }
func (s *Store) taskPath(id string) string { return filepath.Join(s.tasksDir(), id+".json") }
func (s *Store) metaPath() string          { return filepath.Join(s.root, "meta.json") }
func (s *Store) bodiesDir() string         { return filepath.Join(s.root, "bodies") }
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

// Load folds every tasks/<id>.json shard into one in-memory Index (with the
// schema version from meta.json). A missing tasks/ is not an error — a fresh
// .furrow returns an empty, well-formed Index so `furrow add` works day one.
// Shards are read in filename order (== id order), which is deterministic; the
// app canonicalizes into display order (lane->priority->id) afterward. The fold
// keeps every shard as a separate array entry, so a duplicate id introduced by a
// hand-edit surfaces to `furrow lint` rather than being silently merged.
func (s *Store) Load() (*core.Index, error) {
	ver := s.metaVersion()
	if err := core.CheckSchemaVersion(ver); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.tasksDir())
	if os.IsNotExist(err) {
		return &core.Index{SchemaVersion: ver, Tasks: []core.Task{}}, nil
	}
	if err != nil {
		return nil, core.Internalf("index", "read tasks/: %v", err)
	}
	tasks := make([]core.Task, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.tasksDir(), e.Name()))
		if err != nil {
			return nil, core.Internalf("index", "read shard %s: %v", e.Name(), err)
		}
		t, err := core.UnmarshalTask(b)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return &core.Index{SchemaVersion: ver, Tasks: tasks}, nil
}

// metaVersion returns meta.json's schema version, defaulting to the current
// SchemaVersion when meta.json is absent or unreadable (a fresh store, or one
// written before meta.json existed). The version is informational — the shards
// are the data — so a missing/garbled meta must not fail a Load.
func (s *Store) metaVersion() int {
	b, err := os.ReadFile(s.metaPath())
	if err != nil {
		return core.SchemaVersion
	}
	m, err := core.UnmarshalMeta(b)
	if err != nil {
		return core.SchemaVersion
	}
	return m.SchemaVersion
}

// Save splits the index into per-task shards under tasks/, writes meta.json, and
// deletes the shards of any ids no longer present. Two properties matter:
//   - Determinism/no churn: each shard is serialized via the single
//     core.MarshalTask path and written ONLY when its bytes differ from disk, so
//     re-saving an untouched board rewrites nothing (zero git churn) and meta.json
//     — constant content — is written once and then skipped.
//   - Atomicity: every file is written tmp+rename, so a crash never leaves a
//     half-written shard. A single-task change (the common case) is one shard =
//     fully atomic; a bulk change is per-shard atomic (each shard independently
//     valid and the operation is safely re-runnable).
//
// index.json is never read or written — the abolished monolith stays abolished.
func (s *Store) Save(idx *core.Index) error {
	// Version gate on the write side too: never let this binary rewrite (and
	// silently strip fields from) a board written by a newer furrow.
	if err := core.CheckSchemaVersion(s.metaVersion()); err != nil {
		return err
	}
	if err := os.MkdirAll(s.tasksDir(), 0o755); err != nil {
		return core.Internalf("index", "create tasks/: %v", err)
	}

	// meta.json carries the version of the layout furrow writes (always current),
	// held in one file so it never becomes a per-shard merge point.
	metaB, err := core.MarshalMeta(&core.Meta{SchemaVersion: core.SchemaVersion})
	if err != nil {
		return err
	}
	if err := s.writeIfChanged(s.metaPath(), metaB); err != nil {
		return err
	}

	// One shard per task, written only when it differs from disk.
	want := make(map[string]bool, len(idx.Tasks))
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		want[t.ID] = true
		data, err := core.MarshalTask(t)
		if err != nil {
			return err
		}
		if err := s.writeIfChanged(s.taskPath(t.ID), data); err != nil {
			return err
		}
	}

	// Drop the shards of ids that left the index (done->archive, etc.).
	existing, err := s.ListTaskIDs()
	if err != nil {
		return err
	}
	for _, id := range existing {
		if !want[id] {
			if err := os.Remove(s.taskPath(id)); err != nil && !os.IsNotExist(err) {
				return core.Internalf(id, "delete stale shard: %v", err)
			}
		}
	}
	return nil
}

// writeIfChanged writes data atomically only when it differs from what is on
// disk. Comparing bytes first is what makes a no-op Save produce zero git churn:
// an unchanged shard keeps its exact contents and mtime.
func (s *Store) writeIfChanged(path string, data []byte) error {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return nil
	}
	return s.atomicWrite(path, data)
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
// tasks<->body 1:1 lint check.
func (s *Store) ListBodyIDs() ([]string, error) {
	return s.listStems(s.bodiesDir(), ".md")
}

// ListTaskIDs returns the id of every tasks/<id>.json shard, sorted. lint uses
// it (paired with ListBodyIDs) to check the tasks/<->bodies/ 1:1 mapping and to
// confirm each shard's filename matches the id it carries — both by pure
// directory enumeration, no parse.
func (s *Store) ListTaskIDs() ([]string, error) {
	return s.listStems(s.tasksDir(), ".json")
}

// listStems returns the sorted filename stems (name minus ext) of the files in
// dir with the given extension. A missing dir yields no ids and no error.
func (s *Store) listStems(dir, ext string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, core.Internalf(filepath.Base(dir), "read %s: %v", filepath.Base(dir), err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ext))
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
