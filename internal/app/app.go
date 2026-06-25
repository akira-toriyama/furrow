// Package app is the coordinator layer: it wires a Store and Config together
// and exposes every task mutation as a method. It is the ONLY mutation funnel —
// the CLI and TUI call App, never the store directly. That keeps invariants
// (frozen ids, canonical order, closed-timestamp rules, body<->index pairing)
// in one place instead of scattered across two presentation layers.
package app

import (
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

// Store is what App needs from a store: the core port plus the few extras the
// coordinator uses. Both fsstore and memstore satisfy it.
type Store interface {
	core.Store
	DeleteBody(id string) error
	BumpSeqTo(n int) error
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
}

// Open discovers the .furrow directory (FURROW_DIR, else the nearest ancestor
// of startDir that contains one), loads config, and builds an fsstore. It is a
// CodeValidation error to run a store command outside a furrow repo — the
// message points at `furrow init`.
func Open(startDir string) (*App, error) {
	dir, err := discover(startDir)
	if err != nil {
		return nil, err
	}
	return openAt(dir)
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

// discover finds the .furrow directory: FURROW_DIR if set, else walk up from
// startDir to the filesystem root.
func discover(startDir string) (string, error) {
	if env := os.Getenv(EnvDir); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil {
			return "", core.Validationf("", "%s=%q is not a valid path: %v", EnvDir, env, err)
		}
		// An explicit FURROW_DIR must point at an existing store directory;
		// a typo'd path should fail loudly, not act as an empty store.
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return "", core.Validationf("", "%s=%q is not an existing directory", EnvDir, abs)
		}
		return abs, nil
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", core.Internalf("", "resolve %q: %v", startDir, err)
	}
	for {
		cand := filepath.Join(dir, DirName)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the root
			return "", core.Validationf("", "no %s found in %q or any parent; run `furrow init`", DirName, startDir)
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

	id, err := a.Store.NextID()
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
// ready+in-progress) AND every dependency already done.
func (a *App) Next(limit int) ([]core.Task, error) {
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

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
