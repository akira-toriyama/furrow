# Per-Repo Pointer (.furrow-pointer.toml) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a code repo carry a `.furrow-pointer.toml` so `furrow` run from inside it auto-targets a central board and scopes every command to that repo's label — recovering repo-local ergonomics on a central board.

**Architecture:** A new `config.LoadPointer` parses the pointer file; `app.discover` walks up from cwd and, when it meets a pointer before a local `.furrow`, resolves the pointer's `board` path and carries its `default_label` onto `App.DefaultLabel`. The app layer unions that label into every `add`; the CLI layer filters reads by it (with an explicit `-l ''` escape) and announces the active scope on stderr.

**Tech Stack:** Go 1.23 (stdlib + `github.com/pelletier/go-toml/v2`, already a dependency), cobra CLI.

## Global Constraints

- Go 1.23 toolchain — prefix every go command with `GOTOOLCHAIN=local` (e.g. `GOTOOLCHAIN=local go test ./...`).
- Layer rule: `internal/core` is pure (stdlib only). Pointer TOML parsing lives in `internal/config`; filesystem path resolution lives in `internal/app`; presentation/flags live in `internal/cli`. Never add cross-layer imports.
- Single marshaller path: do NOT touch `core.Marshal` or serialize `*Index` anywhere else. This feature changes no on-disk index/schema shape — no schema/golden changes.
- Output contract: **JSON/data to stdout ONLY; logs, notices, banners, errors to stderr.** The scope banner MUST go to stderr.
- `config.toml` policy is clamp-don't-reject; the pointer file is the opposite — it fails loud (a misrouted write is worse than a stop). Keep these distinct.
- Commits: gitmoji + Conventional — `<:gitmoji:> <type>(<scope>)<!>: <subject>`, subject in English. End every commit message with a trailing line: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`. The repo's commit-msg hook (`scripts/hooks`) is active in this worktree.
- Bilingual docs: any user-visible change updates BOTH `README.md` and `README.ja.md`.
- Precedence (locked in design): `FURROW_DIR` (no label injection) > nearest dir walking up where, at each dir, a local `.furrow` beats a `.furrow-pointer.toml` > "run `furrow init`" error.
- Read-scope behavior (locked, "A′"): `add` always unions the default label; `ls`/`next`/`revisit` filter by it unless `-l` is set (`-l ''` = whole board); every scoped read prints `furrow: board=… scope=label=… (-l '' for all)` to stderr.

---

### Task 1: `config.LoadPointer` — parse `.furrow-pointer.toml`

**Files:**
- Create: `internal/config/pointer.go`
- Test: `internal/config/pointer_test.go`

**Interfaces:**
- Consumes: nothing (leaf).
- Produces:
  - `type Pointer struct { Board string; DefaultLabel string }`
  - `func LoadPointer(path string) (*Pointer, error)` — reads the file at `path`, parses TOML keys `board` (required) and `default_label` (optional). Returns the read error if the file can't be read, a wrapped error on malformed TOML, and an error if `board` is empty. `Board`/`DefaultLabel` are returned verbatim (NOT path-resolved — the caller resolves `board` against the pointer file's directory).

- [ ] **Step 1: Write the failing test**

Create `internal/config/pointer_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writePointer(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".furrow-pointer.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPointer_BoardAndLabel(t *testing.T) {
	p, err := LoadPointer(writePointer(t, "board = \"../projects/.furrow\"\ndefault_label = \"chord\"\n"))
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if p.Board != "../projects/.furrow" {
		t.Errorf("Board = %q, want ../projects/.furrow", p.Board)
	}
	if p.DefaultLabel != "chord" {
		t.Errorf("DefaultLabel = %q, want chord", p.DefaultLabel)
	}
}

func TestLoadPointer_BoardOnly(t *testing.T) {
	p, err := LoadPointer(writePointer(t, "board = \"/abs/.furrow\"\n"))
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if p.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty", p.DefaultLabel)
	}
}

