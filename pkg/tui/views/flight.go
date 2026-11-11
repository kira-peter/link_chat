package views

import (
	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// The flying name.
//
// When a conversation opens, its name leaves its row in the sidebar and travels to
// the conversation's title; when the list unfolds it travels back. Where it is at
// any moment is a straight-line interpolation between those two places driven by
// the fold — the same number that narrows the sidebar — so the name and the
// columns move as one thing (design.md decision 1).

// NameFlight describes one frame of the flying name.
type NameFlight struct {
	// Name is the text being carried.
	Name string
	// Fold is how far the sidebar has folded, 0..1.
	Fold float64
	// FromX, FromY is where the name sits in the open sidebar.
	FromX, FromY int
	// ToX, ToY is where the title sits in the conversation.
	ToX, ToY int
}

// Position is where the name is at this fold, in whole cells.
//
// The interpolated offset is rounded to the nearest cell in either direction. A
// plain int(v + 0.5) rounds correctly only for positive v: Go truncates towards
// zero, so for a name travelling left or up (a negative offset) it lands one cell
// short, and at fold 1 the name would stop one cell away from the title it is
// supposed to land on.
func (f NameFlight) Position() (x, y int) {
	t := clamp01(f.Fold)
	x = f.FromX + roundCells(float64(f.ToX-f.FromX)*t)
	y = f.FromY + roundCells(float64(f.ToY-f.FromY)*t)
	return x, y
}

// roundCells rounds half away from zero.
func roundCells(v float64) int {
	if v < 0 {
		return -int(-v + 0.5)
	}
	return int(v + 0.5)
}

// InFlight reports whether the name is between its two homes. At either end the
// name is drawn by its owner (the sidebar row, or the transcript title) and the
// flight layer draws nothing, so the two never show the same name at once.
func (f NameFlight) InFlight() bool {
	t := clamp01(f.Fold)
	return t > 0 && t < 1
}

// TitleAnchor is where the conversation's title starts: the left edge of the
// transcript at the given fold, on the content's first row.
func TitleAnchor(content widget.Rect, fold float64) (x, y int) {
	main := MainRectAt(content, fold)
	return main.X, main.Y
}

// SidebarRowAnchor is where a sidebar row's text starts, for a row drawn at line
// within the open sidebar's body. The name begins two cells in, past the marker
// column.
func SidebarRowAnchor(content widget.Rect, line int) (x, y int) {
	side := SidebarRectAt(content, 0)
	return side.X + 2, side.Y + line
}

// FlightLayer draws the name while it travels.
//
// It is a layer of its own, drawn last, for the same reason the focus marker is:
// the name belongs to neither the sidebar nor the transcript while it moves, and
// keeping it out of both means they stay pure functions of their own state.
type FlightLayer struct {
	T theme.Tokens
}

// Render draws the name at its current position. Outside the flight it draws
// nothing. The name is clipped to the screen so a long one near the right edge does
// not run off it.
func (l FlightLayer) Render(s tcell.Screen, bounds widget.Rect, f NameFlight, focus client.Focus, lang i18n.Lang) {
	if !f.InFlight() || f.Name == "" || bounds.Empty() {
		return
	}
	x, y := f.Position()
	if y < bounds.Y || y >= bounds.Y+bounds.H {
		return
	}
	avail := bounds.X + bounds.W - x
	if avail <= 0 {
		return
	}
	// The same colour the title takes when the conversation has the keyboard: the
	// name is arriving at (or leaving) that title, so it wears its colour.
	style := l.T.Heading()
	if focus == client.FocusList {
		style = l.T.Selected()
	}
	text := layout.Truncate(f.Name, avail)
	span := layout.StringWidth(text)

	// Clear the cells the name will occupy, widened so no wide glyph is left half
	// drawn. The name passes over whatever the sidebar or the transcript drew; a CJK
	// glyph owns two cells, and overwriting only one of them leaves the other as a
	// stray half-character. So the run is extended left if it would start on the
	// second cell of a wide glyph, and right if it would end on the first cell of
	// one. What counts as "wide" is what the screen itself reports for each cell,
	// not a width table, because a table can disagree with how the terminal drew it.
	left, right := x, x+span // [left, right)
	left = widenLeft(s, bounds, left, y)
	right = widenRight(s, bounds, right, y)
	if right > bounds.X+bounds.W {
		right = bounds.X + bounds.W
	}
	if left < bounds.X {
		left = bounds.X
	}
	if right > left {
		cc := widget.NewCanvas(s, l.T, widget.Rect{X: left, Y: y, W: right - left, H: 1})
		cc.Fill(l.T.Base())
	}

	c := widget.NewCanvas(s, l.T, widget.Rect{X: x, Y: y, W: avail, H: 1})
	c.Text(0, 0, text, style)
}

// widenLeft moves a run's left edge back to the start of a wide glyph it would cut
// through. The screen reports a wide glyph at its first cell with width 2; the
// cell after it is a continuation. So if the cell just left of the edge is a width-2
// glyph, the edge is inside it and has to move back one.
func widenLeft(s tcell.Screen, bounds widget.Rect, edge, y int) int {
	if edge-1 < bounds.X {
		return edge
	}
	if _, _, _, w := s.GetContent(edge-1, y); w == 2 {
		return edge - 1
	}
	if _, _, _, w := s.GetContent(edge-2, y); edge-2 >= bounds.X && w == 2 {
		// The cell at edge-1 is the glyph's continuation, so the glyph starts at
		// edge-2 and this edge cuts it in half.
		return edge - 2
	}
	return edge
}

// widenRight moves a run's right edge out past a wide glyph it would cut through:
// if the last cell inside the run is the first cell of a width-2 glyph, its
// continuation lies just outside and must be cleared with it.
func widenRight(s tcell.Screen, bounds widget.Rect, edge, y int) int {
	if edge-1 < bounds.X || edge-1 >= bounds.X+bounds.W {
		return edge
	}
	if _, _, _, w := s.GetContent(edge-1, y); w == 2 {
		return edge + 1
	}
	return edge
}
