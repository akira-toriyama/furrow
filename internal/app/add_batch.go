package app

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/akira-toriyama/furrow/internal/core"
)

// BatchSpec is one line of `furrow add --batch`: an AddSpec plus an optional
// Key — a name valid only inside this batch, so a line can depend on another
// line and a body can link to it before either has an id. Keys never reach a
// shard: AddBatch resolves every key to the id it minted and the tasks are
// stored exactly as `add` would store them.
type BatchSpec struct {
	Key string
	AddSpec
}

// keyLink matches a [[key]] link. Keys are batch-local, so only the ones this
// batch declares are rewritten; anything else ([[t-k3m9p]], prose in brackets)
// is left verbatim for lint's dangling-link check to judge as usual.
var keyLink = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

// AddBatch creates the specs in ONE write, with intra-batch references
// resolved: a dep may name a batch key instead of an existing id, and a
// [[key]] in a title, body, or checklist item becomes [[id]]. Ids are minted
// up front so the references can be rewritten before anything is written, and
// every spec is validated first — a duplicate key, a key that shadows an
// existing id, a dep naming neither an id nor a key, or a dep cycle inside the
// batch is a validation error that writes nothing (addMany's all-or-nothing
// contract). The returned tasks are in spec order; keys[i] is spec i's key.
func (a *App) AddBatch(specs []BatchSpec) ([]core.Task, []string, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	idx, err := a.load()
	if err != nil {
		return nil, nil, err
	}
	// Keys: unique, and never an id the board already has — a dep spelled
	// "t-k3m9p" must mean one thing.
	byKey := map[string]int{}
	for i, s := range specs {
		k := s.Key
		if k == "" {
			continue
		}
		if strings.ContainsAny(k, "[]") || strings.TrimSpace(k) != k {
			return nil, nil, core.Validationf("", "spec %d (%q): key %q may not contain brackets or surrounding whitespace", i, s.Title, k)
		}
		if j, dup := byKey[k]; dup {
			return nil, nil, core.Validationf("", "spec %d (%q): key %q is already used by spec %d (%q)", i, s.Title, k, j, specs[j].Title)
		}
		if idx.Has(k) {
			return nil, nil, core.Validationf("", "spec %d (%q): key %q is an existing task id — pick a key that is not an id", i, s.Title, k)
		}
		byKey[k] = i
	}
	// Mint every id before resolving anything: a key is only a name for an id
	// this write has not stored yet.
	reserved := map[string]bool{}
	ids := make([]string, len(specs))
	for i := range specs {
		id, err := a.uniqueIDExcluding(idx, reserved)
		if err != nil {
			return nil, nil, err
		}
		reserved[id] = true
		ids[i] = id
	}
	idOfKey := func(k string) (string, bool) {
		i, ok := byKey[k]
		if !ok {
			return "", false
		}
		return ids[i], true
	}
	// Deps: an existing id passes through, a key becomes its id, anything else
	// is the error single `add` gives for a missing dep — spelled for a batch.
	edges := make([][]int, len(specs)) // intra-batch dep graph, for the cycle check
	out := make([]AddSpec, len(specs))
	for i, s := range specs {
		spec := s.AddSpec
		spec.ID = ids[i]
		spec.Deps = nil
		for _, d := range s.Deps {
			switch {
			case idx.Has(d):
				spec.Deps = append(spec.Deps, d)
			default:
				id, ok := idOfKey(d)
				if !ok {
					return nil, nil, core.Validationf("", "spec %d (%q): dependency %q is neither an existing task id nor a key in this batch", i, s.Title, d)
				}
				if id == ids[i] {
					return nil, nil, core.Validationf("", "spec %d (%q): a task cannot depend on itself (key %q)", i, s.Title, d)
				}
				spec.Deps = append(spec.Deps, id)
				edges[i] = append(edges[i], byKey[d])
			}
		}
		rewrite := func(text string) string {
			return keyLink.ReplaceAllStringFunc(text, func(m string) string {
				k := m[2 : len(m)-2]
				if id, ok := idOfKey(k); ok {
					return "[[" + id + "]]"
				}
				return m
			})
		}
		spec.Title = rewrite(spec.Title)
		spec.Body = rewrite(spec.Body)
		if len(spec.Checklist) > 0 {
			items := make([]string, len(spec.Checklist))
			for j, c := range spec.Checklist {
				items[j] = rewrite(c)
			}
			spec.Checklist = items
		}
		out[i] = spec
	}
	if cyc := batchCycle(edges); cyc != nil {
		names := make([]string, 0, len(cyc))
		for _, i := range cyc {
			names = append(names, fmt.Sprintf("%q", specs[i].Key))
		}
		return nil, nil, core.Validationf("", "dependency cycle inside the batch: %s", strings.Join(names, " -> "))
	}
	created, err := a.addMany(out, true)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, len(specs))
	for i := range specs {
		keys[i] = specs[i].Key
	}
	return created, keys, nil
}

// batchCycle returns one dependency cycle among the batch's own edges (spec
// indexes, first node repeated at the end), or nil. Edges to existing tasks
// cannot close a cycle — an existing task never depends on a task that does
// not exist yet — so only the intra-batch graph is walked.
func batchCycle(edges [][]int) []int {
	const (
		white = iota
		grey
		black
	)
	color := make([]int, len(edges))
	var stack []int
	var found []int
	var visit func(int) bool
	visit = func(u int) bool {
		color[u] = grey
		stack = append(stack, u)
		for _, v := range edges[u] {
			switch color[v] {
			case grey:
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] == v {
						found = append(append([]int(nil), stack[i:]...), v)
						return true
					}
				}
			case white:
				if visit(v) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return false
	}
	for u := range edges {
		if color[u] == white && visit(u) {
			return found
		}
	}
	return nil
}
