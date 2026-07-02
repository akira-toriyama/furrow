// Package tui is the bubbletea (v1) adapter — furrow's interactive terminal UI.
// Like the CLI, it is presentation only: every mutation goes through
// internal/app.App (the single funnel), and it never writes files itself.
//
// Layout: a filterable task list (left) + a glamour-rendered body/detail pane
// (right). Keys: navigate, done, move lane, reorder within a lane (K/J), edit
// body in $EDITOR, reload, quit.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

// Run builds the model and starts the program (alt-screen). It is what
// `furrow ui` calls.
func Run(a *app.App) error {
	m, err := newModel(a)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type model struct {
	app  *app.App
	list list.Model
	vp   viewport.Model
	help help.Model
	keys keyMap

	// renderer is cached and rebuilt only when the detail pane width changes —
	// constructing a glamour renderer on every cursor move was the main source
	// of navigation lag (it re-detects the color profile and compiles styles).
	renderer      *glamour.TermRenderer
	rendererWidth int
	// bodies caches loaded body markdown by id so moving the cursor doesn't
	// re-read the file from disk on every keystroke. Invalidated on edit/reload.
	bodies map[string]string

	width, height int
	ready         bool
	focusDetail   bool
	status        string
	shownID       string // id whose body is currently rendered in vp
	checkIdx      int    // focused checklist item in the detail pane
}

// editedMsg is returned after the $EDITOR subprocess exits.
type editedMsg struct {
	id  string
	err error
}

func newModel(a *app.App) (model, error) {
	delegate := list.NewDefaultDelegate()
	l := list.New(nil, delegate, 0, 0)
	l.Title = "furrow"
	l.SetShowHelp(false) // we render our own help footer across both panes
	l.SetStatusBarItemName("task", "tasks")

	m := model{
		app:    a,
		list:   l,
		vp:     viewport.New(0, 0),
		help:   help.New(),
		keys:   defaultKeys(),
		bodies: map[string]string{},
	}
	if _, err := m.reload(); err != nil {
		return m, err
	}
	return m, nil
}

func (m model) Init() tea.Cmd { return nil }

// reload pulls the current tasks from the store into the list, preserving the
// selection by TASK ID (not raw index, which is a filtered-view index when a
// filter is applied and would point at the wrong row after a re-sort). It
// returns the tea.Cmd from SetItems — which re-runs the fuzzy filter when one
// is applied — and that cmd MUST be scheduled by the caller, or the list blanks.
func (m *model) reload() (tea.Cmd, error) {
	curID := ""
	if t, ok := m.selected(); ok {
		curID = t.ID
	}
	tasks, err := m.app.List(app.QueryOpts{})
	if err != nil {
		return nil, err
	}
	items := make([]list.Item, len(tasks))
	sel := -1
	for i, t := range tasks {
		items[i] = taskItem{t: t}
		if t.ID == curID {
			sel = i
		}
	}
	cmd := m.list.SetItems(items)
	switch {
	case sel >= 0:
		m.list.Select(sel)
	case len(items) > 0:
		m.list.Select(0)
	}
	m.shownID = "" // force detail re-render
	return cmd, nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.ready = true
		m.shownID = "" // width changed — force a re-render (and renderer rebuild)
		m.refreshDetail()
		return m, nil

	case editedMsg:
		if msg.err != nil {
			m.status = "editor: " + msg.err.Error()
		} else {
			m.status = "edited " + msg.id
			delete(m.bodies, msg.id) // body changed on disk — drop the cached copy
		}
		cmd, _ := m.reload()
		m.refreshDetail()
		return m, cmd

	case tea.KeyMsg:
		// While the list's fuzzy filter is open, every key belongs to it
		// (typing the query, esc/enter to apply) — don't intercept.
		if m.list.FilterState() == list.Filtering {
			break
		}
		// Checklist interaction in the focused detail pane: up/down move a
		// cursor over the items, space toggles. At the ends, up/down fall
		// through to the viewport so a long body still scrolls.
		if m.focusDetail {
			if t, ok := m.selected(); ok && len(t.Checklist) > 0 {
				switch {
				case key.Matches(msg, m.keys.Toggle):
					return m, m.toggleCheck(t)
				case key.Matches(msg, m.keys.Up) && m.checkIdx > 0:
					m.checkIdx--
					m.renderDetail()
					return m, nil
				case key.Matches(msg, m.keys.Down) && m.checkIdx < len(t.Checklist)-1:
					m.checkIdx++
					m.renderDetail()
					return m, nil
				}
			}
		}
		// Reorder within a lane (list focus only): K/J swap the selected task's
		// priority with its same-lane neighbor above/below in canonical order.
		// In the detail pane these fall through (the viewport ignores them).
		if !m.focusDetail {
			switch {
			case key.Matches(msg, m.keys.MoveUp):
				return m, m.reorderSelected(-1)
			case key.Matches(msg, m.keys.MoveDown):
				return m, m.reorderSelected(+1)
			}
		}
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.layout()
			return m, nil
		case key.Matches(msg, m.keys.Detail):
			m.focusDetail = !m.focusDetail
			return m, nil
		case key.Matches(msg, m.keys.Reload):
			m.bodies = map[string]string{} // explicit reload — drop all cached bodies
			cmd, _ := m.reload()
			m.refreshDetail()
			m.status = "reloaded"
			return m, cmd
		case key.Matches(msg, m.keys.Done):
			return m, m.mutateSelected(func(id string) (string, error) {
				t, err := m.app.Done(id)
				if err != nil {
					return "", err
				}
				return "done " + t.ID, nil
			})
		case key.Matches(msg, m.keys.LaneFw):
			return m, m.moveSelected(+1)
		case key.Matches(msg, m.keys.LaneBw):
			return m, m.moveSelected(-1)
		case key.Matches(msg, m.keys.Edit):
			return m, m.editSelected()
		}
	}

	// Route remaining messages to the focused pane.
	var cmd tea.Cmd
	if m.focusDetail {
		m.vp, cmd = m.vp.Update(msg)
	} else {
		m.list, cmd = m.list.Update(msg)
		m.refreshDetail() // selection may have changed
	}
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// selected returns the currently highlighted task, if any.
func (m model) selected() (core.Task, bool) {
	if it, ok := m.list.SelectedItem().(taskItem); ok {
		return it.t, true
	}
	return core.Task{}, false
}

