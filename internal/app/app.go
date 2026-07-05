// Package app is the coordinator layer: it wires a Store and Config together
// and exposes every task mutation as a method. It is the ONLY mutation funnel —
// the CLI and TUI call App, never the store directly. That keeps invariants
// (frozen ids, canonical order, closed-timestamp rules, body<->index pairing)
// in one place instead of scattered across two presentation layers.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/store/fsstore"
)

// DirName is the per-repo store directory.
const DirName = ".furrow"

// EnvDir overrides discovery with an explicit .furrow path.
const EnvDir = "FURROW_DIR"

// EnvBoard overrides the user-level central boards with one explicit board path
// (the central .furrow). Like EnvDir it is a single-value env override; the
// scope is derived from the board and the repo mode is "auto".
const EnvBoard = "FURROW_BOARD"

// PointerName is a repo-local file that redirects furrow at a central board
// (and optionally scopes it to a repo) instead of holding its own .furrow.
const PointerName = ".furrow-pointer.toml"

// Store is what App needs from a store: the core port plus the few extras the
// coordinator uses. Both fsstore and memstore satisfy it.
type Store interface {
	core.Store
	DeleteBody(id string) error
	BodyFile(id string) string // absolute path for $EDITOR; "" if not file-backed
}

// App holds the resolved store, config, clock, and any config warnings (so lint
// can surface them).
type App struct {
	Store    Store
	Cfg      *config.Config
	Clock    core.Clock
	Dir      string   // the .furrow directory
	Warnings []string // config clamp warnings

	// DefaultLabel is a central board's LITERAL `label` tag ("" = none): `add`
	// unions it into the task's labels, like a GitHub Issues label auto-applied
	// per board. It never filters reads — scoping is DefaultRepo's job.
	DefaultLabel string

	// DefaultRepo is the board-scope repo from a pointer or a central board
	// ("" = none): `add` unions it into the task's repos (suppressed by
	// --draft) and reads filter by it when AutoFilter is on.
	DefaultRepo string

	// AutoFilter reports whether read commands (ls/next/revisit) auto-filter by
	// DefaultRepo. A pointer always scopes (true); a central board honors its
	// per-board auto_filter (default true). Meaningless when DefaultRepo is "".
	AutoFilter bool

	// ScopeWarnings are discovery-time notes bound for stderr (e.g. the global
	// default board activated but found no enclosing git repo to derive an
	// auto repo from).
	ScopeWarnings []string

	// BoardRepos is the repo set derived from the enclosing checkout (the
	// owner/repo parsed from the git origin URL — see deriveScopeRepo). Open
	// populates it from DefaultRepo; it participates in the short-name
	// resolution universe (see repoUniverse), so a derived repo resolves short
	// names even before its first task exists.
	BoardRepos []string

	// sleep is the backoff sleeper used by Sync's transient-rebase retry. nil
	// means the real time.Sleep (see sleeper); tests set a no-op to run the
	// retry budget instantly.
	sleep func(time.Duration)
}

// sleeper returns the App's backoff sleeper, defaulting to time.Sleep so the
// production path needs no wiring and only tests override it.
func (a *App) sleeper() func(time.Duration) {
	if a.sleep != nil {
		return a.sleep
	}
	return time.Sleep
}

// Open discovers the store (FURROW_DIR, else the nearest ancestor of startDir
// holding a .furrow, else a .furrow-pointer.toml redirecting to a central board),
// loads config, and builds an fsstore. Outside any of these it is a validation
// error pointing at `furrow init`.
func Open(startDir string) (*App, error) {
	res, err := discover(startDir)
	if err != nil {
		return nil, err
	}
	a, err := openAt(res.Dir)
	if err != nil {
		return nil, err
	}
	a.DefaultLabel = res.DefaultLabel
	a.DefaultRepo = res.DefaultRepo
	a.AutoFilter = res.AutoFilter
	a.ScopeWarnings = res.ScopeWarn
	if res.DefaultRepo != "" {
		a.BoardRepos = []string{res.DefaultRepo}
	}
	return a, nil
}

func openAt(dir string) (*App, error) {
	cfg, warn, err := config.Load(filepath.Join(dir, "config.toml"))
	if err != nil {
		return nil, core.Validationf("config", "%v", err)
	}
	st := fsstore.New(dir, cfg.Lanes, cfg.IDPrefix, cfg.IDWidth)
	return &App{Store: st, Cfg: cfg, Clock: core.SystemClock(), Dir: dir, Warnings: warn}, nil
}

// NewWithStore builds an App over an arbitrary Store (for tests / dry-runs).
func NewWithStore(st Store, cfg *config.Config, clk core.Clock) *App {
	return &App{Store: st, Cfg: cfg, Clock: clk}
}

