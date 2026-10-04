// Store discovery and board opening: FURROW_DIR, a local .furrow, a pointer,
// the user-level [[board]] scopes, and `furrow init`. Nothing here mutates a
// task; it decides WHICH store an App is bound to and what repo scope that
// binding injects.

package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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
		if err := notStoreErr(fmt.Sprintf("%s=%q", EnvDir, abs), abs); err != nil {
			return resolution{}, err
		}
		return resolution{Dir: abs, Source: SourceEnv}, nil
	}
	if _, err := filepath.Abs(startDir); err != nil {
		return resolution{}, core.Internalf("", "resolve %q: %v", startDir, err)
	}
	// A local .furrow wins at each level, then a pointer; the nearest of
	// either ends the walk.
	dir, ok := walkUp(startDir, holdsStoreOrPointer)
	if !ok { // reached the root: try the user-level default board, else give up
		if res, ok, err := resolveGlobalBoard(startDir); err != nil {
			return resolution{}, err
		} else if ok {
			return res, nil
		}
		return resolution{}, discoveryUnresolvedErr(startDir)
	}
	if cand := filepath.Join(dir, DirName); isDir(cand) {
		return resolution{Dir: cand, Source: SourceLocal}, nil
	}
	return resolvePointer(dir, filepath.Join(dir, PointerName))
}

// walkUp climbs from start to the filesystem root and returns the first
// directory probe accepts. Three walks carried this loop — discovery, the
// nearest .furrow for `config init`, the nearest .git for the origin
// derivation — each with its own Abs/Stat/Dir/parent==dir dance (t-y6ya).
func walkUp(start string, probe func(dir string) bool) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if probe(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// holdsStoreOrPointer is the walk's probe: a local .furrow or a pointer.
func holdsStoreOrPointer(dir string) bool {
	return isDir(filepath.Join(dir, DirName)) || isFile(filepath.Join(dir, PointerName))
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// exists reports any entry at p, a dangling symlink included.
func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// storeMarkers are what a furrow store holds, each of its own kind: `furrow
// init` makes bodies/ and then writes config.toml, every layout since v2 keeps
// meta.json and tasks/, and a v1 board is the monolithic index.json. That last
// one stays a store so it behaves exactly as it always has (it opens
// unstamped); judging a layout is the schema gate's job, not this check's.
var storeMarkers = []struct {
	name string
	dir  bool
}{{"config.toml", false}, {"meta.json", false}, {"tasks", true}, {"bodies", true}, {"index.json", false}}

// isStoreDir reports whether p is a directory a store lives in, or may:
//
//  1. one holding furrow's own data (furrowData) is a store, whatever sits beside it —
//     a stray nested .furrow, a self-link, even a repo root an older binary
//     already wrote through the slip: each opened before, and furrow does not
//     guess which of two stores holds the tasks (a guess that says "remove the
//     other one" destroys data when it is wrong);
//  2. otherwise one holding a MARKED .furrow is the repo-root slip, whatever
//     else (a foreign config.toml, an Ansible tasks/) sits beside it — unless
//     p is itself named .furrow, which is never a repo root. Nothing of
//     furrow's is in p, so the .furrow as did-you-mean strands nothing;
//  3. otherwise a store marker of its kind makes a store, and so does a
//     store-to-be (storeToBe): what an empty directory always was.
//
// Only a marker's ABSENCE counts against p: one that cannot be read for any
// other reason counts as present, so an unreadable store goes on to fail with
// its own permission error rather than be called "not a store".
func isStoreDir(p string) bool {
	if !isDir(p) {
		return false
	}
	if furrowData(p) {
		return true
	}
	if filepath.Base(p) != DirName {
		inner := filepath.Join(p, DirName)
		if isDir(inner) && hasStoreMarker(inner) && canonicalPath(inner) != canonicalPath(p) {
			return false
		}
	}
	return hasStoreMarker(p) || storeToBe(p)
}

// furrowData reports data only furrow writes: its meta.json, its archive
// sub-store's, or a task shard core's decoder accepts. Any of them settles
// rule 1 — even with meta.json unreadable, conflicted or deleted (fsstore's
// own remedy for an unreadable one), the shards or the archive are the store,
// and opening it lets the real meta.json error speak.
func furrowData(p string) bool {
	if furrowMeta(p) || furrowMeta(filepath.Join(p, "archive")) {
		return true
	}
	shards, _ := filepath.Glob(filepath.Join(p, "tasks", "*.json"))
	for _, f := range shards {
		b, err := os.ReadFile(f) // #nosec G304 -- a shard under the configured store path
		if err != nil {
			continue
		}
		// furrow names a shard after its task: a foreign tasks/*.json is not one.
		if t, err := core.UnmarshalTask(b); err == nil && t.ID != "" && t.ID+".json" == filepath.Base(f) {
			return true
		}
	}
	return false
}

// hasStoreMarker reports a store marker of its kind in p; a marker that exists
// but cannot be stat'ed counts as present (see isStoreDir).
func hasStoreMarker(p string) bool {
	for _, m := range storeMarkers {
		fi, err := os.Stat(filepath.Join(p, m.name))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return true
			}
			continue
		}
		if fi.IsDir() == m.dir {
			return true
		}
	}
	return false
}

