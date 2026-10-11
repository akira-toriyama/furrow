package core

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"
)

// Field-grain three-way merge of one machine-written shard — the resolver
// `furrow sync` runs when a pull --rebase stops on a shard both sides wrote.
//
// Why it exists: every write stamps `updated`, so two writes to the same shard
// conflict TEXTUALLY even when they touch unrelated fields — the task-status
// bot closing a task (status / closed / updated) one second before a session
// ticks its checklist (checklist / updated) stalled a whole shared board's
// pushes for a quarter of an hour (t-v1fg). Bodies have git's union driver for
// this; JSON cannot be unioned by lines, so the merge is done here, per key,
// on the parsed object.
//
// Why the key grain and nothing finer: a top-level key is the unit furrow
// writes (one command sets `status`, `labels`, `checklist` whole), so "did this
// side change the key" is exactly "did this side's command touch the field". A
// deeper merge (per checklist row, per label) would have to guess at the
// semantics of a value; this one only ever compares bytes and defers the rest
// to the canonicalizer.

// ShardKind names which canonical (decode, encode) pair a merged shard is
// normalised through — the single-marshaller contract applies to a merge
// result exactly as to any other write.
type ShardKind int

const (
	ShardTask ShardKind = iota // tasks/<id>.json and archive/tasks/<id>.json
	ShardEpic                  // epics/<id>.json
	ShardRepo                  // repos/<name>.json
)

// mergeTimestamps names the keys whose both-sides-changed case is a CLOCK
// disagreement rather than a disagreement about the task, and how it resolves:
// `updated` is advanced by every write, so the later stamp is the truthful
// "last written"; `closed` set by both sides means both closed it, and the
// first close is when it was closed (the double-`done` of a PR footer and a
// session closing the same task). Anything else that both sides set to
// different values is a real conflict — and so is ANY `closed` difference on
// a shard whose base carried a `repeat` rule (see repeatCloseConflict).
var mergeTimestamps = map[string]func(a, b time.Time) time.Time{
	"updated": func(a, b time.Time) time.Time {
		if b.After(a) {
			return b
		}
		return a
	},
	"closed": func(a, b time.Time) time.Time {
		if b.Before(a) {
			return b
		}
		return a
	},
}

