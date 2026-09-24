package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// t-wrcg: a dependency graph is written in one NDJSON file with batch-local
// keys, in any order, and --json hands back each task with its key — the only
// way to learn the ids, since shards never carry keys.
func TestCLIAddBatchKeysDepsAndLinks(t *testing.T) {
	initStore(t)
	in := strings.Join([]string{
		`{"key":"serve","title":"serve dinner","deps":["cook"],"body":"needs [[cook]] first","checklist":["clear [[cook]] station"],"value":4,"labels":["day-of"]}`,
		``,
		`{"key":"cook","title":"cook the mains","status":"ready","effort":2}`,
		`{"title":"no key, plain line"}`,
	}, "\n") + "\n"
	stdout, stderr, code := runSplitIn(t, in, "--json", "add", "--batch", "-", "-l", "batch", "-r", "o/r")
	if code != 0 {
		t.Fatalf("add --batch exit %d:\n%s\n%s", code, stdout, stderr)
	}
	var rows []struct {
		ID     string   `json:"id"`
		Key    string   `json:"key"`
		Title  string   `json:"title"`
		Status string   `json:"status"`
		Deps   []string `json:"deps"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("parse: %v\n%s", err, stdout)
	}
	if len(rows) != 3 || rows[0].Key != "serve" || rows[1].Key != "cook" || rows[2].Key != "" {
		t.Fatalf("rows = %+v, want spec order with keys echoed (blank key omitted)", rows)
	}
	serve, cook := rows[0], rows[1]
	if len(serve.Deps) != 1 || serve.Deps[0] != cook.ID {
		t.Errorf("serve.deps = %v, want [%s] (the key resolved to the minted id)", serve.Deps, cook.ID)
	}
	if cook.Status != "ready" {
		t.Errorf("a per-line status must win over the default lane: %q", cook.Status)
	}
	// The shared -l unions with the line's own labels.
	if strings.Join(serve.Labels, ",") != "batch,day-of" {
		t.Errorf("labels = %v, want the flag's tag unioned with the line's", serve.Labels)
	}
	show, _ := run(t, "--json", "show", serve.ID)
	if !strings.Contains(show, "[["+cook.ID+"]]") || strings.Contains(show, "[[cook]]") {
		t.Errorf("body and checklist must carry [[id]], never [[key]]:\n%s", show)
	}
	// The pair is a real dep: cook is actionable, serve is blocked on it.
	next, _ := run(t, "next", "--all-epics")
	if !strings.Contains(next, cook.ID) || strings.Contains(next, serve.ID) {
		t.Errorf("next must list cook and not serve:\n%s", next)
	}
}

// Refusals: an unknown field, a bad reference, a stdin clash — each exit 2,
// nothing written.
func TestCLIAddBatchRefusals(t *testing.T) {
	initStore(t)
	cases := []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{"unknown field", `{"title":"x","labl":["a"]}`, nil, "unknown field(s) labl"},
		{"missing title", `{"key":"a"}`, nil, "title is required"},
		{"not an object", `[1,2]`, nil, "not a JSON object"},
		{"unknown dep", `{"title":"x","deps":["ghost"]}`, nil, "neither an existing task id nor a key"},
		{"cycle", "{\"key\":\"a\",\"title\":\"a\",\"deps\":[\"b\"]}\n{\"key\":\"b\",\"title\":\"b\",\"deps\":[\"a\"]}", nil, "cycle inside the batch"},
		{"with --stdin", `{"title":"x"}`, []string{"--stdin"}, "cannot combine --batch with --stdin"},
		{"with a title arg", `{"title":"x"}`, []string{"extra"}, "cannot combine --batch with title arguments"},
		{"with --body -", `{"title":"x"}`, []string{"--body", "-"}, "single stream"},
		{"empty input", "\n\n", nil, "no task objects"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"add", "--batch", "-"}, c.args...)
			fe, _ := runErrIn(t, c.stdin, args...)
			if fe == nil || exitOf(fe) != 2 || !strings.Contains(fe.Msg, c.want) {
				t.Fatalf("want exit 2 naming %q, got %+v", c.want, fe)
			}
			if c.name == "unknown field" && len(fe.Candidates) == 0 {
				t.Errorf("an unknown field must carry the vocabulary in candidates: %+v", fe)
			}
		})
	}
	if out, _ := run(t, "--json", "ls", "-r", "''"); strings.Contains(out, "\"id\"") {
		t.Errorf("a refused batch must write nothing:\n%s", out)
	}
}

// --batch reads a file too, and the plain (human) output is the ordinary task
// table — keys are --json's payload.
func TestCLIAddBatchFromFileHumanTable(t *testing.T) {
	initStore(t)
	path := filepath.Join(t.TempDir(), "plan.ndjson")
	if err := os.WriteFile(path, []byte("{\"key\":\"k\",\"title\":\"from a file\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := run(t, "add", "--batch", path)
	if code != 0 || !strings.Contains(out, "from a file") || strings.Contains(out, "\"key\"") {
		t.Fatalf("exit %d, out:\n%s", code, out)
	}
}
