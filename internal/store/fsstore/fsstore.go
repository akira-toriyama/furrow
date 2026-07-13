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
func (s *Store) assetsDir() string { return filepath.Join(s.bodiesDir(), "assets") }
func (s *Store) reposDir() string  { return filepath.Join(s.root, "repos") }
func (s *Store) repoPath(repo string) string {
	return filepath.Join(s.reposDir(), core.RepoStem(repo)+".json")
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
	ver, err := s.BoardVersion()
	if err != nil {
		return nil, err
	}
	if err := core.CheckSchemaVersion(ver); err != nil {
		return nil, err
	}
	if ver == 0 {
		// No meta.json (a fresh store, or one predating the file): read leniently
		// and report the layout we understand. Only WRITING such a board is gated.
		ver = core.SchemaVersion
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

// BoardVersion returns the layout version meta.json DECLARES — the board's own
// number, never this binary's. 0 means the file is absent (a fresh store, or one
// predating meta.json); an unreadable file is an error.
//
// The old code defaulted BOTH of those cases to core.SchemaVersion, which
// quietly disabled the version gate: a corrupt meta.json made any binary believe
// the board was exactly as new as itself. A version we cannot read is a question
// for the operator, not something to guess.
func (s *Store) BoardVersion() (int, error) {
	m, err := s.LoadMeta()
	if err != nil {
		return 0, err
	}
	return m.SchemaVersion, nil
}

// LoadMeta reads meta.json whole — the declared version AND the unknown top-level
// keys the passthrough parked. An absent file is a zero-valued Meta (version 0),
// which is what gives BoardVersion its "0 means absent" contract; an unreadable
// one is an error, never a guess.
//
// Everything that touches meta.json goes through here (BoardVersion, lint's
// unknown-key check, SetBoardVersion's read-raise-write): one parse, one error
// message, and no second place that could decide to fall back to "whatever
// version this binary is" — the fallback that silently disabled the gate on
// 2026-07-13.
func (s *Store) LoadMeta() (*core.Meta, error) {
	// #nosec G304 -- metaPath is a furrow-internal store path, not attacker-supplied.
	b, err := os.ReadFile(s.metaPath())
	if os.IsNotExist(err) {
		return &core.Meta{}, nil
	}
	if err != nil {
		return nil, core.Internalf("meta", "read meta.json: %v", err)
	}
	m, err := core.UnmarshalMeta(b)
	if err != nil {
		return nil, core.Internalf("meta", "meta.json is unreadable: %v — restore it from git, or delete it and re-stamp with `furrow upgrade --yes`", err)
	}
	return m, nil
}

// Writable answers "may this binary write this board?" without touching it —
// the predicate half of gateWrite, so `furrow board` / `furrow lint` / the
// archive flow can ask the question without performing the write.
//
// A GENUINELY fresh store — no version AND no shards — is writable: there is no
// prior layout to misrepresent, so the first write may stamp it. That is `furrow
// init`, and it is how .furrow/archive/ comes into being on its first archive. A
// store with shards but NO meta.json is the opposite: a board of unknown layout,
// and stamping it with our version would be a lie — refuse.
func (s *Store) Writable() error {
	v, err := s.BoardVersion()
	if err != nil {
		return err
	}
	if v == 0 {
		ids, err := s.ListTaskIDs()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil // fresh: the one board a write may stamp
		}
	}
	return core.CheckWritable(v) // v == 0 here means "shards but no meta" -> refuse
}

// gateWrite is the check every mutating method runs first, and the enforcement
// half of Writable: refuse, or (on a genuinely fresh store) stamp meta.json so
// the board declares the layout it is about to be written in.
//
// It guards the WHOLE store, not just the task shards, because "read-only board"
// has to mean read-only. repos/<owner>__<repo>.json in particular exists only
// from schema v4 (`furrow review`) — writing one onto a v3 board would plant a
// field that board never promised, which is the very lie the shard gate exists to
// prevent, just through a different door. And a body/asset write that succeeded
// while its shard write was refused would leave an orphan behind, so a refusal
// would not be total.
//
// SetBoardVersion is the sole exemption from this gate; it is the raiser.
func (s *Store) gateWrite() error {
	if err := s.Writable(); err != nil {
		return err
	}
	v, err := s.BoardVersion()
	if err != nil {
		return err
	}
	if v == 0 {
		if err := os.MkdirAll(s.root, 0o755); err != nil {
			return core.Internalf("meta", "create %s: %v", s.root, err)
		}
		return s.SetBoardVersion(core.SchemaVersion)
	}
	return nil
}

// SetBoardVersion stamps meta.json with a layout version, through the single
// marshaller path like every other write. This is the ONLY way a board's version
// ever moves: `furrow upgrade` calls it, ordinary mutations never do (they are
// all behind gateWrite, which this deliberately skips). Raising it is a flag day
// — every binary still on the old layout (a pinned CI's included) loses write
// access to this board the moment it lands.
func (s *Store) SetBoardVersion(v int) error {
	// Read the meta we are about to raise, rather than building a fresh one: a
	// fresh core.Meta carries no unknown keys, so writing it would EAT anything a
	// newer furrow had put in meta.json. `furrow upgrade` — the one command whose
	// whole job is to move a board forward — would be the thing that destroys the
	// forward-compatible keys. Read, raise, write back (core/passthrough.go).
	m, err := s.LoadMeta() // absent file -> zero Meta, which is the fresh-store case
	if err != nil {
		return err
	}
	m.SchemaVersion = v

	b, err := core.MarshalMeta(m)
	if err != nil {
		return err
	}
	return s.writeIfChanged(s.metaPath(), b)
}

// Save splits the index into per-task shards under tasks/ and deletes the shards
// of any ids no longer present. It does NOT write meta.json — it READS it
// (gateWrite), and the only board it may stamp is a genuinely fresh one. Three
// properties matter:
//   - The board's layout version is an input, never an output: an ordinary write
//     never raises it. That one line — stamping meta.json with the binary's
//     version on every Save — is what took the shared board down on 2026-07-13.
//   - Determinism/no churn: each shard is serialized via the single
//     core.MarshalTask path and written ONLY when its bytes differ from disk, so
//     re-saving an untouched board rewrites nothing (zero git churn).
//   - Atomicity: every file is written tmp+rename, so a crash never leaves a
//     half-written shard. A single-task change (the common case) is one shard =
//     fully atomic; a bulk change is per-shard atomic (each shard independently
//     valid and the operation is safely re-runnable).
//
// index.json is never read or written — the abolished monolith stays abolished.
func (s *Store) Save(idx *core.Index) error {
	// The write gate. The board's declared version is an INPUT here, never an
	// output: this binary writes only a board that already speaks its exact
	// layout. Too new -> refuse (we'd strip fields we don't know). Too old ->
	// refuse (we'd write fields the board never promised, and silently perform a
	// flag day that locks out every pinned reader — the 2026-07-13 outage).
	if err := s.gateWrite(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.tasksDir(), 0o755); err != nil {
		return core.Internalf("index", "create tasks/: %v", err)
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
	// #nosec G304 -- path is a furrow-internal store path (store dir joined
	// with a validated id), read back only to compare against what we are
	// about to write; not attacker-influenced.
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return nil
	}
	return s.atomicWrite(path, data)
}

// LoadRepo returns the review record for owner/repo, or ok=false when no shard
// exists yet (the repo has never been reviewed). The per-repo twin of loading a
// task shard.
func (s *Store) LoadRepo(repo string) (*core.RepoRecord, bool, error) {
	// #nosec G304 -- repoPath is a furrow-internal store path (repos/ joined
	// with an owner/repo-derived stem), not attacker-supplied.
	b, err := os.ReadFile(s.repoPath(repo))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, core.Internalf(repo, "read repo shard: %v", err)
	}
	rec, err := core.UnmarshalRepo(b)
	if err != nil {
		return nil, false, err
	}
	return rec, true, nil
}

