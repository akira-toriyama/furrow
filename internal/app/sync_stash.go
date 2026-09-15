// The autostash probe: git can strand an operator's working-tree edits in
// the stash and exit 0, so every sync reads the stash back and reports what
// is held there. Reporting only, on the failure path's contract.

package app

import (
	"context"
	"strings"

	"github.com/akira-toriyama/furrow/internal/gitrepo"
)

// StashEntry is one autostash entry left holding working-tree changes. Commit is
// the stable handle (the stash@{N} in Ref is an INDEX that shifts as entries are
// pushed and dropped); Paths says which files are in there, so "did it eat my
// body?" is answerable without running git.
type StashEntry struct {
	Ref    string   `json:"ref"`
	Commit string   `json:"commit"`
	Paths  []string `json:"paths"`
}

// autostashCommits is the cheap BEFORE probe: the oids of the autostash entries
// already in the stash when the pull starts. Taking it first is what lets a
// leftover from an earlier sync (or an earlier machine) be told apart from one
// THIS sync just stranded — the difference between a nudge and a failure.
func autostashCommits(ctx context.Context, r *gitrepo.Repo) map[string]bool {
	set := map[string]bool{}
	for _, e := range r.StashEntries(ctx) {
		if e.Subject == gitrepo.AutostashSubject {
			set[e.Commit] = true
		}
	}
	return set
}

// autostashEntries resolves the stash entries git stored on OUR behalf — subject
// "autostash", never an operator's own `git stash` ("WIP on …") — to the reported
// form (ref + stable oid + the paths they are holding hostage).
func autostashEntries(ctx context.Context, r *gitrepo.Repo) []StashEntry {
	var out []StashEntry
	for _, e := range r.StashEntries(ctx) {
		if e.Subject != gitrepo.AutostashSubject {
			continue
		}
		out = append(out, StashEntry{Ref: e.Ref, Commit: e.Commit, Paths: r.StashedPaths(ctx, e.Commit)})
	}
	return out
}

// strandedStash is the AFTER probe: the autostash entries holding working-tree
// changes once the pull has finished (or been aborted), plus whether any of them
// is new — i.e. whether this sync is the one that stranded it.
//
// This exists because git's failure here is silent by construction. `git rebase
// --autostash` re-applies the stash at the end (or on abort); when that apply
// conflicts, git stores the entry back in the stash, prints a warning to stderr,
// and exits 0. The dirty files are simply gone from the working tree. So an exit
// code cannot see it and neither can a rebase-in-progress probe — only the stash
// itself can. We compare refs rather than grep git's warning because git localizes
// its prose but not `stash store -m autostash`'s subject.
func strandedStash(ctx context.Context, r *gitrepo.Repo, before map[string]bool) (all []StashEntry, fresh bool) {
	all = autostashEntries(ctx, r)
	for _, e := range all {
		if !before[e.Commit] {
			fresh = true
		}
	}
	return all, fresh
}

// stashSummary renders the entries for a human error line: "stash@{0} (bodies/t-x.md)".
func stashSummary(entries []StashEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.Ref
		if len(e.Paths) > 0 {
			parts[i] += " (" + strings.Join(e.Paths, ", ") + ")"
		}
	}
	return strings.Join(parts, "; ")
}
