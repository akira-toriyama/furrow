package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{"git@github.com:akira-toriyama/furrow.git", "akira-toriyama/furrow", true},
		{"git@github.com:akira-toriyama/furrow", "akira-toriyama/furrow", true},
		{"ssh://git@github.com/akira-toriyama/furrow.git", "akira-toriyama/furrow", true},
		{"ssh://git@github.com:22/akira-toriyama/furrow.git", "akira-toriyama/furrow", true},
		{"git+ssh://git@github.com/akira-toriyama/furrow.git", "akira-toriyama/furrow", true},
		{"https://github.com/akira-toriyama/furrow.git", "akira-toriyama/furrow", true},
		{"https://github.com/akira-toriyama/furrow", "akira-toriyama/furrow", true},
		{"https://github.com/akira-toriyama/furrow/", "akira-toriyama/furrow", true},
		{"git://github.com/o/r.git", "o/r", true},
		{"github.com:o/r.git", "o/r", true},              // scp-like without user@
		{"git@host.example:/o/r.git", "o/r", true},       // scp-like with an absolute path
		{"https://gitlab.com/group/sub/proj", "", false}, // nested groups are not owner/repo
		{"/srv/git/repo.git", "", false},                 // local path: no owner
		{"file:///srv/git/repo.git", "", false},
		{"", "", false},
		{"https://github.com/", "", false},
	}
	for _, c := range cases {
		got, ok := parseGitURL(c.url)
		if got != c.want || ok != c.ok {
			t.Errorf("parseGitURL(%q) = (%q, %v), want (%q, %v)", c.url, got, ok, c.want, c.ok)
		}
	}
}

