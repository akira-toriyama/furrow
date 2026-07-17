package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCLIBriefJSONShape(t *testing.T) {
	initStore(t)
	ra := addTask(t, "ready a", "-s", "ready", "-r", "o/r")
	addTask(t, "wip task", "-s", "in-progress", "-r", "o/r")
	bl := addTask(t, "blocked b", "-s", "ready", "-r", "o/r", "--dep", ra)
	addTask(t, "loose idea") // no repo = a draft

	out, code := run(t, "--json", "brief", "-n", "1")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	var b struct {
		Repo string `json:"repo"`
		Next []struct {
			ID       string `json:"id"`
			BodyText string `json:"body_text"`
		} `json:"next"`
		NextTotal int `json:"next_total"`
		Blocked   []struct {
			ID        string   `json:"id"`
			BlockedBy []string `json:"blocked_by"`
		} `json:"blocked"`
		Revisit map[string]any `json:"revisit"`
		Drafts  int            `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("parse brief --json: %v\n%s", err, out)
	}
	if len(b.Next) != 1 || b.Next[0].ID != ra {
		t.Fatalf("next = %+v, want just ready-a (the -n cap)", b.Next)
	}
	if !strings.Contains(b.Next[0].BodyText, "# ready a") {
		t.Errorf("next picks must carry body_text, got %q", b.Next[0].BodyText)
	}
	if b.NextTotal != 2 {
		t.Errorf("next_total = %d, want 2 (cap must not hide the queue size)", b.NextTotal)
	}
	if len(b.Blocked) != 1 || b.Blocked[0].ID != bl || strings.Join(b.Blocked[0].BlockedBy, ",") != ra {
		t.Errorf("blocked = %+v, want blocked-b waiting on ready-a", b.Blocked)
	}
	if b.Drafts != 1 {
		t.Errorf("drafts = %d, want 1", b.Drafts)
	}
	if _, ok := b.Revisit["dep_done"]; !ok {
		t.Errorf("revisit summary must keep its sync shape (dep_done key):\n%s", out)
	}
}

func TestCLIBriefHumanAndEmpty(t *testing.T) {
	initStore(t)

	// An empty board is a healthy brief: exit 0, every section present.
	out, code := run(t, "brief")
	if code != 0 {
		t.Fatalf("empty brief exit = %d:\n%s", code, out)
	}
	for _, want := range []string{"next", "blocked", "revisit", "drafts"} {
		if !strings.Contains(out, want) {
			t.Errorf("human brief missing the %q section:\n%s", want, out)
		}
	}

	addTask(t, "ready a", "-s", "ready", "-r", "o/r")
	out, code = run(t, "brief")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "ready a") {
		t.Errorf("human brief should list the next pick:\n%s", out)
	}
	if strings.Contains(out, "# ready a") {
		t.Errorf("human brief must NOT dump bodies (that is --json's payload):\n%s", out)
	}
}

func TestCLIBriefNDJSONIsOneLine(t *testing.T) {
	initStore(t)
	addTask(t, "ready a", "-s", "ready", "-r", "o/r")

	out, code := run(t, "--ndjson", "brief")
	if code != 0 {
		t.Fatalf("brief --ndjson exit = %d:\n%s", code, out)
	}
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n"); n != 0 {
		t.Errorf("--ndjson must emit ONE compact line (single-object command), got %d extra newlines:\n%s", n, out)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("ndjson line must be valid JSON: %v\n%s", err, out)
	}
}

func TestCLIBriefEmptyCollectionsAreNeverNull(t *testing.T) {
	initStore(t)

	// A board with nothing to report must still emit [] everywhere — an agent
	// indexes the arrays unconditionally (the house nil-slice rule). sync hides
	// an empty revisit summary behind omitempty; brief always shows it, so it
	// must normalize.
	out, code := run(t, "--json", "brief")
	if code != 0 {
		t.Fatalf("brief exit = %d:\n%s", code, out)
	}
	if strings.Contains(out, "null") {
		t.Errorf("brief --json must not contain null (empty = []):\n%s", out)
	}
}