// mutateSelected runs fn against the selected task id, then reloads. fn returns
// a status string to show.
func (m *model) mutateSelected(fn func(id string) (string, error)) tea.Cmd {
	t, ok := m.selected()
	if !ok {
		return nil
	}
	status, err := fn(t.ID)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	m.status = status
	cmd, _ := m.reload()
	m.refreshDetail()
	return cmd
}

// toggleCheck flips the checklist item under the detail-pane cursor, preserving
// the cursor position across the reload so repeated toggles stay put. t is the
// pre-reload task; after reload the same id is reselected with its new state.
func (m *model) toggleCheck(t core.Task) tea.Cmd {
	idx := m.checkIdx
	if idx < 0 || idx >= len(t.Checklist) {
		return nil
	}
	cur := t.Checklist[idx]
	if _, err := m.app.Check(t.ID, idx, !cur.Done); err != nil {
		m.status = err.Error()
		return nil
	}
	mark := "✓"
	if cur.Done {
		mark = "○"
	}
	m.status = fmt.Sprintf("checklist #%d %s", idx+1, mark)
	cmd, _ := m.reload() // reload sets shownID="" and reselects the same id
	m.renderDetail()     // repaint with the same checkIdx (no reset)
	m.shownID = t.ID     // keep the refreshDetail guard consistent
	return cmd
}

// moveSelected cycles the selected task through the configured lanes.
func (m *model) moveSelected(dir int) tea.Cmd {
	return m.mutateSelected(func(id string) (string, error) {
		t, ok := m.selected()
		if !ok {
			return "", core.NotFound(id)
		}
		lane := m.cycleLane(t.Status, dir)
		nt, err := m.app.Move(id, lane)
		if err != nil {
			return "", err
		}
		return "moved " + nt.ID + " → " + nt.Status, nil
	})
}

func (m model) cycleLane(cur string, dir int) string {
	lanes := m.app.Cfg.Lanes
	if len(lanes) == 0 {
		return cur
	}
	idx := 0
	for i, l := range lanes {
		if l == cur {
			idx = i
			break
		}
	}
	return lanes[((idx+dir)%len(lanes)+len(lanes))%len(lanes)]
}

