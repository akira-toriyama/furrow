package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

func writeBodyFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// storeBodies returns every bodies/<id>.md in the store, keyed by id — the
// disk itself, so "nothing was written" is asserted without trusting a read
// command.
func storeBodies(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(os.Getenv(app.EnvDir), "bodies")
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read bodies dir: %v", err)
	}
	bodies := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read body %s: %v", e.Name(), err)
		}
		bodies[strings.TrimSuffix(e.Name(), ".md")] = string(b)
	}
	return bodies
}

func assertNothingWritten(t *testing.T) {
	t.Helper()
	if got := storeBodies(t); len(got) != 0 {
		t.Errorf("a refused command wrote %d body file(s): %q", len(got), got)
	}
}

func assertRefusedAsPath(t *testing.T, fe *core.Error, candidates ...string) {
	t.Helper()
	if fe == nil {
		t.Fatalf("a --body naming an existing file was accepted; want exit 2")
	}
	if fe.Code != core.CodeValidation || fe.Kind != core.KindValidation {
		t.Errorf("exit/kind = %d/%q, want %d/%q", fe.Code, fe.Kind, core.CodeValidation, core.KindValidation)
	}
	if !slices.Equal(fe.Candidates, candidates) {
		t.Errorf("candidates = %q, want %q", fe.Candidates, candidates)
	}
}

// TestBodyNamingAFileIsRefused covers t-4x3c: `--body <path>` used to exit 0
// and store the path string as the whole body. Each command that takes --body
// refuses it before writing, and every candidate is a spelling that works from
// where the caller stands.
func TestBodyNamingAFileIsRefused(t *testing.T) {
	path := writeBodyFile(t, "body.md", "# from the file\n")
	both := []string{"--body-file " + path, "--body - < " + path}

	t.Run("add", func(t *testing.T) {
		initStore(t)
		fe, _ := runErr(t, "add", "path as body", "--body", path)
		assertRefusedAsPath(t, fe, both...)
		assertNothingWritten(t)
	})

	t.Run("edit", func(t *testing.T) {
		initStore(t)
		id := addTask(t, "keep me")
		before := readTaskBody(t, id)
		fe, _ := runErr(t, "edit", id, "--body", path)
		assertRefusedAsPath(t, fe, both...)
		if got := readTaskBody(t, id); got != before {
			t.Errorf("a refused edit changed the body: %q, want %q", got, before)
		}
	})

	t.Run("epic add", func(t *testing.T) {
		initStore(t)
		fe, _ := runErr(t, "epic", "add", "path as body", "--body", path)
		assertRefusedAsPath(t, fe, both...)
		assertNothingWritten(t)
	})

	// The spelling callers actually type: relative to where they stand.
	t.Run("a relative path", func(t *testing.T) {
		initStore(t)
		t.Chdir(filepath.Dir(path))
		fe, _ := runErr(t, "add", "path as body", "--body", "body.md")
		assertRefusedAsPath(t, fe, "--body-file body.md", "--body - < body.md")
		assertNothingWritten(t)
	})

	t.Run("a path that needs quoting", func(t *testing.T) {
		initStore(t)
		spaced := writeBodyFile(t, "my plan.md", "x\n")
		fe, _ := runErr(t, "add", "path as body", "--body", spaced)
		assertRefusedAsPath(t, fe, "--body-file '"+spaced+"'", "--body - < '"+spaced+"'")
	})

	// What `--body <(cmd)` hands over is not a regular file either.
	t.Run("a non-regular file", func(t *testing.T) {
		if _, err := os.Stat(os.DevNull); err != nil {
			t.Skipf("no %s here: %v", os.DevNull, err)
		}
		initStore(t)
		fe, _ := runErr(t, "add", "path as body", "--body", os.DevNull)
		assertRefusedAsPath(t, fe, "--body-file "+os.DevNull, "--body - < "+os.DevNull)
		assertNothingWritten(t)
	})

	// With stdin already spoken for, `--body -` would itself be refused, so
	// only the file spelling is offered.
	for name, args := range map[string][]string{
		"under --stdin":   {"add", "--stdin", "--body", path},
		"under --batch -": {"add", "--batch", "-", "--body", path},
	} {
		t.Run(name, func(t *testing.T) {
			initStore(t)
			fe, _ := runErrIn(t, `{"title":"one"}`+"\n", args...)
			assertRefusedAsPath(t, fe, "--body-file "+path)
			assertNothingWritten(t)
		})
	}
}

