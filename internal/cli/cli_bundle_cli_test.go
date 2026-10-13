package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/v9/internal/core"
)

// `check <id> 5 --add x` appended and dropped the 5 in silence; an index
// beside --add is refused.
func TestCheckAddRefusesAnIndex(t *testing.T) {
	initStore(t)
	id := addTask(t, "x")
	fe, _ := runErr(t, "check", id, "0", "--add", "step")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "--add") {
		t.Errorf("check <id> 0 --add = %+v; want exit 2 naming --add", fe)
	}
	if out, code := run(t, "check", id, "--add", "step"); code != 0 {
		t.Errorf("check <id> --add still appends: %d\n%s", code, out)
	}
}

// `add T --body-file -` with an empty stdin wrote the default heading where
// an empty FILE is exit 2; stdin refuses the same way.
func TestBodyFileStdinRefusesEmptyInput(t *testing.T) {
	initStore(t)
	fe, out := runErrIn(t, "", "add", "T", "--body-file", "-")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "stdin was empty") {
		t.Errorf("add --body-file - <empty> = %+v\n%s", fe, out)
	}
	if out, code := runIn(t, "# T\n\nbody\n", "add", "T", "--body-file", "-"); code != 0 {
		t.Errorf("a real stdin body still adds: %d\n%s", code, out)
	}
}

// migrate's human warnings go to stderr under the warning: prefix (they were
// "warn: …" lines on stdout, between the data rows).
func TestMigrateWarningsAreStderrWarnings(t *testing.T) {
	initStore(t)
	md := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(md, []byte("### loose task before any lane heading\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	so, se, code := runSplit(t, "migrate", md, "--yes")
	if code != 0 {
		t.Fatalf("migrate exit %d\n%s%s", code, so, se)
	}
	if strings.Contains(so, "warn") || !strings.Contains(se, "warning: task before any section heading") {
		t.Errorf("warnings must be stderr warning: lines:\nstdout:\n%s\nstderr:\n%s", so, se)
	}
}

// `dep <id> --list --expect-updated x` was silently accepted; --list reads.
func TestDepListRefusesExpectUpdated(t *testing.T) {
	initStore(t)
	id := addTask(t, "x")
	fe, _ := runErr(t, "dep", id, "--list", "--expect-updated", "2026-01-01T00:00:00Z")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(fe.Msg, "--list") {
		t.Errorf("dep --list --expect-updated = %+v; want exit 2", fe)
	}
}

// Every human stderr line this package prints starts with note:, warning: or
// error: (or is a JSON object): the convention a reader classifies stderr by.
// Pinned at the source — a print on errOut must go through stderrNote /
// stderrWarn, carry the prefix in its literal, or be marked `// stderr-raw`
// with the reason (git's relayed lines, the JSON envelope).
func TestStderrLinesCarryAPrefix(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`fmt\.Fprint(?:ln|f)?\(errOut,\s*(.*)\)`)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "output.go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			m := call.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if strings.Contains(line, "// stderr-raw") {
				continue
			}
			arg := strings.TrimSpace(m[1])
			if strings.HasPrefix(arg, `"note:`) || strings.HasPrefix(arg, `"warning:`) || strings.HasPrefix(arg, `"error:`) {
				continue
			}
			t.Errorf("%s:%d: stderr print without a note:/warning:/error: prefix — use stderrNote/stderrWarn or mark it // stderr-raw: %s", name, i+1, strings.TrimSpace(line))
		}
	}
}

// Blank values are refused on every list, in the app: a blank checklist item,
// `epic set --rm-label ""`, and a blank box repo (which read as repo-unknown).
func TestBlankListValuesAreRefusedEverywhere(t *testing.T) {
	initStore(t)
	id := addTask(t, "x")
	box := addEpic(t, "box", "-r", "o/r")
	// "" is dropped by the flag parser (the CLI names the empty flag), " " reaches
	// the app (which names the blank value); both are exit 2 naming the flag.
	for _, args := range [][]string{
		{"check", id, "--add", "  "},
		{"epic", "set", box, "--rm-label", ""},
		{"epic", "set", box, "--rm-label", " "},
		{"epic", "set", box, "--add-repo", ""},
		{"epic", "set", box, "--add-repo", " "},
		{"epic", "set", box, "--rm-repo", " "},
		{"epic", "add", "new", "-r", ""},
		{"epic", "add", "new", "-r", " "},
		{"epic", "add", "new", "--label", " "},
	} {
		fe, _ := runErr(t, args...)
		flag := args[len(args)-2]
		named := fe != nil && strings.Contains(fe.Msg, strings.TrimLeft(flag, "-")) && (strings.Contains(fe.Msg, "blank") || strings.Contains(fe.Msg, "empty"))
		if fe == nil || fe.Code != core.CodeValidation || !named {
			t.Errorf("%v = %+v; want exit 2 naming the flag and the blank/empty value", args, fe)
		}
	}
}

// brief applies the -l did-you-mean guard the other filtering reads apply: a
// tag that matched nothing but names a repo is exit 2 with the repo.
func TestBriefLabelDidYouMean(t *testing.T) {
	initStore(t)
	addTask(t, "x", "-r", "o/gamma")
	fe, _ := runErr(t, "brief", "-l", "gamma")
	if fe == nil || fe.Code != core.CodeValidation || !strings.Contains(strings.Join(fe.Candidates, ","), "o/gamma") {
		t.Errorf("brief -l gamma = %+v; want exit 2 naming the repo", fe)
	}
}

// Two refusals say the remedy: --yes beside ids, and an unknown lint code.
func TestRefusalWording(t *testing.T) {
	initStore(t)
	id := addTask(t, "x")
	if fe, _ := runErr(t, "done", id, "--yes"); fe == nil || !strings.Contains(fe.Msg, "drop --yes") {
		t.Errorf("done <id> --yes = %+v; want the remedy", fe)
	}
	if fe, _ := runErr(t, "lint", "--code", "nope"); fe == nil || !strings.Contains(fe.Msg, "furrow vocab lint-codes") {
		t.Errorf("lint --code nope = %+v; want the vocab pointer", fe)
	}
}

// vocab is CLI contract (the docs send the reader to it): visible in the
// command table, and --ndjson streams one member per line.
func TestVocabIsVisibleAndStreams(t *testing.T) {
	initStore(t)
	if help := mustRun(t, "--help"); !strings.Contains(help, "vocab") {
		t.Errorf("root help must list vocab:\n%s", help)
	}
	out := mustRun(t, "--ndjson", "vocab", "error-kinds")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 10 {
		t.Fatalf("vocab --ndjson must stream one member per line, got %d line(s):\n%s", len(lines), out)
	}
	for _, l := range lines[:3] {
		var s string
		if err := json.Unmarshal([]byte(l), &s); err != nil {
			t.Errorf("line %q is not a JSON string: %v", l, err)
		}
	}
}
