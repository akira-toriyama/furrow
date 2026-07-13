package core

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// THE regression test for the silent-field-strip hole. furrow's version gate
// (#112) only fires when someone BUMPS SchemaVersion. If a future furrow adds a
// shard field and does NOT bump — because the change looks "additive" — then
// meta.json still says v4, no gate fires anywhere, and an older binary reads the
// shard, drops the key it doesn't know, and writes the loss back on the next
// save. The 2026-07-13 outage was only VISIBLE because someone did the right
// thing and bumped; this is the silent version of the same bug.
//
// A shard round-trip must therefore PRESERVE what it does not understand.
func TestTaskRoundTripPreservesUnknownKeys(t *testing.T) {
	// A shard as a NEWER furrow would write it: every key this binary knows, plus
	// two it does not (a scalar and a nested object).
	shard := []byte(`{
  "id": "t-k3m9p",
  "title": "a task",
  "status": "ready",
  "priority": 100,
  "value": null,
  "effort": null,
  "labels": [],
  "repos": [],
  "deps": [],
  "refs": [],
  "checklist": [],
  "created": "2026-07-13T00:00:00Z",
  "updated": "2026-07-13T00:00:00Z",
  "closed": null,
  "reviewed": null,
  "body": "bodies/t-k3m9p.md",
  "estimate_hours": 3.5,
  "assignee": {"login": "akira-toriyama", "since": "2026-07-13T00:00:00Z"}
}
`)

	task, err := UnmarshalTask(shard)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "t-k3m9p" || task.Title != "a task" {
		t.Fatalf("known fields lost: %+v", task)
	}

	out, err := MarshalTask(task)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"estimate_hours", "assignee", "akira-toriyama"} {
		if !bytes.Contains(out, []byte(key)) {
			t.Errorf("re-marshalled shard lost %q — one ordinary write would destroy a field a newer furrow wrote:\n%s", key, out)
		}
	}

	// And it must be STABLE: writing it again changes nothing. Otherwise every
	// save of an untouched task rewrites its shard and churns git — the property
	// fsstore.Save's byte-comparison depends on.
	again, err := MarshalTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, again) {
		t.Errorf("re-marshal is not stable:\n%s\n---\n%s", out, again)
	}
	reread, err := UnmarshalTask(out)
	if err != nil {
		t.Fatal(err)
	}
	third, err := MarshalTask(reread)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, third) {
		t.Errorf("round-trip is not a fixed point:\n%s\n---\n%s", out, third)
	}
}

// The byte recipe still holds with extras present: 2-space indent, unknown keys
// AFTER the known ones and sorted (determinism — the order a map iterates in is
// not one), no HTML escaping, one trailing newline.
func TestUnknownKeysObeyTheByteRecipe(t *testing.T) {
	shard := []byte(`{"id":"t-1","title":"x","status":"ready","priority":100,"labels":[],"repos":[],"deps":[],"refs":[],"checklist":[],"created":"2026-07-13T00:00:00Z","updated":"2026-07-13T00:00:00Z","body":"bodies/t-1.md","zeta":1,"alpha":"<b>&","mid":{"k":"v"}}`)
	task, err := UnmarshalTask(shard)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalTask(task)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	if !strings.HasSuffix(s, "}\n") || strings.HasSuffix(s, "\n\n") {
		t.Errorf("trailing newline recipe broken:\n%q", s)
	}
	// SetEscapeHTML(false) means `< > &` SURVIVE VERBATIM — in unknown values too.
	// A plain json.Marshal anywhere on the way would emit the < form instead,
	// and the outer encoder cannot undo an escape that already happened: it can
	// only decline to add one. So this asserts the raw characters, and the ABSENCE
	// of the escaped form.
	escaped := "\\u003c"
	if !strings.Contains(s, `"alpha": "<b>&"`) || strings.Contains(s, escaped) {
		t.Errorf("an unknown value was HTML-escaped; the byte recipe says it survives verbatim:\n%s", s)
	}
	// Unknown keys come after every known key, and among themselves are sorted.
	iBody, iAlpha, iMid, iZeta := strings.Index(s, `"body"`), strings.Index(s, `"alpha"`), strings.Index(s, `"mid"`), strings.Index(s, `"zeta"`)
	if iBody < 0 || iAlpha < 0 || iMid < 0 || iZeta < 0 {
		t.Fatalf("a key went missing:\n%s", s)
	}
	if iBody >= iAlpha || iAlpha >= iMid || iMid >= iZeta {
		t.Errorf("unknown keys must follow the known ones, sorted (alpha < mid < zeta):\n%s", s)
	}
	// Nested unknown values are indented like everything else, not left compact.
	if !strings.Contains(s, "\"mid\": {\n    \"k\": \"v\"\n  }") {
		t.Errorf("a nested unknown value was not re-indented to the 2-space recipe:\n%s", s)
	}
	// The whole thing is still valid JSON.
	var any map[string]any
	if err := json.Unmarshal(out, &any); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
}

