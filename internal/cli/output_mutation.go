// The {before, after, changed} envelope every task mutation prints, and the
// field comparison that computes `changed`. A new persisted Task field must
// be added to changedFields, or `changed` silently omits it — the shard-fields
// golden does not see this file.

package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// printOK prints a short confirmation line for a mutation (human mode) or the
// task as one JSON value (--json indented / --ndjson compact one-line).
func printOK(verb string, t *core.Task) {
	if jsonMode() {
		emitObject(t)
		return
	}
	fmt.Fprintf(out, "%s %s  %s\n", verb, t.ID, t.Title)
}

// printMutation reports a single-task edit. In machine mode it emits
// {before, after, changed} so an agent sees the effect of a mutation inline,
// without a follow-up `show` — indented under --json, compact one-line under
// --ndjson. Any `extra` keys (e.g. a `clamped` signal) are merged into that
// envelope. In human mode it prints the short verb line.
func printMutation(verb string, before, after *core.Task, extra map[string]any) {
	if jsonMode() {
		emitObject(mutationEnvelope(before, after, extra))
		return
	}
	fmt.Fprintf(out, "%s %s  %s\n", verb, after.ID, after.Title)
}

// mutationEnvelope builds the documented {before, after, changed} object, with
// any extra keys (a `clamped` signal, a `renumbered` array) merged in. The
// single-id and batch paths BOTH go through it: the shape is a published
// contract, and it was previously assembled in two places, so a new key had to
// be added twice or the batch path would quietly emit a different envelope from
// the single one.
func mutationEnvelope(before, after *core.Task, extra map[string]any) map[string]any {
	return envelope(before, after, changedFields(before, after), extra)
}

// envelope is the {before, after, changed} shape every mutation's --json
// carries, plus the caller's extras — assembled ONCE, so a key added to the
// envelope is added for tasks and boxes alike (t-3tq4).
func envelope(before, after any, changed []string, extra map[string]any) map[string]any {
	m := map[string]any{
		"before":  before,
		"after":   after,
		"changed": changed,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// warnClamp writes a stderr note when an explicit 1..5 estimate was silently
// rounded by the marshaller's clamp (nil requested / in-range = no-op). An
// explicit CLI arg deserves a signal — clamp-don't-reject is a config-file
// policy, not for a typed command argument (t-abj3). stdout stays pure.
func warnClamp(field string, requested, stored *int) {
	if requested == nil || (*requested >= core.EstimateMin && *requested <= core.EstimateMax) {
		return
	}
	s := 0
	if stored != nil {
		s = *stored
	}
	fmt.Fprintf(errOut, "note: %s %d clamped to %d (valid range %d..%d)\n", field, *requested, s, core.EstimateMin, core.EstimateMax)
}

// clampEntry returns the {requested, stored} envelope entry when an explicit
// estimate was clamped, else nil — the machine-readable twin of warnClamp for
// the mutation's --json/--ndjson `clamped` field.
func clampEntry(requested, stored *int) map[string]any {
	if requested == nil || (*requested >= core.EstimateMin && *requested <= core.EstimateMax) {
		return nil
	}
	s := 0
	if stored != nil {
		s = *stored
	}
	return map[string]any{"requested": *requested, "stored": s}
}

// changedFields lists the task fields that differ between before and after
// (json field names), so an agent need not diff the two objects itself. The
// `updated` stamp and the immutable `created`/`body` are omitted — `updated`
// because it is derived: it moves exactly when this list is non-empty (or when
// the write was prose, which `changed` deliberately does not track), so
// reporting it would only ever restate the answer.
// An empty result is [] (never null), and a nil before yields [].
func changedFields(before, after *core.Task) []string {
	ch := []string{}
	if before == nil {
		return ch
	}
	if before.Status != after.Status {
		ch = append(ch, "status")
	}
	if before.Priority != after.Priority {
		ch = append(ch, "priority")
	}
	if !intpEq(before.Value, after.Value) {
		ch = append(ch, "value")
	}
	if !intpEq(before.Effort, after.Effort) {
		ch = append(ch, "effort")
	}
	if before.Title != after.Title {
		ch = append(ch, "title")
	}
	if before.Epic != after.Epic {
		ch = append(ch, "epic")
	}
	if !strsEq(before.Labels, after.Labels) {
		ch = append(ch, "labels")
	}
	if !strsEq(before.Repos, after.Repos) {
		ch = append(ch, "repos")
	}
	if !strsEq(before.Deps, after.Deps) {
		ch = append(ch, "deps")
	}
	if !strsEq(before.Refs, after.Refs) {
		ch = append(ch, "refs")
	}
	if !checklistEq(before.Checklist, after.Checklist) {
		ch = append(ch, "checklist")
	}
	if !timeEq(before.Closed, after.Closed) {
		ch = append(ch, "closed")
	}
	if !timeEq(before.Reviewed, after.Reviewed) {
		ch = append(ch, "reviewed")
	}
	if !timeEq(before.Due, after.Due) {
		ch = append(ch, "due")
	}
	// In struct order, after due. A close CONSUMES a rule, so without these a
	// write that rewrote the shard and advanced `updated` reported changed: [].
	if before.Repeat != after.Repeat {
		ch = append(ch, "repeat")
	}
	if !timeEq(before.RepeatAnchor, after.RepeatAnchor) {
		ch = append(ch, "repeat_anchor")
	}
	return ch
}

// intpEq compares two optional ints: both nil is equal; one nil is not.
func intpEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// strsEq compares two string slices; nil and empty compare equal.
func strsEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checklistEq compares two checklists (ChecklistItem is comparable).
func checklistEq(a, b []core.ChecklistItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// timeEq compares optional timestamps: both nil is equal; one nil is not.
func timeEq(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// atoiArg parses a CLI integer argument into a validation error on failure.
func atoiArg(name, s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, core.Validationf("", "%s must be an integer, got %q", name, s)
	}
	return n, nil
}
