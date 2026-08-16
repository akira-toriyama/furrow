// Package memstore is an in-memory core.Store. It is a normal (non-test)
// package so runtime code that must not touch disk could use it as well; today
// every caller is a test. Mirrors chord's AdapterTest-as-a-real-target
// convention.
package memstore

import (
	"crypto/rand"
	"fmt"
	"sort"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Store keeps each task as its own map entry, keyed by id — the in-memory twin
// of fsstore's one-shard-per-id layout, so tests exercise the same "every task
// is an independent record" semantics (no shared array to imply ordering). The
// zero value is not ready — use New.
type Store struct {
	tasks      map[string]core.Task // id -> task, one entry per shard
	bodies     map[string]string
	assets     map[string][]byte          // basename -> bytes, the in-memory twin of bodies/assets/<name>
	repos      map[string]core.RepoRecord // owner/repo -> review record, one entry per repos/ shard
	epics      map[string]core.Epic       // id -> epic, one entry per epics/ shard
	idPrefix   string
	epicPrefix string
	idLen      int
	nextID     func() (string, error) // id generator; random by default
	// schemaVersion mirrors fsstore's meta.json so tests can exercise the
	// version gate (Load/Save refuse a board newer than the binary). Defaults
	// to the current core.SchemaVersion via New.
	schemaVersion int
}

// compile-time proof memstore satisfies the port.
var _ core.Store = (*Store)(nil)

// New returns an empty in-memory store with the given id formatting.
func New(idPrefix, epicPrefix string, idLen int) *Store {
	s := &Store{
		tasks:         map[string]core.Task{},
		bodies:        map[string]string{},
		assets:        map[string][]byte{},
		repos:         map[string]core.RepoRecord{},
		epics:         map[string]core.Epic{},
		idPrefix:      idPrefix,
		epicPrefix:    epicPrefix,
		idLen:         idLen,
		schemaVersion: core.SchemaVersion,
	}
	s.nextID = s.randomID
	return s
}

// BoardVersion returns the layout version this board declares — the in-memory
// twin of reading meta.json. Never an error here: memory cannot be garbled.
func (s *Store) BoardVersion() (int, error) { return s.schemaVersion, nil }

// LoadMeta returns the board's meta record. A memstore has no meta.json to hand-
// edit and no bytes to parse, so it can never carry an unknown key: the record is
// synthesized from the version alone and its ExtraKeys() is always empty. That is
// faithful, not a shortcut — the passthrough is a property of the FILE format, so
// the in-memory twin has nothing to preserve.
func (s *Store) LoadMeta() (*core.Meta, error) {
	return &core.Meta{SchemaVersion: s.schemaVersion}, nil
}

// SetBoardVersion raises the board's layout version. As in fsstore, this is the
// ONE deliberate raiser (`furrow upgrade`'s engine) — Save never touches it, so
// a memstore seeded to an older version behaves exactly like an outdated board
// on disk and the app layer can be tested against the real refusal.
func (s *Store) SetBoardVersion(v int) error { s.schemaVersion = v; return nil }

// Writable mirrors fsstore's predicate: may this binary write the board? (No
// fresh-store case here — New always seeds a version, so a memstore is never
// unstamped.)
func (s *Store) Writable() error { return core.CheckWritable(s.schemaVersion) }

// PruneMetaExtras is fsstore's meta prune on a store whose meta can never
// carry an unknown key (see LoadMeta): always a clean no-op.
func (s *Store) PruneMetaExtras() ([]string, error) { return nil, nil }

// gateWrite mirrors fsstore's: every mutating method refuses a board that does
// not declare this binary's exact layout, so the fake is faithful where it
// matters most — the app layer's tests exercise the SAME refusal the real store
// performs.
func (s *Store) gateWrite() error { return s.Writable() }

// Load folds the per-id task entries into one Index, in id order (deterministic;
// the app canonicalizes into display order afterward), mirroring fsstore's
// glob-and-fold. The tasks are DEEP-copied out so callers mutating the result do
// not alter the store until they Save — including through the slice and pointer
// fields. A struct copy alone left every slice sharing its backing array with
// the store, so the routine Load -> Canonicalize (whose sortDedup sorts labels
// IN PLACE) rewrote the store without any Save: the test double was making a
// weaker isolation promise than fsstore, whose Load parses fresh bytes.
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
		tasks = append(tasks, cloneTask(s.tasks[id]))
	}
	return &core.Index{SchemaVersion: s.schemaVersion, Tasks: tasks}, nil
}

// cloneTask deep-copies a task's slice and pointer fields (the struct copy
// covers the scalars). Nil-ness is preserved — a clone that turned [] into nil
// would change what tests observe. The unexported extras carrier is shared by
// the struct copy, which is fine: nothing outside core can reach into it.
func cloneTask(t core.Task) core.Task {
	t.Labels = cloneStrings(t.Labels)
	t.Repos = cloneStrings(t.Repos)
	t.Deps = cloneStrings(t.Deps)
	t.Refs = cloneStrings(t.Refs)
	if t.Checklist != nil {
		t.Checklist = append(make([]core.ChecklistItem, 0, len(t.Checklist)), t.Checklist...)
	}
	t.Value = cloneInt(t.Value)
	t.Effort = cloneInt(t.Effort)
	t.Closed = cloneTime(t.Closed)
	t.Reviewed = cloneTime(t.Reviewed)
	t.Due = cloneTime(t.Due)
	return t
}

