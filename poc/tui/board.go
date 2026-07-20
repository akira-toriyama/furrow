package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// nowFn is indirected so tests get deterministic timestamps.
var nowFn = time.Now

// priorityStep is furrow's sparse-priority spacing: reordering edits one
// integer field rather than renumbering a lane.
const priorityStep = 10

// ChecklistItem mirrors furrow's shard checklist entry.
type ChecklistItem struct {
	Text string
	Done bool
}

// Task mirrors the fields of a furrow task shard that this POC renders.
type Task struct {
	ID        string
	Title     string
	Status    string // the lane
	Priority  int    // sparse, 10-step; order WITHIN the lane
	Type      string // "" means the board's default type ("task")
	Value     int    // 1..5
	Effort    int    // 1..5
	Labels    []string
	Repos     []string
	Parent    string
	Deps      []string
	Refs      []string
	Checklist []ChecklistItem
	Created   time.Time
	Updated   time.Time
	Closed    time.Time
	Reviewed  time.Time
	Body      string
}

// EffectiveType resolves a type-less task to the board default, the way
// furrow's `ls --type task` includes the type-less majority.
func (t *Task) EffectiveType() string {
	if t.Type == "" {
		return defaultType
	}
	return t.Type
}

// ShortRepo renders "akira-toriyama/vista" as "vista" for a narrow card.
func (t *Task) ShortRepo() string {
	if len(t.Repos) == 0 {
		return ""
	}
	r := t.Repos[0]
	if i := strings.LastIndex(r, "/"); i >= 0 {
		r = r[i+1:]
	}
	if len(t.Repos) > 1 {
		return fmt.Sprintf("%s+%d", r, len(t.Repos)-1)
	}
	return r
}

// CheckProgress counts the task's own checklist.
func (t *Task) CheckProgress() (done, total int) {
	for _, c := range t.Checklist {
		if c.Done {
			done++
		}
	}
	return done, len(t.Checklist)
}

// Lane is one column of the board.
type Lane struct {
	Name string
	Next bool // one of the lanes `furrow next` considers
	Done bool
	Term bool // terminal: work does not leave it
	WIP  int  // 0 = unset. RENDERED, never enforced — GitHub Projects parity.
}