// SaveRepo writes one repo review shard via the single core.MarshalRepo path,
// atomically and only when its bytes changed (zero git churn on a no-op), the
// repos/ twin of a task Save.
func (s *Store) SaveRepo(rec *core.RepoRecord) error {
	if err := s.gateWrite(); err != nil {
		return err
	}

	if err := os.MkdirAll(s.reposDir(), 0o755); err != nil {
		return core.Internalf(rec.Repo, "create repos/: %v", err)
	}
	data, err := core.MarshalRepo(rec)
	if err != nil {
		return err
	}
	return s.writeIfChanged(s.repoPath(rec.Repo), data)
}

// ListRepos returns every repo review record, sorted by Repo. A missing repos/
// dir yields nil (a board that never reviewed), not an error.
func (s *Store) ListRepos() ([]core.RepoRecord, error) {
	entries, err := os.ReadDir(s.reposDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, core.Internalf("repos", "read repos/: %v", err)
	}
	var recs []core.RepoRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// #nosec G304 -- repos/ entry path is store-internal, not attacker-supplied.
		b, err := os.ReadFile(filepath.Join(s.reposDir(), e.Name()))
		if err != nil {
			return nil, core.Internalf("repos", "read repo shard %s: %v", e.Name(), err)
		}
		rec, err := core.UnmarshalRepo(b)
		if err != nil {
			return nil, err
		}
		recs = append(recs, *rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Repo < recs[j].Repo })
	return recs, nil
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
	if err := s.gateWrite(); err != nil {
		return err
	}

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