// cloneEpic is cloneTask's box twin (slices + the Meta map).
func cloneEpic(e core.Epic) core.Epic {
	e.Labels = cloneStrings(e.Labels)
	e.Repos = cloneStrings(e.Repos)
	e.Deps = cloneStrings(e.Deps)
	if e.Meta != nil {
		m := make(map[string]string, len(e.Meta))
		for k, v := range e.Meta {
			m[k] = v
		}
		e.Meta = m
	}
	e.Closed = cloneTime(e.Closed)
	return e
}

func cloneStrings(ss []string) []string {
	if ss == nil {
		return nil
	}
	return append(make([]string, 0, len(ss)), ss...)
}

func cloneInt(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneTime(p *time.Time) *time.Time {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Save replaces the task set from idx: every task becomes its own entry and any
// id no longer present is dropped — the in-memory twin of writing one shard per
// task and deleting the shards of removed ids.
//
// Each task round-trips through the single core.MarshalTask/UnmarshalTask path
// (as SaveRepo and SaveEpic do), which buys the two things a struct copy did
// not:
//
//   - the STORED task is canonicalized (labels/repos/deps sorted+deduped,
//     nil slices -> [], timestamps UTC whole-second, value/effort clamped), so a
//     test that saves a messy task reads back the same shape fsstore would have
//     persisted instead of the messy one;
//   - the CALLER's index is canonicalized IN PLACE, because canonicalizeTask
//     mutates through the pointer — exactly fsstore.Save's side effect, which
//     comes from marshalling each &idx.Tasks[i] on its way to a shard. Hence the
//     index-based loop: `for _, t := range` would normalize a copy and leave the
//     caller holding the pre-Save shape.
//
// Without it the double promised LESS than fsstore delivers, and app code grew
// point-fixes to paper over the gap. Re-parsing also isolates the stored task in
// both directions (fsstore serializes, so it is isolated too): a caller that
// keeps mutating idx after Save cannot reach the store through a shared backing
// array. The bytes are the same ones fsstore would write, extras included.
func (s *Store) Save(idx *core.Index) error {
	// The write gate, same as fsstore: write only a board that already declares
	// this binary's layout — never raise it as a side effect.
	if err := s.gateWrite(); err != nil {
		return err
	}
	next := make(map[string]core.Task, len(idx.Tasks))
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		data, err := core.MarshalTask(t)
		if err != nil {
			return err
		}
		norm, err := core.UnmarshalTask(data)
		if err != nil {
			return err
		}
		next[norm.ID] = *norm
	}
	s.tasks = next
	return nil
}

// LoadRepo returns a copy of the repo review record, or ok=false when absent —
// the in-memory twin of reading repos/<owner>__<repo>.json.
func (s *Store) LoadRepo(repo string) (*core.RepoRecord, bool, error) {
	rec, ok := s.repos[repo]
	if !ok {
		return nil, false, nil
	}
	rec = cloneRepo(rec)
	return &rec, true, nil
}

// cloneRepo is cloneTask's review-record twin: the struct copy covers Repo, so
// only the two clocks need deep-copying. Every read path returns one — a caller
// that advances a returned *time.Time must not reach the stored record, which is
// exactly what fsstore's parse-fresh-bytes read gives for free.
func cloneRepo(rec core.RepoRecord) core.RepoRecord {
	rec.LastReviewed = cloneTime(rec.LastReviewed)
	rec.LastAgentReviewed = cloneTime(rec.LastAgentReviewed)
	return rec
}

// SaveRepo stores one repo review record — the in-memory twin of writing a
// repos/ shard. The record is canonicalized through the single MarshalRepo path
// (then re-parsed) so the in-memory copy matches what fsstore would persist.
func (s *Store) SaveRepo(rec *core.RepoRecord) error {
	if err := s.gateWrite(); err != nil {
		return err
	}

	data, err := core.MarshalRepo(rec)
	if err != nil {
		return err
	}
	norm, err := core.UnmarshalRepo(data)
	if err != nil {
		return err
	}
	s.repos[norm.Repo] = *norm
	return nil
}

// ListRepos returns every repo review record, sorted by Repo — the in-memory
// twin of listing repos/. An empty store yields nil (never reviewed). Each
// record is DEEP-copied out (cloneRepo), like LoadRepo's: a plain struct copy
// carries the two *time.Time fields, so a caller advancing a returned clock
// rewrote the store with no SaveRepo.
func (s *Store) ListRepos() ([]core.RepoRecord, error) {
	if len(s.repos) == 0 {
		return nil, nil
	}
	recs := make([]core.RepoRecord, 0, len(s.repos))
	for _, rec := range s.repos {
		recs = append(recs, cloneRepo(rec))
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Repo < recs[j].Repo })
	return recs, nil
}

