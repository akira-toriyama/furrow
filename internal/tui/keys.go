package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap is furrow's TUI keybinding set, wired to bubbles/help so the footer
// stays in sync with what actually works.
type keyMap struct {
	Up       key.Binding
	Down     key.Binding
	Filter   key.Binding
	Detail   key.Binding
	Toggle   key.Binding
	Done     key.Binding
	LaneFw   key.Binding
	LaneBw   key.Binding
	MoveUp   key.Binding
	MoveDown key.Binding
	Edit     key.Binding
	Reload   key.Binding
	Help     key.Binding
	Quit     key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Detail:   key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "focus body")),
		Toggle:   key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle ✓ (in body)")),
		Done:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "done")),
		LaneFw:   key.NewBinding(key.WithKeys("]", "L"), key.WithHelp("]", "lane →")),
		LaneBw:   key.NewBinding(key.WithKeys("[", "H"), key.WithHelp("[", "lane ←")),
		MoveUp:   key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "move ↑")),
		MoveDown: key.NewBinding(key.WithKeys("J"), key.WithHelp("J", "move ↓")),
		Edit:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit body")),
		Reload:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reload")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp / FullHelp satisfy help.KeyMap.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Down, k.Up, k.Done, k.LaneFw, k.Edit, k.Filter, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Filter, k.Detail},
		{k.Done, k.LaneFw, k.LaneBw, k.MoveUp, k.MoveDown},
		{k.Edit, k.Toggle, k.Reload, k.Help, k.Quit},
	}
}