// DisplayName is the lane as a HUMAN reads it — "In progress", not the
// `in-progress` slug. Presentation only: Name stays the slug everywhere the
// model, the filter grammar and `furrow set --status` are concerned.
func (l Lane) DisplayName() string {
	s := strings.ReplaceAll(l.Name, "-", " ")
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

const defaultType = "task"

// boardLanes is the lane vocabulary, in board order.
var boardLanes = []Lane{
	{Name: "inbox"},
	{Name: "backlog"},
	{Name: "ready", Next: true, WIP: 2},
	{Name: "in-progress", Next: true, WIP: 1},
	{Name: "done", Done: true, Term: true},
	{Name: "icebox", Term: true},
}

// containerTypes is furrow's `[types].containers`: a box, not work.
var containerTypes = map[string]bool{"epic": true}

// Board is the in-memory task set. Lane membership is Task.Status and lane
// ORDER is Task.Priority — there is no separate ordering table to fall out of
// sync with, exactly as in a real furrow store.
type Board struct {
	tasks []*Task
	lanes []Lane
}

// NewBoard builds a board over the given tasks.
func NewBoard(tasks []*Task) *Board {
	return &Board{tasks: tasks, lanes: append([]Lane(nil), boardLanes...)}
}

// Lanes returns the lane vocabulary in board order.
func (b *Board) Lanes() []Lane { return b.lanes }

// Lane looks a lane up by name.
func (b *Board) Lane(name string) *Lane {
	for i := range b.lanes {
		if b.lanes[i].Name == name {
			return &b.lanes[i]
		}
	}
	return nil
}

// LaneIndex is the lane's position in board order, or -1.
func (b *Board) LaneIndex(name string) int {
	for i, l := range b.lanes {
		if l.Name == name {
			return i
		}
	}
	return -1
}

// Tasks returns every task on the board.
func (b *Board) Tasks() []*Task { return b.tasks }

// Task looks a task up by id, nil when absent.
func (b *Board) Task(id string) *Task {
	for _, t := range b.tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// LaneTasks returns a lane's tasks ordered by priority (id breaks ties, so the
// order is total and stable).
func (b *Board) LaneTasks(lane string) []*Task {
	var out []*Task
	for _, t := range b.tasks {
		if t.Status == lane {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// IndexIn is the task's position within its lane, or -1.
func (b *Board) IndexIn(lane, id string) int {
	for i, t := range b.LaneTasks(lane) {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// AdjustDropIndex converts an insertion index measured against a lane AS
// DISPLAYED — which still contains the moving card, because neither move mode
// nor a mouse drag reflows the board under the cursor — into the post-removal
// index MoveTo expects.
//
// This is the remove-then-insert boundary. Without it, dragging a card one slot
// DOWN inside its own lane is a no-op (the vacated slot absorbs the move), the
// classic off-by-one in every hand-rolled kanban.
func AdjustDropIndex(sameLane bool, fromIdx, idx int) int {
	if sameLane && idx > fromIdx {
		return idx - 1
	}
	return idx
}

// MoveTo places task id into lane at insertion index idx, where idx counts the
// destination lane WITHOUT the task (see AdjustDropIndex). It edits the sparse
// Priority field; only when the gap between neighbours is exhausted does it
// respace the whole lane, returning the ids it renumbered — furrow's contract.
func (b *Board) MoveTo(id, lane string, idx int) (renumbered []string, err error) {
	t := b.Task(id)
	if t == nil {
		return nil, fmt.Errorf("unknown task %q", id)
	}
	dst := b.Lane(lane)
	if dst == nil {
		return nil, fmt.Errorf("unknown lane %q", lane)
	}

	var peers []*Task
	for _, x := range b.LaneTasks(lane) {
		if x.ID != id {
			peers = append(peers, x)
		}
	}
	if idx < 0 {
		idx = 0
	}
	if idx > len(peers) {
		idx = len(peers)
	}

	wasDone := b.isDoneLane(t.Status)
	t.Status = lane
	t.Updated = nowFn().UTC().Truncate(time.Second)
	switch {
	case dst.Done && !wasDone:
		t.Closed = t.Updated
	case !dst.Done && wasDone:
		t.Closed = time.Time{}
	}

	if p, ok := sparsePriority(peers, idx); ok {
		t.Priority = p
		return nil, nil
	}
	return b.respace(lane, t, idx), nil
}

// sparsePriority finds a priority strictly between the neighbours at the
// insertion point, reporting false when the gap is exhausted.
func sparsePriority(peers []*Task, idx int) (int, bool) {
	switch {
	case len(peers) == 0:
		return priorityStep, true
	case idx == 0:
		if hi := peers[0].Priority; hi > priorityStep {
			return hi - priorityStep, true
		}
		return 0, false
	case idx == len(peers):
		return peers[len(peers)-1].Priority + priorityStep, true
	default:
		lo, hi := peers[idx-1].Priority, peers[idx].Priority
		if hi-lo >= 2 {
			return lo + (hi-lo)/2, true
		}
		return 0, false
	}
}

// respace rewrites the whole lane on the 10-step grid with t inserted at idx,
// returning the OTHER ids whose priority moved. Their Updated deliberately does
// not advance: a respace is positional bookkeeping, not progress.
func (b *Board) respace(lane string, t *Task, idx int) []string {
	var order []*Task
	for _, x := range b.LaneTasks(lane) {
		if x.ID != t.ID {
			order = append(order, x)
		}
	}
	if idx > len(order) {
		idx = len(order)
	}
	order = append(order[:idx:idx], append([]*Task{t}, order[idx:]...)...)

	var moved []string
	for i, x := range order {
		p := (i + 1) * priorityStep
		if x.Priority != p {
			if x.ID != t.ID {
				moved = append(moved, x.ID)
			}
			x.Priority = p
		}
	}
	return moved
}

func (b *Board) isDoneLane(name string) bool {
	l := b.Lane(name)
	return l != nil && l.Done
}

// DoneLane is the board's done lane name.
func (b *Board) DoneLane() string {
	for _, l := range b.lanes {
		if l.Done {
			return l.Name
		}
	}
	return ""
}

// Close moves a task to the done lane, appending it at the end.
func (b *Board) Close(id string) error {
	d := b.DoneLane()
	if d == "" {
		return fmt.Errorf("board has no done lane")
	}
	if t := b.Task(id); t != nil && t.Status == d {
		return nil
	}
	_, err := b.MoveTo(id, d, len(b.LaneTasks(d)))
	return err
}

// ToggleCheck flips one checklist item.
func (b *Board) ToggleCheck(id string, i int) error {
	t := b.Task(id)
	if t == nil {
		return fmt.Errorf("unknown task %q", id)
	}
	if i < 0 || i >= len(t.Checklist) {
		return fmt.Errorf("task %s has no checklist item %d", id, i)
	}
	t.Checklist[i].Done = !t.Checklist[i].Done
	t.Updated = nowFn().UTC().Truncate(time.Second)
	return nil
}

// SetBody replaces a task's prose and stamps Updated, the way `furrow note`
// does (a bare file edit would leave Updated stale).
func (b *Board) SetBody(id, body string) error {
	t := b.Task(id)
	if t == nil {
		return fmt.Errorf("unknown task %q", id)
	}
	t.Body = body
	t.Updated = nowFn().UTC().Truncate(time.Second)
	return nil
}