// resolution is the outcome of discovery: which .furrow to open, and (only when
// reached via a pointer or central board) the repo/label to scope commands to.
type resolution struct {
	Dir          string
	DefaultLabel string // literal board tag (add-time union; never read-filters)
	DefaultRepo  string // board-scope repo ("" = none)
	AutoFilter   bool   // scope reads by DefaultRepo (pointer: always; board: its auto_filter)
	ScopeWarn    []string
}

// discover finds the store: FURROW_DIR if set (no scope injection), else walk up
// from startDir. At each directory a local .furrow wins; failing that, a
// .furrow-pointer.toml redirects to a central board and supplies its repo.
func discover(startDir string) (resolution, error) {
	if env := os.Getenv(EnvDir); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil {
			return resolution{}, core.Validationf("", "%s=%q is not a valid path: %v", EnvDir, env, err)
		}
		// An explicit FURROW_DIR must point at an existing store directory;
		// a typo'd path should fail loudly, not act as an empty store.
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return resolution{}, core.Validationf("", "%s=%q is not an existing directory", EnvDir, abs)
		}
		return resolution{Dir: abs}, nil
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return resolution{}, core.Internalf("", "resolve %q: %v", startDir, err)
	}
	for {
		cand := filepath.Join(dir, DirName)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return resolution{Dir: cand}, nil
		}
		ptr := filepath.Join(dir, PointerName)
		if fi, err := os.Stat(ptr); err == nil && !fi.IsDir() {
			return resolvePointer(dir, ptr)
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the root: try the user-level default board, else give up
			if res, ok, err := resolveGlobalBoard(startDir); err != nil {
				return resolution{}, err
			} else if ok {
				return res, nil
			}
			return resolution{}, core.Validationf("", "no %s or %s found in %q or any parent; run `furrow init`", DirName, PointerName, startDir)
		}
		dir = parent
	}
}

// resolvePointer reads a .furrow-pointer.toml, resolves its board path against
// the pointer file's directory (~ → home, relative → that dir, absolute as-is),
// and requires the board to be an existing directory.
func resolvePointer(pointerDir, pointerPath string) (resolution, error) {
	p, pwarn, err := config.LoadPointer(pointerPath)
	if err != nil {
		return resolution{}, core.Validationf("", "%s: %v", pointerPath, err)
	}
	board, err := resolvePathRelTo(pointerDir, p.Board)
	if err != nil {
		return resolution{}, err
	}
	if fi, err := os.Stat(board); err != nil || !fi.IsDir() {
		return resolution{}, core.Validationf("", "%s: board %q is not an existing directory", pointerPath, board)
	}
	repo, rwarn := deriveScopeRepo(p.DefaultRepo, pointerDir)
	return resolution{Dir: board, DefaultRepo: repo, AutoFilter: true, ScopeWarn: append(pwarn, rwarn...)}, nil
}

// resolvePathRelTo turns a path (bare ~ or ~/path, relative to baseDir, or
// absolute) into a cleaned absolute path. It does NOT check existence — that is
// the caller's job, since only the caller has the context for the error. Shared
// by resolvePointer (a board path) and resolveGlobalBoard (board AND scope paths).
func resolvePathRelTo(baseDir, p string) (string, error) {
	if strings.HasPrefix(p, "~") {
		rest := p[1:]
		// Only bare ~ / ~/path is supported; ~user would silently resolve onto
		// the current user's home, so reject it loudly rather than misroute.
		if rest != "" && !strings.HasPrefix(rest, "/") {
			return "", core.Validationf("", "path %q uses the unsupported ~user form; use an absolute path", p)
		}
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", core.Internalf("", "resolve ~ in path %q: %v", p, herr)
		}
		p = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	return filepath.Clean(p), nil
}

