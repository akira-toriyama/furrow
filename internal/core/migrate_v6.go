package core

import (
	"regexp"
	"strings"
)

// The v5 -> v6 migration: the pure half of `furrow upgrade`'s one semantic
// conversion. Schema v6 retired Task.Type and Task.Parent and made the epic an
// entity of its own, so a v5 board's `"type": "epic"` tasks must BECOME epics
// and its `parent` edges must become `epic` membership — otherwise the flag day
// would strand both as unknown keys: preserved by the passthrough, flagged by
// lint's unknown-shard-key forever, and honoured by nothing.
//
// This binary no longer has Type/Parent fields, so on a v5 shard those keys
// arrive PARKED in the task's extras. This file is the one place allowed to
// reach into them by name and consume them — a migration is exactly the
// "deliberate, versioned" act the passthrough's no-way-in rule defers to.
//
// Everything here is pure: the app layer owns store I/O (shard deletion, epic
// saves, body renames) and injects the two policies core cannot know — how an
// epic id is derived from a task id ([ids].prefix / [ids].epic_prefix) and
// which lanes count as closed ([lanes] terminal).

// The retired v5 vocabulary, matched exactly (see extrasStringValue for why
// exact and not case-folded).
const (
	v5KeyType   = "type"
	v5KeyParent = "parent"
	v5TypeEpic  = "epic"
	v5TypeTask  = "task"
)

// V6Epic is one planned conversion: the v5 `type:"epic"` task and the epic
// entity it becomes. The Epic is complete and ready to save — id derived from
// the task's (so preview and apply name the same id, and a `[[t-…]]` link is
// rewritable by map lookup), title/labels/repos/timestamps carried over, Goal
// empty on purpose (a body's first paragraph is not reliably a closing
// condition; a human fills it with `furrow epic set --goal`).
type V6Epic struct {
	TaskID string
	Epic   Epic
}

// V6Conversion is the report row for one planned conversion — what `furrow
// upgrade`'s preview prints as `t-xxxx "<title>" -> e-xxxx`.
type V6Conversion struct {
	TaskID string `json:"task_id"`
	EpicID string `json:"epic_id"`
	Title  string `json:"title"`
	Closed bool   `json:"closed"`
}