// TestBodyFileReadsTheBody: --body-file is the spelling the refusal points at,
// on the same three commands, from a path or (`-`) from stdin.
func TestBodyFileReadsTheBody(t *testing.T) {
	const content = "# from the file\n\nsecond paragraph\n"
	path := writeBodyFile(t, "body.md", content)

	for _, src := range []struct{ name, stdin, arg string }{
		{name: "a path", arg: path},
		{name: "stdin", stdin: content, arg: "-"},
	} {
		t.Run("add from "+src.name, func(t *testing.T) {
			initStore(t)
			out, code := runIn(t, src.stdin, "--json", "add", "file body", "--body-file", src.arg)
			if code != 0 {
				t.Fatalf("add --body-file exit = %d:\n%s", code, out)
			}
			if got := readTaskBody(t, parseAddID(t, out)); got != content {
				t.Errorf("body = %q, want %q", got, content)
			}
		})

		t.Run("edit from "+src.name, func(t *testing.T) {
			initStore(t)
			id := addTask(t, "to be replaced")
			out, code := runIn(t, src.stdin, "--json", "edit", id, "--body-file", src.arg)
			if code != 0 {
				t.Fatalf("edit --body-file exit = %d:\n%s", code, out)
			}
			if got := readTaskBody(t, id); got != content {
				t.Errorf("body = %q, want %q", got, content)
			}
			if rb, _ := parseEnvelope(t, out)["replaced_bytes"].(float64); int(rb) != len(content) {
				t.Errorf("replaced_bytes = %d, want the on-disk length %d", int(rb), len(content))
			}
		})

		t.Run("epic add from "+src.name, func(t *testing.T) {
			initStore(t)
			out, code := runIn(t, src.stdin, "--json", "epic", "add", "file body", "--body-file", src.arg)
			if code != 0 {
				t.Fatalf("epic add --body-file exit = %d:\n%s", code, out)
			}
			if got := readTaskBody(t, parseAddID(t, out)); got != content {
				t.Errorf("body = %q, want %q", got, content)
			}
		})
	}
}

// TestBodyThatIsNotAFileStaysLiteral pins the refusal's edges: only a ONE-LINE
// value resolving to something other than a directory is refused, and stdin is
// never path-checked — the escape for a body that really is that line.
//
// bite-exempt: pins what the refusal must leave alone, which held before it existed
func TestBodyThatIsNotAFileStaysLiteral(t *testing.T) {
	path := writeBodyFile(t, "body.md", "unused\n")
	for _, tc := range []struct {
		name  string
		stdin string
		body  string
		want  string
	}{
		{name: "no such file", body: "docs/missing.md", want: "docs/missing.md"},
		{name: "a directory", body: filepath.Dir(path), want: filepath.Dir(path)},
		{name: "a path on the first of several lines", body: path + "\nsee the file above", want: path + "\nsee the file above"},
		{name: "the path through stdin", stdin: path, body: "-", want: path},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initStore(t)
			out, code := runIn(t, tc.stdin, "--json", "add", "literal body", "--body", tc.body)
			if code != 0 {
				t.Fatalf("add --body %q exit = %d:\n%s", tc.body, code, out)
			}
			if got := strings.TrimRight(readTaskBody(t, parseAddID(t, out)), "\n"); got != tc.want {
				t.Errorf("body = %q, want the literal %q", got, tc.want)
			}
		})
	}
}

// TestBodyFileRejectsBadInput: every unusable --body-file is exit 2 and writes
// nothing — an interpolated-empty value, an unreadable path and an empty file
// included, where falling back to the default heading would hide the miss.
func TestBodyFileRejectsBadInput(t *testing.T) {
	path := writeBodyFile(t, "body.md", "x\n")
	empty := writeBodyFile(t, "empty.md", "\n")
	absent := filepath.Join(filepath.Dir(path), "absent.md")

	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{name: "add beside --body", args: []string{"add", "t", "--body", "inline", "--body-file", path}, want: "none of the others can be"},
		{name: "epic add beside --body", args: []string{"epic", "add", "t", "--body", "inline", "--body-file", path}, want: "none of the others can be"},
		{name: "add missing file", args: []string{"add", "t", "--body-file", absent}, want: "--body-file: open "},
		{name: "add empty value", args: []string{"add", "t", "--body-file", ""}, want: "empty value"},
		{name: "add empty file", args: []string{"add", "t", "--body-file", empty}, want: "is empty"},
		{name: "epic add empty file", args: []string{"epic", "add", "t", "--body-file", empty}, want: "is empty"},
		{name: "stdin taken by --stdin", stdin: "a\n", args: []string{"add", "--stdin", "--body-file", "-"}, want: "--body-file -"},
		{name: "stdin taken by --batch -", stdin: `{"title":"a"}`, args: []string{"add", "--batch", "-", "--body-file", "-"}, want: "--body-file -"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initStore(t)
			fe, out := runErrIn(t, tc.stdin, tc.args...)
			if fe == nil || fe.Code != core.CodeValidation {
				t.Fatalf("%v: want exit %d, got %+v\n%s", tc.args, core.CodeValidation, fe, out)
			}
			if !strings.Contains(fe.Msg, tc.want) {
				t.Errorf("message %q does not name %q", fe.Msg, tc.want)
			}
			assertNothingWritten(t)
		})
	}

	for _, tc := range []struct {
		name  string
		flags []string
		want  string
	}{
		{name: "edit beside --body", flags: []string{"--body", "inline", "--body-file", path}, want: "none of the others can be"},
		{name: "edit empty file", flags: []string{"--body-file", empty}, want: "is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initStore(t)
			id := addTask(t, "keep me")
			before := readTaskBody(t, id)
			fe, out := runErr(t, append([]string{"edit", id}, tc.flags...)...)
			if fe == nil || fe.Code != core.CodeValidation {
				t.Fatalf("edit %v: want exit %d, got %+v\n%s", tc.flags, core.CodeValidation, fe, out)
			}
			if !strings.Contains(fe.Msg, tc.want) {
				t.Errorf("message %q does not name %q", fe.Msg, tc.want)
			}
			if got := readTaskBody(t, id); got != before {
				t.Errorf("a refused edit changed the body: %q, want %q", got, before)
			}
		})
	}
}

