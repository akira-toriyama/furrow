package cli

import (
	"strings"
	"testing"
)

// `furrow --help` is the agent contract's one source: every rule CLAUDE.md
// and README state about driving furrow is here, so those two can point at
// it instead of restating (and contradicting) it. The decisions the three
// used to disagree on are pinned too: the schema gate is branched on by
// KIND; the furrow-owned files are tasks/epics/repos shards and meta.json;
// the destructive list is the whole list; the front-ends are ridge and
// vista.
func TestRootHelpIsTheAgentContract(t *testing.T) {
	help := mustRun(t, "--help")
	for _, want := range []string{
		"session-busy (exit 2)", "`add --draft`", "There is no force flag",
		"emits an ARRAY at every arity", "{before, after, changed}", "emit ONE object whatever they took",
		"A mutation\nthat changes nothing leaves `updated` alone",
		"`furrow config set` is the one write that works on an older-layout",
		"rm\nrefuses a target something still references (kind referenced",
		"release the furrow that ships the schema, bump every",
		"the one list: archive, rm,\nepic rm, tidy, upgrade, migrate, a day move on `epic set --anchor`",
		"-q/-l/-r selection on done/move/set",
		"vocab error-kinds",
		"The prose writes — `note`, `edit --body`",
		"`furrow board --json` reports",
		"branch on the kind; the exit codes differ too",
		"tasks/*.json,\nepics/*.json, repos/*.json, meta.json — is furrow-OWNED",
		"ridge, a TUI; vista, a GUI",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("furrow --help lacks %q", want)
		}
	}
}
