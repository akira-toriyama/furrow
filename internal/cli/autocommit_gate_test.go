package cli

import "testing"

// The autocommit post-run hook fires only for commands in mutatingCommands.
// Three invariants keep it honest: every name in the set is a REAL registered
// command (a typo would silently never autocommit), no READ/diagnostic
// command is in it (a read must never autocommit a pre-dirty tree — that is
// how a co-located operator's WIP body would get swept in on a plain `furrow
// ls`), and EVERY registered command is classified one way or the other — a
// store writer missing from the set leaves its own change uncommitted while
// its neighbours commit (edit --body: t-m0v2; unarchive and tidy in its
// sweep), and the three names an earlier test asserted were just the ones
// already found missing.
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
	// rides on the next mutator; its siblings are reads), migrate (a rare,
	// deliberately hand-committed rewrite) and vocab must NEVER autocommit.
	reads := map[string]bool{
		"ls": true, "show": true, "next": true, "brief": true, "revisit": true, "search": true, "stats": true,
		"board": true, "boards": true, "doctor": true, "lint": true, "config": true, "schema": true, "version": true,
		"sync": true, "migrate": true, "vocab": true, "commands": true,
	}
	// Commands that open no App at all, so the hook has nothing to flush:
	// init creates the store the hook would commit INTO; help and completion
	// are cobra's own.
	noApp := map[string]bool{"init": true, "help": true, "completion": true}
	for read := range reads {
		if mutatingCommands[read] {
			t.Errorf("%q must NOT be in mutatingCommands (it would autocommit a pre-dirty tree)", read)
		}
		if !registered[read] {
			t.Errorf("this test lists %q as a read, but no such command is registered (typo? renamed?)", read)
		}
	}
	// The closed world: a command is a store writer (in the set), a read
	// (named above) or opens no App. A new command that is none of the three
	// fails here, so it cannot land as a writer that never autocommits.
	for name := range registered {
		if mutatingCommands[name] || reads[name] || noApp[name] {
			continue
		}
		t.Errorf("%q is registered but classified nowhere: add it to mutatingCommands if it writes the store in-process, or to this test's reads if it must never autocommit", name)
	}
}
