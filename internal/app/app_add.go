// Task creation: AddOpts and the one Add path, including the scope repo union,
// the inherited epic and the id draw.

package app

import (
	"github.com/akira-toriyama/furrow/internal/core"
)

// seedChecklist turns repeatable --check values into shard checklist items.
// Add and AddMany BOTH call it: the bulk path used to omit Checklist from its
// task literal entirely, so `add --stdin --check x` silently created an empty
// checklist while the identical single add stored the item — the same
// silent-drop divergence as t-ek9y (--type) and t-adx9 (--value/--effort).
// One function, one behavior. Blanks never reach here: requireNonBlank rejects
// them up front, the same way `check --add ""` is exit 2.
func seedChecklist(texts []string) []core.ChecklistItem {
	out := make([]core.ChecklistItem, 0, len(texts))
	for _, text := range texts {
		out = append(out, core.ChecklistItem{Text: text})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AddOpts are the optional fields for Add. A nil Priority means "auto" (append
// after the lane's last task using the sparse step).
type AddOpts struct {
	Status   string
	Priority *int
	Value    *int // optional coarse 1..5 estimate; nil = unset
	Effort   *int // optional coarse 1..5 estimate; nil = unset
	Labels   []string
	// Repos attaches the task to repositories. Each entry is resolved strictly
	// (full owner/repo, or a short name naming exactly one repo in the board's
	// universe — see resolveRepoIn).
	Repos []string
	// Epic is the box to file this task under: an epic REFERENCE (id, unique id
	// prefix, or unique title substring), resolved by Add through ResolveEpic so a
	// typo is exit 2 with candidates rather than a task filed under nothing.
	// Resolution lives here, not in the CLI, so every front-end gets the same
	// spelling rules. "" INHERITS the board scope's single active epic when there
	// is exactly one (see inheritableEpic) — the epic mirror of the withBoardRepo
	// union — and otherwise leaves the task unfiled (legal at add time, an error
	// in `furrow lint` while the task is open once the board has boxes).
	Epic string
	// NoEpic suppresses the active-epic inheritance (the CLI's explicit
	// `-e ''`): the task is created unfiled even when the scope has exactly one
	// active box.
	NoEpic bool
	Deps   []string
	Refs   []string
	Body   string // initial body markdown; "" seeds a heading from the title
	// Repeat is the raw `--repeat` spelling (see recur.Compile), anchored to
	// this task's own Due — which is therefore required alongside it.
	Repeat string
	// Checklist seeds unchecked checklist items at creation (repeatable --check).
	// A plain `add --body '- [ ] x'` does NOT populate the shard's checklist —
	// the body is prose — so this makes a seed-time checklist first-class. Blank
	// entries are dropped; text is taken verbatim (commas included), like
	// `check --add`.
	Checklist []string
	// Draft marks the task as deliberately repo-less (repos == [], the
	// issue-draft analogue). It conflicts with explicit Repos, and it
	// suppresses exactly the board-scope repo union (see withBoardRepo) — the
	// escape hatch for "note this on the board, attach it later".
	Draft bool
	// Due is the raw `--due` spelling (a date, a date+time, or a signed offset),
	// bound to an instant by ParseDue at creation. A REFERENCE like Epic, not a
	// stored value: resolution lives here so every front-end reads "+1d" the same
	// way, against the app's clock. "" leaves the task dateless.
	Due string
}

// requireNonBlankValues applies the blank rule to every list-valued creation
// flag, so `add -l "a,"` cannot be the door a blank label comes in through
// after `label --add` closed it. Deps and Repos are covered too: both are
// resolved against existing ids/repos, and a blank there produced a confusing
// "does not exist" instead of naming the real fault.
func (o AddOpts) requireNonBlankValues(id string) error {
	for _, f := range []struct {
		flag string
		vals []string
	}{
		{"-l/--label", o.Labels},
		{"-r/--repo", o.Repos},
		{"--ref", o.Refs},
		{"--dep", o.Deps},
		{"--check", o.Checklist},
	} {
		if err := requireNonBlank(id, f.flag, f.vals); err != nil {
			return err
		}
	}
	return nil
}

// Add creates a task, writes its body file, and saves the index — a
// one-element addMany, so the task-creation rule has exactly ONE
// implementation (t-dk6w: Add and AddMany used to duplicate every step of it,
// and every recorded divergence — t-adx9, t-ek9y, the dropped --check,
// the unfolded title — was one path forgetting a step the other had). Errors
// keep the classic single-task wording (no "spec 0" prefix). Returns the
// created task.
func (a *App) Add(title string, o AddOpts) (*core.Task, error) {
	created, err := a.addMany([]AddSpec{{Title: title, AddOpts: o}}, false)
	if err != nil {
		return nil, err
	}
	return &created[0], nil
}

// uniqueID draws random ids from the store until one is not already present in
// idx. AddMany appends each created task to idx before the next call, so this
// also keeps a batch internally unique. Ids are random, so the first draw almost
// always wins; the cap turns a pathological store into a loud error rather than
// an infinite loop.
func (a *App) uniqueID(idx *core.Index) (string, error) { return a.uniqueIDExcluding(idx, nil) }

// uniqueIDExcluding is uniqueID that also avoids ids a caller has already handed
// out in this same write but not yet inserted — the case a pre-pass creates,
// where the index cannot yet answer for its own batch.
func (a *App) uniqueIDExcluding(idx *core.Index, reserved map[string]bool) (string, error) {
	for i := 0; i < 100; i++ {
		id, err := a.Store.NextID()
		if err != nil {
			return "", err
		}
		if !idx.Has(id) && !reserved[id] {
			return id, nil
		}
	}
	return "", core.Internalf("", "could not generate a unique id after 100 attempts")
}

// withDefaultLabel unions a central board's literal `label` tag (if any) into a
// label set, so `add` on that board carries the tag without an explicit -l.
// Returns a copy; a no-op when no board label is set or it is already present.
func (a *App) withDefaultLabel(labels []string) []string {
	if a.DefaultLabel == "" || contains(labels, a.DefaultLabel) {
		return labels
	}
	return append(append([]string(nil), labels...), a.DefaultLabel)
}

// inheritableEpic returns the id of the ONE active epic visible in the board
// scope, or "" — the box a plain `add` files new work under (the epic mirror of
// withBoardRepo's repo union: on a board with boxes, every open task must name
// one — lint's epic-required — so a capture during a declared focus defaults to
// that focus, disclosed by the CLI and reversed with `set -e ”`). Exactly-one
// is the rule: zero actives means no focus to inherit, and two or more (a
// multi-repo board) would make furrow GUESS which focus the capture belongs to,
// which it never does. Pinned-but-inactive channels do not count — pinned is a
// visibility declaration, not a focus. Errors resolve to "" (capture must not
// fail on a broken epic shard; the task just lands unfiled).
func (a *App) inheritableEpic() string {
	scope := ""
	if a.AutoFilter {
		scope = a.DefaultRepo
	}
	items, err := a.EpicList(EpicQueryOpts{Repo: scope})
	if err != nil {
		return ""
	}
	found := ""
	for _, it := range items {
		if !it.Epic.Active {
			continue
		}
		if found != "" {
			return "" // two actives — never guess between focuses
		}
		found = it.Epic.ID
	}
	return found
}

// withBoardRepo unions the board-scope repo (a pointer's / central board's
// DefaultRepo) into a task's repos on add — the repos-field mirror of
// withDefaultLabel. Draft suppresses exactly this union (the task stays a
// draft); an explicit -r adds to the board repo rather than replacing it,
// mirroring the old label-union semantics. Returns a copy.
func (a *App) withBoardRepo(repos []string, draft bool) []string {
	if draft || a.DefaultRepo == "" || contains(repos, a.DefaultRepo) {
		return repos
	}
	return append(append([]string(nil), repos...), a.DefaultRepo)
}
