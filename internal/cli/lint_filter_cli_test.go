package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

// writeConfig overwrites the store's config.toml (initStore already wrote the
// init template) with body — for exercising [labels].required / [lint].ignore_codes.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(os.Getenv(app.EnvDir), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lintProblems(t *testing.T, args ...string) ([]core.Problem, int) {
	t.Helper()
	out, code := run(t, append([]string{"--json", "lint"}, args...)...)
	if strings.Contains(out, `"error"`) {
		return nil, code // an error envelope, not a problem array
	}
	var ps []core.Problem
	if err := json.Unmarshal([]byte(out), &ps); err != nil {
		t.Fatalf("parse lint --json: %v\n%s", err, out)
	}
	return ps, code
}

func hasCode(ps []core.Problem, code string) bool {
	for _, p := range ps {
		if p.Code == code {
			return true
		}
	}
	return false
}

// TestCLILintCodeAndExcludeFilter: --code is an allow-list, --exclude-code a
// deny-list, both warn-only here so the exit stays 0.
func TestCLILintCodeAndExcludeFilter(t *testing.T) {
	initStore(t)
	addTask(t, "haslink", "--body", "see [[t-zzzzz]]") // dangling-link warn

	// --code keeps only the named code.
	ps, code := lintProblems(t, "--code", "dangling-link")
	if code != 0 {
		t.Fatalf("warn-only filter should exit 0, got %d", code)
	}
	if !hasCode(ps, "dangling-link") {
		t.Errorf("--code dangling-link should keep it: %v", ps)
	}

	// --exclude-code drops the named code -> empty here.
	ps, code = lintProblems(t, "--exclude-code", "dangling-link")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if hasCode(ps, "dangling-link") {
		t.Errorf("--exclude-code dangling-link should drop it: %v", ps)
	}
}

// TestCLILintSeverityDrivesExit is the load-bearing spec: the filter drives the
// exit code. A real error (label-required) makes lint exit 2; excluding that code
// — or asking only for warnings — leaves no error in the reported set, so exit 0.
func TestCLILintSeverityDrivesExit(t *testing.T) {
	initStore(t)
	addTask(t, "no labels here")                  // add BEFORE requiring labels (add would reject it otherwise)
	writeConfig(t, "[labels]\nrequired = true\n") // now the label-less task is a label-required ERROR

	// Unfiltered: the error makes lint exit 2.
	if _, code := lintProblems(t, "--severity", "error"); code != int(core.CodeValidation) {
		t.Fatalf("an error present -> exit 2, got %d", code)
	}
	// Excluding the only error code -> nothing left is an error -> exit 0.
	ps, code := lintProblems(t, "--exclude-code", "label-required")
	if code != int(core.CodeOK) {
		t.Fatalf("excluding the only error must exit 0, got %d: %v", code, ps)
	}
	if hasCode(ps, "label-required") {
		t.Errorf("label-required should be excluded: %v", ps)
	}
	// --severity warn hides the error too -> exit 0.
	if _, code := lintProblems(t, "--severity", "warn"); code != int(core.CodeOK) {
		t.Fatalf("--severity warn hides errors -> exit 0, got %d", code)
	}
}

// TestCLILintUnknownCodeExit2: an unknown --code / --exclude-code token is a loud
// exit 2 with the vocabulary in candidates (a closed vocabulary, like a lane) —
// never a silent empty listing.
func TestCLILintUnknownCodeExit2(t *testing.T) {
	initStore(t)
	for _, flag := range []string{"--code", "--exclude-code"} {
		fe, _ := runErr(t, "--json", "lint", flag, "no-such-code")
		if fe == nil || fe.Code != core.CodeValidation {
			t.Fatalf("%s no-such-code should be a validation error, got %+v", flag, fe)
		}
		found := false
		for _, c := range fe.Candidates {
			if c == "dangling-link" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s error should list the code vocabulary in candidates: %v", flag, fe.Candidates)
		}
	}
}

func TestCLILintUnknownSeverityExit2(t *testing.T) {
	initStore(t)
	out, code := run(t, "lint", "--severity", "info")
	if code != int(core.CodeValidation) {
		t.Fatalf("--severity info -> exit 2, got %d:\n%s", code, out)
	}
}

// TestCLILintIgnoreCodesConfig: [lint].ignore_codes suppresses a code everywhere,
// and an entry naming no real code warns (clamp-don't-reject) rather than erroring.
func TestCLILintIgnoreCodesConfig(t *testing.T) {
	initStore(t)
	writeConfig(t, "[lint]\nignore_codes = [\"dangling-link\", \"totally-made-up\"]\n")
	addTask(t, "haslink", "--body", "see [[t-zzzzz]]") // dangling-link warn

	ps, code := lintProblems(t, "--severity", "warn")
	if code != int(core.CodeOK) {
		t.Fatalf("exit = %d", code)
	}
	if hasCode(ps, "dangling-link") {
		t.Errorf("ignore_codes should suppress dangling-link everywhere: %v", ps)
	}
	// The typo'd ignore entry is surfaced as a config-clamp warn naming it.
	found := false
	for _, p := range ps {
		if p.Code == "config-clamp" && strings.Contains(p.Msg, "totally-made-up") {
			found = true
		}
	}
	if !found {
		t.Errorf("an unknown ignore_codes entry should warn (config-clamp): %v", ps)
	}
}

// TestLintCodeRegistryCoversEmitted greps every non-test .go file under internal/
// for a lint code literal and asserts it is registered in core.IsLintCode — so a
// new code that forgot to register (which would make `lint --code <new>` reject a
// real code) fails the build. Mirrors the source-grepping guards in scripts/.
func TestLintCodeRegistryCoversEmitted(t *testing.T) {
	// Patterns that spell a lint code at an emission site:
	//   Code: "x-y"            (keyed Problem literal)
	//   Problem{SevX, "x-y"    (positional Problem literal)
	//   cycleProblems(idx, "x-y"  (the one code passed as an argument)
	pats := []*regexp.Regexp{
		regexp.MustCompile(`Code:\s*"([a-z][a-z-]*)"`),
		regexp.MustCompile(`Problem\{Sev\w+,\s*"([a-z][a-z-]*)"`),
		regexp.MustCompile(`cycleProblems\(idx,\s*"([a-z][a-z-]*)"`),
	}
	seen := map[string]string{} // code -> file where found
	root := filepath.Join("..") // internal/
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasSuffix(path, "lint_filter.go") {
			return nil // the registry itself
		}
		if strings.HasSuffix(path, filepath.Join("app", "doctor.go")) {
			// doctor's findings are a SEPARATE closed vocabulary: they never flow
			// through lint's --code/--exclude-code/ignore_codes filter, so
			// registering them would make `lint --code board-behind` a valid
			// filter that can never match — the exact confusion the registry
			// exists to prevent. (schema-outdated, which lint ALSO emits, is
			// registered via lint's own emission site.)
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // test walks its own repo
		if err != nil {
			return err
		}
		for _, re := range pats {
			for _, m := range re.FindAllStringSubmatch(string(data), -1) {
				seen[m[1]] = path
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	if len(seen) < 20 {
		t.Fatalf("grep found only %d codes — the patterns likely broke, not the registry", len(seen))
	}
	for code, file := range seen {
		if !core.IsLintCode(code) {
			t.Errorf("lint code %q emitted in %s is NOT in core's lintCodes registry — add it (else `lint --code %s` rejects a real code)", code, file, code)
		}
	}
}
