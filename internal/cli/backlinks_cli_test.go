package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShowBacklinks(t *testing.T) {
	initStore(t)
	target := addTask(t, "target task", "-s", "ready")
	mentioner := addTask(t, "the mentioner", "-s", "ready", "--body", "blocks [["+target+"]]")

	// human: --backlinks lists the mentioning task under a "Mentioned in" section.
	out, code := run(t, "show", target, "--backlinks")
	if code != 0 {
		t.Fatalf("show --backlinks exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "Mentioned in") || !strings.Contains(out, mentioner) || !strings.Contains(out, "the mentioner") {
		t.Errorf("human --backlinks should list the mentioner:\n%s", out)
	}

	// plain show is unchanged: no backlinks scan, no "Mentioned in".
	out, _ = run(t, "show", target)
	if strings.Contains(out, "Mentioned in") {
		t.Errorf("plain show must not scan backlinks:\n%s", out)
	}

	// --json --backlinks: mentioned_by carries the mentioner (id/title/status).
	out, code = run(t, "--json", "show", target, "--backlinks")
	if code != 0 {
		t.Fatalf("show --backlinks --json exit=%d:\n%s", code, out)
	}
	var v struct {
		MentionedBy []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"mentioned_by"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("parse show --backlinks --json: %v\n%s", err, out)
	}
	if len(v.MentionedBy) != 1 || v.MentionedBy[0].ID != mentioner || v.MentionedBy[0].Title != "the mentioner" {
		t.Errorf("mentioned_by should list %s (the mentioner), got %+v", mentioner, v.MentionedBy)
	}

	// plain --json show must not carry mentioned_by (opt-in only).
	out, _ = run(t, "--json", "show", target)
	if strings.Contains(out, "mentioned_by") {
		t.Errorf("plain show --json must not include mentioned_by:\n%s", out)
	}
}
