package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// t-v6ax: the help and the refusals say what the flags are actually called
// and what the command actually accepts.

func TestCLIHelpNamesRealFlags(t *testing.T) {
	out, _ := run(t, "show", "--help")
	if strings.Contains(out, "f.archived") {
		t.Errorf("show --help names a flag that does not exist:\n%s", out)
	}
	out, _ = run(t, "set", "--help")
	if strings.Contains(out, "Rerepo") {
		t.Errorf("set --help leaks an internal method name:\n%s", out)
	}
	out, _ = run(t, "value", "--help")
	if !strings.Contains(out, "value <id> [<1-5>]") {
		t.Errorf("value's Use must say the score is optional (--clear):\n%s", out)
	}
}

// --yes with neither ids nor a selection is the guard's refusal (it names what
// --yes confirms), not cobra's "requires at least 1 arg(s)".
func TestCLIYesWithoutSelectionIsTheGuardsRefusal(t *testing.T) {
	initStore(t)
	for _, args := range [][]string{{"done", "--yes"}, {"move", "ready", "--yes"}, {"set", "--yes", "-s", "ready"}} {
		fe, _ := runErr(t, args...)
		if fe == nil || fe.Code != 2 {
			t.Fatalf("%v: want exit 2, got %+v", args, fe)
		}
		if !strings.Contains(fe.Msg, "--yes confirms") {
			t.Errorf("%v: want the guard's wording, got %q", args, fe.Msg)
		}
	}
}

// `schema` honors --ndjson (one compact line) and refuses an unknown kind at
// the arity gate; --json prints the same bytes a plain call does.
func TestCLISchemaModes(t *testing.T) {
	plain, code := run(t, "schema")
	if code != 0 {
		t.Fatal("schema failed")
	}
	nd, code := run(t, "schema", "--ndjson")
	if code != 0 || strings.Count(strings.TrimRight(nd, "\n"), "\n") != 0 {
		t.Errorf("schema --ndjson must be one line, got %d newlines", strings.Count(nd, "\n"))
	}
	var a, b any
	if err := json.Unmarshal([]byte(plain), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(nd), &b); err != nil {
		t.Fatalf("ndjson line is not JSON: %v", err)
	}
	if js, code := run(t, "schema", "--json"); code != 0 || js != plain {
		t.Errorf("schema --json must print the same bytes")
	}
	if _, code := run(t, "schema", "bogus"); code != 2 {
		t.Errorf("an unknown schema kind must be exit 2, got %d", code)
	}
}

func TestCLIEpicShowNoBody(t *testing.T) {
	initStore(t)
	out, code := run(t, "--json", "epic", "add", "box", "--repo", "o/r")
	if code != 0 {
		t.Fatalf("epic add: %s", out)
	}
	var e struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &e)
	out, code = run(t, "--json", "epic", "show", e.ID, "--no-body")
	if code != 0 {
		t.Fatalf("epic show --no-body: %s", out)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	if _, has := v["body_text"]; has {
		t.Errorf("--no-body must omit body_text: %s", out)
	}
	if v["id"] != e.ID {
		t.Errorf("the box's metadata must remain: %s", out)
	}
}
