package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// assertNotAStore checks the refusal every discovery arm owes a configured path
// that names an existing directory which is not a store: a validation error
// naming the setting, with the .furrow inside as the did-you-mean when there is
// one — and nothing written into the directory the path wrongly named.
func assertNotAStore(t *testing.T, err error, subject, wrong, inner string) {
	t.Helper()
	var fe *core.Error
	if !errors.As(err, &fe) || fe.Kind != core.KindValidation {
		t.Fatalf("want a validation error, got %#v", err)
	}
	if !strings.Contains(fe.Msg, subject) || !strings.Contains(fe.Msg, "not a furrow store") {
		t.Errorf("message must name %s and say it is not a store: %s", subject, fe.Msg)
	}
	if inner != "" && (len(fe.Candidates) != 1 || fe.Candidates[0] != inner) {
		t.Errorf("want did-you-mean candidate %q, got %v", inner, fe.Candidates)
	}
	for _, m := range storeMarkers {
		if _, serr := os.Stat(filepath.Join(wrong, m.name)); serr == nil {
			t.Errorf("%s was written into the directory the path wrongly named", m.name)
		}
	}
}

// t-tsds: FURROW_DIR at a board's REPO root (the commonest slip — /.furrow
// dropped) opened as a fresh, writable, empty board: reads exit 0 on nothing
// and the first `add` grew tasks/ + meta.json + bodies/ in the repo root.
func TestFurrowDirAtANonStoreDirIsRefused(t *testing.T) {
	writeGlobalConfig(t, "")
	t.Setenv(EnvBoard, "")
	repo := t.TempDir()
	inner := mustInitBoard(t, repo)
	t.Setenv(EnvDir, repo)

	_, err := Open(t.TempDir())
	assertNotAStore(t, err, `FURROW_DIR="`+repo+`"`, repo, inner)

	t.Setenv(EnvDir, inner)
	if _, err := Open(t.TempDir()); err != nil {
		t.Fatalf("the .furrow itself must open: %v", err)
	}
}

func TestPointerAtANonStoreDirIsRefused(t *testing.T) {
	writeGlobalConfig(t, "")
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	boardRepo := t.TempDir()
	inner := mustInitBoard(t, boardRepo)
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, PointerName), []byte("board = \""+boardRepo+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Open(checkout)
	assertNotAStore(t, err, `board "`+boardRepo+`"`, boardRepo, inner)
}

func TestCentralBoardAtANonStoreDirIsRefused(t *testing.T) {
	t.Setenv(EnvDir, "")
	root := t.TempDir()
	scope := filepath.Join(root, "org")
	boardRepo := filepath.Join(scope, "projects")
	inner := mustInitBoard(t, boardRepo)
	repoX := mkGitRepo(t, filepath.Join(scope, "repoX"))

	// A [[board]] entry naming the repo root, and FURROW_BOARD doing the same:
	// both reach the winner's check (repoX sits inside either scope).
	writeGlobalConfig(t, boardEntry(boardRepo, "auto", scope))
	t.Setenv(EnvBoard, "")
	_, err := Open(repoX)
	assertNotAStore(t, err, `[[board]] path "`+boardRepo+`"`, boardRepo, inner)

	// FURROW_BOARD names itself on both sides of its scope (t-xr78's give-up).
	writeGlobalConfig(t, "")
	t.Setenv(EnvBoard, boardRepo) // derived scope = root, which encloses repoX
	_, err = Open(repoX)
	assertNotAStore(t, err, `FURROW_BOARD="`+boardRepo+`"`, boardRepo, inner)

	// Outside its scope it also claims no scope "even once that store exists":
	// the fix moves the path, so the scope two levels above the WRONG path is
	// not the one that will apply.
	_, err = Open(t.TempDir())
	assertNotAStore(t, err, `FURROW_BOARD="`+boardRepo+`"`, boardRepo, inner)
	if strings.Contains(err.Error(), "even once") {
		t.Errorf("no post-fix scope may be derived from the wrong path: %v", err)
	}
}