func TestOriginURL(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want string
		ok   bool
	}{
		{
			name: "plain origin",
			cfg:  "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = git@github.com:o/r.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n",
			want: "git@github.com:o/r.git", ok: true,
		},
		{
			name: "pushurl ignored",
			cfg:  "[remote \"origin\"]\n\tpushurl = git@github.com:evil/other.git\n\turl = https://github.com/o/r\n",
			want: "https://github.com/o/r", ok: true,
		},
		{
			name: "second url line ignored (first wins)",
			cfg:  "[remote \"origin\"]\n\turl = git@github.com:o/r.git\n\turl = git@github.com:foreign/repo.git\n",
			want: "git@github.com:o/r.git", ok: true,
		},
		{
			name: "other remotes ignored",
			cfg:  "[remote \"upstream\"]\n\turl = git@github.com:up/stream.git\n[remote \"origin\"]\n\turl = git@github.com:o/r.git\n[remote \"fork\"]\n\turl = git@github.com:f/k.git\n",
			want: "git@github.com:o/r.git", ok: true,
		},
		{
			name: "key outside a remote section never matches",
			cfg:  "[core]\n\turl = git@github.com:not/aremote.git\n",
			want: "", ok: false,
		},
		{
			name: "no origin",
			cfg:  "[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = git@github.com:up/stream.git\n",
			want: "", ok: false,
		},
		{
			name: "empty config",
			cfg:  "",
			want: "", ok: false,
		},
		{
			name: "case-insensitive section and key, quoted value",
			cfg:  "[Remote \"origin\"]\n\tURL = \"git@github.com:o/r.git\"\n",
			want: "git@github.com:o/r.git", ok: true,
		},
		{
			name: "subsection is case-sensitive (Origin != origin)",
			cfg:  "[remote \"Origin\"]\n\turl = git@github.com:o/r.git\n",
			want: "", ok: false,
		},
		{
			name: "comments skipped",
			cfg:  "[remote \"origin\"]\n\t# url = git@github.com:commented/out.git\n\turl = git@github.com:o/r.git # the real one\n",
			want: "git@github.com:o/r.git", ok: true,
		},
	}
	for _, c := range cases {
		got, ok := originURL(c.cfg)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: originURL = (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// mkGitRepoWithOrigin creates dir with a .git DIRECTORY whose config carries
// the given origin URL ("" = a config with no origin).
func mkGitRepoWithOrigin(t *testing.T, dir, url string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[core]\n\tbare = false\n"
	if url != "" {
		cfg += "[remote \"origin\"]\n\turl = " + url + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDeriveScopeRepo_AutoFromOrigin(t *testing.T) {
	dir := mkGitRepoWithOrigin(t, filepath.Join(t.TempDir(), "repoX"), "git@github.com:me/proj.git")
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, warn := deriveScopeRepo("auto", sub)
	if repo != "me/proj" || len(warn) != 0 {
		t.Errorf("deriveScopeRepo = (%q, %v), want (me/proj, no warnings)", repo, warn)
	}
}

func TestDeriveScopeRepo_NoGitRepoWarns(t *testing.T) {
	repo, warn := deriveScopeRepo("auto", t.TempDir())
	if repo != "" || len(warn) != 1 {
		t.Errorf("deriveScopeRepo = (%q, %v), want empty + one warning", repo, warn)
	}
}

// The bare-name invariant at the entrance: a checkout with no usable origin and
// no ghq-style path derives NOTHING (empty + warning) — never the directory
// basename.
func TestDeriveScopeRepo_NeverBareDirName(t *testing.T) {
	dir := mkGitRepoWithOrigin(t, filepath.Join(t.TempDir(), "repoX"), "")
	repo, warn := deriveScopeRepo("auto", dir)
	if repo != "" {
		t.Fatalf("derived %q from a repo with no origin; a bare dir name must never be derived", repo)
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "draft") {
		t.Errorf("warn = %v, want one drafts warning", warn)
	}
}

func TestDeriveScopeRepo_GhqPathFallback(t *testing.T) {
	root := t.TempDir()
	dir := mkGitRepoWithOrigin(t, filepath.Join(root, "src", "github.com", "me", "proj"), "")
	repo, warn := deriveScopeRepo("auto", dir)
	if repo != "me/proj" || len(warn) != 0 {
		t.Errorf("deriveScopeRepo = (%q, %v), want (me/proj, no warnings)", repo, warn)
	}
}

func TestDeriveScopeRepo_OriginBeatsGhqPath(t *testing.T) {
	root := t.TempDir()
	dir := mkGitRepoWithOrigin(t, filepath.Join(root, "github.com", "path", "name"), "git@github.com:origin/wins.git")
	repo, _ := deriveScopeRepo("auto", dir)
	if repo != "origin/wins" {
		t.Errorf("repo = %q, want origin/wins (URL beats the path fallback)", repo)
	}
}

func TestDeriveScopeRepo_LiteralAndEmpty(t *testing.T) {
	if repo, warn := deriveScopeRepo("me/proj", t.TempDir()); repo != "me/proj" || len(warn) != 0 {
		t.Errorf("literal = (%q, %v), want (me/proj, none)", repo, warn)
	}
	if repo, warn := deriveScopeRepo("", t.TempDir()); repo != "" || len(warn) != 0 {
		t.Errorf("empty = (%q, %v), want (\"\", none)", repo, warn)
	}
	// A non-owner/repo literal is clamped away with a warning, never stored.
	repo, warn := deriveScopeRepo("justaname", t.TempDir())
	if repo != "" || len(warn) != 1 {
		t.Errorf("bare literal = (%q, %v), want empty + one warning", repo, warn)
	}
}

func TestGhqRepo(t *testing.T) {
	cases := []struct {
		dir  string
		want string
		ok   bool
	}{
		{"/ws/src/github.com/me/proj", "me/proj", true},
		{"/Volumes/workspace/github.com/akira-toriyama/furrow", "akira-toriyama/furrow", true},
		{"/home/u/plain/dir", "", false},
		{"/github.com", "", false}, // host with nothing under it
	}
	for _, c := range cases {
		got, ok := ghqRepo(c.dir)
		if got != c.want || ok != c.ok {
			t.Errorf("ghqRepo(%q) = (%q, %v), want (%q, %v)", c.dir, got, ok, c.want, c.ok)
		}
	}
}

// A REAL `git worktree` (built via exec git — tests only; runtime code never
// execs git): the worktree's .git FILE redirects via gitdir + commondir to the
// main repo's shared config, so a worktree dir named chord-fix-y still derives
// the main repo's owner/repo. This is the fix for the known worktree dir-name
// mismatch.
func TestDeriveScopeRepo_WorktreeFollowsCommondir(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	root := t.TempDir()
	main := filepath.Join(root, "chord")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+root)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(main, "init", "-b", "main", ".")
	run(main, "remote", "add", "origin", "git@github.com:akira-toriyama/chord.git")
	run(main, "commit", "--allow-empty", "-m", "seed")
	wt := filepath.Join(root, "chord-fix-y")
	run(main, "worktree", "add", wt)

	// Sanity: the worktree's .git must be a FILE, or this test proves nothing.
	if fi, err := os.Lstat(filepath.Join(wt, ".git")); err != nil || fi.IsDir() {
		t.Fatalf(".git in the worktree should be a file, got %v, %v", fi, err)
	}
	repo, warn := deriveScopeRepo("auto", wt)
	if repo != "akira-toriyama/chord" {
		t.Errorf("worktree derived %q, want akira-toriyama/chord (gitdir -> commondir -> shared config)", repo)
	}
	if len(warn) != 0 {
		t.Errorf("warn = %v, want none", warn)
	}
}

// A hand-built worktree layout (no git binary needed): .git file -> gitdir ->
// commondir -> shared config, all with RELATIVE paths.
func TestGitConfigPath_GitFileRelativeGitdirAndCommondir(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "main", ".git")
	wtGitdir := filepath.Join(shared, "worktrees", "wt")
	if err := os.MkdirAll(wtGitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:o/r.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGitdir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: ../main/.git/worktrees/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ok := gitConfigPath(wt)
	if !ok || cfg != filepath.Join(shared, "config") {
		t.Errorf("gitConfigPath = (%q, %v), want the shared config", cfg, ok)
	}
	if repo, _ := deriveScopeRepo("auto", wt); repo != "o/r" {
		t.Errorf("derived %q, want o/r", repo)
	}
}

// A submodule-style .git file: gitdir with NO commondir reads that gitdir's own
// config (a submodule's origin lives there).
func TestGitConfigPath_SubmoduleGitdirOwnConfig(t *testing.T) {
	root := t.TempDir()
	gitdir := filepath.Join(root, "parent", ".git", "modules", "sub")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "parent", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ok := gitConfigPath(sub)
	if !ok || cfg != filepath.Join(gitdir, "config") {
		t.Errorf("gitConfigPath = (%q, %v), want the module's own config", cfg, ok)
	}
}

func TestGitConfigPath_MalformedGitFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("not a gitdir line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := gitConfigPath(dir); ok {
		t.Error("a .git file without a gitdir: line must not resolve")
	}
}
