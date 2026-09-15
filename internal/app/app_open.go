// Store discovery and board opening: FURROW_DIR, a local .furrow, a pointer,
// the user-level [[board]] scopes, and `furrow init`. Nothing here mutates a
// task; it decides WHICH store an App is bound to and what repo scope that
// binding injects.

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

// resolution is the outcome of discovery: which .furrow to open, and (only when
// reached via a pointer or central board) the repo/label to scope commands to.
type resolution struct {
	Dir          string
	DefaultLabel string // literal board tag (add-time union; never read-filters)
	DefaultRepo  string // board-scope repo ("" = none; a board's own default_repo is the fallback)
	AutoFilter   bool   // scope reads by DefaultRepo (pointer: always; board: its auto_filter)
	AutoCommit   bool   // git-commit .furrow/ after each mutating command (user-config [[board]] opt-in)
	ScopeWarn    []string
	Source       string // discovery mechanism: env|local|pointer|user-config
	// ScopeDeclared marks an arm that CONSULTED an operator-authored scope
	// declaration — a pointer's default_repo, a [[board]]'s repo — including
	// when that declaration resolved to no repo. It gates the board's own
	// default_repo fallback (see applyBoardScope), which is why it is a field
	// and not a Source check: Source "env" covers both FURROW_DIR (declares
	// nothing) and FURROW_BOARD (a synthetic [[board]] that declares "auto").
	ScopeDeclared bool
}

// applyBoardScope lets a board's OWN committed config.toml supply the repo scope
// for a resolution that discovery left unscoped, and returns the config-clamp
// warnings for an unusable declaration.
//
// The rule is one sentence: the board's `default_repo` applies only when
// discovery ran an arm that declares no scope at all, and when it applies it
// always filters. So it bites exactly the arms that inject no scope at all —
// a local `.furrow` (cwd inside the board's own tree) and FURROW_DIR — while a
// pointer or a `[[board]]`, having ANSWERED the scope question, ends it. That is
// the whole point: without it the same board answers `ls` differently depending
// on which directory reached it, and a bare `add` from inside the board silently
// produces repo-less drafts.
//
// The gate is the arm, not an empty repo, and the difference is load-bearing.
// "No repo" is a real answer those files can give: `repo = ""` documents itself
// as no scope, and a `repo = "auto"` that fails to derive has ALREADY told the
// operator on stderr that new tasks will be drafts. Filling either in from the
// board would invert furrow's nearest-wins rule with a committed, shared file,
// and in the second case would filter reads while stderr says nothing is scoped.
//
// It is also deliberately narrower than the keys it falls back from:
//
//   - "auto" is REFUSED. config.toml is committed and shared, so a cwd-derived
//     repo would differ per checkout and per machine — reintroducing the very
//     cwd-dependence the key exists to remove. A pointer/[[board]] may say
//     "auto" because those files are per-repo/per-machine; this one may not.
//   - There is no companion board-side `auto_filter`. Declaring the scope is
//     declaring it for reads too (a pointer behaves the same way); an explicit
//     empty -r is the per-command escape, one flag away.
//
// A bad value is clamped away with a warning rather than rejected, like every
// other config.toml value, and the warning is produced even when the key would
// have been inert (a nearer arm answered) — otherwise whether a typo gets
// reported would itself depend on the directory you ran from.
func applyBoardScope(res *resolution, cfg *config.Config) []string {
	repo := cfg.DefaultRepo
	if repo == "" {
		return nil
	}
	if w := defaultRepoWarning(repo); w != "" {
		return []string{w}
	}
	if res.ScopeDeclared {
		return nil // a nearer arm already answered the scope question
	}
	res.DefaultRepo, res.AutoFilter = repo, true
	return nil
}

// defaultRepoWarning is the clamp the app layer applies to `default_repo` —
// config stores it verbatim because core.IsRepoShaped lives here. It is ONE
// function because `config set` must ask the same question before writing:
// its regression guard compared only config's own warnings, so `config set
// default_repo notarepo` was exit 0 and wrote a value the next read clamped
// away (t-ge22). "" = the value is usable.
func defaultRepoWarning(repo string) string {
	switch {
	case repo == "auto":
		return fmt.Sprintf("default_repo %q is not usable in a board config (it is committed and shared, so a derived repo would differ per checkout); ignored (use a literal owner/repo)", repo)
	case !core.IsRepoShaped(repo):
		return fmt.Sprintf("default_repo %q is not owner/repo-shaped; ignored", repo)
	}
	return ""
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
		return resolution{Dir: abs, Source: SourceEnv}, nil
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return resolution{}, core.Internalf("", "resolve %q: %v", startDir, err)
	}
	for {
		cand := filepath.Join(dir, DirName)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return resolution{Dir: cand, Source: SourceLocal}, nil
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
			return resolution{}, discoveryUnresolvedErr(startDir)
		}
		dir = parent
	}
}