// resolveGlobalBoard is the last-resort arm of discover: a user-level central
// board (FURROW_BOARD, else the [[board]] entries in
// ${XDG_CONFIG_HOME:-~/.config}/furrow/config.toml) that backs many repos
// without a per-repo pointer. Several boards may be configured; the one whose
// scope most specifically (longest canonical prefix) encloses cwd wins, with
// ties broken by file order. It returns ok=false with no error whenever there is
// no default board OR cwd is outside every scope, so discover falls through to
// the usual "run furrow init" error and behaves exactly as before. A bad board
// path is a loud error, but only for the winning board once the scope gate has
// passed (so a stray config never breaks furrow in unrelated repos).
func resolveGlobalBoard(startDir string) (resolution, bool, error) {
	boards, cfgDir, warn, err := loadGlobalBoards()
	if err != nil {
		return resolution{}, false, err
	}
	if len(boards) == 0 {
		return resolution{}, false, nil
	}
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return resolution{}, false, core.Internalf("", "resolve %q: %v", startDir, err)
	}
	cdir := canonicalPath(abs)

	// Pick the board whose matching scope is the longest (most specific) canonical
	// prefix of cwd. Boards are visited in file order and ties keep the first
	// match (strict >), so the choice is deterministic. Paths are resolved for the
	// comparison but NOT stat'd here — only the eventual winner is checked to
	// exist, so a broken board in an unrelated scope never breaks furrow.
	//
	// A path/scope that cannot even be resolved (e.g. the unsupported ~user form)
	// is DROPPED with a warning, never a hard error: clamp-don't-reject means one
	// half-written entry must not break furrow in every directory on the machine.
	// Only the winner's existence is ever loud (the os.Stat below).
	var winner *config.GlobalBoard
	var winBoard string
	winLen := -1
	for i := range boards {
		b := &boards[i]
		board, err := resolvePathRelTo(cfgDir, b.Path)
		if err != nil {
			warn = append(warn, fmt.Sprintf("ignoring central board %q: %v", b.Path, err))
			continue
		}
		for _, s := range boardScopes(b, board) {
			cs, ok, err := canonicalScopeUnder(cdir, cfgDir, s)
			if err != nil {
				warn = append(warn, fmt.Sprintf("ignoring scope %q of central board %q: %v", s, b.Path, err))
				continue
			}
			if ok && len(cs) > winLen {
				winner, winBoard, winLen = b, board, len(cs)
			}
		}
	}
	if winner == nil {
		return resolution{}, false, nil // out of every scope: inert, behaves like today
	}
	if fi, err := os.Stat(winBoard); err != nil || !fi.IsDir() {
		return resolution{}, false, core.Validationf("", "central board %q is not an existing directory", winBoard)
	}
	repo, rwarn := deriveScopeRepo(winner.Repo, abs)
	return resolution{Dir: winBoard, DefaultLabel: winner.Label, DefaultRepo: repo, AutoFilter: winner.AutoFilter, ScopeWarn: append(warn, rwarn...)}, true, nil
}

// boardScopes returns the scopes to match a board against. A board loaded from
// config always carries at least one (the clamp drops scope-less entries); the
// nil-scopes sentinel belongs only to FURROW_BOARD, whose scope is derived from
// the board's repo parent: …/<org>/<repo>/.furrow -> repo …/<org>/<repo> ->
// scope …/<org>.
func boardScopes(b *config.GlobalBoard, resolvedBoard string) []string {
	if b.Scopes == nil {
		return []string{filepath.Dir(filepath.Dir(resolvedBoard))}
	}
	return b.Scopes
}

// loadGlobalBoards resolves the user-level central boards: FURROW_BOARD (an env
// override supplying only a board path) wins as a single synthetic board with
// nil scopes (the derive-from-parent sentinel); else the [[board]] entries from
// the config file at globalConfigPath. cfgDir is the base for resolving relative
// board/scope paths.
func loadGlobalBoards() (boards []config.GlobalBoard, cfgDir string, warn []string, err error) {
	if env := os.Getenv(EnvBoard); env != "" {
		base, _ := os.Getwd()
		return []config.GlobalBoard{{Path: env, Scopes: nil, Repo: "auto", AutoFilter: true}}, base, nil, nil
	}
	path, err := globalConfigPath()
	if err != nil {
		return nil, "", nil, err
	}
	boards, warn, err = config.LoadGlobalBoards(path)
	if err != nil {
		return nil, "", nil, err
	}
	return boards, filepath.Dir(path), warn, nil
}

// globalConfigPath is ${XDG_CONFIG_HOME}/furrow/config.toml when XDG_CONFIG_HOME
// is an absolute path, else ~/.config/furrow/config.toml. (os.UserConfigDir is
// deliberately avoided: on darwin it returns ~/Library/Application Support,
// which violates the ~/.config contract.)
func globalConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "furrow", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", core.Internalf("", "resolve home for the furrow config: %v", err)
	}
	return filepath.Join(home, ".config", "furrow", "config.toml"), nil
}