func TestLoadPointer_MissingBoardErrors(t *testing.T) {
	if _, err := LoadPointer(writePointer(t, "default_label = \"chord\"\n")); err == nil {
		t.Fatal("expected error for missing board, got nil")
	}
}

func TestLoadPointer_MalformedErrors(t *testing.T) {
	if _, err := LoadPointer(writePointer(t, "board = \"x\" this = is = broken\n")); err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/config/ -run TestLoadPointer -v`
Expected: FAIL — `undefined: LoadPointer` / `undefined: Pointer`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/config/pointer.go`:

```go
package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// Pointer is a repo-local .furrow-pointer.toml: it redirects furrow at a central
// board and optionally scopes every command to one label (the repo's name on a
// shared tracker). It is the central-board counterpart to a repo-local .furrow.
type Pointer struct {
	Board        string // path to the central .furrow (relative to the pointer file, ~, or absolute)
	DefaultLabel string // label auto-applied on add and auto-filtered on reads ("" = redirect only)
}

type rawPointer struct {
	Board        string `toml:"board"`
	DefaultLabel string `toml:"default_label"`
}

// LoadPointer parses a .furrow-pointer.toml. Unlike config.Load it does NOT
// clamp: a pointer with no board is useless and a malformed pointer must fail
// loudly rather than silently send writes to the wrong store. Resolving the
// board path (relative/~/abs) and checking it exists is the caller's job — only
// the caller knows the pointer file's directory.
func LoadPointer(path string) (*Pointer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r rawPointer
	if err := toml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("malformed pointer TOML: %w", err)
	}
	if r.Board == "" {
		return nil, fmt.Errorf("pointer is missing required key `board`")
	}
	return &Pointer{Board: r.Board, DefaultLabel: r.DefaultLabel}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local go test ./internal/config/ -run TestLoadPointer -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/pointer.go internal/config/pointer_test.go
git commit -m "$(printf ':sparkles: feat(config): add LoadPointer for .furrow-pointer.toml\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 2: `app.discover` resolves a pointer and carries `DefaultLabel`

**Files:**
- Modify: `internal/app/app.go` (the `Open`/`discover` block at lines 18-98, and the `App` struct at lines 32-40)
- Test: `internal/app/pointer_test.go` (create)

**Interfaces:**
- Consumes: `config.LoadPointer` and `config.Pointer` (Task 1).
- Produces:
  - New field `App.DefaultLabel string` (the pointer's label, "" when not pointer-resolved).
  - New const `PointerName = ".furrow-pointer.toml"`.
  - `discover(startDir string) (resolution, error)` where `type resolution struct { Dir string; DefaultLabel string }`.
  - Behavior: `FURROW_DIR` → `resolution{Dir: abs}` (no label). Else walk up; at each dir a `.furrow` dir wins (`resolution{Dir: cand}`); otherwise a `.furrow-pointer.toml` resolves the board (relative→pointer dir, `~`→home, abs as-is) and must be an existing dir, yielding `resolution{Dir: board, DefaultLabel: ptr.DefaultLabel}`.

- [ ] **Step 1: Write the failing test**

Create `internal/app/pointer_test.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"testing"
)

