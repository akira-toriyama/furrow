package core

import (
	"strings"
	"testing"
)

// A v10 shard has to survive the two things that destroy shards: the marshaller
// re-ordering it, and the unknown-key passthrough dropping what this binary
// does not know. The two new fields sit at the END of the struct and extras are
// re-emitted AFTER the known keys, so a shard carrying both must come back
// byte-identical — otherwise every ordinary write churns the board.
func TestARepeatingShardRoundTripsByteIdentical(t *testing.T) {
	const shard = `{
  "id": "t-k3m9p",
  "title": "水やり",
  "status": "inbox",
  "priority": 100,
  "labels": [],
  "repos": [],
  "deps": [],
  "refs": [],
  "checklist": [],
  "created": "2026-03-01T00:00:00Z",
  "updated": "2026-03-01T00:00:00Z",
  "closed": null,
  "reviewed": null,
  "body": "bodies/t-k3m9p.md",
  "due": "2026-03-01T14:59:59Z",
  "repeat": "FREQ=MONTHLY;BYMONTHDAY=-1",
  "repeat_anchor": "2026-03-01T14:59:59Z",
  "sprint": "a key written by a newer furrow"
}
`
	task, err := UnmarshalTask([]byte(shard))
	if err != nil {
		t.Fatalf("UnmarshalTask: %v", err)
	}
	if task.Repeat != "FREQ=MONTHLY;BYMONTHDAY=-1" || task.RepeatAnchor == nil {
		t.Fatalf("the known repeat fields did not load: %q / %v", task.Repeat, task.RepeatAnchor)
	}
	if keys := task.ExtraKeys(); len(keys) != 1 || keys[0] != "sprint" {
		t.Fatalf("extras = %v, want just the unknown key", keys)
	}

	out, err := MarshalTask(task)
	if err != nil {
		t.Fatalf("MarshalTask: %v", err)
	}
	if string(out) != shard {
		t.Errorf("a v10 shard did not round-trip byte-identically:\n--- got ---\n%s\n--- want ---\n%s", out, shard)
	}

	// The unknown key must come AFTER the known ones, which is the only order
	// that keeps an old binary's re-emit and a new binary's write in agreement.
	if strings.Index(string(out), `"repeat_anchor"`) > strings.Index(string(out), `"sprint"`) {
		t.Error("the extra key was spliced before a known field")
	}
}

// A task that does not repeat must serialize exactly as it did at v9: both new
// fields are omitempty, so no board in the fleet sees a rewritten shard on
// upgrade. TestFrozenBoardRoundTripsByteIdentical proves it on real bytes; this
// pins the reason.
func TestANonRepeatingShardCarriesNeitherKey(t *testing.T) {
	out, err := MarshalTask(&Task{
		ID: "t-k3m9p", Title: "x", Status: "inbox", Priority: 100,
		Body: BodyPath("t-k3m9p"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"repeat"`, `"repeat_anchor"`} {
		if strings.Contains(string(out), key) {
			t.Errorf("a task with no rule wrote %s:\n%s", key, out)
		}
	}
}