// TestBulkAddAndEpicAddResolveTheBody: the body is a shared flag, so the bulk
// paths and `epic add` get the resolved text. `--batch <file> --body -` and
// `epic add --body -` used to store the literal "-".
func TestBulkAddAndEpicAddResolveTheBody(t *testing.T) {
	const content = "shared body\n"
	path := writeBodyFile(t, "body.md", content)
	batch := writeBodyFile(t, "plan.ndjson", `{"title":"one"}`+"\n"+`{"title":"two"}`+"\n")

	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
		n     int
	}{
		{name: "--stdin with --body-file", stdin: "one\ntwo\n", args: []string{"add", "--stdin", "--body-file", path}, n: 2},
		{name: "--batch file with --body -", stdin: content, args: []string{"add", "--batch", batch, "--body", "-"}, n: 2},
		{name: "--batch file with --body-file", args: []string{"add", "--batch", batch, "--body-file", path}, n: 2},
		{name: "epic add with --body -", stdin: content, args: []string{"epic", "add", "box", "--body", "-"}, n: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initStore(t)
			if out, code := runIn(t, tc.stdin, tc.args...); code != 0 {
				t.Fatalf("%v exit = %d:\n%s", tc.args, code, out)
			}
			bodies := storeBodies(t)
			if len(bodies) != tc.n {
				t.Fatalf("wrote %d body file(s), want %d: %q", len(bodies), tc.n, bodies)
			}
			for id, got := range bodies {
				if got != content {
					t.Errorf("%s body = %q, want %q", id, got, content)
				}
			}
		})
	}
}

// stdinTripwire fails the test if the command under test reads stdin at all.
type stdinTripwire struct{ t *testing.T }

func (s stdinTripwire) Read([]byte) (int, error) {
	s.t.Error("stdin was read before the arguments were checked")
	return 0, io.EOF
}

// TestAddChecksItsArgumentsBeforeReadingStdin: resolving the body may read
// stdin to EOF, so a call its arguments already doom must fail first — on a
// terminal or a pipe nobody closes, reading first is a hang, not an error.
//
// bite-exempt: holds the order the body resolution must keep, which the base already had
func TestAddChecksItsArgumentsBeforeReadingStdin(t *testing.T) {
	batch := writeBodyFile(t, "plan.ndjson", `{"title":"one"}`+"\n")
	for name, args := range map[string][]string{
		"no title":              {"add", "--body", "-"},
		"--batch beside titles": {"add", "--batch", batch, "a title", "--body", "-"},
	} {
		t.Run(name, func(t *testing.T) {
			initStore(t)
			var sink bytes.Buffer
			prevOut, prevErr := out, errOut
			out, errOut = &sink, &sink
			defer func() { out, errOut = prevOut, prevErr }()

			root := newRootCmd()
			root.SetArgs(args)
			root.SetOut(&sink)
			root.SetErr(&sink)
			root.SetIn(stdinTripwire{t})
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v succeeded; want exit %d", args, core.CodeValidation)
			}
			if fe := classifyFailure(err); fe.Code != core.CodeValidation {
				t.Errorf("%v exit = %d, want %d", args, fe.Code, core.CodeValidation)
			}
		})
	}
}

// TestApplyBodyFileDashReadsStdin: `--body-file -` means stdin on every command
// that has the flag. `apply` used to look for a file named "-".
func TestApplyBodyFileDashReadsStdin(t *testing.T) {
	initStore(t)
	id := addTask(t, "to be closed")
	out, code := runIn(t, "SetStatus-task: "+id+" done\n", "apply", "--on", "merge", "--dry-run", "--body-file", "-")
	if code != 0 {
		t.Fatalf("apply --body-file - exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("the directive on stdin was not applied:\n%s", out)
	}
}