// V6Kept is a retired key the migration deliberately did NOT consume, left
// parked in the shard's extras for a human: lint's unknown-shard-key keeps
// naming it until someone decides. Reasons:
//
//   - "parent-not-epic": the parent is not one of the converted epics (the
//     board's one task filed under a plain task, or a dangling id).
//   - "epic-already-set": the task already carries an epic that contradicts
//     its parent — the newer, first-class field wins; the old edge is kept as
//     evidence, never overwritten.
//   - "not-a-string": the value is not a JSON string; a hand edit we cannot
//     read is one we must not consume.
//   - "foreign-type": a type outside the v5 built-in vocabulary ("task",
//     "epic") — a custom [types] member this migration has no mapping for.
type V6Kept struct {
	TaskID string `json:"task_id"`
	Key    string `json:"key"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// V6DroppedDep is one dep edge the migration REMOVED: the target became an
// epic, and an epic cannot gate a task (deps resolve among tasks; a dangling
// dep would wedge the task out of `furrow next` forever). The information is
// not destroyed — the app layer appends a note naming the epic to the task's
// body, which is the progress record of authority.
type V6DroppedDep struct {
	TaskID string `json:"task_id"`
	DepID  string `json:"dep_id"`  // the retired task id the dep pointed at
	EpicID string `json:"epic_id"` // the epic it became
}

// V6Changes reports what ApplyV6 did to one store's index.
type V6Changes struct {
	Rehomed     int            // tasks whose parent edge became epic membership
	Kept        []V6Kept       // retired keys left for a human, in task order
	DroppedDeps []V6DroppedDep // dep edges onto converted epics, removed + noted
}

// PlanV6Epics scans an index for tasks declaring the retired v5 `"type":
// "epic"` and returns the epic each becomes. Read-only: the index is not
// touched, so a preview can call it and walk away.
//
// isClosed decides whether the box arrives closed (v5 epics in a terminal lane
// — done, icebox — become closed epics). The closed timestamp is the task's
// own Closed when it has one, else its Updated: a migration carries history,
// it does not stamp "now".
func PlanV6Epics(idx *Index, epicIDFor func(taskID string) string, isClosed func(*Task) bool) []V6Epic {
	var out []V6Epic
	for i := range idx.Tasks {
		t := &idx.Tasks[i]
		v, ok, isStr := extrasStringValue(t.extras, v5KeyType)
		if !ok || !isStr || v != v5TypeEpic {
			continue
		}
		id := epicIDFor(t.ID)
		e := Epic{
			ID: id, Title: t.Title,
			Labels:  append([]string(nil), t.Labels...),
			Repos:   append([]string(nil), t.Repos...),
			Created: t.Created, Updated: t.Updated,
			Body: BodyPath(id),
		}
		if isClosed(t) {
			c := t.Closed
			if c == nil {
				u := t.Updated
				c = &u
			}
			e.Closed = c
		}
		out = append(out, V6Epic{TaskID: t.ID, Epic: e})
	}
	return out
}

// ApplyV6 rewrites one store's index for schema v6, given the conversion map
// (old task id -> new epic id) for the WHOLE board — every store's conversions,
// not just this one's, because a hot task's parent can point at an epic that
// was archived, and a per-store map would leave that edge behind as if the epic
// had never existed. In place:
//
//   - a converted task is removed (it is an epic now; the store's Save deletes
//     the shard for the id no longer present),
//   - a `parent` edge onto a converted epic becomes `epic` membership and the
//     retired key is consumed,
//   - a retired `"type": "task"` is consumed silently (in v5's built-in
//     vocabulary, absent and "task" meant the same thing),
//   - a dep onto a converted epic is DROPPED and reported (an epic cannot gate
//     a task, and `next` treats an unresolvable dep as never-satisfied — the
//     edge would wedge the task off the board forever),
//   - everything else retired stays parked, reported as Kept.
//
// Idempotent: a second run finds no retired keys and changes nothing.
func ApplyV6(idx *Index, conv map[string]string) *V6Changes {
	ch := &V6Changes{}
	tasks := make([]Task, 0, len(idx.Tasks))
	for i := range idx.Tasks {
		t := idx.Tasks[i]
		if _, converted := conv[t.ID]; converted {
			continue // it is an epic now
		}
		if v, ok, isStr := extrasStringValue(t.extras, v5KeyType); ok {
			switch {
			case isStr && v == v5TypeTask:
				consumeExtra(&t, v5KeyType)
			case isStr:
				ch.Kept = append(ch.Kept, V6Kept{TaskID: t.ID, Key: v5KeyType, Value: v, Reason: "foreign-type"})
			default:
				ch.Kept = append(ch.Kept, V6Kept{TaskID: t.ID, Key: v5KeyType, Reason: "not-a-string"})
			}
		}
		if v, ok, isStr := extrasStringValue(t.extras, v5KeyParent); ok {
			eid, isEpic := conv[v]
			switch {
			case !isStr:
				ch.Kept = append(ch.Kept, V6Kept{TaskID: t.ID, Key: v5KeyParent, Reason: "not-a-string"})
			case !isEpic:
				ch.Kept = append(ch.Kept, V6Kept{TaskID: t.ID, Key: v5KeyParent, Value: v, Reason: "parent-not-epic"})
			case t.Epic != "" && t.Epic != eid:
				ch.Kept = append(ch.Kept, V6Kept{TaskID: t.ID, Key: v5KeyParent, Value: v, Reason: "epic-already-set"})
			default:
				if t.Epic == "" {
					t.Epic = eid
					ch.Rehomed++
				}
				consumeExtra(&t, v5KeyParent)
			}
		}
		if len(t.Deps) > 0 {
			kept := t.Deps[:0]
			for _, dep := range t.Deps {
				if eid, isEpic := conv[dep]; isEpic {
					ch.DroppedDeps = append(ch.DroppedDeps, V6DroppedDep{TaskID: t.ID, DepID: dep, EpicID: eid})
					continue
				}
				kept = append(kept, dep)
			}
			t.Deps = kept
		}
		tasks = append(tasks, t)
	}
	idx.Tasks = tasks
	return ch
}

// consumeExtra removes one parked key, returning the extras map to nil when it
// was the last — nil is the "no unknown keys" representation everywhere else
// (ExtraKeys, the marshaller's byte-identity), and an empty non-nil map would
// quietly become a third state.
func consumeExtra(t *Task, key string) {
	delete(t.extras, key)
	if len(t.extras) == 0 {
		t.extras = nil
	}
}

// RewriteV6Links rewrites `[[t-…]]` mentions of converted epics to their new
// `[[e-…]]` ids and reports how many it changed. It honours the SAME code rules
// as ExtractLinks/stripCode — a link inside a ``` fence ``` or `inline code` is
// documentation, not a mention, and must survive verbatim — because the whole
// point of the rewrite is agreeing with lint's dangling-link check about which
// links are real.
func RewriteV6Links(text string, conv map[string]string, idPrefix string) (string, int) {
	if len(conv) == 0 {
		return text, 0
	}
	re := LinkPattern(idPrefix)
	n := 0
	lines := strings.Split(text, "\n")
	inFence := false
	for li, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[li] = rewriteLinksOutsideInlineCode(line, re, conv, &n)
	}
	return strings.Join(lines, "\n"), n
}

// rewriteLinksOutsideInlineCode applies the link rewrite to the non-code
// segments of one line, walking inline code spans with the same rules as
// stripInlineCode (a run of N backticks opens, the next run of exactly N
// closes; an unterminated run code-quotes the rest of the line) — but keeping
// the spans verbatim instead of dropping them.
func rewriteLinksOutsideInlineCode(line string, re *regexp.Regexp, conv map[string]string, n *int) string {
	rewrite := func(seg string) string {
		return re.ReplaceAllStringFunc(seg, func(m string) string {
			id := m[2 : len(m)-2] // the match is the full [[id]]
			if eid, ok := conv[id]; ok {
				*n++
				return "[[" + eid + "]]"
			}
			return m
		})
	}
	var b strings.Builder
	start := 0
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		run := backtickRun(line, i)
		j := i + run
		for j < len(line) {
			if line[j] == '`' && backtickRun(line, j) == run {
				break
			}
			j++
		}
		b.WriteString(rewrite(line[start:i]))
		if j >= len(line) {
			b.WriteString(line[i:]) // unterminated span: the tail is code
			return b.String()
		}
		b.WriteString(line[i : j+run]) // the span, verbatim
		i = j + run
		start = i
	}
	b.WriteString(rewrite(line[start:]))
	return b.String()
}
