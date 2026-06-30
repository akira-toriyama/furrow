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
// scope is derived from the board and the label mode is "auto".
const EnvBoard = "FURROW_BOARD"

// PointerName is a repo-local file that redirects furrow at a central board
// (and optionally scopes it to a label) instead of holding its own .furrow.
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

	DefaultLabel string // scope label from a pointer or a central board ("" otherwise)

	// ScopeWarnings are discovery-time notes bound for stderr (e.g. the global
	// default board activated but found no enclosing git repo for an auto label).
	ScopeWarnings []string
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
	a.ScopeWarnings = res.ScopeWarn
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
// reached via a pointer) the label to scope commands to.
type resolution struct {
	Dir          string
	DefaultLabel string
	ScopeWarn    []string
}

// discover finds the store: FURROW_DIR if set (no label injection), else walk up
// from startDir. At each directory a local .furrow wins; failing that, a
// .furrow-pointer.toml redirects to a central board and supplies its label.
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
	p, err := config.LoadPointer(pointerPath)
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
	return resolution{Dir: board, DefaultLabel: p.DefaultLabel}, nil
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
	label, lwarn := deriveScopeLabel(winner.Label, abs)
	return resolution{Dir: winBoard, DefaultLabel: label, ScopeWarn: append(warn, lwarn...)}, true, nil
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
		return []config.GlobalBoard{{Path: env, Scopes: nil, Label: "auto"}}, base, nil, nil
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

// deriveScopeLabel turns the configured label mode into the actual scope label:
// "auto" -> the nearest enclosing git repo's basename (empty + a warning when
// there is none), "" -> no label, anything else -> a literal label.
func deriveScopeLabel(mode, startDir string) (label string, warn []string) {
	switch mode {
	case "auto":
		if l, ok := nearestRepoLabel(startDir); ok {
			return l, nil
		}
		return "", []string{"furrow: default board active but no enclosing git repo; no auto label (use -l to scope)"}
	case "":
		return "", nil
	default:
		return mode, nil
	}
}

// nearestRepoLabel walks up from startDir looking for a directory holding a
// `.git` entry (a dir for a normal repo, a file for a worktree/submodule); the
// label is that directory's basename. It never shells out to git. Returns
// ("", false) when no git repo encloses startDir.
func nearestRepoLabel(startDir string) (string, bool) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return filepath.Base(dir), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Init creates a fresh .furrow at dir/.furrow (config.toml template + empty
// index.json + bodies/). It is an error if one already exists.
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
	Parent   string
	Deps     []string
	Refs     []string
	Body     string // initial body markdown; "" seeds a heading from the title
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

	idx, err := a.load()
	if err != nil {
		return nil, err
	}

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
		Labels: o.Labels, Parent: o.Parent, Deps: o.Deps, Refs: o.Refs,
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

// QueryOpts filters List. Zero values mean "no filter".
type QueryOpts struct {
	Status string
	Label  string
	Limit  int
}

// List returns tasks in canonical order, after applying the filters.
func (a *App) List(o QueryOpts) ([]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	var out []core.Task
	for _, t := range idx.Tasks {
		if o.Status != "" && t.Status != o.Status {
			continue
		}
		if o.Label != "" && !contains(t.Labels, o.Label) {
			continue
		}
		out = append(out, t)
		if o.Limit > 0 && len(out) >= o.Limit {
			break
		}
	}
	return out, nil
}

// Next returns the actionable tasks in canonical order — the work that is ready
// to pick up: status in the configured next-lanes ([next].lanes, default
// ready+in-progress) AND every dependency already done. A non-empty label
// restricts the result to tasks carrying that label (same semantics as List).
func (a *App) Next(label string, limit int) ([]core.Task, error) {
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
		if label != "" && !contains(t.Labels, label) {
			continue
		}
		if a.Cfg.IsNextLane(t.Status) && idx.Actionable(t, a.Cfg.Terminal, doneIDs) {
			out = append(out, *t)
			if limit > 0 && len(out) >= limit {
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

// SetTitle renames a task's one-line summary.
func (a *App) SetTitle(id, title string) (*core.Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, core.Validationf(id, "title must not be empty")
	}
	return a.mutate(id, func(t *core.Task) { t.Title = title })
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

// withDefaultLabel unions the pointer-provided default label (if any) into a
// label set, so `add` from a pointer-scoped repo tags the repo without an
// explicit -l. Returns a copy; a no-op when no pointer label is set or it is
// already present.
func (a *App) withDefaultLabel(labels []string) []string {
	if a.DefaultLabel == "" || contains(labels, a.DefaultLabel) {
		return labels
	}
	return append(append([]string(nil), labels...), a.DefaultLabel)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
