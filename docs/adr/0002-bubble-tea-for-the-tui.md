# Bubble Tea for the terminal UI

The terminal UI is built on Bubble Tea v2 (with bubbles and lipgloss) rather than tview, even though tview ships the tree, cell-addressable grid, double-click, and modal widgets the UI needs.
We chose the Elm-style model because every state transition is a pure `Update` that can be unit- and golden-tested, styling is not boxed in, and v2's kitty keyboard protocol support makes chords like Ctrl+Enter reachable in terminals that implement it.

## Consequences

The Catalog tree, the cell-addressable result grid with horizontal scrolling, mouse hit-testing, and double-click detection are our own components, not library widgets.
