package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Board-scope repo derivation (repo = "auto"): turn the enclosing checkout into
// an owner/repo identifier by FILE READS ONLY — no git subprocess. The chain is
// origin URL -> ghq-style path -> nothing (the scope stays empty and add
// creates drafts, with a stderr warning). The invariant this file guards: every
// value it returns is owner/repo-shaped (core.IsRepoShaped) — a bare directory
// name is NEVER derived, so it can never be written into a task's repos.

// deriveScopeRepo turns a board's/pointer's repo mode into the actual scope
// repo: "auto" -> derive from the nearest enclosing git repo (empty plus a
// warning when it fails), "" -> no scope, a literal owner/repo -> itself. A
// literal that is not owner/repo-shaped is ignored with a warning
// (clamp-don't-reject; the bare-name invariant holds at the entrance, the
// `furrow lint` shape warn is only a backstop).
func deriveScopeRepo(mode, startDir string) (repo string, warn []string) {
	switch mode {
	case "auto":
		dir, ok := nearestGitDir(startDir)
		if !ok {
			return "", []string{"furrow: board active but no enclosing git repo; no repo scope (new tasks are drafts; use -r to attach)"}
		}
		if r, ok := originRepo(dir); ok {
			return r, nil
		}
		if r, ok := ghqRepo(dir); ok {
			return r, nil
		}
		return "", []string{fmt.Sprintf("furrow: cannot derive owner/repo for %s (no usable origin URL or ghq-style path); new tasks are drafts (use -r to attach)", dir)}
	case "":
		return "", nil
	default:
		if core.IsRepoShaped(mode) {
			return mode, nil
		}
		return "", []string{fmt.Sprintf("furrow: board repo %q is not owner/repo-shaped; ignoring it (use \"auto\", \"\", or owner/repo)", mode)}
	}
}

// nearestGitDir walks up from startDir looking for a directory holding a `.git`
// entry (a dir for a normal repo, a file for a worktree/submodule). Returns
// ("", false) when no git repo encloses startDir.
func nearestGitDir(startDir string) (string, bool) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// originRepo derives owner/repo from repoDir's git config: locate the config
// file (worktree-aware), take the FIRST url of [remote "origin"], parse it.
func originRepo(repoDir string) (string, bool) {
	cfg, ok := gitConfigPath(repoDir)
	if !ok {
		return "", false
	}
	// #nosec G304 -- cfg is a git config path resolved from repoDir (cwd),
	// read only to derive owner/repo; not attacker-supplied.
	data, err := os.ReadFile(cfg)
	if err != nil {
		return "", false
	}
	u, ok := originURL(string(data))
	if !ok {
		return "", false
	}
	return parseGitURL(u)
}

// gitConfigPath resolves repoDir's SHARED git config file. A `.git` directory
// holds it directly. A `.git` FILE (worktree/submodule) is a `gitdir: <path>`
// redirect: follow it, then follow that dir's `commondir` file (a worktree's
// pointer back to the shared .git — this is what makes a worktree named
// `chord-fix-y` still derive akira-toriyama/chord). A submodule gitdir has no
// commondir and carries its own config, which is the right one to read.
func gitConfigPath(repoDir string) (string, bool) {
	gitPath := filepath.Join(repoDir, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return filepath.Join(gitPath, "config"), true
	}
	// #nosec G304 -- gitPath is repoDir/.git (a git-managed pointer file), read
	// to follow the worktree gitdir redirect; not attacker-supplied.
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", false
	}
	first, _, _ := strings.Cut(string(data), "\n")
	target, found := strings.CutPrefix(strings.TrimSpace(first), "gitdir:")
	if !found {
		return "", false
	}
	gitdir := strings.TrimSpace(target)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(repoDir, gitdir)
	}
	// #nosec G304 -- gitdir comes from the .git pointer file above; reading its
	// commondir follows git's own worktree layout, not attacker-supplied input.
	if cd, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common := strings.TrimSpace(string(cd))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
		return filepath.Join(common, "config"), true
	}
	return filepath.Join(gitdir, "config"), true
}

// originURL extracts the FIRST `url` value of [remote "origin"] from git-config
// text, parsed as section-aware INI. Only that one line counts: never
// `pushurl`, never a second `url` line (a real-world config carried a foreign
// repo's URL as the second url), never another remote's url.
func originURL(data string) (string, bool) {
	inOrigin := false
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inOrigin = isOriginHeader(line)
			continue
		}
		if !inOrigin {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "url") {
			continue
		}
		// The first url line is authoritative even if unusable — falling
		// through to a second line is exactly the misattribution this guards.
		return gitConfigValue(v), true
	}
	return "", false
}

// isOriginHeader reports whether a trimmed `[...]` line opens [remote "origin"]
// (section name case-insensitive, subsection exact — git's own rule).
func isOriginHeader(line string) bool {
	if !strings.HasSuffix(line, "]") {
		return false
	}
	name, sub, ok := strings.Cut(strings.TrimSpace(line[1:len(line)-1]), " ")
	return ok && strings.EqualFold(name, "remote") && strings.TrimSpace(sub) == `"origin"`
}

// gitConfigValue trims a git-config value: surrounding quotes are stripped, an
// unquoted value ends at a `#`/`;` comment.
func gitConfigValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		return v[1 : len(v)-1]
	}
	if i := strings.IndexAny(v, "#;"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// parseGitURL turns a git remote URL into owner/repo. Supported forms: scp-like
// (git@github.com:o/r.git), ssh://, git+ssh://, git://, and http(s):// — each
// with or without the .git suffix. Anything whose path is not exactly
// owner/repo-shaped (e.g. a host-less local path, or a forge with nested
// groups) is ok=false, and the caller falls through the derivation chain.
func parseGitURL(u string) (string, bool) {
	u = strings.TrimSpace(u)
	var path string
	if _, rest, ok := strings.Cut(u, "://"); ok {
		_, path, ok = strings.Cut(rest, "/") // drop [user@]host[:port]
		if !ok {
			return "", false
		}
	} else if _, p, ok := strings.Cut(u, ":"); ok {
		path = p // scp-like [user@]host:path
	} else {
		return "", false
	}
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if !core.IsRepoShaped(path) {
		return "", false
	}
	return path, true
}

// ghqRepo derives owner/repo from a ghq-style path: a host-like component
// followed by <owner>/<repo> anywhere in repoDir (e.g.
// ~/src/github.com/me/proj). The match closest to the repo wins. This is the
// fallback for a checkout with no usable origin (typically a repo not pushed
// yet), keeping the owner/repo invariant without a network guess.
func ghqRepo(repoDir string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(repoDir)), "/")
	for i := len(parts) - 3; i >= 0; i-- {
		if !hostLikeRe.MatchString(parts[i]) {
			continue
		}
		if cand := parts[i+1] + "/" + parts[i+2]; core.IsRepoShaped(cand) {
			return cand, true
		}
	}
	return "", false
}

// hostLikeRe matches a plausible forge host path component (github.com,
// gitlab.example-org.io, …): dotted DNS labels, nothing more exotic.
var hostLikeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*(\.[A-Za-z0-9][A-Za-z0-9-]*)+$`)