// reorderSelected swaps the selected task's priority with its same-lane neighbor
// one row up (dir=-1) or down (dir=+1), then reloads so the list re-sorts and
// the cursor follows the moved task by id. Neighbors come from the canonical
// task list (not the possibly-filtered visible rows), so a swap always targets
// the true adjacent task. Swapping the two priority *values* keeps the lane's
// exact set of priorities, so the sparse spacing is never disturbed. It is a
// no-op at a lane boundary (the neighbor is in another lane, or there is none).
func (m *model) reorderSelected(dir int) tea.Cmd {
	cur, ok := m.selected()
	if !ok {
		return nil
	}
	tasks, err := m.app.List(app.QueryOpts{})
	if err != nil {
		m.status = err.Error()
		return nil
	}
	idx := -1
	for i, t := range tasks {
		if t.ID == cur.ID {
			idx = i
			break
		}
	}
	nb := idx + dir
	if idx < 0 || nb < 0 || nb >= len(tasks) || tasks[nb].Status != cur.Status {
		return nil // lane boundary — nothing to swap with
	}
	neighbor := tasks[nb]
	if _, err := m.app.Reorder(cur.ID, neighbor.Priority); err != nil {
		m.status = err.Error()
		return nil
	}
	if _, err := m.app.Reorder(neighbor.ID, cur.Priority); err != nil {
		m.status = err.Error()
		return nil
	}
	m.status = "reordered " + cur.ID
	cmd, _ := m.reload()
	m.refreshDetail()
	return cmd
}

// editSelected suspends the TUI and opens the body in $EDITOR.
func (m *model) editSelected() tea.Cmd {
	t, ok := m.selected()
	if !ok {
		return nil
	}
	path, err := m.app.EditPath(t.ID)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	editor := firstNonEmpty(os.Getenv("FURROW_EDITOR"), os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	parts := strings.Fields(editor)
	parts = append(parts, path)
	c := exec.Command(parts[0], parts[1:]...)
	return tea.ExecProcess(c, func(err error) tea.Msg { return editedMsg{id: t.ID, err: err} })
}

// refreshDetail renders the selected task's body into the viewport (only when
// the selection actually changed, to avoid re-running glamour every keypress).
func (m *model) refreshDetail() {
	t, ok := m.selected()
	if !ok {
		m.vp.SetContent(dimStyle.Render("(no task selected)"))
		m.shownID = ""
		return
	}
	if t.ID == m.shownID && m.ready {
		return
	}
	m.shownID = t.ID
	m.checkIdx = 0 // new task — reset the checklist cursor
	m.renderDetail()
	m.vp.GotoTop()
}

// renderDetail (re)builds and paints the selected task's detail into the
// viewport. Unlike refreshDetail it has no shownID guard, so it repaints the
// same task after a checklist cursor move or toggle. It does not reset scroll.
func (m *model) renderDetail() {
	t, ok := m.selected()
	if !ok {
		return
	}
	body, ok := m.bodies[t.ID]
	if !ok {
		b, err := m.app.Store.LoadBody(t.ID)
		if err != nil {
			body = "_could not load body: " + err.Error() + "_"
		} else {
			m.bodies[t.ID] = b // cache successful loads only, so errors retry
			body = b
		}
	}
	md := m.detailMarkdown(t, body)

	width := m.vp.Width
	if width <= 0 {
		width = 80
	}
	rendered := md
	if r := m.ensureRenderer(width); r != nil {
		if s, err := r.Render(md); err == nil {
			rendered = s
		}
	}
	m.vp.SetContent(rendered)
}

// ensureRenderer returns a glamour renderer for the given width, rebuilding it
// only when the width changes. Building one per cursor move was the dominant
// cost behind the navigation lag.
func (m *model) ensureRenderer(width int) *glamour.TermRenderer {
	if m.renderer != nil && m.rendererWidth == width {
		return m.renderer
	}
	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(width))
	if err != nil {
		return nil
	}
	m.renderer, m.rendererWidth = r, width
	return r
}

// detailMarkdown composes the metadata header + body for the detail pane.
func (m model) detailMarkdown(t core.Task, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** — %s  ·  priority %d\n\n", t.ID, t.Status, t.Priority)
	if len(t.Labels) > 0 {
		fmt.Fprintf(&b, "labels: %s\n\n", strings.Join(t.Labels, ", "))
	}
	if len(t.Repos) > 0 {
		fmt.Fprintf(&b, "repos: %s\n\n", strings.Join(t.Repos, ", "))
	}
	if len(t.Deps) > 0 {
		fmt.Fprintf(&b, "deps: %s\n\n", strings.Join(t.Deps, ", "))
	}
	for i, c := range t.Checklist {
		box := "[ ]"
		if c.Done {
			box = "[x]"
		}
		text := c.Text
		if m.focusDetail && i == m.checkIdx {
			text = "**" + text + "**  ◂" // cursor highlight while the detail pane is focused
		}
		fmt.Fprintf(&b, "- %s %s\n", box, text)
	}
	if len(t.Checklist) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	if strings.TrimSpace(body) == "" {
		b.WriteString("_(empty body — press e to edit)_\n")
	} else {
		b.WriteString(body)
	}
	return b.String()
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