// LoadEpic returns a copy of the epic, or ok=false when absent — the in-memory
// twin of reading epics/<id>.json.
func (s *Store) LoadEpic(id string) (*core.Epic, bool, error) {
	e, ok := s.epics[id]
	if !ok {
		return nil, false, nil
	}
	e = cloneEpic(e)
	return &e, true, nil
}

// LoadEpics returns every epic, sorted by ID — the in-memory twin of listing
// epics/. An empty store yields nil (a board with no boxes declared).
func (s *Store) LoadEpics() ([]core.Epic, error) {
	if len(s.epics) == 0 {
		return nil, nil
	}
	epics := make([]core.Epic, 0, len(s.epics))
	for _, e := range s.epics {
		epics = append(epics, cloneEpic(e))
	}
	sort.Slice(epics, func(i, j int) bool { return epics[i].ID < epics[j].ID })
	return epics, nil
}

// SaveEpic stores one epic — the in-memory twin of writing an epics/ shard. The
// record round-trips through the single MarshalEpic/UnmarshalEpic path (not just
// assigned) so the in-memory copy is canonicalized exactly as fsstore would
// persist it: a test that saves an epic with unsorted labels must read back the
// sorted ones, or memstore-backed tests would pass on shapes the disk rejects.
func (s *Store) SaveEpic(e *core.Epic) error {
	if err := s.gateWrite(); err != nil {
		return err
	}
	data, err := core.MarshalEpic(e)
	if err != nil {
		return err
	}
	norm, err := core.UnmarshalEpic(data)
	if err != nil {
		return err
	}
	s.epics[norm.ID] = *norm
	return nil
}

// ListEpicIDs returns every epic id, sorted — the in-memory twin of listing
// epics/<id>.json filenames. Identical to the ids in LoadEpics here (memory has
// no filename that can disagree with its content), which is exactly why the
// integrity lint it feeds can only ever fire against fsstore.
func (s *Store) ListEpicIDs() ([]string, error) {
	if len(s.epics) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(s.epics))
	for id := range s.epics {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// NextEpicID returns a fresh random epic id — the epic prefix plus the same
// random suffix NextID uses.
func (s *Store) NextEpicID() (string, error) {
	suffix, err := core.RandomIDSuffix(s.idLen, rand.Reader)
	if err != nil {
		return "", err
	}
	return s.epicPrefix + suffix, nil
}

func (s *Store) LoadBody(id string) (string, error) { return s.bodies[id], nil }

func (s *Store) SaveBody(id, content string) error {
	if err := s.gateWrite(); err != nil {
		return err
	}

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
	if err := s.gateWrite(); err != nil {
		return "", err
	}

	base := id + "-" + core.SanitizeAssetName(srcName)
	name := core.NextAssetName(base, func(cand string) bool {
		_, ok := s.assets[cand]
		return ok
	})
	s.assets[name] = append([]byte(nil), data...)
	return name, nil
}

// ListAssets returns every stored asset as name+size, sorted by name — the
// in-memory twin of fsstore reading bodies/assets/. Size is the byte length of
// the stored data. An empty store yields nil (no assets), matching fsstore's
// missing-dir behavior.
func (s *Store) ListAssets() ([]core.AssetInfo, error) {
	if len(s.assets) == 0 {
		return nil, nil
	}
	assets := make([]core.AssetInfo, 0, len(s.assets))
	for name, data := range s.assets {
		assets = append(assets, core.AssetInfo{Name: name, Size: int64(len(data))})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	return assets, nil
}

// LoadAsset returns a copy of the stored asset's bytes, or a NotFound error when
// absent — the in-memory twin of fsstore reading bodies/assets/<name>.
func (s *Store) LoadAsset(name string) ([]byte, error) {
	data, ok := s.assets[name]
	if !ok {
		return nil, core.NotFound(name)
	}
	return append([]byte(nil), data...), nil
}

// DeleteAsset removes the stored asset; absent is not an error (mirrors fsstore).
func (s *Store) DeleteAsset(name string) error {
	if err := s.gateWrite(); err != nil {
		return err
	}

	delete(s.assets, name)
	return nil
}

// BodyFile returns "" — an in-memory store is not file-backed, so $EDITOR
// shell-out (the only caller) is not supported against it.
func (s *Store) BodyFile(id string) string { return "" }

func (s *Store) DeleteBody(id string) error {
	if err := s.gateWrite(); err != nil {
		return err
	}

	delete(s.bodies, id)
	return nil
}

func (s *Store) ListBodyIDs() ([]string, error) { return sortedKeys(s.bodies), nil }

// ListTaskIDs returns the ids of all task shards, sorted — the twin of
// fsstore.ListTaskIDs (in-memory the "shard filename" is just the map key, so
// it always matches the task's id).
func (s *Store) ListTaskIDs() ([]string, error) { return sortedKeys(s.tasks), nil }

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
