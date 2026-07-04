package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// Repo resolution. A repo argument (-r/--repo, repo --add/--rm) is either a
// full owner/repo or a short name; short names resolve against the board's
// repo universe — the union of every task's repos and the board-derived repos
// (App.BoardRepos). Resolution is strict: an ambiguous or unresolvable short
// name is a validation error carrying the candidates, never a silent guess.

// repoUniverse is the short-name resolution universe for an index: every repo
// any task carries, unioned with the board-derived repos, sorted+deduped.
func repoUniverse(idx *core.Index, boardRepos []string) []string {
	set := map[string]bool{}
	for i := range idx.Tasks {
		for _, r := range idx.Tasks[i].Repos {
			set[r] = true
		}
	}
	for _, r := range boardRepos {
		set[r] = true
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// resolveRepoIn resolves one repo argument against the universe: a unique
// match (full or short form; case-insensitive, at a "/" boundary) resolves to
// the universe's canonical casing; an unmatched full owner/repo shape passes
// through verbatim (that is how a repo's first task gets attached); an
// ambiguous short name is a validation error carrying the candidates, and an
// unresolvable one is a plain validation error. id tags the error's task.
func resolveRepoIn(arg, id string, universe []string) (string, error) {
	m := core.RepoMatches(arg, universe)
	switch {
	case len(m) == 1:
		return m[0], nil
	case len(m) > 1:
		return "", &core.Error{
			Code:       core.CodeValidation,
			ID:         id,
			Msg:        fmt.Sprintf("repo %q is ambiguous (matches %s); use the full owner/repo form", arg, strings.Join(m, ", ")),
			Candidates: m,
		}
	case core.IsRepoShaped(arg):
		return arg, nil
	default:
		return "", core.Validationf(id, "repo %q matches no known repo; use the full owner/repo form", arg)
	}
}

// resolveRepoArgs maps resolveRepoIn over a flag's values.
func resolveRepoArgs(args []string, id string, universe []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(args))
	for _, a := range args {
		r, err := resolveRepoIn(a, id, universe)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ResolveRepo resolves a single -r/--repo argument for the read commands
// (ls/next/revisit): full owner/repo passes (canonicalized against the
// universe's casing when known), a short name must match exactly one known
// repo. See resolveRepoIn for the error contract.
func (a *App) ResolveRepo(arg string) (string, error) {
	idx, err := a.load()
	if err != nil {
		return "", err
	}
	return resolveRepoIn(arg, "", repoUniverse(idx, a.BoardRepos))
}

// ResolveRepos resolves a repeatable -r/--repo flag against the board's repo
// universe in a single load (each arg follows ResolveRepo's contract). It backs
// the archive command's repo scope; an empty args yields nil (no scope).
func (a *App) ResolveRepos(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	return resolveRepoArgs(args, "", repoUniverse(idx, a.BoardRepos))
}

// Rerepo attaches and/or detaches repos on a task — the repos-field mirror of
// Relabel. Both --add and --rm values go through strict resolution (full
// owner/repo, or a short name naming exactly one known repo); attaching a repo
// already present, or detaching one already absent, is a no-op so re-runs
// don't churn the diff. A call with neither is a bad-usage error. The
// marshaller keeps the stored set sorted and deduped.
func (a *App) Rerepo(id string, add, remove []string) (*core.Task, error) {
	if len(add) == 0 && len(remove) == 0 {
		return nil, core.Validationf(id, "provide at least one --add or --rm repo")
	}
	idx, err := a.load()
	if err != nil {
		return nil, err
	}
	t, i := idx.Find(id)
	if i < 0 {
		return nil, core.NotFound(id)
	}
	universe := repoUniverse(idx, a.BoardRepos)
	addR, err := resolveRepoArgs(add, id, universe)
	if err != nil {
		return nil, err
	}
	rmR, err := resolveRepoArgs(remove, id, universe)
	if err != nil {
		return nil, err
	}
	rm := make(map[string]bool, len(rmR))
	for _, r := range rmR {
		rm[r] = true
	}
	next := make([]string, 0, len(t.Repos)+len(addR))
	for _, r := range t.Repos {
		if !rm[r] {
			next = append(next, r)
		}
	}
	for _, r := range addR {
		if !contains(next, r) {
			next = append(next, r)
		}
	}
	return a.mutate(id, func(t *core.Task) { t.Repos = next })
}

// DidYouMeanRepo is the -l did-you-mean guard: when an explicit label filter
// came back empty, the label matches no task on the whole board, and it
// uniquely names a repo (short form) that does have tasks, it returns a
// validation error steering the caller to -r — with the repo in Candidates so
// an agent acts on the envelope, never the prose. It returns nil whenever the
// guard does not apply (the empty result then stands on its own); a pure tag
// that matches any task is never second-guessed.
func (a *App) DidYouMeanRepo(label string) error {
	idx, err := a.load()
	if err != nil {
		return nil // the primary query already surfaced any load error
	}
	for i := range idx.Tasks {
		if contains(idx.Tasks[i].Labels, label) {
			return nil // the label exists as a tag; the empty result is genuine
		}
	}
	m := core.RepoMatches(label, repoUniverse(idx, a.BoardRepos))
	if len(m) != 1 {
		return nil
	}
	n := 0
	for i := range idx.Tasks {
		if contains(idx.Tasks[i].Repos, m[0]) {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return &core.Error{
		Code:       core.CodeValidation,
		Msg:        fmt.Sprintf("label %q matches no tasks; repo %s has %d task(s) — use -r %s", label, m[0], n, label),
		Candidates: []string{m[0]},
	}
}