// SaveAsset copies data into bodies/assets/<id>-<sanitized name>, picking a
// collision-free basename so an existing asset is never overwritten, and returns
// the final basename. The write is atomic (temp + rename), mirroring SaveBody.
func (s *Store) SaveAsset(id, srcName string, data []byte) (string, error) {
	if err := s.gateWrite(); err != nil {
		return "", err
	}

	if err := os.MkdirAll(s.assetsDir(), 0o755); err != nil {
		return "", core.Internalf(id, "create bodies/assets/: %v", err)
	}
	base := id + "-" + core.SanitizeAssetName(srcName)
	name := core.NextAssetName(base, func(cand string) bool {
		_, err := os.Stat(filepath.Join(s.assetsDir(), cand))
		return err == nil
	})
	if err := s.atomicWrite(filepath.Join(s.assetsDir(), name), data); err != nil {
		return "", err
	}
	return name, nil
}

// LoadAsset returns the bytes of bodies/assets/<name>. Missing is a NotFound
// error (archive lists first, so absence here is unexpected).
func (s *Store) LoadAsset(name string) ([]byte, error) {
	// #nosec G304 -- name is a store-internal asset name under assetsDir; the
	// path stays within furrow's own store, not attacker-supplied.
	data, err := os.ReadFile(filepath.Join(s.assetsDir(), name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, core.NotFound(name)
		}
		return nil, core.Internalf(name, "read asset: %v", err)
	}
	return data, nil
}

// SaveAssetRaw writes data to bodies/assets/<name> with the EXACT basename (no
// sanitize, no collision-avoidance), atomically — used by archive to move an
// asset while preserving its filename. (SaveAsset is the attach path, which
// derives a fresh collision-free name instead.)
func (s *Store) SaveAssetRaw(name string, data []byte) error {
	if err := s.gateWrite(); err != nil {
		return err
	}

	if err := os.MkdirAll(s.assetsDir(), 0o755); err != nil {
		return core.Internalf(name, "create bodies/assets/: %v", err)
	}
	return s.atomicWrite(filepath.Join(s.assetsDir(), name), data)
}

// DeleteAsset removes bodies/assets/<name>. Absent is not an error (mirrors
// DeleteBody), so a re-run after a partial archive is idempotent.
func (s *Store) DeleteAsset(name string) error {
	if err := s.gateWrite(); err != nil {
		return err
	}

	err := os.Remove(filepath.Join(s.assetsDir(), name))
	if err != nil && !os.IsNotExist(err) {
		return core.Internalf(name, "delete asset: %v", err)
	}
	return nil
}

// DeleteBody removes bodies/<id>.md (used by archive). Absent is not an error.
func (s *Store) DeleteBody(id string) error {
	if err := s.gateWrite(); err != nil {
		return err
	}

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

// ListAssets returns every file under bodies/assets/ as name+size, sorted by
// name, for lint's orphan/oversized checks. Enumeration only — contents are not
// read. A missing bodies/assets/ dir yields nil and no error, so lint works on a
// board that never attached anything (mirroring listStems).
func (s *Store) ListAssets() ([]core.AssetInfo, error) {
	entries, err := os.ReadDir(s.assetsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, core.Internalf("assets", "read bodies/assets: %v", err)
	}
	var assets []core.AssetInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			return nil, core.Internalf(e.Name(), "stat asset: %v", err)
		}
		assets = append(assets, core.AssetInfo{Name: e.Name(), Size: fi.Size()})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	return assets, nil
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