// A task with NO unknown keys must be byte-identical to what furrow wrote before
// this change — otherwise every existing shard on every board churns on the next
// save, and the golden round-trip test is the canary.
func TestNoExtrasChangesNothing(t *testing.T) {
	task := &Task{
		ID: "t-0001", Title: "plain", Status: "ready", Priority: 100,
		Body: BodyPath("t-0001"),
	}
	out, err := MarshalTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("extra")) {
		t.Errorf("the extras carrier leaked into the shard as a key:\n%s", out)
	}
	// The exact shape furrow has always written: no stray comma, no empty object.
	if !bytes.HasPrefix(out, []byte("{\n  \"id\": \"t-0001\",\n")) {
		t.Errorf("the known-key prologue changed:\n%s", out)
	}
	rt, err := UnmarshalTask(out)
	if err != nil {
		t.Fatal(err)
	}
	if rt.ExtraKeys() != nil {
		t.Errorf("a shard with no unknown keys must carry a nil extras map, got %v", rt.ExtraKeys())
	}
}

// The repo review shards (repos/*.json) and meta.json are machine-written too,
// and a field added to either without a version bump is the same silent loss.
func TestRepoAndMetaPreserveUnknownKeys(t *testing.T) {
	repo, err := UnmarshalRepo([]byte(`{"repo":"owner/app","last_reviewed":null,"last_agent_reviewed":null,"cadence_days":14}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("cadence_days")) {
		t.Errorf("repo shard lost an unknown key:\n%s", out)
	}

	meta, err := UnmarshalMeta([]byte(`{"schema_version":4,"min_reader":3}`))
	if err != nil {
		t.Fatal(err)
	}
	mo, err := MarshalMeta(meta)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(mo, []byte("min_reader")) {
		t.Errorf("meta.json lost an unknown key:\n%s", mo)
	}
	if meta.SchemaVersion != 4 {
		t.Errorf("known field lost: %+v", meta)
	}
}

// encoding/json matches struct fields CASE-INSENSITIVELY: a shard key "BODY"
// populates Task.Body. So a case-sensitive known-key set would classify "BODY" as
// unknown, park it in extras, and re-emit it — producing a shard with BOTH "body"
// and "BODY". Worse, it self-replicates: the next read parks it again. A duplicate
// key inside the one function CLAUDE.md marks DO-NOT-REGRESS is not acceptable.
func TestKnownKeysAreCaseFolded(t *testing.T) {
	shard := []byte(`{"id":"t-1","TITLE":"shouty","status":"ready","priority":100,"BODY":"bodies/t-1.md"}`)
	task, err := UnmarshalTask(shard)
	if err != nil {
		t.Fatal(err)
	}
	// encoding/json already folded these into the known fields...
	if task.Title != "shouty" || task.Body != "bodies/t-1.md" {
		t.Fatalf("encoding/json should have case-folded TITLE/BODY into the struct: %+v", task)
	}
	// ...so they must NOT also be parked as unknown.
	if n := len(task.ExtraKeys()); n != 0 {
		t.Errorf("extras = %v, want none — those keys ARE known, just differently cased", task.ExtraKeys())
	}

	out, err := MarshalTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(`"BODY"`)) || bytes.Contains(out, []byte(`"TITLE"`)) {
		t.Errorf("a case-variant key was re-emitted, so the shard now carries it twice:\n%s", out)
	}
	// And the duplicate would self-replicate on the next round-trip.
	again, err := UnmarshalTask(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.ExtraKeys()) != 0 {
		t.Errorf("the duplicate replicated: %v", again.ExtraKeys())
	}
}

// "Case-folded" is not "lowercased", and the difference is two corruption bugs.
//
// encoding/json matches a key to a field by preferring an exact match and else
// accepting a UNICODE SIMPLE CASE-FOLD match. The first implementation of this
// file used strings.ToLower, which is a DIFFERENT function — it disagrees with
// json in both directions, and both were reproduced end-to-end against the real
// binary. isKnown now uses strings.EqualFold, which is json's own relation.
//
// These two subtests pin each direction. Each one FIRST asserts what encoding/json
// actually did, so if the stdlib ever changes its matcher the test says so instead
// of silently passing.
func TestKnownKeysFoldExactlyLikeEncodingJSON(t *testing.T) {
	// U+017F LATIN SMALL LETTER LONG S folds into the s/S orbit, so encoding/json
	// DOES feed "statuſ" to Task.Status. strings.ToLower does not fold it, so a
	// ToLower set would ALSO park it as unknown — and since extras are re-emitted
	// LAST, the stale copy would win on the next read. `furrow move` would write
	// "status" and the very next read would revert it: the task wedges in a lane
	// forever, and nothing ever removes an extra.
	t.Run("json consumed it, so it must NOT be parked", func(t *testing.T) {
		task, err := UnmarshalTask([]byte(`{"id":"t-1","title":"x","statuſ":"done","priority":1,"body":"bodies/t-1.md"}`))
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != "done" {
			t.Fatalf("encoding/json no longer case-folds U+017F into Status (got %q) — this test's premise moved", task.Status)
		}
		if keys := task.ExtraKeys(); len(keys) != 0 {
			t.Fatalf("extras = %v, want none: encoding/json already took that key, so parking it duplicates the field", keys)
		}
		out, err := MarshalTask(task)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out, []byte("statuſ")) {
			t.Errorf("the shard now carries status TWICE, and the trailing copy wins on the next read:\n%s", out)
		}
	})

	// U+0130 LATIN CAPITAL LETTER I WITH DOT ABOVE lowercases to "i", but its
	// SimpleFold orbit is empty — so encoding/json never matches "İd" to Task.ID. A
	// ToLower set would call it KNOWN and DELETE it, while Task.ID stayed empty:
	// the key destroyed, the task's identity with it. That is precisely the silent
	// data loss this file exists to prevent, reintroduced by the guard against it.
	t.Run("json ignored it, so it MUST be parked", func(t *testing.T) {
		task, err := UnmarshalTask([]byte(`{"İd":"t-1","title":"x","status":"ready","priority":1,"body":"bodies/t-1.md"}`))
		if err != nil {
			t.Fatal(err)
		}
		if task.ID != "" {
			t.Fatalf("encoding/json now folds U+0130 into ID (got %q) — this test's premise moved", task.ID)
		}
		if keys := task.ExtraKeys(); len(keys) != 1 || keys[0] != "İd" {
			t.Fatalf("extras = %v, want [İd]: encoding/json did NOT take that key, so dropping it destroys data", keys)
		}
		out, err := MarshalTask(task)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(out, []byte("İd")) {
			t.Errorf("a key encoding/json never read was destroyed on write — the exact bug passthrough exists to fix:\n%s", out)
		}
	})
}

// The splice is new load-bearing byte code inside the DO-NOT-REGRESS marshaller.
// Fuzz the property that matters: whatever a shard contains, a round-trip must be
// a FIXED POINT — marshal(unmarshal(x)) == marshal(unmarshal(marshal(unmarshal(x)))).
// If it is not, every save rewrites the shard and the board churns forever.
func FuzzTaskRoundTripIsAFixedPoint(f *testing.F) {
	f.Add(`{"id":"t-1","title":"x","status":"ready","priority":1,"body":"bodies/t-1.md"}`)
	f.Add(`{"id":"t-1","title":"x","zz":{"deep":{"deeper":[1,2,{"a":null}]}},"aa":1e400}`)
	f.Add(`{"id":"t-1","title":"日本語","note":"<b>&</b>","big":9007199254740993}`)
	f.Add(`{"id":"t-1","EMPTY":{},"arr":[],"nul":null,"BODY":"bodies/t-1.md"}`)
	// Keys where encoding/json's case-FOLD and strings.ToLower disagree — the two
	// directions TestKnownKeysFoldExactlyLikeEncodingJSON pins. Seeded here too so
	// the fixed-point and no-duplicate-key properties are hammered against them:
	// U+017F (json folds it into status), U+0130 (json does not fold it into id),
	// U+212A KELVIN SIGN (folds into k).
	f.Add(`{"id":"t-1","statuſ":"done","status":"ready","body":"bodies/t-1.md"}`)
	f.Add(`{"İd":"t-1","id":"t-2","title":"x","checKlist":[]}`)

	f.Fuzz(func(t *testing.T, shard string) {
		first, err := UnmarshalTask([]byte(shard))
		if err != nil {
			return // not a task shard; the store rejects it with a validation error
		}
		once, err := MarshalTask(first)
		if err != nil {
			t.Fatalf("marshalling a shard we just parsed must not fail: %v\ninput: %s", err, shard)
		}
		second, err := UnmarshalTask(once)
		if err != nil {
			t.Fatalf("furrow's own output must parse: %v\n%s", err, once)
		}
		twice, err := MarshalTask(second)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(once, twice) {
			t.Fatalf("round-trip is not a fixed point — every save would rewrite this shard:\n%s\n---\n%s", once, twice)
		}
		// And the output must always be a legal, single-keyed JSON object: a splice
		// bug that emitted a duplicate key would still parse, so check explicitly.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(once, &raw); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, once)
		}
		seen := map[string]bool{}
		dec := json.NewDecoder(bytes.NewReader(once))
		tok, err := dec.Token() // '{'
		if err != nil {
			t.Fatal(err)
		}
		_ = tok
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				t.Fatal(err)
			}
			key := k.(string)
			if seen[key] {
				t.Fatalf("duplicate key %q in the output — the splice re-emitted a key the struct already wrote:\n%s", key, once)
			}
			seen[key] = true
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				t.Fatal(err)
			}
		}
	})
}
