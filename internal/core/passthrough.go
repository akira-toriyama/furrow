package core

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// Unknown-key passthrough — the other half of the version gate.
//
// #112 made the board's layout version an INPUT to every write, so a binary can
// only write a board that declares its exact layout. That closes the 2026-07-13
// outage: a routine write can no longer migrate a shared board behind its owner's
// back. But every bit of that protection is keyed on the version being BUMPED.
//
// If a future furrow adds a shard field and does NOT bump SchemaVersion — because
// the change looks "additive" — then meta.json still says v4, no gate fires
// anywhere, and an older binary reads the shard, drops the key it doesn't know
// (encoding/json's lenient unmarshal), and writes the loss back on its next save.
// One ordinary write, one destroyed field, no error. 2026-07-13 was only visible
// because someone did the right thing and bumped; this is its silent twin.
//
// The fix is not another rule to remember. It is to make the round-trip lossless:
// PRESERVE what you do not understand. A shard's unknown keys are parked in an
// extras map and re-emitted on the way out, so an old binary hands a future field
// back exactly as it found it.
//
// It lives beside the marshaller, on the STORE's write path, because that is
// where the data is: core.UnmarshalTask parks the unknown keys, core.MarshalTask
// puts them back. The byte recipe (2-space indent, no HTML escaping, trailing
// newline) is not duplicated — the object is composed COMPACTLY here and indented
// once, as a finished document.

// Extras carries the keys a shard had that this binary does not know. nil when
// there are none — which is the overwhelmingly common case, and keeps a shard
// written by a current binary byte-identical to what it has always written.
type Extras map[string]json.RawMessage

// knownNames returns the json key names a struct type declares, so unmarshalling
// can tell "a field I know" from "a field from the future". Derived by reflection
// rather than hand-listed: a hand-list would drift the moment someone adds a
// field, and drifting is the exact failure this file exists to prevent.
func knownNames(v any) []string {
	rt := reflect.TypeOf(v)
	names := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue // the unexported extras carrier is not on disk
		}
		names = append(names, name)
	}
	return names
}

// isKnown reports whether encoding/json already consumed this shard key into a
// struct field. It MUST agree with encoding/json EXACTLY, in BOTH directions —
// each disagreement is its own corruption bug, and both were reproduced
// end-to-end against the real binary before this was fixed:
//
//   - A key json takes but we call unknown is parked AND re-emitted, so the shard
//     ends up carrying it twice.
//   - A key json ignores but we call known is deleted — the silent data loss this
//     whole file exists to prevent, reintroduced by the guard against it.
//
// encoding/json matches an object key to a field by preferring an exact match and
// otherwise accepting a UNICODE SIMPLE CASE-FOLD match (its internal foldName).
// strings.EqualFold is that same relation, which is why it is used here instead of
// the obvious strings.ToLower over a set: lowercasing and case-FOLDING are not the
// same function, and they disagree in both directions.
//
//   - json folds it, ToLower does not — "statuſ" (U+017F LATIN SMALL LETTER LONG S)
//     is fed to Task.Status by json, while ToLower leaves it unequal to "status". A
//     ToLower set parks it, the shard then carries BOTH "status" and "statuſ", and
//     extras are re-emitted LAST — so the stale copy wins on the next read and
//     `furrow move` never takes. The task is wedged in a lane forever.
//   - ToLower folds it, json does not — "İd" (U+0130 LATIN CAPITAL LETTER I WITH
//     DOT ABOVE) lowercases to "id", but its SimpleFold orbit is empty, so json
//     never matches it. A ToLower set calls it known and DROPS it while Task.ID
//     stays empty — destroying the key and the task's identity, silently.
//
// EqualFold agrees with json on both, so a key is parked if and only if json
// ignored it. It subsumes the exact match too, so one comparison covers both of
// json's cases; the scan is linear over ~17 names, once per shard key.
func isKnown(names []string, key string) bool {
	for _, n := range names {
		if strings.EqualFold(n, key) {
			return true
		}
	}
	return false
}

// splitExtras separates a raw object's keys into the ones the struct declares and
// the ones it does not. The unknown ones are returned verbatim (json.RawMessage),
// never parsed into a Go shape we would then have to re-derive — preserving is
// the point, and a value we do not understand is a value we must not rewrite.
func splitExtras(data []byte, known []string) (Extras, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	for k := range all {
		if isKnown(known, k) { // encoding/json already took it
			delete(all, k)
		}
	}
	if len(all) == 0 {
		return nil, nil // the common case: no allocation, and extras stays nil
	}
	return all, nil
}