// pointerLayout builds tmp/central/.furrow (a real store) and a sibling repo dir
// holding a .furrow-pointer.toml; it returns the repo dir to Open from.
func pointerLayout(t *testing.T, label string) (repoDir, boardDir string) {
	t.Helper()
	t.Setenv(EnvDir, "") // ensure FURROW_DIR does not override discovery
	root := t.TempDir()
	central := filepath.Join(root, "central")
	if _, err := Init(central); err != nil {
		t.Fatal(err)
	}
	boardDir = filepath.Join(central, DirName)
	repoDir = filepath.Join(root, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "board = \"../central/.furrow\"\n"
	if label != "" {
		body += "default_label = \"" + label + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(repoDir, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoDir, boardDir
}

func TestDiscover_PointerRedirectsAndScopes(t *testing.T) {
	repoDir, boardDir := pointerLayout(t, "chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != boardDir {
		t.Errorf("Dir = %q, want %q", a.Dir, boardDir)
	}
	if a.DefaultLabel != "chord" {
		t.Errorf("DefaultLabel = %q, want chord", a.DefaultLabel)
	}
}

func TestDiscover_PointerBoardOnlyNoLabel(t *testing.T) {
	repoDir, _ := pointerLayout(t, "")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty", a.DefaultLabel)
	}
}

func TestDiscover_LocalFurrowBeatsPointer(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	// Give the repo dir its OWN .furrow; it must win over the pointer.
	if _, err := Init(repoDir); err != nil {
		t.Fatal(err)
	}
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != filepath.Join(repoDir, DirName) {
		t.Errorf("Dir = %q, want local .furrow", a.Dir)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty (local store, no pointer)", a.DefaultLabel)
	}
}

func TestDiscover_FurrowDirBeatsPointer(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	other := t.TempDir()
	if _, err := Init(other); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, filepath.Join(other, DirName))
	a, err := Open(repoDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if a.Dir != filepath.Join(other, DirName) {
		t.Errorf("Dir = %q, want FURROW_DIR store", a.Dir)
	}
	if a.DefaultLabel != "" {
		t.Errorf("DefaultLabel = %q, want empty (FURROW_DIR injects no label)", a.DefaultLabel)
	}
}

func TestDiscover_PointerBadBoardErrors(t *testing.T) {
	t.Setenv(EnvDir, "")
	repoDir := t.TempDir()
	body := "board = \"./nope/.furrow\"\n"
	if err := os.WriteFile(filepath.Join(repoDir, PointerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(repoDir); err == nil {
		t.Fatal("expected error for non-existent board, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/app/ -run TestDiscover -v`
Expected: FAIL — `undefined: PointerName` and `a.DefaultLabel` undefined.

- [ ] **Step 3: Write minimal implementation**

In `internal/app/app.go`:

(a) Add the pointer const next to `EnvDir` (after line 22):

```go
// PointerName is a repo-local file that redirects furrow at a central board
// (and optionally scopes it to a label) instead of holding its own .furrow.
const PointerName = ".furrow-pointer.toml"
```

(b) Add the field to the `App` struct (after `Dir string` at line 38):

```go
	DefaultLabel string // pointer-provided scope label ("" unless resolved via a .furrow-pointer.toml)
```

(c) Replace `Open` (lines 46-52) with:

```go
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
	return a, nil
}
```

(d) Replace `discover` (lines 68-98) with:

```go
// resolution is the outcome of discovery: which .furrow to open, and (only when
// reached via a pointer) the label to scope commands to.
type resolution struct {
	Dir          string
	DefaultLabel string
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
		if parent == dir { // reached the root
			return resolution{}, core.Validationf("", "no %s or %s found in %q or any parent; run `furrow init`", DirName, PointerName, startDir)
		}
		dir = parent
	}
}

// resolvePointer reads a .furrow-pointer.toml, resolves its board path against
// the pointer file's directory, and requires the board to be an existing dir.
func resolvePointer(pointerDir, pointerPath string) (resolution, error) {
	p, err := config.LoadPointer(pointerPath)
	if err != nil {
		return resolution{}, core.Validationf("", "%s: %v", pointerPath, err)
	}
	board := p.Board
	if strings.HasPrefix(board, "~") {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return resolution{}, core.Internalf("", "resolve ~ in board %q: %v", board, herr)
		}
		board = filepath.Join(home, strings.TrimPrefix(board, "~"))
	}
	if !filepath.IsAbs(board) {
		board = filepath.Join(pointerDir, board)
	}
	board = filepath.Clean(board)
	if fi, err := os.Stat(board); err != nil || !fi.IsDir() {
		return resolution{}, core.Validationf("", "%s: board %q is not an existing directory", pointerPath, board)
	}
	return resolution{Dir: board, DefaultLabel: p.DefaultLabel}, nil
}
```

Note: `internal/app/app.go` already imports `os`, `path/filepath`, `strings`, and `internal/config` — no import changes needed.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local go test ./internal/app/ -run TestDiscover -v`
Expected: PASS (5 tests). Then `GOTOOLCHAIN=local go test ./internal/app/` (whole package green — confirms the `discover` signature change didn't break callers).

- [ ] **Step 5: Commit**

```bash
git add internal/app/app.go internal/app/pointer_test.go
git commit -m "$(printf ':sparkles: feat(app): resolve .furrow-pointer.toml in discovery (central board + scope label)\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 3: `add` unions the pointer's default label

**Files:**
- Modify: `internal/app/app.go` (`Add`, around lines 150-164; add a helper near `contains` at line 521)
- Modify: `internal/app/import.go` (`AddMany`, the validation loop at lines 31-45)
- Test: `internal/app/pointer_test.go` (append)

**Interfaces:**
- Consumes: `App.DefaultLabel` (Task 2).
- Produces: `func (a *App) withDefaultLabel(labels []string) []string` — returns `labels` with `a.DefaultLabel` appended when set and not already present (a copy; never mutates the caller's slice). A no-op when `DefaultLabel == ""`.

- [ ] **Step 1: Write the failing test**

Append to `internal/app/pointer_test.go`:

```go
func contains2(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestAdd_InjectsDefaultLabel(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("a task", AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !contains2(task.Labels, "chord") {
		t.Errorf("labels = %v, want to contain chord", task.Labels)
	}
}

func TestAdd_UnionsWithExplicitLabel(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Add("a task", AddOpts{Labels: []string{"bug"}})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !contains2(task.Labels, "chord") || !contains2(task.Labels, "bug") {
		t.Errorf("labels = %v, want both chord and bug", task.Labels)
	}
}

func TestAddMany_InjectsDefaultLabel(t *testing.T) {
	repoDir, _ := pointerLayout(t, "chord")
	a, err := Open(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.AddMany([]AddSpec{{Title: "x"}, {Title: "y"}})
	if err != nil {
		t.Fatalf("AddMany: %v", err)
	}
	for _, task := range created {
		if !contains2(task.Labels, "chord") {
			t.Errorf("%s labels = %v, want to contain chord", task.ID, task.Labels)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/app/ -run 'TestAdd_|TestAddMany_' -v`
Expected: FAIL — labels do not contain `chord` (injection not implemented).

- [ ] **Step 3: Write minimal implementation**

(a) In `internal/app/app.go`, add the helper just above `func contains` (line 521):

```go
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
```

(b) In `Add` (app.go), inject right after the empty-title check and BEFORE the `LabelsRequired` check, so an injected label also satisfies `[labels].required`. Change:

```go
	if title == "" {
		return nil, core.Validationf("", "title must not be empty")
	}
	status := o.Status
```

to:

```go
	if title == "" {
		return nil, core.Validationf("", "title must not be empty")
	}
	o.Labels = a.withDefaultLabel(o.Labels)
	status := o.Status
```

(c) In `internal/app/import.go` `AddMany`, inject before the validation loop so both the `LabelsRequired` check and task creation see it. Change the start of the validate loop:

```go
	// validate every lane/title before writing anything.
	for i, s := range specs {
```

to:

```go
	// Union the pointer default label into every spec up front, so the
	// LabelsRequired check below and the created tasks both see it.
	for i := range specs {
		specs[i].Labels = a.withDefaultLabel(specs[i].Labels)
	}

	// validate every lane/title before writing anything.
	for i, s := range specs {
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local go test ./internal/app/ -run 'TestAdd_|TestAddMany_' -v`
Expected: PASS (3 tests). Then `GOTOOLCHAIN=local go test ./internal/app/` (whole package green).

- [ ] **Step 5: Commit**

```bash
git add internal/app/app.go internal/app/import.go internal/app/pointer_test.go
git commit -m "$(printf ':sparkles: feat(app): union pointer default_label into add/addmany\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 4: CLI read scoping + stderr banner for `ls`/`next`/`revisit`

**Files:**
- Modify: `internal/cli/output.go` (add the `errOut` writer near `out` at line 22)
- Create: `internal/cli/scope.go`
- Modify: `internal/cli/cmd_query.go` (`ls` RunE line 24, `next` RunE line 76, `revisit` RunE line 116)
- Test: `internal/cli/scope_test.go` (create)

**Interfaces:**
- Consumes: `App.DefaultLabel`, `App.Dir`.
- Produces: `func scopedLabel(cmd *cobra.Command, a *app.App, flagLabel string) string` — when `--label` is unset and `a.DefaultLabel != ""`, prints the scope banner to `errOut` and returns `a.DefaultLabel`; otherwise returns `flagLabel` unchanged (so an explicit `-l ''` means the whole board). Plus package writer `var errOut io.Writer = os.Stderr`.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/scope_test.go`:

```go
package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// labelCmd builds a throwaway command carrying the shared --label flag, so a
// test can toggle whether the flag was "Changed".
func labelCmd() (*cobra.Command, *string) {
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	var label string
	cmd.Flags().StringVarP(&label, "label", "l", "", "")
	return cmd, &label
}

func TestScopedLabel_DefaultAppliesAndAnnounces(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd() // --label NOT changed
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", Dir: "/b/.furrow"}, "")
	if got != "chord" {
		t.Errorf("label = %q, want chord", got)
	}
	if !strings.Contains(se.String(), "scope=label=chord") {
		t.Errorf("banner missing scope, stderr = %q", se.String())
	}
}

func TestScopedLabel_ExplicitEmptyEscapesNoBanner(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd()
	_ = cmd.Flags().Set("label", "") // Changed=true, value ""
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", Dir: "/b/.furrow"}, "")
	if got != "" {
		t.Errorf("label = %q, want empty (whole board)", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

func TestScopedLabel_ExplicitOtherWins(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd()
	_ = cmd.Flags().Set("label", "other")
	got := scopedLabel(cmd, &app.App{DefaultLabel: "chord", Dir: "/b/.furrow"}, "other")
	if got != "other" {
		t.Errorf("label = %q, want other", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}

func TestScopedLabel_NoPointerNoBanner(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	cmd, _ := labelCmd()
	got := scopedLabel(cmd, &app.App{DefaultLabel: "", Dir: "/b/.furrow"}, "")
	if got != "" {
		t.Errorf("label = %q, want empty", got)
	}
	if se.Len() != 0 {
		t.Errorf("expected no banner, stderr = %q", se.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/cli/ -run TestScopedLabel -v`
Expected: FAIL — `undefined: errOut` / `undefined: scopedLabel`.

- [ ] **Step 3: Write minimal implementation**

(a) In `internal/cli/output.go`, add right after `var out io.Writer = os.Stdout` (line 22):

```go
// errOut is stderr; overridable in tests. The scope banner and other
// human-facing notices go here so stdout stays pure data (JSON/table).
var errOut io.Writer = os.Stderr
```

(b) Create `internal/cli/scope.go`:

```go
package cli

import (
	"fmt"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/spf13/cobra"
)

// scopedLabel resolves the effective label filter for a read command (ls/next/
// revisit) when a per-repo pointer is active. With no explicit --label and a
// pointer default_label, it returns that label and announces the scope on stderr
// — so the filtering is never silent and stdout stays pure data. An explicit
// --label always wins, including --label "" which means "the whole board".
// Returns flagLabel unchanged when no pointer label is set.
func scopedLabel(cmd *cobra.Command, a *app.App, flagLabel string) string {
	if cmd.Flags().Changed("label") || a.DefaultLabel == "" {
		return flagLabel
	}
	fmt.Fprintf(errOut, "furrow: board=%s scope=label=%s (-l '' for all)\n", a.Dir, a.DefaultLabel)
	return a.DefaultLabel
}
```

(c) In `internal/cli/cmd_query.go`, apply the scope in each read command, right after `openApp()` succeeds.

`ls` RunE — change:

```go
			tasks, err := a.List(app.QueryOpts{Status: status, Label: label, Limit: limit})
```
to:
```go
			tasks, err := a.List(app.QueryOpts{Status: status, Label: scopedLabel(cmd, a, label), Limit: limit})
```

`next` RunE — change:

```go
			tasks, err := a.Next(label, limit)
```
to:
```go
			tasks, err := a.Next(scopedLabel(cmd, a, label), limit)
```

`revisit` RunE — change:

```go
			items, err := a.Revisit(label, days, limit)
```
to:
```go
			items, err := a.Revisit(scopedLabel(cmd, a, label), days, limit)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local go test ./internal/cli/ -run TestScopedLabel -v`
Expected: PASS (4 tests). Then `GOTOOLCHAIN=local go test ./internal/cli/` (whole package green — existing ls/next/revisit tests use FURROW_DIR, so `DefaultLabel==""` and behavior is unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/output.go internal/cli/scope.go internal/cli/cmd_query.go internal/cli/scope_test.go
git commit -m "$(printf ':sparkles: feat(cli): scope ls/next/revisit by pointer label, announce on stderr\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 5: Documentation + full verification

**Files:**
- Modify: `README.md`
- Modify: `README.ja.md`

**Interfaces:** none (docs + verification only).

- [ ] **Step 1: Add a "Central board (per-repo pointer)" section to `README.md`**

Find the section that explains the store / `FURROW_DIR` / discovery (search for `FURROW_DIR`). Immediately after it, add:

```markdown
### Central board: per-repo pointer

A repo without its own `.furrow` can point at a central board (e.g. a private
cross-repo tracker) and have furrow auto-scope to that repo's label. Drop a
`.furrow-pointer.toml` at the repo root:

```toml
board = "../projects/.furrow"   # the central .furrow (relative to this file, ~, or absolute)
default_label = "chord"         # optional: scope this repo to one label
```

Discovery precedence: `FURROW_DIR` (explicit, no label injection) → the nearest
ancestor directory holding a `.furrow` (a real local store wins) → a
`.furrow-pointer.toml` redirecting to the board → `furrow init`.

With a pointer in effect:

- `furrow add "…"` unions `default_label` into the task's labels (and satisfies
  `[labels].required`); an explicit `-l x` adds to it rather than replacing.
- `furrow ls|next|revisit` filter to `default_label` and print the active scope
  to stderr, e.g. `furrow: board=… scope=label=chord (-l '' for all)`. Pass
  `-l ''` to see the whole board, or `-l other` for another label.
```

- [ ] **Step 2: Mirror the section in `README.ja.md`**

Add the equivalent Japanese section in the matching location (after the `FURROW_DIR`/discovery section):

```markdown
### 中央ボード: per-repo pointer

自前の `.furrow` を持たない repo から、中央ボード（横断トラッカー等）を指し、その
repo のラベルへ自動スコープできる。repo 直下に `.furrow-pointer.toml` を置く:

```toml
board = "../projects/.furrow"   # 中央 .furrow（本ファイル基準の相対・~・絶対）
default_label = "chord"         # 任意: この repo を 1 ラベルにスコープ
```

発見の優先順位: `FURROW_DIR`（明示・ラベル注入なし）→ 直近の親で `.furrow` を持つ
ディレクトリ（実体のローカルストアが勝つ）→ `.furrow-pointer.toml`（中央ボードへ
redirect）→ `furrow init`。

pointer 有効時:

- `furrow add "…"` は `default_label` をラベルに union（`[labels].required` も充足）。
  明示 `-l x` は置換でなく追加。
- `furrow ls|next|revisit` は `default_label` で絞り、スコープを stderr に表示
  （例 `furrow: board=… scope=label=chord (-l '' for all)`）。`-l ''` で全件、
  `-l other` で別ラベル。
```

- [ ] **Step 3: Commit the docs**

```bash
git add README.md README.ja.md
git commit -m "$(printf ':memo: docs: document per-repo pointer (.furrow-pointer.toml)\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

- [ ] **Step 4: Run the full verification gate**

Run: `sh scripts/check.sh`
Expected: green — marshaller single-path guard, `go build`/`vet`/`go test ./...`, golangci-lint, schema/config drift diffs, and the CLI smoke all pass. (Green here == green CI.)

If golangci flags style (e.g. an unused param or a comment-format nit), fix inline and re-run until green. Do NOT silence lints.

- [ ] **Step 5: Final sanity — manual smoke of the new behavior**

```bash
# Build once.
GOTOOLCHAIN=local go build -o /tmp/furrow-dev ./cmd/furrow
# Stand up a throwaway central board + a pointer repo.
tmp=$(mktemp -d); (cd "$tmp" && mkdir central repo && cd central && /tmp/furrow-dev init >/dev/null)
printf 'board = "../central/.furrow"\ndefault_label = "demo"\n' > "$tmp/repo/.furrow-pointer.toml"
# add from the repo: should tag `demo`; ls should scope + banner on stderr.
(cd "$tmp/repo" && /tmp/furrow-dev add "hello from repo" --json | grep -q '"demo"' && echo "add-inject OK")
(cd "$tmp/repo" && /tmp/furrow-dev ls 2>/tmp/se >/tmp/so; grep -q 'scope=label=demo' /tmp/se && echo "banner-on-stderr OK"; grep -q 'hello from repo' /tmp/so && echo "scoped-list OK")
(cd "$tmp/repo" && /tmp/furrow-dev ls -l '' --json 2>/dev/null | grep -q 'hello from repo' && echo "escape -l '' OK")
rm -rf "$tmp"
```
Expected: prints `add-inject OK`, `banner-on-stderr OK`, `scoped-list OK`, `escape -l '' OK`.

---

## Self-Review (filled in at plan-writing time)

**1. Spec coverage:**
- ① pointer file format → Task 1 (parse) + Task 5 (document the keys).
- ② discovery precedence → Task 2 (`discover` rewrite + 5 precedence tests).
- ③ behavior: add union → Task 3; ls/next/revisit scope + `-l ''`/`-l other` → Task 4.
- ④ stderr banner, stdout pure → Task 4 (`scopedLabel` → `errOut`; banner not gated on `--json`).
- ⑤ layers: TOML in config (T1), fs resolution in app (T2), flags/banner in cli (T4) → covered; `core` untouched.
- ⑥ error handling: missing/empty board, malformed TOML, non-existent board → T1 + T2 tests.
- ⑦ tests headless, no TUI/teatest → all tests are app/config/cli unit-level; `scripts/check.sh` in T5.
- ⑧ docs README + README.ja → T5; global CLAUDE.md note is a post-merge handoff (tracked in t-nqnp body), not a code task.

**2. Placeholder scan:** No TBD/TODO; every code/test step shows full code; commands have expected output. `add` deliberately emits no banner (documented in spec ④, ID-based `show` unaffected).

**3. Type consistency:** `Pointer{Board, DefaultLabel}` (T1) consumed verbatim in T2 `resolvePointer`. `resolution{Dir, DefaultLabel}` (T2) feeds `App.DefaultLabel` (T2), read by `withDefaultLabel` (T3) and `scopedLabel` (T4). `scopedLabel(cmd, a, flagLabel)` signature identical across T4 def and the three cmd_query call sites. `errOut` defined in output.go (T4a), used in scope.go (T4b) and scope_test.go.

**Out of scope (separate tasks, per spec):** README root reframe (t-tz9x), CI/PR intake (t-w2kk), `config.toml` `default_label` generalization, a `furrow init --pointer` scaffolder.
