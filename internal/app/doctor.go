package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/akira-toriyama/furrow/internal/config"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// SevInfo is the doctor-only third severity: a fact worth SEEING that is not a
// problem to FIX — e.g. the board's own checkout shadowing the user-config arm,
// which is how hosting a board in a repo under its own scope always works. Info
// findings never make doctor exit non-zero; lint keeps its two-level error/warn
// vocabulary (core.SevError / core.SevWarn) untouched.
const SevInfo = "info"

// DoctorReport is what `furrow doctor` prints: the machine-wide board-setup
// health check. Boards mirrors `furrow boards` (same probe) plus a git
// freshness column; Resolutions simulates discovery at cwd and at every
// asserted dir; Problems is the finding list that drives the exit code.
// Healthy == no error- or warn-severity finding (info never reddens a machine).
type DoctorReport struct {
	Config      string             `json:"config"`           // the user-level config path furrow reads
	EnvDir      string             `json:"env_furrow_dir"`   // FURROW_DIR as set ("" = unset)
	EnvBoard    string             `json:"env_furrow_board"` // FURROW_BOARD as set ("" = unset)
	Boards      []DoctorBoard      `json:"boards"`
	Resolutions []DoctorResolution `json:"resolutions"`
	Problems    []core.Problem     `json:"problems"`
	Healthy     bool               `json:"healthy"`
}

// DoctorBoard is one configured board as doctor sees it: the same probe
// `furrow boards` runs (store/scopes/declared repo/exists + vocabulary +
// schema triple) plus the git-freshness answer sync would care about.
type DoctorBoard struct {
	BoardEntry
	Git DoctorGit `json:"git"`
}

// DoctorGit is a board's git freshness from LOCAL knowledge only — doctor
// never fetches, so ahead/behind are as current as the last fetch. State is a
// closed vocabulary: "ok" (upstream compared), "not-a-repo" (a standalone
// board — legitimate, never a problem), "no-upstream" (a repo with no tracking
// ref), "unavailable" (the probe itself failed — no git binary, or rev-list
// errored), and "unprobed" (the board is not on disk, so there was nothing to
// ask).
type DoctorGit struct {
	State  string `json:"state"`
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
}

// Git-probe states (DoctorGit.State).
const (
	GitOK          = "ok"
	GitNotARepo    = "not-a-repo"
	GitNoUpstream  = "no-upstream"
	GitUnavailable = "unavailable"
	GitUnprobed    = "unprobed"
)

// DoctorResolution is one discovery simulation: which arm
// (env|local|pointer|user-config) wins at Dir, or that none does. The cwd
// probe is informational (Asserted=false — cwd may legitimately be outside
// every scope); a dir passed as an argument is an assertion (Asserted=true),
// so its failure to resolve IS a problem (dir-unresolved).
type DoctorResolution struct {
	Dir       string `json:"dir"`
	Asserted  bool   `json:"asserted"`
	Resolved  bool   `json:"resolved"`
	Store     string `json:"store"`      // "" when unresolved
	Source    string `json:"source"`     // env|local|pointer|user-config; "" when unresolved
	ScopeRepo string `json:"scope_repo"` // the derived board-scope repo ("" = none)
}

// Doctor is the machine-wide board-setup health check — the diagnosis `furrow
// board` cannot give (it answers for one cwd and exits 2 outside every scope),
// grown from the 2026-07-16 hole: a machine with furrow AND the board on disk,
// but no [[board]] scope in the user config, fails only as "exit 2 at the
// moment of use", naming nothing. Doctor reads the config, probes every
// configured board (existence, schema state, scopes, git freshness), simulates
// discovery at cwd and at each asserted dir, and returns findings with
// concrete fixes. It is read-only and network-free (no fetch): the one
// non-reporting side effect class it could have, it doesn't.
//
// It never fails on an unhealthy machine — that is the report, not an error.
// The returned error is reserved for the environment being too broken to even
// report (e.g. no resolvable home directory).
func Doctor(ctx context.Context, cwd string, assertDirs []string) (*DoctorReport, error) {
	path, err := globalConfigPath()
	if err != nil {
		return nil, err
	}
	r := &DoctorReport{
		Config:      path,
		EnvDir:      os.Getenv(EnvDir),
		EnvBoard:    os.Getenv(EnvBoard),
		Boards:      []DoctorBoard{},
		Resolutions: []DoctorResolution{},
		Problems:    []core.Problem{},
	}

	r.Problems = append(r.Problems, envOverrideProblems(r.EnvDir, r.EnvBoard)...)
	r.Problems = append(r.Problems, doctorBoards(ctx, r, path)...)
	r.Problems = append(r.Problems, doctorResolutions(r, cwd, assertDirs)...)

	sortDoctorProblems(r.Problems)
	r.Healthy = true
	for _, p := range r.Problems {
		if p.Severity != SevInfo {
			r.Healthy = false
			break
		}
	}
	return r, nil
}