// discoveryUnresolvedErr is the walk's give-up error, and its remedy depends on
// the machine. With configured [[board]] entries, "run `furrow init`" is
// exactly the WRONG advice — following it grows a stray local board that
// shadows the central one from then on (this repo's .gitignore memorializes
// that incident), and it contradicts doctor's own dir-unresolved wording for
// the same state. So a machine with boards gets doctor's remedy — cd into a
// scope, add the directory to a board's scopes, or use FURROW_BOARD — with the
// configured board paths in candidates; only a board-less machine is steered to
// init. The extra config read happens on this error path only.
func discoveryUnresolvedErr(startDir string) error {
	if boards, cfgDir, _, err := loadGlobalBoards(); err == nil && len(boards) > 0 {
		var cands []string
		for _, b := range boards {
			if p, rerr := resolvePathRelTo(cfgDir, b.Path); rerr == nil {
				cands = append(cands, p)
			}
		}
		cfgPath, _ := globalConfigPath()
		return &core.Error{
			Code: core.CodeValidation,
			Kind: core.KindValidation,
			Msg: fmt.Sprintf("no %s or %s found in %q or any parent, and no [[board]] scope encloses it — cd into a configured scope, add this directory to a board's scopes in %s, or point FURROW_BOARD at a board (never `furrow init` here: a stray local board would shadow the central one)",
				DirName, PointerName, startDir, cfgPath),
			Candidates: cands,
		}
	}
	return core.Validationf("", "no %s or %s found in %q or any parent; run `furrow init`", DirName, PointerName, startDir)
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
	return resolution{Dir: board, DefaultRepo: repo, AutoFilter: true, ScopeDeclared: true, ScopeWarn: append(pwarn, rwarn...), Source: SourcePointer}, nil
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
// no default board OR cwd is outside every scope, so the walk's give-up error
// decides the remedy rather than this arm failing. A bad board
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
		return resolution{}, false, nil // out of every scope: the walk falls through to its give-up error
	}
	if fi, err := os.Stat(winBoard); err != nil || !fi.IsDir() {
		return resolution{}, false, core.Validationf("", "central board %q is not an existing directory", winBoard)
	}
	repo, rwarn := deriveScopeRepo(winner.Repo, abs)
	// FURROW_BOARD enters through loadGlobalBoards as a synthetic board, so a
	// winning board is "env" when that override is set, else a real user-config
	// [[board]] entry.
	source := SourceUserConfig
	if os.Getenv(EnvBoard) != "" {
		source = SourceEnv
	}
	return resolution{Dir: winBoard, DefaultLabel: winner.Label, DefaultRepo: repo, AutoFilter: winner.AutoFilter, AutoCommit: winner.AutoCommit, ScopeDeclared: true, ScopeWarn: append(warn, rwarn...), Source: source}, true, nil
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
	return InitAt(filepath.Join(dir, DirName))
}

// InitAt creates the store at EXACTLY storeDir — the .furrow directory itself,
// not its parent. It exists for the env overrides: FURROW_DIR/FURROW_BOARD name
// the store directory verbatim (it need not be called ".furrow"), and `furrow
// init` under either must create the store the very next command will discover,
// never a stray board in the cwd (which is how this repo once got a committed
// .furrow/ — see .gitignore).
func InitAt(fdir string) (*App, error) {
	if fi, err := os.Stat(fdir); err == nil && fi.IsDir() {
		return nil, core.Validationf("", "%s already exists at %q", DirName, fdir)
	}
	if err := os.MkdirAll(filepath.Join(fdir, "bodies"), 0o755); err != nil {
		return nil, core.Internalf("", "create %s: %v", fdir, err)
	}
	if err := fsstore.WriteFileAtomic(filepath.Join(fdir, "config.toml"), []byte(config.Template)); err != nil {
		return nil, err
	}
	if err := fsstore.WriteFileAtomic(filepath.Join(fdir, ".gitattributes"), []byte(GitAttributesTemplate)); err != nil {
		return nil, err
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
