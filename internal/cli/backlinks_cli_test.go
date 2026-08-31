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

	out, code := run(t, "show", target, "--backlinks")
	if code != 0 {
		t.Fatalf("show --backlinks exit=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "Mentioned in") || !strings.Contains(out, mentioner) || !strings.Contains(out, "the mentioner") {
		t.Errorf("human --backlinks should list the mentioner:\n%s", out)
	}

	out, _ = run(t, "show", target)
	if strings.Contains(out, "Mentioned in") {
		t.Errorf("plain show must not scan backlinks:\n%s", out)
	}

	out, code = run(t, "--json", "show", target, "--backlinks")
	if code != 0 {
		t.Fatalf("show --backlinks --json exit=%d:\n%s", code, out)
	}
	var arr []struct {
		MentionedBy []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"mentioned_by"`
	}
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("parse show --backlinks --json: %v\n%s", err, out)
	}
	if len(arr) != 1 {
		t.Fatalf("want a one-element array (always-array rule), got:\n%s", out)
	}
	v := arr[0]
	if len(v.MentionedBy) != 1 || v.MentionedBy[0].ID != mentioner || v.MentionedBy[0].Title != "the mentioner" {
		t.Errorf("mentioned_by should list %s (the mentioner), got %+v", mentioner, v.MentionedBy)
	}

	out, _ = run(t, "--json", "show", target)
	if strings.Contains(out, "mentioned_by") {
		t.Errorf("plain show --json must not include mentioned_by:\n%s", out)
	}
}