// envOverrideProblems reports the two env overrides. A set override is INFO —
// deliberate machine setup, but every invocation resolves there, shadowing all
// configured boards, so doctor must say it or the rest of the report misleads.
// A BROKEN one is an error: every furrow command on the machine fails until it
// is unset or fixed.
func envOverrideProblems(envDir, envBoard string) []core.Problem {
	var ps []core.Problem
	for _, e := range []struct{ name, val string }{{EnvDir, envDir}, {EnvBoard, envBoard}} {
		if e.val == "" {
			continue
		}
		if fi, err := os.Stat(e.val); err != nil || !fi.IsDir() {
			ps = append(ps, core.Problem{Severity: core.SevError, Code: "env-override-broken", ID: e.name,
				Msg: fmt.Sprintf("%s=%q is not an existing directory — every furrow command fails until it is unset or fixed", e.name, e.val)})
			continue
		}
		ps = append(ps, core.Problem{Severity: SevInfo, Code: "env-override", ID: e.name,
			Msg: fmt.Sprintf("%s=%q is set — every invocation resolves there, shadowing the configured boards", e.name, e.val)})
	}
	return ps
}

// doctorBoards loads the user config, probes every [[board]] into r.Boards,
// and returns the config- and board-level findings.
func doctorBoards(ctx context.Context, r *DoctorReport, cfgPath string) []core.Problem {
	var ps []core.Problem
	entries, warn, err := config.LoadGlobalBoards(cfgPath)
	if err != nil {
		return append(ps, core.Problem{Severity: core.SevError, Code: "global-config-unreadable", ID: "config",
			Msg: fmt.Sprintf("cannot parse %s: %v — no central board resolves anywhere until it parses", cfgPath, err)})
	}
	for _, w := range warn {
		ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "global-config-clamp", ID: "config", Msg: w})
	}

	cfgDir := filepath.Dir(cfgPath)
	for _, b := range entries {
		store, rerr := resolvePathRelTo(cfgDir, b.Path)
		if rerr != nil {
			// Already surfaced by LoadGlobalBoards' clamp warnings via discovery;
			// name it here too so the finding is self-contained.
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "global-config-clamp", ID: "config",
				Msg: fmt.Sprintf("ignoring central board %q: %v", b.Path, rerr)})
			continue
		}
		scopes := []string{}
		for _, s := range b.Scopes {
			sp, serr := resolvePathRelTo(cfgDir, s)
			if serr != nil {
				ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "global-config-clamp", ID: "config",
					Msg: fmt.Sprintf("board %q: ignoring scope %q: %v", b.Path, s, serr)})
				continue
			}
			scopes = append(scopes, sp)
		}
		entry := probeBoardEntry(store, scopes, b)
		db := DoctorBoard{BoardEntry: entry, Git: DoctorGit{State: GitUnprobed}}
		ps = append(ps, doctorBoardProblems(ctx, &db, cfgPath)...)
		r.Boards = append(r.Boards, db)
	}

	// The 2026-07-16 hole itself: a machine with furrow installed but no usable
	// [[board]] — every checkout without its own .furrow/pointer is bare exit 2,
	// and nothing on the machine says why. FURROW_BOARD substitutes for it.
	if len(r.Boards) == 0 && r.EnvBoard == "" {
		detail := ""
		if _, err := os.Stat(cfgPath); err != nil {
			detail = " (the config file does not exist)"
		}
		ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "no-boards", ID: "config",
			Msg: fmt.Sprintf("no usable [[board]] is configured%s — checkouts without a local .furrow/pointer resolve to no board (bare exit 2); run `furrow config init`, then add a [[board]] with path (the central .furrow), repo = \"auto\", and scopes covering your checkouts", detail)})
	}
	return ps
}

