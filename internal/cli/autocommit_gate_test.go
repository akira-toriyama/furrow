package cli

import "testing"

// The autocommit post-run hook fires only for commands in mutatingCommands.
// Two invariants keep it honest: every name in the set is a REAL registered
// command (a typo would silently never autocommit), and no READ/diagnostic
// command is in it (a read must never autocommit a pre-dirty tree — that is how
// a co-located operator's WIP body would get swept in on a plain `furrow ls`).
func TestMutatingCommandsAreRealAndExcludeReads(t *testing.T) {
	root := newRootCmd()
	registered := map[string]bool{}
	for _, c := range root.Commands() {
		registered[c.Name()] = true
	}
	for name := range mutatingCommands {
		if !registered[name] {
			t.Errorf("mutatingCommands has %q, which is not a registered command (typo? renamed?)", name)
		}
	}
	// Reads, diagnostics, sync (its own git ritual), config (set's config.toml
	// rides on the next mutator; its siblings are reads), and migrate (a rare,
	// deliberately hand-committed rewrite) must NEVER autocommit.
	for _, read := range []string{
		"ls", "show", "next", "brief", "revisit", "search", "stats",
		"board", "boards", "doctor", "lint", "config", "schema", "version",
		"sync", "migrate",
	} {
		if mutatingCommands[read] {
			t.Errorf("%q must NOT be in mutatingCommands (it would autocommit a pre-dirty tree)", read)
		}
	}
	// Every store write that is not one of those is in: each of these was
	// found missing (edit --body: t-m0v2; unarchive and tidy in its sweep) and
	// left its own change uncommitted while its neighbours committed.
	for _, write := range []string{"edit", "unarchive", "tidy"} {
		if !mutatingCommands[write] {
			t.Errorf("%q must be in mutatingCommands (it writes the store in-process)", write)
		}
	}
}
