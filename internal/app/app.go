// Package app is the coordinator layer: it wires a Store and Config together
// and exposes every task mutation as a method. It is the ONLY mutation funnel —
// the CLI calls App, never the store directly. That keeps invariants
// (frozen ids, canonical order, closed-timestamp rules, body<->index pairing)
// in one place instead of scattered across the presentation layer.
package app

import (
	"context"
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

	// AutoCommit reports whether this board opted into post-mutation git commits
	// (the user-config [[board]] `autocommit` key; default false). When on, the
	// CLI runs AutoCommitFlush after a successful mutating command. See
	// autocommit.go.
	AutoCommit bool

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

	// Source records how the store was discovered — "env" (FURROW_DIR or
	// FURROW_BOARD), "local" (an ancestor .furrow), "pointer" (a
	// .furrow-pointer.toml), or "user-config" (a global [[board]]). `furrow
	// board` surfaces it so an agent sees why this store/scope is active.
	Source string

	// sleep is the backoff sleeper used by Sync's transient-rebase retry. nil
	// means the real cancellable timer (see ctxSleep); tests set a no-op to run
	// the retry budget instantly.
	sleep func(time.Duration)

	// bodiesTouched is the set of task ids whose bodies/<id>.md THIS process
	// created, modified, or deleted (see saveBody/deleteBody). AutoCommitFlush
	// passes it as SyncOpts.Bodies so autocommit commits the command's OWN body
	// edits (e.g. `furrow note`'s prose) even when the file is already tracked,
	// while partitionSync still leaves a co-located operator's untouched
	// tracked-dirty body alone. nil until the first body write.
	bodiesTouched map[string]bool
}