// doctorBoardProblems checks one probed board: on-disk existence, schema
// state, scope existence, git freshness, and scope shadowing. It also fills
// db.Git as a side effect of the probe.
func doctorBoardProblems(ctx context.Context, db *DoctorBoard, cfgPath string) []core.Problem {
	var ps []core.Problem
	if !db.Exists {
		return append(ps, core.Problem{Severity: core.SevError, Code: "board-missing", ID: db.Store,
			Msg: fmt.Sprintf("board %q is not on disk — clone the board repo there, or fix the [[board]] path in %s", db.Store, cfgPath)})
	}
	// The version pair comes through board.go's schemaVersions — the one
	// allowlisted introspection site — so this file never names the version
	// fields (see check-schema-write-guard.sh, whose grep is deliberately blunt).
	boardV, binaryV := schemaVersions(db.SchemaTriple)
	switch db.SchemaState {
	case SchemaUnreadable:
		ps = append(ps, core.Problem{Severity: core.SevError, Code: "board-unreadable", ID: db.Store,
			Msg: fmt.Sprintf("board %q exists but cannot be read (config.toml or meta.json unparseable) — restore it from the board repo's git history", db.Store)})
	case SchemaOutdated:
		ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "schema-outdated", ID: db.Store,
			Msg: fmt.Sprintf("board is schema v%d; this furrow writes v%d — writes are refused until `furrow upgrade` runs (a flag day: bump every pinned caller FIRST)", boardV, binaryV)})
	case SchemaTooNew:
		ps = append(ps, core.Problem{Severity: core.SevError, Code: "schema-too-new", ID: db.Store,
			Msg: fmt.Sprintf("board is schema v%d; this furrow writes v%d — update furrow (or bump the pinned release)", boardV, binaryV)})
	}
	for _, s := range db.Scopes {
		if fi, err := os.Stat(s); err != nil || !fi.IsDir() {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "scope-missing", ID: s,
				Msg: fmt.Sprintf("scope %q of board %q does not exist on disk — fix the path or remove it", s, db.Store)})
		}
	}
	ps = append(ps, doctorGitProblems(ctx, db)...)
	ps = append(ps, scanShadows(db.Store, db.Scopes)...)
	return ps
}

// doctorGitProblems probes the board's enclosing git repo and fills db.Git.
// A standalone (non-git) board and a repo with no upstream are states, never
// problems; being behind (a stale read is coming) or ahead (unpushed writes)
// warns toward `furrow sync`, and an in-progress rebase/merge warns because
// sync's pre-flight will refuse to run on top of it.
func doctorGitProblems(ctx context.Context, db *DoctorBoard) []core.Problem {
	repo, err := gitrepo.Open(ctx, db.Store)
	if err != nil {
		if fe := core.AsError(err); fe != nil && fe.Code == core.CodeValidation {
			db.Git.State = GitNotARepo // a standalone board — legitimate
		} else {
			db.Git.State = GitUnavailable // no git binary; the probe, not the board
		}
		return nil
	}
	var ps []core.Problem
	if op, mid := repo.MidOperation(ctx); mid {
		ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "board-mid-operation", ID: db.Store,
			Msg: fmt.Sprintf("the board's repo has a %s in progress — finish or abort it (`furrow sync` refuses to start on top of one)", op)})
	}
	ahead, behind, hasUpstream, err := repo.AheadBehind(ctx)
	switch {
	case err != nil:
		db.Git.State = GitUnavailable
	case !hasUpstream:
		db.Git.State = GitNoUpstream
	default:
		db.Git.State = GitOK
		db.Git.Ahead, db.Git.Behind = ahead, behind
		if behind > 0 {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "board-behind", ID: db.Store,
				Msg: fmt.Sprintf("board is %d commit(s) behind its upstream (as of the last fetch) — run `furrow sync` before reading it", behind)})
		}
		if ahead > 0 {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "board-ahead", ID: db.Store,
				Msg: fmt.Sprintf("board is %d commit(s) ahead of its upstream — unpushed writes; run `furrow sync`", ahead)})
		}
	}
	return ps
}

