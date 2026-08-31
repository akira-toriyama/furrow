package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

func vocabLines(t *testing.T, args ...string) []string {
	t.Helper()
	out, code := run(t, append([]string{"vocab"}, args...)...)
	if code != 0 {
		t.Fatalf("vocab %v exit = %d, want 0:\n%s", args, code, out)
	}
	return strings.Fields(out)
}

// With no argument, vocab lists every registered vocabulary name — the app
// registry's names plus the cli-owned `commands`.
func TestVocabListsNames(t *testing.T) {
	got := vocabLines(t)
	want := map[string]bool{"commands": true}
	for name := range app.Vocabularies() {
		want[name] = true
	}
	if len(got) != len(want) {
		t.Fatalf("vocab lists %d names, want %d: %v", len(got), len(want), got)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("vocab lists unknown name %q", n)
		}
	}
}

// Each named vocabulary prints the owning registry's members verbatim, one per
// line — the contract scripts/check-docs-vocab.sh consumes.
func TestVocabMembersMatchRegistries(t *testing.T) {
	for name, want := range app.Vocabularies() {
		if got := vocabLines(t, name); !reflect.DeepEqual(got, want) {
			t.Errorf("vocab %s = %v, want %v", name, got, want)
		}
	}
	if got := vocabLines(t, "lint-codes"); !reflect.DeepEqual(got, core.LintCodeList()) {
		t.Errorf("vocab lint-codes = %v, want core.LintCodeList()", got)
	}
}

// The commands vocabulary is the visible cobra tree: real commands present,
// hidden repo tooling (commands/vocab) and cobra furniture absent.
func TestVocabCommands(t *testing.T) {
	got := vocabLines(t, "commands")
	set := map[string]bool{}
	for _, n := range got {
		set[n] = true
	}
	for _, mustHave := range []string{"init", "add", "ls", "next", "config", "sync", "version"} {
		if !set[mustHave] {
			t.Errorf("vocab commands is missing %q: %v", mustHave, got)
		}
	}
	for _, mustNot := range []string{"commands", "vocab", "help", "completion"} {
		if set[mustNot] {
			t.Errorf("vocab commands must not list %q (hidden/furniture): %v", mustNot, got)
		}
	}
}

// An unknown vocabulary name is exit 2 with the known names in candidates —
// the same did-you-mean contract as an unknown lane or lint code, so a typo in
// the guard's manifest fails loudly instead of checking nothing.
func TestVocabUnknownNameIsExit2WithCandidates(t *testing.T) {
	fe, _ := runErr(t, "vocab", "lint-code")
	if fe == nil {
		t.Fatal("vocab lint-code should fail")
	}
	if fe.Code != core.CodeValidation {
		t.Fatalf("exit = %d, want %d", fe.Code, core.CodeValidation)
	}
	found := false
	for _, c := range fe.Candidates {
		if c == "lint-codes" {
			found = true
		}
	}
	if !found {
		t.Errorf("candidates should include lint-codes: %v", fe.Candidates)
	}
}

// --json emits the members as a JSON array (the wherever-furrow-emits-JSON
// rule holds for hidden tooling too).
func TestVocabJSON(t *testing.T) {
	out, code := run(t, "vocab", "revisit-codes", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", code, out)
	}
	var got []string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("vocab --json is not a JSON array: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(got, core.RevisitCodeList()) {
		t.Errorf("vocab revisit-codes --json = %v, want core.RevisitCodeList()", got)
	}
}