// furrowMeta reports a meta.json in p that declares a layout version furrow
// writes — read by fsstore, the one decoder of that file, so a foreign
// meta.json (or one that does not parse) is not furrow's.
func furrowMeta(p string) bool {
	if !isFile(filepath.Join(p, "meta.json")) {
		return false
	}
	v, err := fsstore.New(p, nil, "", "", 0).BoardVersion()
	return err == nil && v >= 2 // v1 was the monolithic index.json, never a meta.json
}

// storeToBe reports a directory the first write may stamp: nothing in it but
// dot-entries (a .git from cloning an empty board repo, Finder's .DS_Store, a
// .gitkeep, furrow's own .tmp-* staging), and no .furrow among them — a
// directory holding one is the repo-root slip, not a store-to-be. One that
// cannot even be listed is no store-to-be: absence decides there (isStoreDir).
func storeToBe(p string) bool {
	entries, err := os.ReadDir(p)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") || e.Name() == DirName {
			return false
		}
	}
	return true
}

// notStoreFinding states why an existing directory that a configured path
// names is not a store (nil when it is one, or when it does not exist — each
// arm reports that case in its own words), with the .furrow inside as the
// did-you-mean when there is one, and no remedy: each caller appends the one
// that fits its arm. Such a directory used to open as a fresh, writable,
// empty board: reads exit 0 on nothing, and the first write grows tasks/ +
// meta.json wherever the path pointed — the commonest slip being a board's
// repo root for its .furrow (t-tsds). It names what it found, never a content
// claim about a directory it cannot list.
func notStoreFinding(subject, dir string) *core.Error {
	if !isDir(dir) || isStoreDir(dir) {
		return nil
	}
	e := &core.Error{Code: core.CodeValidation, Kind: core.KindValidation}
	if inner := filepath.Join(dir, DirName); isStoreDir(inner) {
		e.Msg = fmt.Sprintf("%s is not a furrow store but the directory holding its .furrow; did you mean %q?", subject, inner)
		e.Candidates = []string{inner}
		return e
	}
	found := "it cannot be listed, so furrow cannot tell it is empty"
	if entries, err := os.ReadDir(dir); err == nil {
		var names []string
		for _, en := range entries {
			if !strings.HasPrefix(en.Name(), ".") {
				names = append(names, en.Name())
			}
		}
		switch {
		case len(names) == 0:
			found = "it holds only dot-entries and a .furrow that is not a store"
		case len(names) > 3:
			found = fmt.Sprintf("it holds %s, … and no store marker", strings.Join(names[:3], ", "))
		default:
			found = fmt.Sprintf("it holds %s and no store marker", strings.Join(names, ", "))
		}
	}
	e.Msg = fmt.Sprintf("%s is a directory but not a furrow store (%s)", subject, found)
	return e
}

