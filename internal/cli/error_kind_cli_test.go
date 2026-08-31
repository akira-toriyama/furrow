package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/core"
)

// The error envelope is the machine contract agents branch on:
// {"error":{kind,subject,retryable,exit,message[,details][,candidates]}}.
// kind is always present; subject is omitted when empty; retryable is ALWAYS
// present (a consumer asks "retry?" of every failure, so false must be
// visible, not absent); exit mirrors the process exit code under a name that
// does not collide with lint's kebab-case `code`.
func TestRenderErrorEnvelopeShape(t *testing.T) {
	var se bytes.Buffer
	errOut = &se
	defer func() { errOut = os.Stderr }()

	renderError(&core.Error{Code: core.CodeNotFound, Kind: core.KindNotFound, Subject: "t-zzzz1", Msg: "task not found: t-zzzz1"})
	var env struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(se.Bytes(), &env); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, se.String())
	}
	if env.Error["kind"] != "not-found" || env.Error["subject"] != "t-zzzz1" || env.Error["exit"] != float64(1) {
		t.Errorf("envelope = %v", env.Error)
	}
	if v, ok := env.Error["retryable"]; !ok || v != false {
		t.Errorf("retryable must be present and false, got %v (present=%v)", v, ok)
	}
	for _, gone := range []string{"id", "code"} {
		if _, ok := env.Error[gone]; ok {
			t.Errorf("legacy key %q must not be emitted (the rename is deliberate breakage, not an alias)", gone)
		}
	}

	se.Reset()
	renderError(&core.Error{Code: core.CodeInternal, Kind: core.KindSyncBusy, Retryable: true, Msg: "busy"})
	env.Error = nil
	if err := json.Unmarshal(se.Bytes(), &env); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, se.String())
	}
	if env.Error["kind"] != "sync-busy" || env.Error["retryable"] != true {
		t.Errorf("envelope = %v", env.Error)
	}
	if _, ok := env.Error["subject"]; ok {
		t.Errorf("empty subject must be omitted, got %v", env.Error)
	}
}

// The root command classifies an unknown TOP-LEVEL command exactly like every
// child parent: kind unknown-subcommand, exit 2, the known names in
// candidates — and it must do so WITHOUT breaking the commands cobra only
// registers at Execute time. A pre-flight Find once rejected `help`,
// `completion`, and the hidden `__complete` (killing every installed shell
// completion); this pins the whole dispatch surface so that cannot return.
func TestRootUnknownCommandAndBuiltinDispatch(t *testing.T) {
	fe, _ := runErr(t, "bogus")
	if fe == nil || fe.Kind != core.KindUnknownSubcommand || fe.Code != core.CodeValidation {
		t.Fatalf("furrow bogus = %+v, want kind unknown-subcommand exit 2", fe)
	}
	if len(fe.Candidates) == 0 || !slices.Contains(fe.Candidates, "ls") {
		t.Errorf("candidates must carry the known commands, got %v", fe.Candidates)
	}
	// The cobra-registered built-ins must keep dispatching (they are added
	// inside Execute, so any dispatch check running before it breaks them).
	for _, args := range [][]string{{"help"}, {"help", "ls"}, {"completion", "zsh"}, {"__complete", "ls", ""}} {
		if fe, _ := runErr(t, args...); fe != nil {
			t.Errorf("furrow %v must dispatch (exit 0), got %+v", args, fe)
		}
	}
}

// TestErrorKindEmissionUsesConstants greps every non-test .go file for a
// string literal assigned to an Error's Kind field. Emission sites must use
// the core.Kind* constants so an unregistered kind is a COMPILE error — the
// stronger form of TestLintCodeRegistryCoversEmitted's grep — and this test is
// what keeps that invariant from eroding one literal at a time.
func TestErrorKindEmissionUsesConstants(t *testing.T) {
	re := regexp.MustCompile(`Kind:\s*"`)
	root := filepath.Join("..") // internal/
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // test walks its own repo
		if err != nil {
			return err
		}
		if re.Match(data) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	for _, f := range offenders {
		t.Errorf("%s assigns a string literal to Kind — use a core.Kind* constant (register a new kind in core/error_kinds.go first)", f)
	}
}
