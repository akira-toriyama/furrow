package core

import "time"

// Ports. These interfaces are the seams between the pure core and the outside
// world. They are declared HERE (in core) and implemented by adapters
// (internal/store/fsstore for the real filesystem, internal/store/memstore for
// an in-memory fake used by tests and dry-runs). The app/CLI layers depend
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
	// Save writes each task as its own shard (via core.MarshalTask) and deletes
	// shards for ids no longer present — all atomically and writing only what
	// changed. It does NOT raise the board's layout version: it refuses a board
	// that does not already declare this binary's SchemaVersion (CheckWritable),
	// and stamps meta.json only for a genuinely fresh store.
	Save(idx *Index) error

	// BoardVersion is the layout version the board DECLARES (meta.json), read
	// ungated: 0 means no meta.json at all, and a garbled one is an error, never
	// a guess. Ungated is load-bearing — it is what lets `furrow board` diagnose
	// a board that no other command can open.
	BoardVersion() (int, error)
	// LoadMeta returns meta.json as a record: the declared layout version PLUS the
	// top-level keys this binary does not know (parked by the passthrough). An
	// absent file yields a zero-valued Meta, not an error — the same "0 means no
	// meta.json" contract BoardVersion has, which is derived from this.
	//
	// It exists because BoardVersion cannot answer the question lint needs to ask:
	// it projects the one field it wants and throws the rest away, so a typo in
	// meta.json would be invisible. meta.json's published schema now declares
	// additionalProperties:true (furrow legitimately writes unknown keys back), so
	// `furrow lint` is the only check left that can flag one — and nothing ever
	// deletes an extra, so an unflagged typo is permanent.
	LoadMeta() (*Meta, error)
	// Writable reports whether this binary may write the board: nil = yes, else
	// the refusal every mutation would raise (schema-upgrade-required /
	// schema-too-new). It has no side effects, so callers that only want to
	// REPORT the state (`furrow board`, `furrow lint`, and the archive flow,
	// which must gate the hot board before it writes a sibling store) ask this
	// instead of re-deriving the rule — a second copy of the rule is how the two
	// halves drift apart.
	Writable() error
	// SetBoardVersion raises the board to a layout version. It is the ONE
	// deliberate raiser (the engine of `furrow upgrade`); no ordinary mutation
	// may call it, because doing so locks out every binary still on the old
	// layout — including a pinned CI's.
	SetBoardVersion(v int) error

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

	// ListAssets returns every file under bodies/assets/ as name+size, sorted by
	// name (enumeration only — contents are not read), for lint's orphan and
	// oversized checks. A missing bodies/assets/ dir yields nil, not an error, so
	// lint works on a board that never attached anything.
	ListAssets() ([]AssetInfo, error)

	// LoadAsset returns the bytes of the asset with this exact basename, or a
	// NotFound error if it is absent. Used by archive to move an asset with its
	// task (the caller lists first, so absence is unexpected).
	LoadAsset(name string) ([]byte, error)

	// DeleteAsset removes the asset with this exact basename. Absent is not an
	// error (mirrors DeleteBody), so a re-run after a partial archive is idempotent.
	DeleteAsset(name string) error

	// NextID returns a fresh, random, collision-resistant id (e.g. "t-k3m9p":
	// prefix + a random Crockford-base32 suffix). There is no shared counter, so
	// concurrent adds in separate worktrees won't collide. The caller (app) is
	// responsible for ensuring the id is not already present in the index.
	NextID() (string, error)

	// LoadRepo returns the review record for an owner/repo, or ok=false when the
	// repo has never been reviewed (no shard yet). One shard per repo under
	// repos/, the per-repo twin of a task shard.
	LoadRepo(repo string) (rec *RepoRecord, ok bool, err error)
	// SaveRepo writes one repo review shard (via core.MarshalRepo), atomically
	// and only when its bytes changed — the repos/ twin of a task Save.
	SaveRepo(rec *RepoRecord) error
	// ListRepos returns every repo review record, sorted by Repo. A store that
	// has never recorded a review yields nil, not an error (the repos/ dir may
	// not exist), so the staleness nudge works on a board that never reviewed.
	ListRepos() ([]RepoRecord, error)
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