// canonicalScopeUnder resolves scope (relative to baseDir, ~ aware) to a
// canonical path and reports whether cdir (already canonical) is the scope
// itself or a descendant of it, using a path-separator boundary so
// "/ws/org-evil" never matches scope "/ws/org". It returns the canonical scope
// so the caller can compare match specificity by length. Both sides are
// canonicalized (symlinks resolved) so a symlinked cwd or scope — e.g. macOS's
// /var -> /private/var — still compares correctly. A blank scope never matches.
func canonicalScopeUnder(cdir, baseDir, scope string) (string, bool, error) {
	if scope == "" {
		return "", false, nil
	}
	sp, err := resolvePathRelTo(baseDir, scope)
	if err != nil {
		return "", false, err
	}
	cs := canonicalPath(sp)
	if cdir == cs || strings.HasPrefix(cdir, cs+string(os.PathSeparator)) {
		return cs, true, nil
	}
	return "", false, nil
}

// canonicalPath cleans p and resolves symlinks when it can; if EvalSymlinks
// fails (e.g. the path does not exist) it falls back to the cleaned path.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// Init creates a fresh .furrow at dir/.furrow (config.toml template + an empty
// tasks/ shard dir + meta.json + bodies/). It is an error if one already
// exists. The tasks/ dir and meta.json are provisioned by the first Store.Save.
func Init(dir string) (*App, error) {
	fdir := filepath.Join(dir, DirName)
	if fi, err := os.Stat(fdir); err == nil && fi.IsDir() {
		return nil, core.Validationf("", "%s already exists at %q", DirName, fdir)
	}
	if err := os.MkdirAll(filepath.Join(fdir, "bodies"), 0o755); err != nil {
		return nil, core.Internalf("", "create %s: %v", fdir, err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "config.toml"), []byte(config.Template), 0o644); err != nil {
		return nil, core.Internalf("", "write config.toml: %v", err)
	}
	a, err := openAt(fdir)
	if err != nil {
		return nil, err
	}
	if err := a.Store.Save(&core.Index{SchemaVersion: core.SchemaVersion, Tasks: []core.Task{}}); err != nil {
		return nil, err
	}
	return a, nil
}

// load reads the index and canonicalizes it, so every read path sees tasks in
// the same lane->priority->id order regardless of any hand-edit.
func (a *App) load() (*core.Index, error) {
	idx, err := a.Store.Load()
	if err != nil {
		return nil, err
	}
	core.Canonicalize(idx, a.Cfg.Lanes)
	return idx, nil
}

// AddOpts are the optional fields for Add. A nil Priority means "auto" (append
// after the lane's last task using the sparse step).
type AddOpts struct {
	Status   string
	Priority *int
	Value    *int // optional coarse 1..5 estimate; nil = unset
	Effort   *int // optional coarse 1..5 estimate; nil = unset
	Labels   []string
	// Repos attaches the task to repositories. Each entry is resolved strictly
	// (full owner/repo, or a short name naming exactly one repo in the board's
	// universe — see resolveRepoIn).
	Repos  []string
	Parent string
	Deps   []string
	Refs   []string
	Body   string // initial body markdown; "" seeds a heading from the title
	// Draft marks the task as deliberately repo-less (repos == [], the
	// issue-draft analogue). It conflicts with explicit Repos, and it
	// suppresses exactly the board-scope repo union (see withBoardRepo) — the
	// escape hatch for "note this on the board, attach it later".
	Draft bool
}

// Add creates a task, writes its body file, and saves the index. Returns the
// created task.
func (a *App) Add(title string, o AddOpts) (*core.Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, core.Validationf("", "title must not be empty")
	}
	o.Labels = a.withDefaultLabel(o.Labels)
	status := o.Status
	if status == "" {
		status = a.Cfg.DefaultLane
	}
	if !a.Cfg.IsLane(status) {
		return nil, core.Validationf("", "unknown lane %q (configured: %s)", status, strings.Join(a.Cfg.Lanes, ", "))
	}
	if a.Cfg.LabelsRequired && len(o.Labels) == 0 {
		return nil, core.Validationf("", "a label is required ([labels].required); add -l <label>")
	}
	if o.Draft && len(o.Repos) > 0 {
		return nil, core.Validationf("", "--draft cannot be combined with an explicit repo (-r): a draft is attached to no repo")
	}

	idx, err := a.load()
	if err != nil {
		return nil, err
	}

	repos, err := resolveRepoArgs(o.Repos, "", repoUniverse(idx, a.BoardRepos))
	if err != nil {
		return nil, err
	}
	repos = a.withBoardRepo(repos, o.Draft)

	id, err := a.uniqueID(idx)
	if err != nil {
		return nil, err
	}

	var prio int
	if o.Priority != nil {
		prio = *o.Priority
	} else {
		prio = idx.NextPriority(status, a.Cfg.PriorityDefault, a.Cfg.PriorityStep)
	}

	now := a.Clock.Now()
	t := core.Task{
		ID: id, Title: title, Status: status, Priority: prio,
		Value: cloneIntp(o.Value), Effort: cloneIntp(o.Effort),
		Labels: o.Labels, Repos: repos, Parent: o.Parent, Deps: o.Deps, Refs: o.Refs,
		Created: now, Updated: now, Body: core.BodyPath(id),
	}
	idx.Add(t)

	body := o.Body
	if body == "" {
		body = "# " + title + "\n"
	}
	if err := a.Store.SaveBody(id, body); err != nil {
		return nil, err
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	saved, _ := idx.Find(id)
	return saved, nil
}