// notStoreErr is notStoreFinding with the discovery arms' remedy: a
// did-you-mean is its own remedy; otherwise name what a store looks like.
func notStoreErr(subject, dir string) error {
	e := notStoreFinding(subject, dir)
	if e == nil {
		return nil
	}
	if len(e.Candidates) == 0 {
		e.Msg += " — point it at a store (a directory holding meta.json; one made by init also has config.toml), or at an empty directory (create it first) for a new one"
	}
	return e
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
//
// A set FURROW_BOARD is its own branch (envBoardGiveUp).
func discoveryUnresolvedErr(startDir string) error {
	if os.Getenv(EnvBoard) != "" {
		return envBoardGiveUp(startDir, true)
	}
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

// envBoardGiveUp is the failure while FURROW_BOARD is set and nothing nearer
// resolved startDir. The override REPLACES the config file's boards, so the
// machine-with-boards remedy is dead here — a scope added to the file is never
// read, FURROW_BOARD is already pointed — and so is its init warning (`init`
// under the override targets the override's own path). It names the
// override's one scope and only exits that work from startDir (t-xr78: a
// launchd job in / got the dead remedy), each one followed and measured:
//
//   - "cd into that scope" only when cdExit (doctor's asserted dir cannot move)
//     and the value is not relative (a relative store and scope re-resolve
//     against every cwd, so neither cd nor making it absolute alone helps);
//   - "unset FURROW_BOARD" only when a configured [[board]] then resolves
//     startDir — otherwise unsetting lands on the next give-up, and on a
//     board-less machine on the init advice this branch exists to avoid;
//   - for an unusable override, "fix it" only when the fixed store's scope
//     would enclose startDir.
func envBoardGiveUp(startDir string, cdExit bool) error {
	store, scope, _, envErr := envBoardScope()
	unset, why, configured := unsetEnvBoardExit(startDir)
	inScope := scope != "" && underScope(startDir, scope)
	var exits, notes []string
	if envErr != nil {
		var cands []string // a not-a-store override's did-you-mean; scope is then the candidate's
		if ce := (*core.Error)(nil); errors.As(envErr, &ce) {
			cands = ce.Candidates
		}
		// "fix it" is a promise: only a missing path (make the store there) or a
		// did-you-mean (point at it) can be fixed in place — an existing
		// directory that is no store cannot become one.
		fixable := !isDir(store) || len(cands) > 0
		if fixable && (scope == "" || inScope) {
			exits = append(exits, "fix it")
		}
		exits = append(exits, fmt.Sprintf("set %s to an existing store", EnvDir))
		if unset != "" {
			exits = append(exits, unset)
		}
		switch {
		case scope == "" || inScope || !fixable:
		case len(cands) > 0:
			notes = append(notes, fmt.Sprintf("pointed at %q instead, %q would still lie outside its one scope %q", cands[0], startDir, scope))
		default:
			notes = append(notes, fmt.Sprintf("even once that store exists, %q lies outside its one scope %q", startDir, scope))
		}
		if why != "" {
			notes = append(notes, why)
		}
		return &core.Error{Code: core.CodeValidation, Kind: core.KindValidation, Msg: envErr.Error() + " — " + orJoin(exits) + parenthesize(notes), Candidates: cands}
	}
	if raw := os.Getenv(EnvBoard); !filepath.IsAbs(raw) && !strings.HasPrefix(raw, "~") {
		notes = append(notes, fmt.Sprintf("%s is relative, so its store and scope re-resolve against every cwd", EnvBoard))
	} else if cdExit {
		exits = append(exits, "cd into that scope")
	}
	exits = append(exits, fmt.Sprintf("set %s=%q to name the store from any directory", EnvDir, store))
	if unset != "" {
		exits = append(exits, unset)
	}
	if why != "" {
		notes = append(notes, why)
	}
	shadow := ""
	if configured {
		shadow = fmt.Sprintf("; the config file's [[board]] entries are not read while %s is set", EnvBoard)
	}
	msg := fmt.Sprintf("no %s or %s found in %q or any parent, and it lies outside %s's one scope %q (two levels above its store%s) — %s",
		DirName, PointerName, startDir, EnvBoard, scope, shadow, orJoin(exits))
	return &core.Error{Code: core.CodeValidation, Kind: core.KindValidation, Msg: msg + parenthesize(notes), Candidates: []string{store}}
}

// unsetEnvBoardExit words the "unset FURROW_BOARD" remedy only when following it
// works: the config file's own pick for startDir (the gate's pickBoard) is a
// board on disk. Otherwise why says what unsetting would meet; configured says
// whether the file holds any [[board]] (no shadow worth naming when it holds
// none). A config that does not parse says nothing — doctor owns that.
func unsetEnvBoardExit(startDir string) (exit, why string, configured bool) {
	boards, cfgDir, _, err := loadConfigBoards()
	if err != nil {
		return "", "", false
	}
	if len(boards) == 0 {
		return "", fmt.Sprintf("no [[board]] is configured, so unsetting %s would resolve nothing either", EnvBoard), false
	}
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", "", true
	}
	winner, store, _ := pickBoard(boards, cfgDir, canonicalPath(abs))
	switch {
	case winner == nil:
		return "", fmt.Sprintf("no configured [[board]] scope encloses it either, so unsetting %s would not help", EnvBoard), true
	case !isStoreDir(store):
		return "", fmt.Sprintf("the configured board %q that encloses it is not an existing furrow store either", store), true
	}
	return fmt.Sprintf("unset %s: the configured board %q resolves here", EnvBoard, store), "", true
}

// parenthesize renders trailing notes as " (a; b)", or nothing.
func parenthesize(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return " (" + strings.Join(notes, "; ") + ")"
}

// walkFindsBoard reports whether discover's walk from dir meets a .furrow or a
// pointer — the arms that answer before any central board, broken or not.
func walkFindsBoard(dir string) bool {
	_, ok := walkUp(dir, holdsStoreOrPointer)
	return ok
}

// configScopeEncloses reports whether a config-file [[board]] scope picks dir —
// whatever then became of that board (missing, or no store).
func configScopeEncloses(dir string) bool {
	boards, cfgDir, _, err := loadConfigBoards()
	if err != nil || len(boards) == 0 {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	winner, _, _ := pickBoard(boards, cfgDir, canonicalPath(abs))
	return winner != nil
}

// underScope reports whether dir is scope or below it, by the gate's own
// canonical comparison.
func underScope(dir, scope string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	_, ok, err := canonicalScopeUnder(canonicalPath(abs), "/", scope)
	return err == nil && ok
}

// orJoin renders remedies as "a", "a or b", "a, b, or c".
func orJoin(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
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
	if err := notStoreErr(fmt.Sprintf("%s: board %q", pointerPath, board), board); err != nil {
		return resolution{}, err
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
	winner, winBoard, pwarn := pickBoard(boards, cfgDir, canonicalPath(abs))
	warn = append(warn, pwarn...)
	if winner == nil {
		return resolution{}, false, nil // out of every scope: the walk falls through to its give-up error
	}
	if fi, err := os.Stat(winBoard); err != nil || !fi.IsDir() {
		if os.Getenv(EnvBoard) != "" { // the synthetic board: name the variable and its live exits
			return resolution{}, false, envBoardGiveUp(startDir, true)
		}
		return resolution{}, false, core.Validationf("", "central board %q is not an existing directory", winBoard)
	}
	cfgPath, _ := globalConfigPath()
	if err := notStoreErr(fmt.Sprintf("the [[board]] path %q in %s", winBoard, cfgPath), winBoard); err != nil {
		if os.Getenv(EnvBoard) != "" {
			return resolution{}, false, envBoardGiveUp(startDir, true)
		}
		return resolution{}, false, err
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

// pickBoard picks the board whose matching scope is the longest (most specific)
// canonical prefix of cdir. Boards are visited in file order and ties keep the
// first match (strict >), so the choice is deterministic. Paths are resolved for
// the comparison but NOT stat'd here — only the eventual winner is checked to
// exist, so a broken board in an unrelated scope never breaks furrow.
//
// A path/scope that cannot even be resolved (e.g. the unsupported ~user form)
// is DROPPED with a warning, never a hard error: clamp-don't-reject means one
// half-written entry must not break furrow in every directory on the machine.
func pickBoard(boards []config.GlobalBoard, cfgDir, cdir string) (winner *config.GlobalBoard, store string, warn []string) {
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
				winner, store, winLen = b, board, len(cs)
			}
		}
	}
	return winner, store, warn
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

// envBoardScope is FURROW_BOARD resolved exactly as the gate resolves it —
// loadGlobalBoards' base, resolvePathRelTo, boardScopes — so no message can name
// another store or scope than discovery used. set is false when the override is
// unset. err is the override's own failure, worded to name it and kept a
// validation error (a user's setting, whatever resolve step failed): a value
// that cannot resolve leaves store and scope empty; a store that is not there
// still fills the scope, which needs no store; a directory that is no store
// fills it from the did-you-mean the fix would point at (its own scope would
// be the wrong one), else from its own path — the best estimate of where a
// fix there would apply. Callers append the remedy.
func envBoardScope() (store, scope string, set bool, err error) {
	raw := os.Getenv(EnvBoard)
	if raw == "" {
		return "", "", false, nil
	}
	boards, base, _, _ := loadGlobalBoards() // the env arm cannot fail
	store, rerr := resolvePathRelTo(base, boards[0].Path)
	if rerr != nil {
		return "", "", true, core.Validationf("", "%s=%q cannot be resolved: %v", EnvBoard, raw, rerr)
	}
	scope = boardScopes(&boards[0], store)[0]
	subject := fmt.Sprintf("%s=%q", EnvBoard, store)
	if raw != store { // quote what the variable holds, then what it named
		subject = fmt.Sprintf("%s=%q (resolved to %q)", EnvBoard, raw, store)
	}
	if !isDir(store) {
		return store, scope, true, core.Validationf("", "%s is not an existing directory", subject)
	}
	if e := notStoreFinding(subject, store); e != nil {
		if len(e.Candidates) == 1 {
			return store, boardScopes(&boards[0], e.Candidates[0])[0], true, e
		}
		return store, scope, true, e
	}
	return store, scope, true, nil
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
	return loadConfigBoards()
}

// loadConfigBoards is loadGlobalBoards' file arm alone: the [[board]] entries
// whatever FURROW_BOARD says — what discovery would read once it is unset.
func loadConfigBoards() (boards []config.GlobalBoard, cfgDir string, warn []string, err error) {
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

// gitAttributes returns the .gitattributes init should write, or nil when the
// file already says everything. A store-to-be init fills may hold one of the
// user's own: its rules are kept and only the missing furrow lines appended,
// in the file's own line ending (t-tsds: init used to replace it whole,
// silently, at exit 0). Read before anything is written, so an unreadable one
// fails init cleanly.
func gitAttributes(path string) ([]byte, error) {
	old, err := os.ReadFile(path) // #nosec G304 -- the .gitattributes of the store init was told to create
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return []byte(GitAttributesTemplate), nil // nothing there yet; MkdirAll reports a bad target itself
	}
	if err != nil {
		return nil, core.Validationf("", "cannot read %s: %v — init merges its union-merge rules into it; make it a readable file first", path, err)
	}
	eol := "\n"
	if strings.Contains(string(old), "\r\n") {
		eol = "\r\n"
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(old), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	merged := string(old)
	for _, l := range strings.Split(GitAttributesTemplate, "\n") {
		if l == "" || strings.HasPrefix(l, "#") || have[l] {
			continue
		}
		if merged != "" && !strings.HasSuffix(merged, "\n") {
			merged += eol
		}
		merged += l + eol
	}
	if merged == string(old) {
		return nil, nil
	}
	return []byte(merged), nil
}

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
// .furrow/ — see .gitignore). An existing directory is filled only when it is a
// store-to-be (empty or dot-only — what discovery accepts there too); a store
// is refused as one, and anything else as what it is, never as ".furrow"
// (t-tsds: init used to call a repo root ".furrow already exists").
func InitAt(fdir string) (*App, error) {
	if fi, err := os.Stat(fdir); err == nil && !fi.IsDir() {
		return nil, core.Validationf("", "%q exists and is not a directory", fdir)
	} else if err == nil {
		switch {
		case storeToBe(fdir) && filepath.Base(fdir) != DirName && exists(filepath.Join(fdir, ".git")):
			// A git work tree's root (a .git dir, or a worktree's/submodule's .git
			// file) is where its repo lives, not its store — unless it is named
			// .furrow: a board repo cloned as a repo's .furrow is exactly where
			// the walk looks.
			return nil, core.Validationf("", "%q is the root of a git work tree — put the store at %q, where discovery looks for it", fdir, filepath.Join(fdir, DirName))
		case storeToBe(fdir):
		case isStoreDir(fdir):
			return nil, core.Validationf("", "a furrow store already exists at %q", fdir)
		case filepath.Base(fdir) == DirName:
			return nil, core.Validationf("", "a %s already exists at %q (the walk from its repo opens it as the local board)", DirName, fdir)
		default:
			e := &core.Error{Code: core.CodeValidation, Kind: core.KindValidation,
				Msg: fmt.Sprintf("%q exists and is not a furrow store — init creates one only in a new, empty or dot-only directory", fdir)}
			if inner := filepath.Join(fdir, DirName); isStoreDir(inner) {
				e.Msg += fmt.Sprintf("; the store is already at %q — point the setting there", inner)
				e.Candidates = []string{inner}
			}
			return nil, e
		}
	}
	// A store inside a store reads as the repo-root slip from then on (`furrow
	// init .furrow` from a board's repo once made one at exit 0): refused when
	// the parent holds furrow's own meta.json, or is a .furrow with a marker.
	if parent := filepath.Dir(fdir); furrowMeta(parent) || (filepath.Base(parent) == DirName && hasStoreMarker(parent)) {
		return nil, core.Validationf("", "%q would nest a store inside the store at %q — run init in a repo, not in a board", fdir, parent)
	}
	attrs, err := gitAttributes(filepath.Join(fdir, ".gitattributes"))
	if err != nil { // before any write: a failure must leave no marker behind
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(fdir, "bodies"), 0o755); err != nil {
		return nil, core.Internalf("", "create %s: %v", fdir, err)
	}
	if err := fsstore.WriteFileAtomic(filepath.Join(fdir, "config.toml"), []byte(config.Template)); err != nil {
		return nil, err
	}
	if attrs != nil {
		if err := fsstore.WriteFileAtomic(filepath.Join(fdir, ".gitattributes"), attrs); err != nil {
			return nil, err
		}
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