// encodeCompactNoHTML serializes v with the recipe's escaping rule but WITHOUT
// indentation — the indentation is applied once, to the finished document, by
// encodeCanonicalWithExtras. Doing it here too would mean two places knew the
// indent rules.
//
// The escaping matters even in the compact form: json.Marshal would turn `<`
// into `<`, and the outer encoder's SetEscapeHTML(false) cannot undo an
// escape that already happened — it can only decline to add one. So a plain
// json.Marshal here would silently break the "CJK and < > & survive verbatim"
// property that makes an app-write equal a hand-edit.
func encodeCompactNoHTML(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// spliceExtras appends the unknown keys to a compact JSON object, sorted by key.
//
// Sorted, and always after the known keys: a map's iteration order is random, and
// a random key order would rewrite every shard on every save — destroying the
// zero-git-churn property that fsstore.Save's byte-comparison rests on.
func spliceExtras(obj []byte, extras Extras) ([]byte, error) {
	if len(extras) == 0 {
		return obj, nil
	}
	if !bytes.HasSuffix(obj, []byte("}")) {
		return nil, Internalf("", "cannot splice unknown keys into a non-object: %s", obj)
	}
	keys := make([]string, 0, len(extras))
	for k := range extras {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := bytes.NewBuffer(obj[:len(obj)-1]) // drop the closing brace
	empty := out.Len() == 1                  // the object was "{}" — no comma before the first key
	for _, k := range keys {
		if !empty {
			out.WriteByte(',')
		}
		empty = false
		kb, err := encodeCompactNoHTML(k)
		if err != nil {
			return nil, err
		}
		out.Write(kb)
		out.WriteByte(':')
		// Compact the value: it came off disk and may carry any whitespace. The
		// outer encoder re-indents the whole document uniformly, so a value that
		// arrived pretty-printed and one that arrived compact end up identical —
		// which is what makes the round-trip a fixed point.
		var vb bytes.Buffer
		if err := json.Compact(&vb, extras[k]); err != nil {
			return nil, err
		}
		out.Write(vb.Bytes())
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// The json key names each persisted type declares, derived once from the structs.
var (
	taskKnownKeys = knownNames(Task{})
	repoKnownKeys = knownNames(RepoRecord{})
	metaKnownKeys = knownNames(Meta{})
)

// encodeCanonicalWithExtras is encodeCanonical plus the unknown keys: compose the
// object compactly (known fields in struct order, then the unknown ones sorted),
// then apply the recipe's indentation to the finished document in ONE pass. The
// indent rules therefore still live in exactly one place.
//
// NOTE ON WHY THIS IS NOT A MarshalJSON METHOD: a MarshalJSON on Task would be
// PROMOTED to every struct that embeds it — and internal/cli's --json views embed
// core.Task to add body_text / snippet / reason / mentioned_by beside it. Go would
// then use the promoted method for the OUTER struct and silently drop every
// sibling field. So the splice happens here, on the store's write path, where the
// data actually lives. A consequence, deliberately accepted: the CLI's --json view
// projects the keys this binary KNOWS; the unknown ones are preserved on disk but
// not surfaced there. Preserving beats displaying — losing them is the bug.
func encodeCanonicalWithExtras(v any, extras Extras) ([]byte, error) {
	compact, err := encodeCompactNoHTML(v)
	if err != nil {
		return nil, err
	}
	compact, err = spliceExtras(compact, extras)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := json.Indent(&b, compact, "", "  "); err != nil {
		return nil, err
	}
	b.WriteByte('\n') // the recipe's trailing newline (Encode would have added it)
	return b.Bytes(), nil
}

// extraKeys names the unknown keys a record arrived with, sorted; nil when there
// are none. It is the ONLY way out of an extras map, and there is deliberately no
// way IN other than unmarshalling a shard: furrow must never be able to invent an
// unknown key, only to hand one back.
func extraKeys(e Extras) []string {
	if len(e) == 0 {
		return nil
	}
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ExtraKeys reports the keys this record carried that furrow does not know.
//
// All THREE persisted types expose it, and that is not symmetry for its own sake:
// all three are machine-written, and all three now declare additionalProperties:
// true in their published schema — so `furrow lint` (internal/app.Lint) is the
// only thing left that can say "this key is preserved, but IGNORED". A type that
// parked unknown keys without an accessor would hide a typo in meta.json or a repo
// review shard from every tool furrow has, forever, because nothing ever deletes
// an extra.
func (t *Task) ExtraKeys() []string { return extraKeys(t.extras) }

// ExtraKeys reports the keys meta.json carried that furrow does not know.
func (m *Meta) ExtraKeys() []string { return extraKeys(m.extras) }

// ExtraKeys reports the keys this repo review shard carried that furrow does not know.
func (r *RepoRecord) ExtraKeys() []string { return extraKeys(r.extras) }