// uniqueID draws random ids from the store until one is not already present in
// idx. AddMany appends each created task to idx before the next call, so this
// also keeps a batch internally unique. Ids are random, so the first draw almost
// always wins; the cap turns a pathological store into a loud error rather than
// an infinite loop.
func (a *App) uniqueID(idx *core.Index) (string, error) {
	for i := 0; i < 100; i++ {
		id, err := a.Store.NextID()
		if err != nil {
			return "", err
		}
		if !idx.Has(id) {
			return id, nil
		}
	}
	return "", core.Internalf("", "could not generate a unique id after 100 attempts")
}

// Get returns a task and its body. NotFound when the id is unknown.
func (a *App) Get(id string) (*core.Task, string, error) {
	idx, err := a.load()
	if err != nil {
		return nil, "", err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, "", core.NotFound(id)
	}
	body, err := a.Store.LoadBody(id)
	if err != nil {
		return nil, "", err
	}
	return t, body, nil
}

// ShowItem is one result of GetBatch: a task plus its body (empty when the
// batch was read without bodies).
type ShowItem struct {
	Task core.Task
	Body string
}

// GetBatch resolves a set of ids in one index load: the found tasks come back
// in input order (duplicates collapse to their first occurrence, misses too),
// and the ids that named no task come back in missing. A miss is data, not an
// error — partial success stays representable, the caller decides what a
// non-empty missing means. err is reserved for load/IO failures. withBody
// loads each found task's body; without it the body files are never touched.
func (a *App) GetBatch(ids []string, withBody bool) (items []ShowItem, missing []string, err error) {
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	items, missing = []ShowItem{}, []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		t, i := idx.Find(id)
		if i < 0 {
			missing = append(missing, id)
			continue
		}
		body := ""
		if withBody {
			if body, err = a.Store.LoadBody(id); err != nil {
				return nil, nil, err
			}
		}
		items = append(items, ShowItem{Task: *t, Body: body})
	}
	return items, missing, nil
}

// Backlinks returns the tasks whose body mentions id via the [[id]] wiki-link
// notation, in canonical order. It is the pull-side twin of GitHub's "mentioned
// in" panel — no server, no rate limit, just a scan of the local bodies (cheap
// at this scale; an index is YAGNI). A task's own body mentioning itself is not
// a backlink, and an orphan body (no task) never surfaces since the result is
// drawn from the index. NotFound when id is unknown.
func (a *App) Backlinks(id string) ([]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	if !idx.Has(id) {
		return nil, core.NotFound(id)
	}
	re := core.LinkPattern(a.Cfg.IDPrefix)
	bodyIDs, err := a.Store.ListBodyIDs()
	if err != nil {
		return nil, err
	}
	mentioners := map[string]bool{}
	for _, bid := range bodyIDs {
		if bid == id {
			continue // a body referring to itself is not a backlink
		}
		body, err := a.Store.LoadBody(bid)
		if err != nil {
			return nil, err
		}
		if contains(core.ExtractLinks(body, re), id) {
			mentioners[bid] = true
		}
	}
	var out []core.Task
	for i := range idx.Tasks {
		if mentioners[idx.Tasks[i].ID] {
			out = append(out, idx.Tasks[i])
		}
	}
	return out, nil
}