// MergeShard three-way merges one shard at the top-level-key grain. base is the
// shard as both sides last agreed on it (nil when there is no common ancestor
// — both sides created it), ours and theirs the two divergent versions.
//
// Per key: unchanged on one side takes the other side's value (set, changed,
// or removed); the same change on both sides is kept once; a key both sides
// changed differently is a conflict — except the two timestamps mergeTimestamps
// names. conflicts lists those keys, sorted; when it is non-empty merged is nil
// and the caller keeps the shard as it was (the sync aborts its rebase).
//
// merged is written through the kind's own decoder and encoder, so the result
// is byte-for-byte what furrow itself would have written: canonical key order,
// the recipe's indentation, unknown keys parked and re-emitted. A merge whose
// result the decoder refuses (a side was not a valid shard) is a validation
// error, and the caller treats it as a conflict.
func MergeShard(kind ShardKind, base, ours, theirs []byte) (merged []byte, conflicts []string, err error) {
	b, err := rawObject(base)
	if err != nil {
		return nil, nil, Validationf("", "merge: base is not a JSON object: %v", err)
	}
	o, err := rawObject(ours)
	if err != nil {
		return nil, nil, Validationf("", "merge: one side is not a JSON object: %v", err)
	}
	t, err := rawObject(theirs)
	if err != nil {
		return nil, nil, Validationf("", "merge: one side is not a JSON object: %v", err)
	}
	repeating := repeatCloseConflict(b)

	keys := map[string]bool{}
	for _, m := range []map[string]json.RawMessage{b, o, t} {
		for k := range m {
			keys[k] = true
		}
	}
	out := make(map[string]json.RawMessage, len(keys))
	for k := range keys {
		bv, ov, tv := compactValue(b[k]), compactValue(o[k]), compactValue(t[k])
		var v json.RawMessage
		switch {
		case bytes.Equal(ov, tv): // same on both sides (both absent included)
			v = ov
		case k == "closed" && repeating:
			conflicts = append(conflicts, k)
			continue
		case bytes.Equal(ov, bv): // only theirs changed
			v = tv
		case bytes.Equal(tv, bv): // only ours changed
			v = ov
		default:
			pick, ok := mergeTimestamps[k]
			if !ok {
				conflicts = append(conflicts, k)
				continue
			}
			ot, oOK := timestampValue(ov)
			tt, tOK := timestampValue(tv)
			if !oOK || !tOK {
				conflicts = append(conflicts, k)
				continue
			}
			if pick(ot, tt).Equal(ot) {
				v = ov
			} else {
				v = tv
			}
		}
		if v != nil { // nil = the key is absent on the winning side
			out[k] = v
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return nil, conflicts, nil
	}
	compact, err := composeObject(out)
	if err != nil {
		return nil, nil, Internalf("", "merge: compose: %v", err)
	}
	merged, err = canonicalShard(kind, compact)
	if err != nil {
		return nil, nil, err
	}
	return merged, nil, nil
}

// repeatCloseConflict reports whether the base shard carried a repeat rule, in
// which case a `closed` that differs between the sides is a conflict however
// it differs — never a clock to resolve, never a one-sided change to take.
// Closing a repeating task MINTS its successor from the state the closer saw:
// a double close has minted two live successors (two shards with distinct
// random ids, which git merges without a murmur), and an edit racing a close
// lands on the closed predecessor while the successor was cut from the base.
// Pre-merge, both stopped the sync for a human; they must still.
func repeatCloseConflict(base map[string]json.RawMessage) bool {
	rule, ok := rawString(base["repeat"])
	return ok && rule != ""
}

// compactValue normalises a raw value for comparison: a hand-edit's whitespace
// must not read as a change. nil (absent) stays nil; a value json cannot
// compact is compared verbatim — it fails the canonicalizer later anyway.
func compactValue(v json.RawMessage) json.RawMessage {
	if v == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, v); err != nil {
		return v
	}
	return buf.Bytes()
}

// timestampValue reads a raw value as the shard's timestamp form (an RFC 3339
// JSON string); ok is false for null, a non-string, or an unparsable string.
func timestampValue(v json.RawMessage) (time.Time, bool) {
	s, ok := rawString(v)
	if !ok {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// composeObject writes the merged keys as one compact JSON object, sorted by
// key. The order is immaterial — canonicalShard re-emits the document in the
// struct's key order — sorted only so the intermediate is deterministic.
func composeObject(m map[string]json.RawMessage) ([]byte, error) {
	keys := sortedKeys(m)
	var out bytes.Buffer
	out.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		kb, err := encodeCompactNoHTML(k)
		if err != nil {
			return nil, err
		}
		out.Write(kb)
		out.WriteByte(':')
		out.Write(m[k])
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// canonicalShard runs the merged object through the kind's own decoder and
// encoder — the single marshaller path — so a merge result is indistinguishable
// from a shard furrow wrote. The decoders refuse only non-JSON, so the one
// identity field is checked here: a side that was an empty object (a truncated
// blob) would otherwise merge into a nameless zero shard.
func canonicalShard(kind ShardKind, data []byte) ([]byte, error) {
	switch kind {
	case ShardTask:
		t, err := UnmarshalTask(data)
		if err != nil {
			return nil, err
		}
		if t.ID == "" {
			return nil, Validationf("", "merge: result has no id")
		}
		return MarshalTask(t)
	case ShardEpic:
		e, err := UnmarshalEpic(data)
		if err != nil {
			return nil, err
		}
		if e.ID == "" {
			return nil, Validationf("", "merge: result has no id")
		}
		return MarshalEpic(e)
	case ShardRepo:
		r, err := UnmarshalRepo(data)
		if err != nil {
			return nil, err
		}
		if r.Repo == "" {
			return nil, Validationf("", "merge: result names no repo")
		}
		return MarshalRepo(r)
	}
	return nil, Internalf("", "merge: unknown shard kind %d", kind)
}