// ctxSleep waits d during Sync's transient-retry backoff, returning early with
// ctx.Err() if the context is cancelled mid-wait (a Ctrl-C / SIGTERM) — so the
// retry loops bail promptly instead of riding out the remaining budget. The real
// wait is a cancellable timer; tests inject a.sleep to run the budget instantly
// (it still honours an already-cancelled context).
func (a *App) ctxSleep(ctx context.Context, d time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if a.sleep != nil {
		a.sleep(d)
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
	a.AutoCommit = res.AutoCommit
	a.ScopeWarnings = res.ScopeWarn
	a.Source = res.Source
	if res.DefaultRepo != "" {
		a.BoardRepos = []string{res.DefaultRepo}
	}
	return a, nil
}

// DiscoverAliases returns the board config's [alias] table for the store
// enclosing startDir, or nil (never an error) when there is no store or no
// aliases — alias expansion must never break furrow where a real command would
// have worked. It reads only the config file (no store/task load), so it is
// cheap enough to run on every invocation before command dispatch.
func DiscoverAliases(startDir string) map[string]string {
	res, err := discover(startDir)
	if err != nil {
		return nil
	}
	cfg, _, err := config.Load(filepath.Join(res.Dir, "config.toml"))
	if err != nil {
		return nil
	}
	return cfg.Alias
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
	AutoCommit   bool   // git-commit .furrow/ after each mutating command (user-config [[board]] opt-in)
	ScopeWarn    []string
	Source       string // discovery mechanism: env|local|pointer|user-config
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
		return resolution{Dir: abs, Source: "env"}, nil
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return resolution{}, core.Internalf("", "resolve %q: %v", startDir, err)
	}
	for {
		cand := filepath.Join(dir, DirName)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return resolution{Dir: cand, Source: "local"}, nil
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
	return resolution{Dir: board, DefaultRepo: repo, AutoFilter: true, ScopeWarn: append(pwarn, rwarn...), Source: "pointer"}, nil
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
	// FURROW_BOARD enters through loadGlobalBoards as a synthetic board, so a
	// winning board is "env" when that override is set, else a real user-config
	// [[board]] entry.
	source := "user-config"
	if os.Getenv(EnvBoard) != "" {
		source = "env"
	}
	return resolution{Dir: winBoard, DefaultLabel: winner.Label, DefaultRepo: repo, AutoFilter: winner.AutoFilter, AutoCommit: winner.AutoCommit, ScopeWarn: append(warn, rwarn...), Source: source}, true, nil
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

// GitAttributesTemplate is the .furrow/.gitattributes furrow init scaffolds.
// Bodies are append-mostly prose: the task-status bot appends marker lines
// while a session appends notes, and two EOF-adjacent appends conflict on
// every pull --rebase (t-44h4). git's built-in union merge driver keeps both
// sides instead — a body never has a meaningful textual conflict to hand a
// human. Shards stay OUT: union on JSON would corrupt it, and the shard
// conflict IS meaningful (two writers disagreeing about one task).
const GitAttributesTemplate = `# machine-written by furrow init — see 'multi-machine sync' in the README.
# Bodies are append-mostly prose; let git fold concurrent appends together
# (the task-status marker × local note race) instead of conflicting.
bodies/*.md merge=union
archive/bodies/*.md merge=union
`

// Init creates a fresh .furrow at dir/.furrow (config.toml template + an empty
// tasks/ shard dir + meta.json + bodies/ + the union-merge .gitattributes). It
// is an error if one already exists. The tasks/ dir and meta.json are
// provisioned by the first Store.Save.
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
	if err := os.WriteFile(filepath.Join(fdir, ".gitattributes"), []byte(GitAttributesTemplate), 0o644); err != nil {
		return nil, core.Internalf("", "write .gitattributes: %v", err)
	}
	a, err := openAt(fdir)
	if err != nil {
		return nil, err
	}
	// The store stamps meta.json itself on a fresh board. Naming the binary's
	// layout version here would be both decorative (Save ignores the field) and a
	// second place that knows how to raise a board. There is exactly one, and it
	// is `furrow upgrade` — see scripts/check-schema-write-guard.sh.
	if err := a.Store.Save(&core.Index{Tasks: []core.Task{}}); err != nil {
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
	// Checklist seeds unchecked checklist items at creation (repeatable --check).
	// A plain `add --body '- [ ] x'` does NOT populate the shard's checklist —
	// the body is prose — so this makes a seed-time checklist first-class. Blank
	// entries are dropped; text is taken verbatim (commas included), like
	// `check --add`.
	Checklist []string
	// Draft marks the task as deliberately repo-less (repos == [], the
	// issue-draft analogue). It conflicts with explicit Repos, and it
	// suppresses exactly the board-scope repo union (see withBoardRepo) — the
	// escape hatch for "note this on the board, attach it later".
	Draft bool
	// Type is the work-item type ([types].order). "" leaves the shard type-less
	// (it reads as the configured default). A non-empty value must be in the
	// vocabulary or Add returns unknownTypeErr (exit 2 + candidates).
	Type string
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
		return nil, a.unknownLaneErr("", status)
	}
	if a.Cfg.LabelsRequired && len(o.Labels) == 0 {
		return nil, core.Validationf("", "a label is required ([labels].required); add -l <label>")
	}
	if o.Draft && len(o.Repos) > 0 {
		return nil, core.Validationf("", "--draft cannot be combined with an explicit repo (-r): a draft is attached to no repo")
	}
	if !a.Cfg.IsType(o.Type) {
		return nil, a.unknownTypeErr("", o.Type)
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

	// A --dep/--parent must name a task that exists, the same contract AddDep
	// enforces — accepting a dangling one silently drops the task out of `next`
	// (an unknown dep reads as unsatisfied) with no error.
	for _, dep := range o.Deps {
		if !idx.Has(dep) {
			return nil, core.Validationf("", "dependency %q does not exist", dep)
		}
	}
	if o.Parent != "" && !idx.Has(o.Parent) {
		return nil, core.Validationf("", "parent %q does not exist", o.Parent)
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
	// A task born directly in the done lane is closed at birth — otherwise it is a
	// closed:null zombie that `done` no-ops on and `archive` skips forever (Move
	// backfills the same field for an already-parked one; lint flags any leak).
	var closed *time.Time
	if status == a.Cfg.DoneLane {
		closed = &now
	}
	var checklist []core.ChecklistItem
	for _, text := range o.Checklist {
		if strings.TrimSpace(text) == "" {
			continue // drop blank --check values, like check --add
		}
		checklist = append(checklist, core.ChecklistItem{Text: text})
	}
	t := core.Task{
		ID: id, Title: title, Status: status, Priority: prio,
		Value: cloneIntp(o.Value), Effort: cloneIntp(o.Effort),
		Labels: o.Labels, Repos: repos, Parent: o.Parent, Deps: o.Deps, Refs: o.Refs,
		Checklist: checklist,
		Created:   now, Updated: now, Closed: closed, Body: core.BodyPath(id),
		Type: o.Type,
	}
	idx.Add(t)

	body := o.Body
	if body == "" {
		body = "# " + title + "\n"
	}
	if err := a.saveBody(id, body); err != nil {
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
func (a *App) GetBatch(ids []string, withBody bool) ([]ShowItem, []string, error) {
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	return getBatchFrom(idx, a.Store.LoadBody, ids, withBody)
}

// getBatchFrom resolves ids against idx in input order (duplicates collapse to
// their first occurrence, misses collected), loading each found task's body via
// loadBody when withBody. It is the shared core of the hot GetBatch and the
// archive GetBatchArchived, so both reads behave identically.
func getBatchFrom(idx *core.Index, loadBody func(string) (string, error), ids []string, withBody bool) ([]ShowItem, []string, error) {
	items, missing := []ShowItem{}, []string{}
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
			b, err := loadBody(id)
			if err != nil {
				return nil, nil, err
			}
			body = b
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
	Type      string // filter by work-item type ([types].order); "" = no type filter
	ScopeRepo string // board-scope repo (a pointer's / central board's DefaultRepo)
	Repo      string // owner/repo filter on the repos field (already resolved)
	Drafts    bool   // only tasks with repos == []; ignores ScopeRepo/Repo
	// Since/Until filter on the Updated timestamp (inclusive bounds); nil = no
	// bound. Only `ls` wires these today, but they live here so any read that
	// funnels through match can gain a date window without a second predicate.
	Since *time.Time
	Until *time.Time
	// Sort re-orders List's result by one of core.SortFields (empty = canonical
	// lane->priority->id order); Reverse flips the default "most first" direction.
	Sort    string
	Reverse bool
	// Archived reads from the sibling .furrow/archive/ store instead of the hot
	// index (the `ls --archived` browse of retired tasks). The same filters/sort
	// apply; only the source index changes.
	Archived bool
	// IncludeContainers relaxes `furrow next`: by default a container type (an
	// epic) is never handed out as work; with this set, a container that is
	// otherwise ready (in a next lane, deps done) is surfaced too (the Jira-style
	// "show me the boxes" escape hatch). Only `next` reads it; List/Tree ignore it.
	IncludeContainers bool
	// Actionable / Blocked are the `ls` derived-state filters, orthogonal to (and
	// ANDing with) the lane/label/repo scope. Actionable keeps only tasks `furrow
	// next` would hand you (a next lane, every dep done, not a container); Blocked
	// keeps only tasks with an unsatisfied dep (a non-empty blocked_by). They are
	// disjoint by construction, so the CLI marks them mutually exclusive. List
	// (hence Tree) honours them; Next/Revisit ignore them.
	Actionable bool
	Blocked    bool
	// Lanes is a one-shot override of the configured [next].lanes for `furrow next`
	// (the --lanes flag): when non-empty, next tests lane membership against THESE
	// lanes instead of the config's, leaving config untouched (non-destructive).
	// nil/empty = use the configured next-lanes. Only Next reads it.
	Lanes []string
	// Query is the raw `-q` typed-query string (empty = no query filter). It is
	// parsed (internal/query) and compiled against the loaded index into a per-task
	// predicate that ANDs with every other filter here, so a query never widens a
	// scoped board. Reads that funnel through listMatched honor it.
	Query string
	Limit int
}

// match reports whether t passes the query's filters (Limit excluded — that is
// the iteration's job). In Drafts mode only repo-less tasks pass and the board
// scope is bypassed; Status and Label still apply. Note a repo-scoped read
// (ScopeRepo or Repo set) hides drafts — the CLI's hidden-drafts hint exists
// for exactly that.
func (o QueryOpts) match(t *core.Task) bool {
	if !matchAnyLane(o.Status, t.Status) {
		return false
	}
	if !matchAnyLabel(o.Label, t.Labels) {
		return false
	}
	// The date window is a field filter like Status/Label, so it applies even in
	// Drafts mode (before the draft short-circuit below).
	if o.Since != nil && t.Updated.Before(*o.Since) {
		return false
	}
	if o.Until != nil && t.Updated.After(*o.Until) {
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

// List returns tasks after applying the filters, in canonical
// lane->priority->id order unless o.Sort re-orders them. A -s filter naming an
// unknown lane, or an unknown --sort field, fails fast (rather than silently
// returning [] / ignoring the flag) — `ls` is the only read carrying these, so
// this is its guard. With a sort, Limit applies AFTER ordering (the top N of the
// sorted set), so it collects all matches first; without a sort the result is
// identical to the old canonical-order-first-N.
func (a *App) List(o QueryOpts) ([]core.Task, error) {
	tasks, _, err := a.listMatched(o)
	return tasks, err
}

// listMatched is List's engine, returning the matched+sorted+limited tasks AND
// the loaded index — so ListItems can enrich the exact same result set without a
// second load. The --actionable/--blocked derived-state filters apply here, BEFORE
// the limit, so `-n` caps the filtered set (not the pre-filter one); computing
// doneIDs is skipped unless one of those filters is set.
func (a *App) listMatched(o QueryOpts) ([]core.Task, *core.Index, error) {
	if err := a.validateLaneFilter(o.Status); err != nil {
		return nil, nil, err
	}
	if err := validateSortField(o.Sort); err != nil {
		return nil, nil, err
	}
	if err := a.validateTypeFilter(o.Type); err != nil {
		return nil, nil, err
	}
	idx, err := a.listIndex(o)
	if err != nil {
		return nil, nil, err
	}
	var doneIDs map[string]bool
	if o.Actionable || o.Blocked {
		doneIDs = a.doneSet(idx)
	}
	// Compile -q once (against the loaded index) into a per-task predicate; a
	// parse/validation fault fails the whole read with exit 2 before any output.
	var qpred func(*core.Task) bool
	if o.Query != "" {
		p, err := a.compileQuery(o.Query, idx)
		if err != nil {
			return nil, nil, err
		}
		qpred = p
	}
	var out []core.Task
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) || !a.matchType(o, t) {
			continue
		}
		if o.Actionable && !a.actionable(idx, t, doneIDs) {
			continue
		}
		if o.Blocked && len(blockedDeps(t, doneIDs)) == 0 {
			continue
		}
		if qpred != nil && !qpred(t) {
			continue
		}
		out = append(out, *t)
	}
	if o.Sort != "" {
		core.SortTasks(out, o.Sort, o.Reverse)
	}
	if o.Limit > 0 && len(out) > o.Limit {
		out = out[:o.Limit]
	}
	return out, idx, nil
}

// ListItem is a task plus the derived facts `ls` exposes on every row — the same
// two the tree carries (actionable, blocked_by) plus the container roll-up (stuck),
// so the flat list answers "what can I pick up / what's in the way" without a
// separate `--tree`. Container rides along because the glyph distinguishes a box.
type ListItem struct {
	Task       core.Task
	Actionable bool
	BlockedBy  []string
	Container  bool
	Stuck      bool
}

// ListItems is List enriched with per-row derived facts (actionable / blocked_by /
// container / stuck), for the flat `ls` human table (the glyph) and its --json.
// It reuses listMatched's single load, and computes each fact through the SAME
// helpers the tree uses (factsFor / isStuck), so flat and tree never disagree.
func (a *App) ListItems(o QueryOpts) ([]ListItem, error) {
	tasks, idx, err := a.listMatched(o)
	if err != nil {
		return nil, err
	}
	doneIDs := a.doneSet(idx)
	var kids map[string][]*core.Task // built lazily: only a container needs it
	items := make([]ListItem, 0, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		actionable, blockedBy, container := a.factsFor(idx, t, doneIDs)
		stuck := false
		if container {
			if kids == nil {
				kids = childrenMap(idx)
			}
			stuck = a.isStuck(idx, t.ID, kids, doneIDs)
		}
		items = append(items, ListItem{Task: *t, Actionable: actionable, BlockedBy: blockedBy, Container: container, Stuck: stuck})
	}
	return items, nil
}

// doneSet returns the ids in the done lane — the shared input to every readiness
// predicate (actionable, blocked_by, stuck).
func (a *App) doneSet(idx *core.Index) map[string]bool {
	done := make(map[string]bool)
	for i := range idx.Tasks {
		if idx.Tasks[i].Status == a.Cfg.DoneLane {
			done[idx.Tasks[i].ID] = true
		}
	}
	return done
}

// validateTypeFilter rejects an unknown --type filter with the configured types
// in Candidates (symmetric with the unknown-lane guard) — a typo'd `--type epci`
// must not silently return []. Empty = no type filter, always valid.
func (a *App) validateTypeFilter(typ string) error {
	if typ == "" || a.Cfg.IsType(typ) {
		return nil
	}
	return a.unknownTypeErr("", typ)
}

// matchType applies a --type filter by EFFECTIVE type, so `--type task` matches
// the type-less majority (whose effective type is the default) as well as tasks
// explicitly typed "task". "" = no filter.
func (a *App) matchType(o QueryOpts, t *core.Task) bool {
	return o.Type == "" || a.Cfg.EffectiveType(t.Type) == o.Type
}

// validateSortField rejects an unknown --sort key with the valid fields in
// Candidates (symmetric with the unknown-lane guard) — a typo must not silently
// fall back to canonical order. Empty = no sort, always valid.
func validateSortField(field string) error {
	if field == "" || core.IsSortField(field) {
		return nil
	}
	return &core.Error{
		Code:       core.CodeValidation,
		Msg:        fmt.Sprintf("unknown sort field %q (valid: %s)", field, strings.Join(core.SortFields, ", ")),
		Candidates: append([]string(nil), core.SortFields...),
	}
}

// Next returns the actionable tasks in canonical order — the work that is ready
// to pick up: status in the configured next-lanes ([next].lanes, default
// ready+in-progress) AND every dependency already done AND not a container type
// (an epic is a box, not work — surface boxes with o.IncludeContainers). The
// query's filters restrict the result with the same semantics as List.
func (a *App) Next(o QueryOpts) ([]core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	doneIDs := a.doneSet(idx)
	// inNextLane is normally the configured [next].lanes membership; --lanes
	// (o.Lanes) overrides it for this call ONLY — a non-destructive, in-memory
	// swap that never rewrites config. The deps-done half of the predicate
	// (idx.Actionable) is unchanged and shared, so `--lanes` widens WHICH lanes
	// count as "now", not what "ready" means.
	inNextLane := a.Cfg.IsNextLane
	if len(o.Lanes) > 0 {
		// A --lanes token is an explicit CLI arg, so an unknown one fails fast with
		// the configured lanes in candidates (symmetric with `ls -s`) rather than
		// silently matching nothing.
		override := make(map[string]bool, len(o.Lanes))
		for _, l := range o.Lanes {
			if !a.Cfg.IsLane(l) {
				return nil, a.unknownLaneErr("", l)
			}
			override[l] = true
		}
		inNextLane = func(lane string) bool { return override[lane] }
	}
	var out []core.Task
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		if !o.match(t) {
			continue
		}
		// "Ready" = in a next lane AND every dep done. The container is excluded
		// unless --containers is set (a box is not work) — the same rule
		// App.actionable/workable encode, expressed here against the (possibly
		// overridden) lane set so `ls --tree`'s ★ and a plain `next` still agree.
		ready := inNextLane(t.Status) && idx.Actionable(t, a.Cfg.Terminal, doneIDs)
		if ready && !o.IncludeContainers {
			ready = !a.Cfg.IsContainerType(t.Type)
		}
		if ready {
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
// parked is not the same as closed. Keying the stamp on Closed==nil (not on the
// lane transition) also backfills a closed:null zombie: `done` on a task already
// parked in the done lane with no timestamp now stamps one instead of no-opping.
func (a *App) Move(id, lane string) (*core.Task, error) {
	if !a.Cfg.IsLane(lane) {
		return nil, a.unknownLaneErr(id, lane)
	}
	return a.mutate(id, func(t *core.Task) { a.applyLane(t, lane) })
}

// applyLane sets t.Status to lane and keeps Closed consistent: it stamps Closed
// on entering the done lane (when unset — also backfilling a zombie), clears it
// on leaving done, and leaves it alone for other terminal lanes (parked ≠
// closed). Shared by Move and Set so the two can never diverge on the rule.
func (a *App) applyLane(t *core.Task, lane string) {
	was := t.Status
	t.Status = lane
	switch {
	case lane == a.Cfg.DoneLane && t.Closed == nil:
		now := a.Clock.Now()
		t.Closed = &now
	case lane != a.Cfg.DoneLane && was == a.Cfg.DoneLane:
		t.Closed = nil
	}
}

// Done moves a task into the done lane (and stamps Closed via Move).
func (a *App) Done(id string) (*core.Task, error) { return a.Move(id, a.Cfg.DoneLane) }

// MoveMany sets the lane on several tasks in ONE index write, all-or-nothing:
// every id is resolved before anything is touched, so a failed batch never
// half-lands (a write must not partially succeed the way a batch READ may —
// GetBatch's missing-is-data contract stops at mutations). An unknown lane is
// the usual exit-2 candidates error; any missing ids fail the whole batch with
// exit 1 and ALL of them in details.missing (the show batch shape, so agents
// branch identically). Duplicates collapse to their first occurrence and
// results come back in input order. The single Save is the point: a triage
// sweep over five tasks is one write, not five.
func (a *App) MoveMany(ids []string, lane string) ([]*core.Task, error) {
	return a.moveMany(ids, lane, "")
}

// moveMany is MoveMany plus an optional note appended to every moved task's
// body (skipped when empty). Bodies are written only after every id has
// resolved, so a failed batch touches neither lanes nor prose.
func (a *App) moveMany(ids []string, lane, note string) ([]*core.Task, error) {
	if !a.Cfg.IsLane(lane) {
		return nil, a.unknownLaneErr("", lane)
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	order, missing := []string{}, []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, i := idx.Find(id); i < 0 {
			missing = append(missing, id)
			continue
		}
		order = append(order, id)
	}
	if len(missing) > 0 {
		return nil, &core.Error{
			Code:    core.CodeNotFound,
			Msg:     fmt.Sprintf("%d of %d ids not found — nothing was moved", len(missing), len(order)+len(missing)),
			Details: map[string]any{"missing": missing},
		}
	}
	now := a.Clock.Now()
	for _, id := range order {
		if note != "" {
			if err := a.appendBody(id, note); err != nil {
				return nil, err
			}
		}
		t, _ := idx.Find(id)
		a.applyLane(t, lane)
		t.Updated = now
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	out := make([]*core.Task, 0, len(order))
	for _, id := range order {
		saved, _ := idx.Find(id)
		out = append(out, saved)
	}
	return out, nil
}

// DoneMany moves several tasks into the done lane in one write (stamping
// Closed on each via moveMany's applyLane).
func (a *App) DoneMany(ids []string) ([]*core.Task, error) {
	return a.moveMany(ids, a.Cfg.DoneLane, "")
}

// DoneNote closes a task AND appends a closing note to its body — the
// done+note ritual ("→ continued in t-xxx", the close-time one-liner) as a
// single command with one Updated stamp. The note follows AddNote's contract
// (a new paragraph, never deduped); an empty note is bad usage, never a
// silent plain close.
func (a *App) DoneNote(id, note string) (*core.Task, error) {
	note, err := normalizeNote(id, note)
	if err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	if err := a.appendBody(id, note); err != nil {
		return nil, err
	}
	a.applyLane(t, a.Cfg.DoneLane)
	t.Updated = a.Clock.Now()
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	saved, _ := idx.Find(id)
	return saved, nil
}

// DoneManyNote is DoneNote over a batch: the SAME note lands on every task's
// body ("superseded by t-yyy", "shipped in repo#42") and the whole close is
// one all-or-nothing index write, MoveMany's contract.
func (a *App) DoneManyNote(ids []string, note string) ([]*core.Task, error) {
	note, err := normalizeNote("", note)
	if err != nil {
		return nil, err
	}
	return a.moveMany(ids, a.Cfg.DoneLane, note)
}

// Reorder sets a task's absolute priority.
func (a *App) Reorder(id string, priority int) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) { t.Priority = priority })
}

// ReorderRelative places id immediately before (or after) ref in ref's lane,
// without the caller computing a priority. Both tasks must share a lane. When
// the sparse gap next to ref is exhausted, the whole lane is respaced in the
// SAME write (plan first, then apply, single Save — all-or-nothing like the dep
// commands), and the neighbors' moves are returned so the CLI can report them.
// Only id's Updated advances: a respace is positional bookkeeping on the
// neighbors, not progress, so it must not disturb staleness signals.
func (a *App) ReorderRelative(id, ref string, before bool) (*core.Task, []core.PriorityChange, error) {
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	target, changes, err := idx.PlanRelativePriority(id, ref, before, a.Cfg.PriorityDefault, a.Cfg.PriorityStep)
	if err != nil {
		return nil, nil, err
	}
	t, _ := idx.Find(id)
	t.Priority = target
	t.Updated = a.Clock.Now()
	for _, c := range changes {
		ct, _ := idx.Find(c.ID)
		ct.Priority = c.To
	}
	if err := a.Store.Save(idx); err != nil {
		return nil, nil, err
	}
	saved, _ := idx.Find(id)
	return saved, changes, nil
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
// Retitle from the CLI so the body's heading is kept in step.
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
		if err := a.saveBody(id, next); err != nil {
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

// RemoveCheck deletes the checklist item at the zero-based index. An
// out-of-range index is a validation error (never a silent no-op), mirroring
// Check — so an agent's exit code and envelope honor the contract.
func (a *App) RemoveCheck(id string, item int) (*core.Task, error) {
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
	return a.mutate(id, func(t *core.Task) {
		t.Checklist = append(t.Checklist[:item], t.Checklist[item+1:]...)
	})
}

// RewordCheck replaces the text of the checklist item at the zero-based index,
// preserving its done state. Out-of-range index and empty text are validation
// errors.
func (a *App) RewordCheck(id string, item int, text string) (*core.Task, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, core.Validationf(id, "checklist item text must not be empty")
	}
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
	return a.mutate(id, func(t *core.Task) { t.Checklist[item].Text = text })
}

// AddDep makes `id` depend on `dep` (id waits on dep). Both ids must exist, a
// task may not depend on itself, and the edge must not create a cycle (dep must
// not already depend on id, directly or transitively). Re-adding an existing
// dep is a no-op; the marshaller keeps the dep list sorted and de-duplicated.
func (a *App) AddDep(id, dep string) (*core.Task, error) { return a.AddDeps(id, []string{dep}) }

// AddDeps adds several dependencies to `id` in one write (`dep a b c`). Every
// dep is validated against the same contract AddDep enforced — must exist, must
// not be `id` itself, must not create a cycle (checked against the graph as it
// grows, so an in-batch edge counts) — and a dep already present is a no-op.
// Validation is all-or-nothing: the first bad dep returns before any save, so a
// partial batch never lands. The marshaller keeps the dep list sorted+deduped.
func (a *App) AddDeps(id string, deps []string) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	for _, dep := range deps {
		if id == dep {
			return nil, core.Validationf(id, "a task cannot depend on itself")
		}
		if !idx.Has(dep) {
			return nil, core.Validationf(id, "dependency %q does not exist", dep)
		}
		if idx.DependsOn(dep, id) {
			return nil, core.Validationf(id, "adding dep %q would create a cycle (%s already depends on %s)", dep, dep, id)
		}
		if !contains(t.Deps, dep) {
			t.Deps = append(t.Deps, dep)
		}
	}
	t.Updated = a.Clock.Now()
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	saved, _ := idx.Find(id)
	return saved, nil
}

// RemoveDep drops `dep` from `id`'s dependency list. It is a validation error
// when id has no such dependency, so the result is never a silent no-op.
func (a *App) RemoveDep(id, dep string) (*core.Task, error) { return a.RemoveDeps(id, []string{dep}) }

// RemoveDeps drops several dependencies from `id` in one write. Each must be a
// current dependency (else a validation error naming it — never a silent no-op),
// and the whole batch is validated before any change, so a bad id aborts without
// a partial removal.
func (a *App) RemoveDeps(id string, deps []string) (*core.Task, error) {
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	rm := make(map[string]bool, len(deps))
	for _, dep := range deps {
		if !contains(t.Deps, dep) {
			return nil, core.Validationf(id, "%q is not a dependency of %s", dep, id)
		}
		rm[dep] = true
	}
	kept := make([]string, 0, len(t.Deps))
	for _, d := range t.Deps {
		if !rm[d] {
			kept = append(kept, d)
		}
	}
	t.Deps = kept
	t.Updated = a.Clock.Now()
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	saved, _ := idx.Find(id)
	return saved, nil
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
	next := labelDelta(t.Labels, add, remove)
	if a.Cfg.LabelsRequired && len(next) == 0 {
		return nil, core.Validationf(id, "a label is required ([labels].required); this relabel would remove the last one")
	}
	return a.mutate(id, func(t *core.Task) { t.Labels = next })
}

// Reref adds and/or removes refs (file:line or URL pointers) on a task, the
// after-the-fact edit for what `add --ref` sets at creation. Adding a ref
// already present, and removing one already absent, are both no-ops
// (idempotent) so re-runs don't churn the diff; a call with neither --add nor
// --rm is a bad-usage error rather than a silent no-op. Unlike labels, refs
// are a user-ordered SEQUENCE (the marshaller deliberately does not sort
// them), so survivors keep their order and adds append at the end.
func (a *App) Reref(id string, add, remove []string) (*core.Task, error) {
	if len(add) == 0 && len(remove) == 0 {
		return nil, core.Validationf(id, "provide at least one --add or --rm ref")
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	next := labelDelta(t.Refs, add, remove)
	return a.mutate(id, func(t *core.Task) { t.Refs = next })
}

// labelDelta returns cur with every entry in remove dropped and every entry in
// add unioned on (idempotent — an add already present, or a remove already
// absent, is a no-op). Survivors keep their order, then the adds; the marshaller
// sorts+dedupes on write, so in-memory order is immaterial. Shared by Relabel
// and Set.
func labelDelta(cur, add, remove []string) []string {
	rm := make(map[string]bool, len(remove))
	for _, l := range remove {
		rm[l] = true
	}
	next := make([]string, 0, len(cur)+len(add))
	for _, l := range cur {
		if !rm[l] {
			next = append(next, l)
		}
	}
	for _, l := range add {
		if !contains(next, l) {
			next = append(next, l)
		}
	}
	return next
}

// SetOpts is the combined-edit payload for Set — the routine triage edits
// (lane, priority, value, effort, labels, type) in one write. A nil pointer /
// empty slice / false flag means "leave that facet alone"; ClearValue/
// ClearEffort explicitly unset an estimate (distinct from "leave alone").
// Priority sets the sparse integer directly; Before/After compute it relative
// to a lane-mate in the DESTINATION lane (so a cross-column drop — lane plus
// position — is one write: `-s <lane> --before <ref>`); the three are mutually
// exclusive. Deps and repos keep their own commands.
type SetOpts struct {
	Status      *string  // move to this lane (validated like Move)
	Priority    *int     // set the sparse priority directly
	Before      string   // place immediately before this task (in the destination lane)
	After       string   // place immediately after this task (in the destination lane)
	Value       *int     // set the value estimate
	ClearValue  bool     // unset the value estimate (wins over Value)
	Effort      *int     // set the effort estimate
	ClearEffort bool     // unset the effort estimate (wins over Effort)
	AddLabels   []string // labels to union on
	RmLabels    []string // labels to drop
	Type        *string  // set the work-item type (validated against [types].order)
}

// empty reports whether o requests no change at all — Set rejects that rather
// than silently touching only the `updated` stamp.
func (o SetOpts) empty() bool {
	return o.Status == nil && o.Priority == nil && o.Before == "" && o.After == "" &&
		o.Value == nil && !o.ClearValue &&
		o.Effort == nil && !o.ClearEffort && len(o.AddLabels) == 0 && len(o.RmLabels) == 0 &&
		o.Type == nil
}

// Set applies several triage edits to one task in a single load/save: move a
// lane, position it (absolute priority, or relative to a lane-mate), set/clear
// value and effort, and add/remove labels — so `set` replaces the
// move+reorder+value+effort+label dance without that many separate writes (and
// `updated` stamps). Everything is validated up front (unknown lane → exit 2
// with candidates like Move; a change that would strip the last label under
// [labels].required → exit 2; a relative target outside the destination lane →
// exit 2), then applied atomically — a relative placement that has to respace
// the lane lands in the SAME write, and the neighbors' moves are returned for
// the CLI's `renumbered` report (their Updated deliberately does not advance).
// At least one change is required.
func (a *App) Set(id string, o SetOpts) (*core.Task, []core.PriorityChange, error) {
	if o.Status != nil && !a.Cfg.IsLane(*o.Status) {
		return nil, nil, a.unknownLaneErr(id, *o.Status)
	}
	if o.Type != nil && !a.Cfg.IsType(*o.Type) {
		return nil, nil, a.unknownTypeErr(id, *o.Type)
	}
	if o.empty() {
		return nil, nil, core.Validationf(id, "set needs at least one change (-s / --priority / --before / --after / --value / --effort / --clear-value / --clear-effort / --add-label / --rm-label / --type)")
	}
	relRef, relBefore := o.Before, true
	if relRef == "" {
		relRef, relBefore = o.After, false
	}
	if (o.Priority != nil && relRef != "") || (o.Before != "" && o.After != "") {
		return nil, nil, core.Validationf(id, "--priority, --before, and --after are mutually exclusive")
	}
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, nil, core.NotFound(id)
	}
	// Pre-flight the relative placement against the DESTINATION lane before any
	// mutation, so a bad target aborts with nothing half-applied (the plan
	// itself re-checks after the lane move, when id and ref must already
	// agree).
	if relRef != "" {
		if relRef == id {
			return nil, nil, core.Validationf(id, "--before/--after must name a different task")
		}
		rt, ri := idx.Find(relRef)
		if ri < 0 {
			return nil, nil, core.NotFound(relRef)
		}
		dest := t.Status
		if o.Status != nil {
			dest = *o.Status
		}
		if rt.Status != dest {
			return nil, nil, core.Validationf(id, "relative target %s is in lane %q, not the destination lane %q — relative order only exists within one lane", relRef, rt.Status, dest)
		}
	}
	nextLabels := t.Labels
	if len(o.AddLabels) > 0 || len(o.RmLabels) > 0 {
		nextLabels = labelDelta(t.Labels, o.AddLabels, o.RmLabels)
		if a.Cfg.LabelsRequired && len(nextLabels) == 0 {
			return nil, nil, core.Validationf(id, "a label is required ([labels].required); this set would remove the last one")
		}
	}
	if o.Status != nil {
		a.applyLane(t, *o.Status)
	}
	var renumbered []core.PriorityChange
	switch {
	case o.Priority != nil:
		t.Priority = *o.Priority
	case relRef != "":
		target, changes, err := idx.PlanRelativePriority(id, relRef, relBefore, a.Cfg.PriorityDefault, a.Cfg.PriorityStep)
		if err != nil {
			return nil, nil, err
		}
		t.Priority = target
		for _, c := range changes {
			ct, _ := idx.Find(c.ID)
			ct.Priority = c.To
		}
		renumbered = changes
	}
	switch {
	case o.ClearValue:
		t.Value = nil
	case o.Value != nil:
		t.Value = cloneIntp(o.Value)
	}
	switch {
	case o.ClearEffort:
		t.Effort = nil
	case o.Effort != nil:
		t.Effort = cloneIntp(o.Effort)
	}
	if o.Type != nil {
		t.Type = *o.Type
	}
	t.Labels = nextLabels
	t.Updated = a.Clock.Now()
	if err := a.Store.Save(idx); err != nil {
		return nil, nil, err
	}
	saved, _ := idx.Find(id)
	return saved, renumbered, nil
}

// AddCheck appends a checklist item.
func (a *App) AddCheck(id, text string) (*core.Task, error) {
	return a.AddChecks(id, []string{text})
}

// AddChecks appends several checklist items in one write, so `check --add A
// --add B` records both (a repeated flag kept only the last before). Items are
// appended verbatim, preserving order and any commas in the text.
func (a *App) AddChecks(id string, items []string) (*core.Task, error) {
	return a.mutate(id, func(t *core.Task) {
		for _, text := range items {
			t.Checklist = append(t.Checklist, core.ChecklistItem{Text: text})
		}
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
		if err := a.saveBody(id, ""); err != nil {
			return "", err
		}
	}
	p := a.Store.BodyFile(id)
	if p == "" {
		return "", core.Internalf(id, "this store is not file-backed; cannot edit")
	}
	return p, nil
}

// AddNote appends text as a new paragraph to a task's body AND stamps Updated,
// in one call — the body is written first, then the shard. It is the in-band way
// to record progress / stop-points / next steps across sessions. The two writes
// are NOT transactional (this per-file-rename store has no cross-file txn), but
// the ordering is the safe one: if the shard write fails after the body was
// written, the note is saved without Updated advancing — a partial failure costs
// a timestamp, never content, and a re-run re-appends and fixes Updated.
//
// It differs from AppendBody (the `apply` annotation helper) on both counts,
// deliberately: AppendBody dedupes an identical line and leaves Updated alone,
// because a re-run of `apply` for the same PR event must be idempotent. A note
// is the opposite — a progress note repeating an earlier one is still a distinct
// event, so it always appends — and it MUST move Updated, because the body is
// the task's content. Hand-editing the file (via EditPath) does not touch the
// shard, which lets Updated go stale and makes lint's reconcile-gap (a dep's
// Closed time vs. Updated) misfire on a task whose progress was recorded only in
// prose; a note keeps Updated honest, so that check stays trustworthy.
//
// NotFound (exit 1) when id names no task; an empty/whitespace-only note is a
// validation error (exit 2).
func (a *App) AddNote(id, text string) (*core.Task, error) {
	text, err := normalizeNote(id, text)
	if err != nil {
		return nil, err
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	if err := a.appendBody(id, text); err != nil {
		return nil, err
	}
	t.Updated = a.Clock.Now()
	if err := a.Store.Save(idx); err != nil {
		return nil, err
	}
	saved, _ := idx.Find(id)
	return saved, nil
}

// normalizeNote trims a note's trailing newlines and rejects an
// empty/whitespace note as bad usage (never a silent no-op append). Shared by
// AddNote and the done --note paths so the two can never diverge on what
// counts as a note.
func normalizeNote(id, text string) (string, error) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return "", core.Validationf(id, "note text is empty")
	}
	return text, nil
}

// appendBody appends text to a task's body as a new paragraph, separated from
// existing content by exactly one blank line, whatever the body's current
// trailing whitespace.
func (a *App) appendBody(id, text string) error {
	body, err := a.Store.LoadBody(id)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(body)
	if body != "" {
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
		if !strings.HasSuffix(body, "\n\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString(text)
	b.WriteString("\n")
	return a.saveBody(id, b.String())
}

// markBodyTouched records that this process created, modified, or deleted the
// body of task id — the set AutoCommitFlush passes as SyncOpts.Bodies so
// autocommit commits the command's OWN body edits even when the file is already
// tracked (see App.bodiesTouched and autocommit.go). Cheap by design so every
// body write can call it unconditionally, whether or not autocommit is on.
func (a *App) markBodyTouched(id string) {
	if a.bodiesTouched == nil {
		a.bodiesTouched = map[string]bool{}
	}
	a.bodiesTouched[id] = true
}

// saveBody persists a task's body through the store AND records the id as
// touched. It is the app-layer funnel for every HOT-store body write; the
// archive substore's bodies land as machine-written archive/ paths (outside
// bodies/) and so need no tracking.
func (a *App) saveBody(id, body string) error {
	if err := a.Store.SaveBody(id, body); err != nil {
		return err
	}
	a.markBodyTouched(id)
	return nil
}

// deleteBody removes a task's body through the store AND records the id as
// touched, so an archive's hot-side body deletion rides in the SAME autocommit
// as the archive/ copy it was moved to — rather than being classified as a
// tracked-dirty pending body and left out of the commit.
func (a *App) deleteBody(id string) error {
	if err := a.Store.DeleteBody(id); err != nil {
		return err
	}
	a.markBodyTouched(id)
	return nil
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

// matchAnyLane reports whether lane satisfies the -s filter. A comma splits the
// filter into an OR-set: an empty filter (or one that trims to no tokens) is no
// constraint; otherwise lane must equal one of the trimmed, non-empty tokens.
// Unknown tokens are rejected upstream (validateLaneFilter, called by List), so
// this membership pass never has to distinguish "unknown" from "no match".
// Re-splitting per task is negligible at furrow's board scale.
func matchAnyLane(filter, lane string) bool {
	matched := false
	any := false
	for _, tok := range strings.Split(filter, ",") {
		if tok = strings.TrimSpace(tok); tok == "" {
			continue
		}
		any = true
		if lane == tok {
			matched = true
		}
	}
	return !any || matched
}

// unknownLaneErr is the shared "unknown lane" validation error. Every lane gate
// (add -s, move, ls -s) returns it, so the message is identical and the
// configured lanes ride along in Candidates — an agent branches on the array
// instead of regexing the prose, the same did-you-mean contract the repo path
// already honors. id tags the offending task ("" when the lane came from a
// filter, not a task).
func (a *App) unknownLaneErr(id, lane string) *core.Error {
	return &core.Error{
		Code:       core.CodeValidation,
		ID:         id,
		Msg:        fmt.Sprintf("unknown lane %q (configured: %s)", lane, strings.Join(a.Cfg.Lanes, ", ")),
		Candidates: append([]string(nil), a.Cfg.Lanes...),
	}
}

// unknownTypeErr is the type-side twin of unknownLaneErr: every type gate (add
// --type, set --type, ls --type) returns it, so a typo'd `--type epci` is exit 2
// with the configured types in Candidates rather than a silent bogus type. The
// empty string is always valid (it means the default type) and never reaches here.
func (a *App) unknownTypeErr(id, typ string) *core.Error {
	return &core.Error{
		Code:       core.CodeValidation,
		ID:         id,
		Msg:        fmt.Sprintf("unknown type %q (configured: %s)", typ, strings.Join(a.Cfg.Types, ", ")),
		Candidates: append([]string(nil), a.Cfg.Types...),
	}
}

// validateLaneFilter checks each comma token of a -s filter against the
// configured lanes, returning unknownLaneErr on the first unknown token.
// Empty/whitespace tokens are dropped (no constraint). Only `ls` exposes -s, so
// this is its fail-fast guard: a lane is a closed vocabulary, so a typo'd -s
// must not silently return [] (clamp-don't-reject is a config-file policy, not
// for an explicit CLI argument — that is symmetric with move/add). Labels stay
// lenient by design (an open vocabulary), so matchAnyLabel is untouched.
func (a *App) validateLaneFilter(filter string) error {
	for _, tok := range strings.Split(filter, ",") {
		if tok = strings.TrimSpace(tok); tok == "" {
			continue
		}
		if !a.Cfg.IsLane(tok) {
			return a.unknownLaneErr("", tok)
		}
	}
	return nil
}

// matchAnyLabel is matchAnyLane for tags: comma = OR, and a task passes when it
// carries at least one of the tokens. Empty/whitespace filter = no constraint.
func matchAnyLabel(filter string, labels []string) bool {
	matched := false
	any := false
	for _, tok := range strings.Split(filter, ",") {
		if tok = strings.TrimSpace(tok); tok == "" {
			continue
		}
		any = true
		if contains(labels, tok) {
			matched = true
		}
	}
	return !any || matched
}