// BacklinksBatch is Backlinks for a set of ids in ONE board pass: a single
// load plus a single body scan, regardless of how many ids are requested — so
// `show <many ids> --backlinks` stays O(board), not O(ids × board). Each entry
// equals what Backlinks would return for that id: mentioners in canonical
// order, self-mentions excluded, each mentioner counted once. Every requested
// id present in the index maps to a (possibly empty, never nil) slice; unknown
// ids are simply absent from the map (the caller has already filtered to found
// tasks, so this never needs to raise NotFound).
func (a *App) BacklinksBatch(ids []string) (map[string][]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	out := map[string][]core.Task{}
	want := map[string]bool{}
	for _, id := range ids {
		if idx.Has(id) {
			want[id] = true
			out[id] = []core.Task{}
		}
	}
	if len(want) == 0 {
		return out, nil
	}
	re := core.LinkPattern(a.Cfg.IDPrefix)
	bodyIDs, err := a.Store.ListBodyIDs()
	if err != nil {
		return nil, err
	}
	// mentions[mentionerID] = the requested targets that body links to (deduped
	// so a body linking the same target twice still counts its author once).
	mentions := map[string][]string{}
	for _, bid := range bodyIDs {
		body, err := a.Store.LoadBody(bid)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, target := range core.ExtractLinks(body, re) {
			if target == bid || !want[target] || seen[target] {
				continue // self-mention isn't a backlink; ignore unrequested/dup
			}
			seen[target] = true
			mentions[bid] = append(mentions[bid], target)
		}
	}
	// Walk tasks in canonical order so each target's mentioners come out ordered.
	for i := range idx.Tasks {
		for _, target := range mentions[idx.Tasks[i].ID] {
			out[target] = append(out[target], idx.Tasks[i])
		}
	}
	return out, nil
}

// QueryOpts filters List/Next/Revisit. Zero values mean "no filter". Label (an
// explicit tag filter) and ScopeRepo (the board scope) are separate on
// purpose: they AND together, so filtering by a tag never widens a scoped
// board. Repo (an explicit -r) and ScopeRepo both filter on the repos field;
// Drafts selects only repo-less tasks and bypasses the scope (a draft belongs
// to no repo, so no repo scope may hide it — see match).
type QueryOpts struct {
	Status    string
	Label     string // explicit tag filter; ANDs with ScopeRepo
	ScopeRepo string // board-scope repo (a pointer's / central board's DefaultRepo)
	Repo      string // owner/repo filter on the repos field (already resolved)
	Drafts    bool   // only tasks with repos == []; ignores ScopeRepo/Repo
	Limit     int
}

// match reports whether t passes the query's filters (Limit excluded — that is
// the iteration's job). In Drafts mode only repo-less tasks pass and the board
// scope is bypassed; Status and Label still apply. Note a repo-scoped read
// (ScopeRepo or Repo set) hides drafts — the CLI's hidden-drafts hint exists
// for exactly that.
func (o QueryOpts) match(t *core.Task) bool {
	if o.Status != "" && t.Status != o.Status {
		return false
	}
	if o.Label != "" && !contains(t.Labels, o.Label) {
		return false
	}
	if o.Drafts {
		return len(t.Repos) == 0
	}
	if o.ScopeRepo != "" && !contains(t.Repos, o.ScopeRepo) {
		return false
	}
	if o.Repo != "" && !contains(t.Repos, o.Repo) {
		return false
	}
	return true
}

// matchRevisit is match with the draft carve-out `revisit` needs: an open
// draft (repos == []) passes the board scope and any repo filter, so drafts
// surface (with the no_repo signal) regardless of scope. The explicit filters
// (Status/Label) still apply — asking for one tag must not return unrelated
// drafts.
func (o QueryOpts) matchRevisit(t *core.Task) bool {
	if len(t.Repos) > 0 {
		return o.match(t)
	}
	d := o
	d.ScopeRepo, d.Repo = "", ""
	return d.match(t)
}

// List returns tasks in canonical order, after applying the filters.
func (a *App) List(o QueryOpts) ([]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	var out []core.Task
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) {
			continue
		}
		out = append(out, *t)
		if o.Limit > 0 && len(out) >= o.Limit {
			break
		}
	}
	return out, nil
}

// Next returns the actionable tasks in canonical order — the work that is ready
// to pick up: status in the configured next-lanes ([next].lanes, default
// ready+in-progress) AND every dependency already done. The query's filters
// restrict the result with the same semantics as List.
func (a *App) Next(o QueryOpts) ([]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	doneIDs := map[string]bool{}
	for _, t := range idx.Tasks {
		if t.Status == a.Cfg.DoneLane {
			doneIDs[t.ID] = true
		}
	}
	var out []core.Task
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) {
			continue
		}
		if a.Cfg.IsNextLane(t.Status) && idx.Actionable(t, a.Cfg.Terminal, doneIDs) {
			out = append(out, *t)
			if o.Limit > 0 && len(out) >= o.Limit {
				break
			}
		}
	}
	return out, nil
}

