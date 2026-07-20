// Command tui is a throwaway proof-of-concept: a GitHub-Projects-shaped kanban
// TUI over furrow's task model, built on bubbletea/v2 + lipgloss/v2's native
// layer compositor.
//
// What it PROVES
//
//   - A GH-Projects visual grammar (columns with counts and value/effort sums,
//     WIP badges, chip-laden cards, horizontal lane scrolling) is achievable in
//     a terminal at 120-140 columns.
//   - GitHub's keyboard "move mode" (Enter to lift a card, arrows to place it,
//     Enter to commit, Esc to restore) is MORE natural in a TUI than in a
//     browser, and it maps exactly onto furrow's sparse-priority reorder.
//   - Mouse drag-and-drop across columns works under MouseModeCellMotion, with
//     a z-ordered ghost card and a drop indicator drawn as lipgloss Layers.
//   - Dependencies become legible without a DAG layout engine: resolved
//     bidirectional lists, a blocked glyph on the card, and jump-to-blocker
//     with a jump stack.
//
// What it FAKES
//
//   - The data is a hardcoded in-memory copy of 24 real tasks (fixture.go).
//     The Provider interface is the seam where a real `furrow --json` client
//     would drop in; the mock never shells out and never touches a real
//     .furrow store. Mutations live and die with the process.
//   - `e` (edit body) launches $EDITOR on a temp file via tea.ExecProcess and
//     writes the result back into the in-memory board only.
//
// # What it is NOT
//
// This is a POC on a throwaway branch. furrow itself stays CLI-only and
// charm-free: this is a separate Go module precisely so that no charm
// dependency can reach furrow's core. Any real TUI would be an out-of-repo
// front-end (ridge) speaking furrow's CLI/JSON contract.
package main
