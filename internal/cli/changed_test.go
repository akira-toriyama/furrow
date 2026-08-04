package cli

import (
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// TestChangedFieldsTracksEpic pins the same contract the `type` field needed: the
// `set --json` envelope's `changed` array must include "epic" when membership
// actually changes. Otherwise a headless agent that branches on `changed` (per
// the integration contract) reads a `set -e <box>` as a no-op and thinks the
// filing never took, even though the shard was written.
func TestChangedFieldsTracksEpic(t *testing.T) {
	before := &core.Task{ID: "t-1"}
	after := &core.Task{ID: "t-1", Epic: "e-k3m9"}

	changed := changedFields(before, after)
	found := false
	for _, c := range changed {
		if c == "epic" {
			found = true
		}
	}
	if !found {
		t.Errorf(`changedFields must report "epic" when it changes; got %v`, changed)
	}

	// Unchanged membership must not be reported.
	if got := changedFields(after, after); len(got) != 0 {
		t.Errorf("an all-equal pair must report no changes; got %v", got)
	}
}

// A flag that WAS passed but carried nothing pflag kept — a bare `--add ""` on
// a StringSlice, or `--before ""` — used to fall through to the command's
// "provide at least one …" guard, telling the caller to pass a flag they just
// passed. The exit code was already 2; only the message was wrong, and a wrong
// message is what sends an agent round the loop again.
func TestEmptyFlagValueNamesTheFlag(t *testing.T) {
	initStore(t)
	id := addTask(t, "probe")

	for _, tc := range []struct{ cmd, flag string }{
		{"label", "--add"},
		{"label", "--rm"},
		{"ref", "--add"},
		{"ref", "--rm"},
		{"repo", "--add"},
		{"repo", "--rm"},
		{"set", "--before"},
		{"set", "--after"},
		{"set", "--add-label"},
		{"set", "--rm-label"},
	} {
		t.Run(tc.cmd+tc.flag, func(t *testing.T) {
			fe, _ := runErr(t, tc.cmd, id, tc.flag, "")
			if fe == nil || fe.Code != core.CodeValidation {
				t.Fatalf("%s %s '' should exit 2, got %+v", tc.cmd, tc.flag, fe)
			}
			if !strings.Contains(fe.Msg, tc.flag) {
				t.Errorf("the message must name %s, got %q", tc.flag, fe.Msg)
			}
			if strings.Contains(fe.Msg, "provide at least one") {
				t.Errorf("%s %s '' must not ask for a flag that WAS passed: %q", tc.cmd, tc.flag, fe.Msg)
			}
		})
	}
}