// Move sets a task's lane. Moving into the done lane stamps Closed; moving out
// of it clears Closed. Other terminal lanes (e.g. icebox) leave Closed alone —
// parked is not the same as closed.
func (a *App) Move(id, lane string) (*core.Task, error) {
	if !a.Cfg.IsLane(lane) {
		return nil, core.Validationf(id, "unknown lane %q (configured: %s)", lane, strings.Join(a.Cfg.Lanes, ", "))
	}
	return a.mutate(id, func(t *core.Task) {
		was := t.Status
		t.Status = lane
		switch {
		case lane == a.Cfg.DoneLane && was != a.Cfg.DoneLane:
			now := a.Clock.Now()
			t.Closed = &now
		case lane != a.Cfg.DoneLane && was == a.Cfg.DoneLane:
			t.Closed = nil
		}
	})
}

// Done moves a task into the done lane (and stamps Closed via Move).
func (a *App) Done(id string) (*core.Task, error) { return a.Move(id, a.Cfg.DoneLane) }

// Reorder sets a task's absolute priority.
func (a *App) Reorder(id string, priority int) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) { t.Priority = priority })
}

// SetValue records a task's value estimate, or clears it when v is nil (back to
// "unset", so triage stays frictionless). An out-of-range score is clamped into
// 1..5 on write by the marshaller. The pointer is copied so a later clamp can't
// reach back into the caller's variable.
func (a *App) SetValue(id string, v *int) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) { t.Value = cloneIntp(v) })
}

// SetEffort records a task's effort estimate, or clears it when v is nil. Same
// clamp/copy semantics as SetValue.
func (a *App) SetEffort(id string, v *int) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) { t.Effort = cloneIntp(v) })
}

// SetTitle renames a task's one-line summary. It touches only the shard; use
// Retitle from the CLI/TUI so the body's heading is kept in step.
func (a *App) SetTitle(id, title string) (*core.Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, core.Validationf(id, "title must not be empty")
	}
	return a.mutate(id, func(t *core.Task) { t.Title = title })
}

// Retitle renames a task and keeps the two homes of a title in step: the shard's
// title field (the source of truth) and the body's leading `# ` heading. Before
// this, a title lived in both places with no command to change it, so a rename
// meant hand-editing the shard AND the body — and once shards became
// furrow-owned, hand-editing them was off-limits entirely. Retitle writes the
// shard, then syncs the body heading. A body whose first line is not an H1 is
// left untouched (there is no second home to drift); an empty body is seeded a
// heading, mirroring add.
func (a *App) Retitle(id, title string) (*core.Task, error) {
	t, err := a.SetTitle(id, title)
	if err != nil {
		return nil, err
	}
	body, err := a.Store.LoadBody(id)
	if err != nil {
		return nil, err
	}
	if next, changed := retitleHeading(body, t.Title); changed {
		if err := a.Store.SaveBody(id, next); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// retitleHeading rewrites body's leading ATX H1 heading to `# <title>`, returning
// the new body and whether it changed. The heading is the first non-blank line
// when that line is an H1 — a single `#` then a space, so `##` and `#foo` are not
// treated as one — and only its text is replaced; everything after is preserved
// byte-for-byte. An empty (or whitespace-only) body is seeded `# <title>`,
// matching add. A non-empty body whose first non-blank line is not an H1 is
// returned unchanged: the title then lives only in the shard, with nothing to
// keep in sync. Operates on LF-delimited markdown (furrow's on-disk form).
func retitleHeading(body, title string) (string, bool) {
	want := "# " + title
	if strings.TrimSpace(body) == "" {
		return want + "\n", true
	}
	lines := strings.Split(body, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue // skip leading blank lines before the heading
		}
		if !strings.HasPrefix(ln, "# ") {
			return body, false // first real line isn't an H1 — leave the body alone
		}
		if ln == want {
			return body, false
		}
		lines[i] = want
		return strings.Join(lines, "\n"), true
	}
	return body, false
}

// Check sets a checklist item's done state by zero-based index. An out-of-range
// index is a validation error (not a silent no-op), so the CLI exit code and
// the {"error":...} envelope honor the contract.
func (a *App) Check(id string, item int, done bool) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	if item < 0 || item >= len(t.Checklist) {
		return nil, core.Validationf(id, "checklist index %d out of range (have %d item(s))", item, len(t.Checklist))
	}
	return a.mutate(id, func(t *core.Task) { t.Checklist[item].Done = done })
}

// AddDep makes `id` depend on `dep` (id waits on dep). Both ids must exist, a
// task may not depend on itself, and the edge must not create a cycle (dep must
// not already depend on id, directly or transitively). Re-adding an existing
// dep is a no-op; the marshaller keeps the dep list sorted and de-duplicated.
func (a *App) AddDep(id, dep string) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	if !idx.Has(id) {
		return nil, core.NotFound(id)
	}
	if id == dep {
		return nil, core.Validationf(id, "a task cannot depend on itself")
	}
	if !idx.Has(dep) {
		return nil, core.Validationf(id, "dependency %q does not exist", dep)
	}
	if idx.DependsOn(dep, id) {
		return nil, core.Validationf(id, "adding dep %q would create a cycle (%s already depends on %s)", dep, dep, id)
	}
	return a.mutate(id, func(t *core.Task) {
		if !contains(t.Deps, dep) {
			t.Deps = append(t.Deps, dep)
		}
	})
}

