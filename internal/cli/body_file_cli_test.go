package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
)

func writeBodyFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "body.md")
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

func assertRefusedAsPath(t *testing.T, fe *core.Error, path string) {
	t.Helper()
	if fe == nil {
		t.Fatalf("a --body naming an existing file was accepted; want exit 2")
	}
	if fe.Code != core.CodeValidation || fe.Kind != core.KindValidation {
		t.Errorf("exit/kind = %d/%q, want %d/%q", fe.Code, fe.Kind, core.CodeValidation, core.KindValidation)
	}
	want := []string{"--body-file " + path, "--body - < " + path}
	if !slices.Equal(fe.Candidates, want) {
		t.Errorf("candidates = %q, want %q", fe.Candidates, want)
	}
}

// TestBodyNamingAFileIsRefused covers t-4x3c: `--body <path>` used to exit 0
// and store the path string as the whole body. Each command that takes --body
// refuses it before writing, with both working spellings in candidates.
func TestBodyNamingAFileIsRefused(t *testing.T) {
	path := writeBodyFile(t, "# from the file\n")

	t.Run("add", func(t *testing.T) {
		initStore(t)
		fe, _ := runErr(t, "add", "path as body", "--body", path)
		assertRefusedAsPath(t, fe, path)
		if got := storeBodies(t); len(got) != 0 {
			t.Errorf("a refused add wrote %d body file(s): %q", len(got), got)
		}
	})

	t.Run("edit", func(t *testing.T) {
		initStore(t)
		id := addTask(t, "keep me")
		before := readTaskBody(t, id)
		fe, _ := runErr(t, "edit", id, "--body", path)
		assertRefusedAsPath(t, fe, path)
		if got := readTaskBody(t, id); got != before {
			t.Errorf("a refused edit changed the body: %q, want %q", got, before)
		}
	})

	t.Run("epic add", func(t *testing.T) {
		initStore(t)
		fe, _ := runErr(t, "epic", "add", "path as body", "--body", path)
		assertRefusedAsPath(t, fe, path)
		if got := storeBodies(t); len(got) != 0 {
			t.Errorf("a refused epic add wrote %d body file(s): %q", len(got), got)
		}
	})
}

// TestBodyFileReadsTheFile: --body-file is the spelling the refusal points at,
// on the same three commands.
func TestBodyFileReadsTheFile(t *testing.T) {
	const content = "# from the file\n\nsecond paragraph\n"
	path := writeBodyFile(t, content)

	t.Run("add", func(t *testing.T) {
		initStore(t)
		id := addTask(t, "file body", "--body-file", path)
		if got := readTaskBody(t, id); got != content {
			t.Errorf("body = %q, want the file's content %q", got, content)
		}
	})

	t.Run("edit", func(t *testing.T) {
		initStore(t)
		id := addTask(t, "to be replaced")
		out, code := run(t, "--json", "edit", id, "--body-file", path)
		if code != 0 {
			t.Fatalf("edit --body-file exit = %d:\n%s", code, out)
		}
		if got := readTaskBody(t, id); got != content {
			t.Errorf("body = %q, want the file's content %q", got, content)
		}
		if rb, _ := parseEnvelope(t, out)["replaced_bytes"].(float64); int(rb) != len(content) {
			t.Errorf("replaced_bytes = %d, want the on-disk length %d", int(rb), len(content))
		}
	})

	t.Run("epic add", func(t *testing.T) {
		initStore(t)
		out, code := run(t, "--json", "epic", "add", "file body", "--body-file", path)
		if code != 0 {
			t.Fatalf("epic add --body-file exit = %d:\n%s", code, out)
		}
		if got := readTaskBody(t, parseAddID(t, out)); got != content {
			t.Errorf("body = %q, want the file's content %q", got, content)
		}
	})
}

// TestBodyThatIsNotAFileStaysLiteral pins the refusal's edges: only a ONE-LINE
// value resolving to a REGULAR file is refused, and stdin is never
// path-checked — the escape for a body that really is that line.
//
// bite-exempt: pins what the refusal must leave alone, which held before it existed
func TestBodyThatIsNotAFileStaysLiteral(t *testing.T) {
	path := writeBodyFile(t, "unused\n")
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
// nothing — an interpolated-empty value and an unreadable path included,
// where falling back to the default heading would hide the miss.
func TestBodyFileRejectsBadInput(t *testing.T) {
	path := writeBodyFile(t, "x\n")
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{name: "beside --body", args: []string{"add", "t", "--body", "inline", "--body-file", path}, want: "none of the others can be"},
		{name: "missing file", args: []string{"add", "t", "--body-file", filepath.Join(filepath.Dir(path), "absent.md")}, want: "--body-file: open "},
		{name: "empty value", args: []string{"add", "t", "--body-file", ""}, want: "empty value"},
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
			if got := storeBodies(t); len(got) != 0 {
				t.Errorf("a refused add wrote %d body file(s): %q", len(got), got)
			}
		})
	}
}

// TestBulkAddAndEpicAddResolveTheBody: the body is a shared flag, so the bulk
// paths and `epic add` get the resolved text. `--batch <file> --body -` and
// `epic add --body -` used to store the literal "-".
func TestBulkAddAndEpicAddResolveTheBody(t *testing.T) {
	const content = "shared body\n"
	path := writeBodyFile(t, content)
	batch := filepath.Join(t.TempDir(), "plan.ndjson")
	if err := os.WriteFile(batch, []byte(`{"title":"one"}`+"\n"+`{"title":"two"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

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
