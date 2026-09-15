// Package app is the coordinator layer: it wires a Store and Config together
// and exposes every task mutation as a method. It is the ONLY mutation funnel —
// the CLI calls App, never the store directly. That keeps invariants
// (frozen ids, canonical order, closed-timestamp rules, body<->index pairing)
// in one place instead of scattered across the presentation layer.
package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/akira-toriyama/furrow/internal/claudecode"
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

// Discovery sources — how Open reached the store, reported by `furrow board`'s
// `source`. A closed vocabulary because the LAYOUT axis is derived from it:
// SourceLocal is the only arm that sits INSIDE the tree it serves (repo-local);
// every other arm was reached by configuration, which is what central means.
const (
	SourceEnv        = "env"         // FURROW_DIR or FURROW_BOARD
	SourceLocal      = "local"       // an ancestor .furrow
	SourcePointer    = "pointer"     // a .furrow-pointer.toml
	SourceUserConfig = "user-config" // a user-level [[board]] entry
)

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

	// Loc is the OPERATOR's zone: the one furrow reads a wall-clock `--due`
	// spelling in and draws the "today" boundary at. nil means time.Local, which
	// is what every real invocation uses (it is also the zone the CLI already
	// renders timestamps in). It is a field, not a call to time.Local, because the
	// Clock is UTC by contract (core.SystemClock) and a test that pinned only the
	// clock would silently assert UTC days.
	Loc *time.Location

	// DefaultLabel is a central board's LITERAL `label` tag ("" = none): `add`
	// unions it into the task's labels, like a GitHub Issues label auto-applied
	// per board. It never filters reads — scoping is DefaultRepo's job.
	DefaultLabel string

	// DefaultRepo is the board-scope repo from a pointer, a central board, or —
	// when neither supplied one — the board's own config.toml `default_repo`
	// ("" = none): `add` unions it into the task's repos (suppressed by
	// --draft) and reads filter by it when AutoFilter is on. See applyBoardScope
	// for the precedence.
	DefaultRepo string

	// AutoFilter reports whether read commands (ls/next/revisit) auto-filter by
	// DefaultRepo. A pointer always scopes (true), as does a board's own
	// `default_repo`; a central board honors its per-board auto_filter (default
	// true). Meaningless when DefaultRepo is "".
	AutoFilter bool

	// AutoCommit reports whether this board opted into post-mutation git commits
	// (the user-config [[board]] `autocommit` key; default false). When on, the
	// CLI runs AutoCommitFlush after a successful mutating command.
	AutoCommit bool

	// ScopeWarnings are discovery-time notes bound for stderr (e.g. the global
	// default board activated but found no enclosing git repo to derive an
	// auto repo from).
	ScopeWarnings []string

	// BoardRepos is the repo set the board scopes to (derived from the enclosing
	// checkout — the owner/repo parsed from the git origin URL — or declared by
	// the board itself). Open populates it from DefaultRepo; it participates in
	// the short-name resolution universe, so a derived repo resolves short names
	// even before its first task exists.
	BoardRepos []string

	// Source records how the store was discovered — one of the Source* constants.
	// `furrow board` surfaces it so an agent sees why this store/scope is active,
	// and it is what the LAYOUT axis is derived from (see layoutOf): SourceLocal
	// is the only arm that sits inside the tree it serves.
	Source string

	// sleep is the backoff sleeper used by Sync's transient-rebase retry. nil
	// means the real cancellable timer (see ctxSleep); tests set a no-op to run
	// the retry budget instantly.
	sleep func(time.Duration)

	// Sessions is the co-located-session write guard's registry (see
	// session_guard.go); nil = the guard is off, which is every process not
	// running under Claude Code (a human shell, CI, tests). Open wires
	// internal/claudecode's registry when the environment says otherwise.
	Sessions core.SessionRegistry
	// Self identifies this process's session in that registry.
	Self SessionRef
	// The guard's per-process state: the registry is read once (sessionRead),
	// self located (sessionSelf/sessionSelfOK), cwd→repo memoized
	// (sessionRepos), idle clashes recorded for the CLI's warning
	// (sessionWarn), and stand-down notes queued for stderr (sessionNotes).
	sessionRead   bool
	sessionSelf   core.Session
	sessionSelfOK bool
	sessionOthers []core.Session
	sessionRepos  map[string]string
	sessionWarn   []core.SessionClash
	sessionNotes  []string

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
	// discover() reads no config file, so the board's own scope declaration can
	// only be applied here, once openAt has loaded it. It is a fallback: a
	// pointer's / a [[board]]'s repo is nearer and already in res.
	a.Warnings = append(a.Warnings, applyBoardScope(&res, a.Cfg)...)
	a.DefaultLabel = res.DefaultLabel
	a.DefaultRepo = res.DefaultRepo
	a.AutoFilter = res.AutoFilter
	a.AutoCommit = res.AutoCommit
	a.ScopeWarnings = res.ScopeWarn
	a.Source = res.Source
	if res.DefaultRepo != "" {
		a.BoardRepos = []string{res.DefaultRepo}
	}
	// The write guard is armed by the environment alone: under Claude Code
	// every write is an AI write. A missing config dir is not an error here —
	// the registry read reports (and stands down on) whatever it finds.
	if pid, id, ok := claudecode.Self(); ok {
		a.Self = SessionRef{PID: pid, ID: id}
		if dir, err := claudecode.DefaultDir(); err == nil {
			a.Sessions = claudecode.Registry{Dir: dir}
		}
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
	st := fsstore.New(dir, cfg.Lanes, cfg.IDPrefix, cfg.EpicIDPrefix, cfg.IDWidth)
	return &App{Store: st, Cfg: cfg, Clock: core.SystemClock(), Dir: dir, Warnings: warn, Loc: cfg.DueTimezone}, nil
}

// NewWithStore builds an App over an arbitrary Store (for tests / dry-runs).
func NewWithStore(st Store, cfg *config.Config, clk core.Clock) *App {
	return &App{Store: st, Cfg: cfg, Clock: clk, Loc: cfg.DueTimezone}
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

// requireNonBlank rejects a blank entry in a value list taken by a mutating
// flag. It is ONE rule for every such flag, enforced in the mutation funnel
// rather than per-command, because the per-command version is what drifted:
// `check --add ""` was already exit 2 (t-fr3e) while `set --add-label ""` wrote
// `"labels": [""]` into the shard at exit 0, and a CSV empty field
// (`label --add "bug,"`, `add -l "a,"`) put a blank entry in EVERY list flag
// that splits on commas. A blank sorts first, so it also led every rendered
// label list. Nothing dropped a value silently and nothing caught it later —
// which is why this refuses at the door instead of filtering.
func requireNonBlank(id, flag string, vals []string) error {
	for _, v := range vals {
		if strings.TrimSpace(v) == "" {
			return core.Validationf(id, "%s: a blank value is not allowed (it would be stored as an empty entry); drop the flag or pass a real value", flag)
		}
	}
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
// Unknown tokens are rejected upstream (validateLaneFilter, called by
// List/Stats/Search), so
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
		Kind:       core.KindUnknownLane,
		Subject:    id,
		Msg:        fmt.Sprintf("unknown lane %q (configured: %s)", lane, strings.Join(a.Cfg.Lanes, ", ")),
		Candidates: append([]string(nil), a.Cfg.Lanes...),
	}
}

// validateLaneFilter checks each comma token of a -s filter against the
// configured lanes, returning unknownLaneErr on the first unknown token.
// Empty/whitespace tokens are dropped (no constraint). Every read that exposes
// -s (ls, stats, search) calls this first, so
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