// RemoveDep drops `dep` from `id`'s dependency list. It is a validation error
// when id has no such dependency, so the result is never a silent no-op.
func (a *App) RemoveDep(id, dep string) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	if !contains(t.Deps, dep) {
		return nil, core.Validationf(id, "%q is not a dependency of %s", dep, id)
	}
	return a.mutate(id, func(t *core.Task) {
		kept := make([]string, 0, len(t.Deps))
		for _, d := range t.Deps {
			if d != dep {
				kept = append(kept, d)
			}
		}
		t.Deps = kept
	})
}

// Relabel adds and/or removes labels on a task. Adding a label already present,
// and removing one already absent, are both no-ops (idempotent) so re-runs don't
// churn the diff. A call with neither --add nor --remove is a bad-usage error
// rather than a silent no-op. When [labels].required is set, a relabel that would
// leave the task with zero labels is rejected. The marshaller keeps the stored
// label set sorted and de-duplicated, so the in-memory order here doesn't matter.
func (a *App) Relabel(id string, add, remove []string) (*core.Task, error) {
	if len(add) == 0 && len(remove) == 0 {
		return nil, core.Validationf(id, "provide at least one --add or --remove label")
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	rm := make(map[string]bool, len(remove))
	for _, l := range remove {
		rm[l] = true
	}
	next := make([]string, 0, len(t.Labels)+len(add))
	for _, l := range t.Labels {
		if !rm[l] {
			next = append(next, l)
		}
	}
	for _, l := range add {
		if !contains(next, l) {
			next = append(next, l)
		}
	}
	if a.Cfg.LabelsRequired && len(next) == 0 {
		return nil, core.Validationf(id, "a label is required ([labels].required); this relabel would remove the last one")
	}
	return a.mutate(id, func(t *core.Task) { t.Labels = next })
}

// AddCheck appends a checklist item.
func (a *App) AddCheck(id, text string) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) {
		t.Checklist = append(t.Checklist, core.ChecklistItem{Text: text})
	})
}

// mutate loads, finds, applies fn, stamps Updated, and saves — the common shape
// of every single-task edit. Returns the updated task.
func (a *App) mutate(id string, fn func(*core.Task)) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	fn(t)
	t.Updated = a.Clock.Now()
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	saved, _ := idx.Find(id)
	return saved, nil
}

// EditPath ensures a task's body file exists (creating an empty one if needed)
// and returns its absolute path for the CLI to hand to $EDITOR.
func (a *App) EditPath(id string) (string, error) {
	idx, err := a.load()
	if err != nil {
		return "", err
	}
	if !idx.Has(id) {
		return "", core.NotFound(id)
	}
	if !a.Store.BodyExists(id) {
		if err := a.Store.SaveBody(id, ""); err != nil {
			return "", err
		}
	}
	p := a.Store.BodyFile(id)
	if p == "" {
		return "", core.Internalf(id, "this store is not file-backed; cannot edit")
	}
	return p, nil
}

// cloneIntp returns a copy of an optional int so callers and the store never
// alias the same *int (Canonicalize clamps in place).
func cloneIntp(p *int) *int {
	if p == nil {
		return nil
	}
	n := *p
	return &n
}

// withDefaultLabel unions a central board's literal `label` tag (if any) into a
// label set, so `add` on that board carries the tag without an explicit -l.
// Returns a copy; a no-op when no board label is set or it is already present.
func (a *App) withDefaultLabel(labels []string) []string {
	if a.DefaultLabel == "" || contains(labels, a.DefaultLabel) {
		return labels
	}
	return append(append([]string(nil), labels...), a.DefaultLabel)
}

// withBoardRepo unions the board-scope repo (a pointer's / central board's
// DefaultRepo) into a task's repos on add — the repos-field mirror of
// withDefaultLabel. Draft suppresses exactly this union (the task stays a
// draft); an explicit -r adds to the board repo rather than replacing it,
// mirroring the old label-union semantics. Returns a copy.
func (a *App) withBoardRepo(repos []string, draft bool) []string {
	if draft || a.DefaultRepo == "" || contains(repos, a.DefaultRepo) {
		return repos
	}
	return append(append([]string(nil), repos...), a.DefaultRepo)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