// Each marker of its own kind makes a store, and so does an empty directory (a
// store-to-be, as before t-tsds); a marker of the wrong kind, an unrelated
// directory, and a directory holding a .furrow store — the repo-root slip,
// whatever else sits beside it — do not.
func TestIsStoreDirMarkers(t *testing.T) {
	for _, m := range storeMarkers {
		d := t.TempDir()
		p := filepath.Join(d, m.name)
		var err error
		if m.dir {
			err = os.Mkdir(p, 0o755)
		} else {
			err = os.WriteFile(p, []byte("{}"), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !isStoreDir(d) {
			t.Errorf("a directory holding %s must count as a store", m.name)
		}
		wrongKind := t.TempDir()
		if m.dir {
			err = os.WriteFile(filepath.Join(wrongKind, m.name), nil, 0o644)
		} else {
			err = os.Mkdir(filepath.Join(wrongKind, m.name), 0o755)
		}
		if err != nil {
			t.Fatal(err)
		}
		if isStoreDir(wrongKind) {
			t.Errorf("a %s of the wrong kind must not make a store", m.name)
		}
	}
	if !isStoreDir(t.TempDir()) {
		t.Error("an empty directory is a store-to-be")
	}
	unrelated := t.TempDir()
	if err := os.WriteFile(filepath.Join(unrelated, "README.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{unrelated, filepath.Join(unrelated, "README.md")} {
		if isStoreDir(d) {
			t.Errorf("%s must not count as a store", d)
		}
	}
	// A repo root that looks like a store (a Hugo config.toml, an Ansible
	// tasks/) but holds a real .furrow is the slip, not a store.
	root := t.TempDir()
	mustInitBoard(t, root)
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("baseURL = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isStoreDir(root) {
		t.Error("a directory holding a .furrow store is never a store itself")
	}
	t.Setenv(EnvDir, root)
	t.Setenv(EnvBoard, "")
	_, err := Open(t.TempDir())
	assertNotAStore(t, err, `FURROW_DIR="`+root+`"`, "", filepath.Join(root, DirName))

	// But a store furrow itself wrote stays one with a stray .furrow inside
	// (`furrow init .furrow` from its repo creates exactly that, exit 0), and so
	// does any path named .furrow — a .furrow is never a repo root.
	store := mustInitBoard(t, t.TempDir())
	t.Setenv(EnvDir, store)
	a, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("real task", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	// The stray an older `furrow init .furrow` left (init now refuses to nest).
	if err := os.MkdirAll(filepath.Join(store, DirName, "bodies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isStoreDir(store) {
		t.Error("a store furrow wrote stays a store with a nested .furrow")
	}
	if _, err := Open(t.TempDir()); err != nil {
		t.Errorf("FURROW_DIR at the real store must still open: %v", err)
	}
	named := filepath.Join(t.TempDir(), DirName)
	if err := os.MkdirAll(filepath.Join(named, "bodies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(named, DirName, "bodies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isStoreDir(named) {
		t.Error("a path named .furrow is never the repo-root slip")
	}
}

// What worked before t-tsds still works: a pre-created EMPTY directory named
// by a path is a store-to-be (the first write stamps it), and an unreadable
// store fails with its own permission error, never "not a furrow store".
func TestStoreCheckKeepsWhatWorked(t *testing.T) {
	writeGlobalConfig(t, "")
	t.Setenv(EnvBoard, "")
	empty := t.TempDir()
	t.Setenv(EnvDir, empty)
	a, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("an empty directory is a store-to-be: %v", err)
	}
	if _, err := a.Add("first", AddOpts{}); err != nil {
		t.Fatalf("the first write stamps the store-to-be: %v", err)
	}
	if !isStoreDir(empty) {
		t.Error("after its first write the directory is a store")
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory")
	}
	locked := mustInitBoard(t, t.TempDir())
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	t.Setenv(EnvDir, locked)
	_, err = Open(t.TempDir())
	if err == nil || strings.Contains(err.Error(), "not a furrow store") {
		t.Errorf("an unreadable store must fail with its own error, got %v", err)
	}
}

// Every exit and every doctor line must agree with what discovery accepts: no
// "unset FURROW_BOARD" toward a configured board that is no store; no
// board-missing beside env-override-broken for a FURROW_DIR at a repo root;
// none for a local .furrow the walk opens, marker or not.
func TestStoreCheckAgreesWithDiscovery(t *testing.T) {
	root := t.TempDir()
	org := filepath.Join(root, "org")
	boardRepo := filepath.Join(org, "projects")
	mustInitBoard(t, boardRepo)
	repoX := mkGitRepo(t, filepath.Join(org, "repoX"))
	writeGlobalConfig(t, boardEntry(boardRepo, "auto", org))
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, filepath.Join(root, "elsewhere", "gone", DirName))
	if _, err := Open(repoX); err == nil || strings.Contains(err.Error(), "unset FURROW_BOARD:") || !strings.Contains(err.Error(), "not an existing furrow store either") {
		t.Errorf("unsetting would land on the non-store [[board]] — no unset exit: %v", err)
	}

	t.Setenv(EnvBoard, "")
	t.Setenv(EnvDir, boardRepo)
	r := mustDoctor(t, "")
	if len(findProblems(r, "env-override-broken")) != 1 || len(findProblems(r, "board-missing")) != 1 {
		t.Errorf("want env-override-broken plus the [[board]] entry's own board-missing only, got %+v", r.Problems)
	}
	for _, i := range findProblems(r, "board-missing") {
		if r.Problems[i].ID != boardRepo {
			t.Errorf("board-missing must be the configured entry's, got %+v", r.Problems[i])
		}
	}

	t.Setenv(EnvDir, "")
	writeGlobalConfig(t, "")
	local := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(local, DirName, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(local); err != nil {
		t.Fatalf("the walk opens any local .furrow: %v", err)
	}
	if r := mustDoctor(t, local); len(findProblems(r, "board-missing")) != 0 {
		t.Errorf("doctor must not call the local .furrow discovery opened missing: %+v", r.Problems)
	}
}

// A directory with only dot-entries (a .git from cloning an empty board repo,
// a .DS_Store) is what an empty one was before t-tsds: a store-to-be. init
// fills an empty one, and refuses anything else as what it is — never calling
// a repo root ".furrow" — with the store inside as the did-you-mean. A
// markerless directory that can be searched but not listed is no store.
func TestStoreToBeAndInit(t *testing.T) {
	writeGlobalConfig(t, "")
	t.Setenv(EnvBoard, "")
	dots := t.TempDir()
	if err := os.Mkdir(filepath.Join(dots, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dots, ".DS_Store"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, dots)
	a, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("a dot-only directory is a store-to-be: %v", err)
	}
	if _, err := a.Add("first", AddOpts{}); err != nil {
		t.Fatal(err)
	}

	empty := t.TempDir()
	if _, err := InitAt(empty); err != nil {
		t.Errorf("init must fill an existing empty directory: %v", err)
	}
	if _, err := InitAt(empty); err == nil || !strings.Contains(err.Error(), "a furrow store already exists") {
		t.Errorf("init on a store must say a store exists, got %v", err)
	}
	repo := t.TempDir()
	inner := mustInitBoard(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = InitAt(repo)
	var fe *core.Error
	if !errors.As(err, &fe) || strings.Contains(fe.Msg, ".furrow already exists") || !strings.Contains(fe.Msg, "is not a furrow store") || len(fe.Candidates) != 1 || fe.Candidates[0] != inner {
		t.Errorf("init at a repo root must say what it is, with the store as did-you-mean: %#v", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root lists a mode-100 directory")
	}
	searchOnly := t.TempDir()
	if err := os.WriteFile(filepath.Join(searchOnly, "README.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(searchOnly, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(searchOnly, 0o755) })
	if isStoreDir(searchOnly) {
		t.Error("every marker provably absent: a search-only directory is no store")
	}
}

// With no override, doctor's dir-unresolved for a dir that a configured scope
// DOES enclose names the board that failed — not "no [[board]] scope encloses
// it" — and a configured entry naming a markerless local .furrow the walk
// opens is no board-missing.
func TestDoctorNamesTheBoardThatDecided(t *testing.T) {
	t.Setenv(EnvDir, "")
	t.Setenv(EnvBoard, "")
	root := t.TempDir()
	org := filepath.Join(root, "org")
	boardRepo := filepath.Join(org, "projects")
	mustInitBoard(t, boardRepo)
	writeGlobalConfig(t, boardEntry(boardRepo, "auto", org))
	repoX := filepath.Join(org, "repoX")
	if err := os.MkdirAll(repoX, 0o755); err != nil {
		t.Fatal(err)
	}
	r := mustDoctor(t, "", repoX)
	n := findProblems(r, "dir-unresolved")
	if len(n) != 1 || strings.Contains(r.Problems[n[0]].Msg, "no [[board]] scope encloses it") || !strings.Contains(r.Problems[n[0]].Msg, "[[board]] path") {
		t.Errorf("dir-unresolved must name the enclosing board's real failure: %+v", r.Problems)
	}

	local := filepath.Join(t.TempDir(), "repo")
	store := filepath.Join(local, DirName)
	if err := os.MkdirAll(filepath.Join(store, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGlobalConfig(t, boardEntry(store, "auto", local))
	r = mustDoctor(t, local)
	if len(findProblems(r, "board-missing")) != 0 {
		t.Errorf("the walk opens this .furrow; doctor must not call it missing: %+v", r.Problems)
	}
}

// The rules in order, each pinned so removing it fails a test: a directory
// holding furrow's own meta.json opens whatever sits beside it — a stray
// nested .furrow, or a repo root an older binary already wrote through the
// slip (furrow does not guess between two stores, as on 018ce1f) — while a
// FOREIGN meta.json does not count, and a dot-only directory holding a
// .furrow is no store-to-be.
func TestStoreRulesInOrder(t *testing.T) {
	t.Setenv(EnvBoard, "")
	writeGlobalConfig(t, "")

	named := filepath.Join(t.TempDir(), "boardstore") // not called .furrow
	if _, err := InitAt(named); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(named, "tasks")); err != nil { // the cloned shape
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(named, DirName, "bodies"), 0o755); err != nil { // a marked stray
		t.Fatal(err)
	}
	if !isStoreDir(named) {
		t.Error("furrow's meta.json makes a store, a stray nested .furrow or not")
	}

	polluted := t.TempDir()
	inner := mustInitBoard(t, polluted)
	b, err := os.ReadFile(filepath.Join(inner, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(polluted, "meta.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, polluted)
	if _, err := Open(t.TempDir()); err != nil {
		t.Errorf("a root an older binary already wrote into opens as before: %v", err)
	}

	foreign := t.TempDir()
	foreignInner := mustInitBoard(t, foreign)
	if err := os.WriteFile(filepath.Join(foreign, "meta.json"), []byte(`{"name": "site"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, foreign)
	_, err = Open(t.TempDir())
	assertNotAStoreMsg(t, err, foreignInner)

	dots := t.TempDir()
	for _, d := range []string{".git", DirName} {
		if err := os.Mkdir(filepath.Join(dots, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if storeToBe(dots) {
		t.Error("a dot-only directory holding a .furrow is no store-to-be")
	}
}

func assertNotAStoreMsg(t *testing.T, err error, inner string) {
	t.Helper()
	var fe *core.Error
	if !errors.As(err, &fe) || !strings.Contains(fe.Msg, "not a furrow store") || len(fe.Candidates) != 1 || fe.Candidates[0] != inner {
		t.Errorf("want a not-a-store refusal with did-you-mean %q, got %#v", inner, err)
	}
}

// FURROW_BOARD's not-a-store scope: from the did-you-mean when there is one
// ("fix it" only where the candidate's scope encloses the cwd), else from the
// path itself — never empty, which offered "fix it" from anywhere, cwd "/"
// included.
func TestEnvBoardNotAStoreScope(t *testing.T) {
	t.Setenv(EnvDir, "")
	writeGlobalConfig(t, "")
	root := t.TempDir()
	repo := filepath.Join(root, "org", "projects")
	mustInitBoard(t, repo)
	// The wrong path's own scope is root/org's parent = root; the candidate's
	// is root/org. A cwd under root but outside root/org sits inside the wrong
	// scope only: no fix-it, and the note names the candidate.
	cwd := mkGitRepo(t, filepath.Join(root, "other", "repoX"))
	t.Setenv(EnvBoard, repo)
	_, err := Open(cwd)
	if err == nil || strings.Contains(err.Error(), "fix it") || !strings.Contains(err.Error(), "pointed at") {
		t.Errorf("outside the candidate's scope: no fix-it, a note naming the candidate: %v", err)
	}

	junk := filepath.Join(root, "x", "y", "notastore")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(junk, "README.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvBoard, junk)
	_, err = Open(cwd) // its own scope root/x does not enclose root/other/repoX
	if err == nil || strings.Contains(err.Error(), "fix it") || !strings.Contains(err.Error(), "it holds README.md") {
		t.Errorf("an existing directory that is no store cannot be fixed in place — no fix-it, and say what is there: %v", err)
	}
}

// init keeps what it finds and never nests: an existing .gitattributes gains
// only the missing union lines; init refuses a target inside a store; a
// markerless local .furrow is called what discovery treats it as.
func TestInitKeepsAndNeverNests(t *testing.T) {
	dir := t.TempDir()
	user := "*.png binary\n*.md text eol=lf\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitAt(dir); err != nil {
		t.Fatalf("a dot-only directory is filled: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), user) || !strings.Contains(string(got), "bodies/*.md merge=union") {
		t.Errorf("the user's rules must survive and the union lines be added: %q", got)
	}

	store := mustInitBoard(t, t.TempDir())
	if _, err := InitAt(filepath.Join(store, DirName)); err == nil || !strings.Contains(err.Error(), "would nest a store") {
		t.Errorf("init inside a store must refuse: %v", err)
	}

	local := filepath.Join(t.TempDir(), DirName)
	if err := os.MkdirAll(filepath.Join(local, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InitAt(local); err == nil || !strings.Contains(err.Error(), "the walk from its repo opens it") {
		t.Errorf("a markerless local .furrow is an existing board to discovery: %v", err)
	}
}

// The refusal names what it found and how to get out — and claims no content
// for a directory it cannot list.
func TestNotStoreMessageNamesWhatItFound(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"README.md", "src"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := notStoreErr("FURROW_DIR", dir)
	if err == nil || !strings.Contains(err.Error(), "README.md, src") || !strings.Contains(err.Error(), "point it at a store") {
		t.Errorf("want the found entries and the remedy, got %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root lists a mode-0300 directory")
	}
	unlistable := t.TempDir()
	if err := os.Chmod(unlistable, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unlistable, 0o755) })
	if err := notStoreErr("FURROW_DIR", unlistable); err == nil || !strings.Contains(err.Error(), "cannot be listed") {
		t.Errorf("an unlistable directory must not get a content claim: %v", err)
	}
}

// init answers about the target before the parent, keeps a CRLF file CRLF, and
// fails before writing anything when it cannot read what it would merge into.
func TestInitOrderAndGitAttributesEdges(t *testing.T) {
	polluted := t.TempDir()
	inner := mustInitBoard(t, polluted)
	b, err := os.ReadFile(filepath.Join(inner, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(polluted, "meta.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitAt(inner); err == nil || !strings.Contains(err.Error(), "a furrow store already exists") {
		t.Errorf("init at an existing store says so, before any nesting verdict: %v", err)
	}

	crlf := t.TempDir()
	if err := os.WriteFile(filepath.Join(crlf, ".gitattributes"), []byte("*.png binary\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitAt(crlf); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(crlf, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "bodies/*.md merge=union\r\n") {
		t.Errorf("appended lines keep the file's CRLF: %q", got)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	locked := t.TempDir()
	ga := filepath.Join(locked, ".gitattributes")
	if err := os.WriteFile(ga, []byte("*.png binary\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ga, 0o644) })
	if _, err := InitAt(locked); err == nil {
		t.Fatal("an unreadable .gitattributes must fail init")
	}
	if _, err := os.Stat(filepath.Join(locked, "config.toml")); err == nil {
		t.Error("a failed init must leave no marker behind")
	}
}

// init puts a store where discovery looks: never at a git work tree's root
// (its store belongs in its .furrow), and never through a path that is a file.
func TestInitRefusesWorkTreeRootAndFiles(t *testing.T) {
	clone := t.TempDir()
	if err := os.Mkdir(filepath.Join(clone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InitAt(clone); err == nil || !strings.Contains(err.Error(), "root of a git work tree") || !strings.Contains(err.Error(), filepath.Join(clone, DirName)) {
		t.Errorf("init at a work tree's root must point at its .furrow: %v", err)
	}
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitAt(file); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("init at a file must say so, not blame .gitattributes: %v", err)
	}
}

// furrow's own data settles rule 1 even when meta.json does not: shards that
// core decodes under their own id, or furrow's archive sub-store — so a store
// whose meta.json is conflicted or gone (fsstore's own remedy deletes it)
// keeps opening, stray .furrow beside it or not. A foreign tasks/*.json is not
// furrow data, and a self-linked .furrow is not the slip.
func TestFurrowDataSettlesRuleOne(t *testing.T) {
	t.Setenv(EnvBoard, "")
	writeGlobalConfig(t, "")
	store := filepath.Join(t.TempDir(), "boardstore")
	if _, err := InitAt(store); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, store)
	a, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add("real", AddOpts{}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store, DirName, "bodies"), 0o755); err != nil { // a marked stray
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "meta.json"), []byte("<<<<<<< ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isStoreDir(store) {
		t.Error("a store's own shards settle it, its meta.json conflicted or not")
	}
	if err := os.Remove(filepath.Join(store, "meta.json")); err != nil {
		t.Fatal(err)
	}
	if !isStoreDir(store) {
		t.Error("a store's own shards settle it after fsstore's remedy deleted meta.json")
	}

	archived := t.TempDir()
	if err := os.MkdirAll(filepath.Join(archived, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(mustInitBoard(t, t.TempDir()), "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archived, "archive", "meta.json"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archived, "README.md"), nil, 0o644); err != nil { // no marker of its own
		t.Fatal(err)
	}
	if !isStoreDir(archived) {
		t.Error("furrow's archive sub-store is furrow data")
	}

	foreign := t.TempDir()
	mustInitBoard(t, foreign)
	if err := os.MkdirAll(filepath.Join(foreign, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "tasks", "deploy.json"), []byte(`{"id": "other"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if furrowData(foreign) || isStoreDir(foreign) {
		t.Error("a foreign tasks/*.json is not furrow data; the repo root stays the slip")
	}

	self := t.TempDir()
	if err := os.WriteFile(filepath.Join(self, "index.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", filepath.Join(self, DirName)); err != nil {
		t.Fatal(err)
	}
	if !isStoreDir(self) {
		t.Error("a .furrow that links back to the directory itself is not the slip")
	}
}

// init fills a board repo cloned as a repo's .furrow (where the walk looks),
// and treats a .git FILE (a linked worktree, a submodule) as a work tree root.
func TestInitGitRootEdges(t *testing.T) {
	clone := filepath.Join(t.TempDir(), DirName)
	if err := os.MkdirAll(filepath.Join(clone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InitAt(clone); err != nil {
		t.Errorf("a board repo cloned as .furrow is filled: %v", err)
	}
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitAt(wt); err == nil || !strings.Contains(err.Error(), "root of a git work tree") {
		t.Errorf("a .git file marks a work tree root too: %v", err)
	}
}