// scanShadows reports where discovery would NOT reach this board from inside
// its own scopes: a local .furrow or a .furrow-pointer.toml is nearer than the
// user-config arm, so it wins. Both cases are INFO, not problems — a repo-local
// board is a documented opt-out, and the board's own checkout (its .furrow IS
// the store) is how hosting a board inside its scope always looks — but each
// changes behavior an operator should know about (the own-checkout case runs
// source=local, WITHOUT the board's repo scope). The scan is the scope root
// plus its immediate children only: the house layout is scope = the owner
// directory with checkouts as its children, and a deeper walk would stat the
// world for no additional signal.
func scanShadows(store string, scopes []string) []core.Problem {
	canonStore := canonicalPath(store)
	var ps []core.Problem
	for _, scope := range scopes {
		dirs := []string{scope}
		if entries, err := os.ReadDir(scope); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					dirs = append(dirs, filepath.Join(scope, e.Name()))
				}
			}
		}
		for _, d := range dirs {
			if p, ok := shadowProblem(d, store, canonStore); ok {
				ps = append(ps, p)
			}
		}
	}
	return ps
}

// shadowProblem inspects one directory for a discovery arm that beats the
// user-config board: a local .furrow (reported — the board's own checkout gets
// the scope-less-reads phrasing, any other store the opt-out phrasing) or a
// pointer (reported only when it redirects AWAY from this board; a pointer TO
// the board reproduces the scope, so there is nothing to know).
func shadowProblem(d, store, canonStore string) (core.Problem, bool) {
	local := filepath.Join(d, DirName)
	if fi, err := os.Stat(local); err == nil && fi.IsDir() {
		if canonicalPath(local) == canonStore {
			return core.Problem{Severity: SevInfo, Code: "scope-shadowed", ID: d,
				Msg: fmt.Sprintf("%s holds the board's own .furrow — commands there run source=local, WITHOUT the board's repo scope (use -r to scope reads)", d)}, true
		}
		return core.Problem{Severity: SevInfo, Code: "scope-shadowed", ID: d,
			Msg: fmt.Sprintf("%s holds its own .furrow, which wins over the configured board there (nearest wins) — a deliberate opt-out, or a leftover", d)}, true
	}
	ptr := filepath.Join(d, PointerName)
	if fi, err := os.Stat(ptr); err != nil || fi.IsDir() {
		return core.Problem{}, false
	}
	if p, _, err := config.LoadPointer(ptr); err == nil {
		if board, err := resolvePathRelTo(d, p.Board); err == nil && canonicalPath(board) == canonStore {
			return core.Problem{}, false // a pointer TO this board reproduces the scope
		}
	}
	return core.Problem{Severity: SevInfo, Code: "scope-shadowed", ID: d,
		Msg: fmt.Sprintf("%s holds a %s redirecting elsewhere, which wins over the configured board there (nearest wins)", d, PointerName)}, true
}

// doctorResolutions simulates discovery at cwd (informational — cwd may
// legitimately be outside every scope) and at each asserted dir (an argument
// is an assertion: failing to resolve IS the problem). An asserted dir that
// does not exist was validated away by the CLI before this runs.
func doctorResolutions(r *DoctorReport, cwd string, assertDirs []string) []core.Problem {
	var ps []core.Problem
	probe := func(dir string, asserted bool) {
		res, err := discover(dir)
		dr := DoctorResolution{Dir: dir, Asserted: asserted}
		if err == nil {
			dr.Resolved = true
			dr.Store, dr.Source, dr.ScopeRepo = res.Dir, res.Source, res.DefaultRepo
		} else if asserted {
			ps = append(ps, core.Problem{Severity: core.SevError, Code: "dir-unresolved", ID: dir,
				Msg: fmt.Sprintf("no board resolves at %q (no local .furrow, no pointer, no [[board]] scope encloses it) — add it to a board's scopes in %s", dir, r.Config)})
		}
		r.Resolutions = append(r.Resolutions, dr)
	}
	if cwd != "" {
		probe(cwd, false)
	}
	for _, d := range assertDirs {
		probe(d, true)
	}
	return ps
}

// sortDoctorProblems orders findings by severity (error, warn, info), then id,
// then message — lint's determinism rule with doctor's third level ranked last.
func sortDoctorProblems(ps []core.Problem) {
	rank := map[string]int{core.SevError: 0, core.SevWarn: 1, SevInfo: 2}
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Severity != ps[j].Severity {
			return rank[ps[i].Severity] < rank[ps[j].Severity]
		}
		if ps[i].ID != ps[j].ID {
			return ps[i].ID < ps[j].ID
		}
		return ps[i].Msg < ps[j].Msg
	})
}
