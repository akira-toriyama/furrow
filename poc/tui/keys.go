package main

import "charm.land/bubbles/v2/key"

// keyMap is the whole keyboard surface. Every mouse gesture in this POC has an
// entry here — that is a hard rule, not a nicety: a terminal user may be on a
// mouse-less tmux, and drag-and-drop is the bonus, not the contract.
//
// Note WithKeys("space"), not WithKeys(" "): in bubbletea v2 key.Matches
// compares Key.String(), which renders the space bar as "space". " " compiles
// fine and silently never matches.
type keyMap struct {
	Up, Down, Left, Right key.Binding
	NextCol, PrevCol      key.Binding
	Top, Bottom           key.Binding

	Move       key.Binding
	Commit     key.Binding
	Cancel     key.Binding
	MoveTop    key.Binding
	MoveBottom key.Binding
	MoveFirst  key.Binding
	MoveLast   key.Binding
	QuickUp    key.Binding
	QuickDown  key.Binding
	LaneFwd    key.Binding
	LaneBack   key.Binding
	Peek       key.Binding
	Filter     key.Binding
	JumpBlock  key.Binding
	JumpBack   key.Binding
	Done       key.Binding
	Edit       key.Binding
	Reload     key.Binding
	Tree       key.Binding
	View       key.Binding
	Mouse      key.Binding
	Check      key.Binding
	OnlyBlock  key.Binding
	Help       key.Binding
	Quit       key.Binding
	PeekScroll key.Binding

	Graph       key.Binding
	GraphRoot   key.Binding
	GraphRadius key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Left:    key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "left")),
		Right:   key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "right")),
		NextCol: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next column")),
		PrevCol: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("⇧tab", "prev column")),
		Top:     key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:  key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),

		// shift+space USED to be a third alias for move mode (GitHub Projects
		// documents it as one). It now opens the dependency graph, which is the
		// gesture the user asked for by name. Move mode keeps ⏎ and m — the two
		// bindings a lift is actually reached by — so nothing became
		// unreachable; one alias changed owner.
		Move:   key.NewBinding(key.WithKeys("enter", "m"), key.WithHelp("⏎/m", "move mode")),
		Commit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "commit")),
		Cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		// ctrl+arrow = move to the extremes, also straight from GitHub's table.
		MoveTop:    key.NewBinding(key.WithKeys("ctrl+up"), key.WithHelp("^↑", "to top")),
		MoveBottom: key.NewBinding(key.WithKeys("ctrl+down"), key.WithHelp("^↓", "to bottom")),
		MoveFirst:  key.NewBinding(key.WithKeys("ctrl+left"), key.WithHelp("^←", "first lane")),
		MoveLast:   key.NewBinding(key.WithKeys("ctrl+right"), key.WithHelp("^→", "last lane")),
		QuickUp:    key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "raise")),
		QuickDown:  key.NewBinding(key.WithKeys("J"), key.WithHelp("J", "lower")),
		LaneBack:   key.NewBinding(key.WithKeys("["), key.WithHelp("[", "lane ←")),
		LaneFwd:    key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "lane →")),

		Peek:      key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "detail")),
		Filter:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		JumpBlock: key.NewBinding(key.WithKeys(">"), key.WithHelp(">", "jump to blocker")),
		JumpBack:  key.NewBinding(key.WithKeys("<"), key.WithHelp("<", "jump back")),
		OnlyBlock: key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "only blocked")),
		Tree:      key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "dep tree")),
		View:      key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "board/table")),

		Done:       key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "done")),
		Edit:       key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "$EDITOR")),
		Check:      key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "toggle check")),
		Reload:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reload")),
		Mouse:      key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "mouse on/off")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		PeekScroll: key.NewBinding(key.WithKeys("ctrl+d", "ctrl+u"), key.WithHelp("^d/^u", "scroll peek")),

		// Note WithKeys("shift+space"), not " " and not "shift+ ": key.Matches
		// compares Key.String(), which renders the space bar as "space".
		Graph:       key.NewBinding(key.WithKeys("shift+space", "S"), key.WithHelp("⇧space", "dep graph")),
		GraphRoot:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "re-root here")),
		GraphRadius: key.NewBinding(key.WithKeys("z", "1", "2", "3", "0"), key.WithHelp("z/1-3/0", "hop radius")),
	}
}

// ShortHelp is the one-line footer.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Left, k.Down, k.Up, k.Right, k.Move, k.Peek, k.Graph, k.Filter, k.JumpBlock, k.Help, k.Quit}
}

// graphHelp is the footer inside the graph view — a different mode gets a
// different keymap, so the footer never lies about what the arrows do.
func (k keyMap) graphHelp() []key.Binding {
	return []key.Binding{k.Left, k.Down, k.Up, k.Right, k.GraphRoot, k.JumpBack,
		k.GraphRadius, k.Peek, k.Cancel, k.Quit}
}

// FullHelp is the `?` overlay.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Left, k.Right, k.NextCol, k.PrevCol, k.Top, k.Bottom},
		{k.Move, k.Commit, k.Cancel, k.QuickUp, k.QuickDown, k.LaneBack, k.LaneFwd},
		{k.MoveTop, k.MoveBottom, k.MoveFirst, k.MoveLast},
		{k.Peek, k.Tree, k.PeekScroll, k.Filter, k.OnlyBlock, k.View},
		{k.Graph, k.GraphRoot, k.GraphRadius},
		{k.JumpBlock, k.JumpBack, k.Done, k.Check, k.Edit, k.Reload},
		{k.Mouse, k.Help, k.Quit},
	}
}

// moveHelp is the footer while a card is lifted — a different mode gets a
// different keymap, so the footer never lies about what the arrows will do.
func (k keyMap) moveHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Left, k.Right, k.MoveTop, k.MoveBottom, k.Commit, k.Cancel}
}
