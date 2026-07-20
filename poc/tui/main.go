package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

func main() {
	var (
		dump   = flag.Bool("dump", false, "render one frame to stdout at -w x -h and exit (no TTY needed)")
		w      = flag.Int("w", 140, "width for -dump")
		h      = flag.Int("h", 40, "height for -dump")
		filter = flag.String("filter", "", "initial filter query, e.g. 'lane:backlog is:blocked'")
		peek   = flag.Bool("peek", false, "-dump with the detail side-peek open")
		tree   = flag.Bool("tree", false, "-dump with the dep tree overlay open (implies -peek)")
		table  = flag.Bool("table", false, "-dump the table view")
		light  = flag.Bool("light", false, "light palette")
		plain  = flag.Bool("plain", false, "-dump without ANSI styling (diffable)")
		demo   = flag.String("demo", "", "-dump in a transient state: move|drag|help")
	)
	flag.Parse()

	m := New(newMockProvider())
	if *light {
		m.th = newTheme(false)
	}
	if *filter != "" {
		m.ti.SetValue(*filter)
		m.applyFilter(*filter)
	}
	if *table {
		m.view = viewTable
	}
	if *peek || *tree {
		m.peekOpen = true
		m.treeOpen = *tree
	}

	if *dump {
		m.w, m.h = *w, *h
		m.help.SetWidth(*w)
		m.recompute()
		m.relayout()
		if err := m.demoState(*demo); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		out := m.View().Content
		if *plain {
			out = ansiStrip(out)
		}
		fmt.Println(out)
		return
	}

	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// demoState puts the model into a transient state that a single -dump frame
// could not otherwise reach, because it only exists mid-gesture. Without this,
// "does the drop indicator render?" is a question only a human at a terminal
// can answer — and the house rule is that everything is provable headless.
func (m *Model) demoState(kind string) error {
	switch kind {
	case "":
		return nil

	case "help":
		m.fullHelp = true

	case "move":
		m.curLane = m.b.LaneIndex("backlog")
		m.setPos(1)
		m.enterMove()
		m.dropLane, m.dropIdx = "ready", 1
		m.followDrop()

	case "drag":
		src := m.lay.Col("backlog")
		dst := m.lay.Col("ready")
		if src == nil || dst == nil || len(src.Cards) < 2 {
			return fmt.Errorf("demo drag: the board is too small at this size")
		}
		grab := src.Cards[1]
		m.Update(tea.MouseClickMsg{X: grab.X + 3, Y: grab.Y + 1, Button: tea.MouseLeft})
		m.Update(tea.MouseMotionMsg{X: dst.X + 8, Y: dst.Top + 4, Button: tea.MouseLeft})

	default:
		return fmt.Errorf("unknown -demo %q (want move|drag|help)", kind)
	}
	m.relayout()
	return nil
}
