// Package widget provides the small drawing primitives every view composes:
// text, rules, selection rows, boxes, badges and the composer frame.
//
// There is no component tree and no retained state. A widget is a function that
// paints onto a rect of a tcell screen. That keeps rendering a pure "state in,
// cells out" transform, which is what makes the interface testable and fast
// (design.md decision 7).
package widget

import (
	"strconv"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
)

// Rect is a screen region, in cells. X,Y is the top-left corner.
type Rect struct {
	X, Y, W, H int
}

// Empty reports whether the rect has no drawable area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Inset shrinks the rect by n cells on every side.
func (r Rect) Inset(n int) Rect {
	return Rect{r.X + n, r.Y + n, r.W - 2*n, r.H - 2*n}
}

// InsetXY shrinks by dx horizontally and dy vertically.
func (r Rect) InsetXY(dx, dy int) Rect {
	return Rect{r.X + dx, r.Y + dy, r.W - 2*dx, r.H - 2*dy}
}

// Row returns the n-th row of the rect as a rect of height one.
func (r Rect) Row(n int) Rect {
	return Rect{r.X, r.Y + n, r.W, 1}
}

// Take reserves h rows from the top, returning them and the remainder.
func (r Rect) Take(h int) (head, rest Rect) {
	if h > r.H {
		h = r.H
	}
	return Rect{r.X, r.Y, r.W, h}, Rect{r.X, r.Y + h, r.W, r.H - h}
}

// TakeBottom reserves h rows from the bottom.
func (r Rect) TakeBottom(h int) (head, foot Rect) {
	if h > r.H {
		h = r.H
	}
	return Rect{r.X, r.Y, r.W, r.H - h}, Rect{r.X, r.Y + r.H - h, r.W, h}
}

// Canvas is a thin drawing surface over a tcell.Screen, bound to a rect and a
// token set. Views draw through it so they never compute absolute coordinates by
// hand.
type Canvas struct {
	S    tcell.Screen
	T    theme.Tokens
	Rect Rect
}

// NewCanvas binds a canvas to a region.
func NewCanvas(s tcell.Screen, t theme.Tokens, r Rect) *Canvas {
	return &Canvas{S: s, T: t, Rect: r}
}

// Fill paints the whole rect with a style.
func (c *Canvas) Fill(st tcell.Style) {
	if c.Rect.Empty() {
		return
	}
	for y := c.Rect.Y; y < c.Rect.Y+c.Rect.H; y++ {
		for x := c.Rect.X; x < c.Rect.X+c.Rect.W; x++ {
			c.S.SetContent(x, y, ' ', nil, st)
		}
	}
}

// Text draws s at a position relative to the canvas, clipped to the canvas
// width. It returns the number of cells actually written, so callers can lay out
// siblings after it without measuring twice.
func (c *Canvas) Text(x, y int, s string, st tcell.Style) int {
	if y < 0 || y >= c.Rect.H || x >= c.Rect.W {
		return 0
	}
	avail := c.Rect.W - x
	if avail <= 0 {
		return 0
	}
	s = layout.Truncate(s, avail)
	return c.rawText(c.Rect.X+x, c.Rect.Y+y, s, st)
}

// TextPadded draws s and then pads the rest of the line with the same style,
// which is how a full-row highlight is painted.
func (c *Canvas) TextPadded(x, y int, s string, st tcell.Style) {
	c.TextFill(x, y, c.Rect.W-x, s, st)
}

// TextFill draws s and pads exactly w cells with the same style.
//
// The bounded width matters inside dialogs: a fill that ran to the canvas edge
// would paint over the border and break the frame.
func (c *Canvas) TextFill(x, y, w int, s string, st tcell.Style) {
	if y < 0 || y >= c.Rect.H || w <= 0 {
		return
	}
	avail := c.Rect.W - x
	if avail <= 0 {
		return
	}
	if w > avail {
		w = avail
	}
	written := c.rawText(c.Rect.X+x, c.Rect.Y+y, layout.Truncate(s, w), st)
	for i := written; i < w; i++ {
		c.S.SetContent(c.Rect.X+x+i, c.Rect.Y+y, ' ', nil, st)
	}
}

// rawText writes s without truncation, advancing by display width so wide
// characters occupy both of their cells.
func (c *Canvas) rawText(absX, absY int, s string, st tcell.Style) int {
	x := absX
	for _, r := range s {
		w := runeWidth(r)
		if w == 0 {
			// A zero-width rune (combining mark) attaches to the previous cell;
			// writing it separately would misalign everything after it.
			if x > absX {
				c.S.SetContent(x-1, absY, ' ', nil, st)
			}
			continue
		}
		if x >= c.Rect.X+c.Rect.W {
			break
		}
		c.S.SetContent(x, absY, r, nil, st)
		// A wide rune owns the following cell; tcell marks it as a continuation
		// when it renders. Writing a space over it here would split the
		// character in real terminals, so we only advance past it.
		x += w
	}
	return x - absX
}

// TextRight draws s right-aligned within the canvas.
func (c *Canvas) TextRight(y int, s string, st tcell.Style) {
	w := layout.StringWidth(s)
	if w > c.Rect.W {
		s = layout.Truncate(s, c.Rect.W)
		w = layout.StringWidth(s)
	}
	c.Text(c.Rect.W-w, y, s, st)
}

// Rule draws a horizontal divider.
//
// The divider stops short of the canvas edge and uses the soft border colour:
// a full-bleed rule in a bright colour is the single most common thing that
// makes a terminal UI look cheap (spec tui-design-system / 边框与分隔线).
func (c *Canvas) Rule(y int, st tcell.Style) {
	if y < 0 || y >= c.Rect.H {
		return
	}
	ch := '─'
	if !c.T.UnicodeBox {
		ch = '-'
	}
	for x := 0; x < c.Rect.W; x++ {
		c.S.SetContent(c.Rect.X+x, c.Rect.Y+y, ch, nil, st)
	}
}

// RuleWithin draws a rule over a sub-range of the row, used to separate regions
// without crossing the gutter.
func (c *Canvas) RuleWithin(y, x, w int, st tcell.Style) {
	if y < 0 || y >= c.Rect.H {
		return
	}
	ch := '─'
	if !c.T.UnicodeBox {
		ch = '-'
	}
	for i := 0; i < w; i++ {
		if x+i >= c.Rect.W {
			break
		}
		c.S.SetContent(c.Rect.X+x+i, c.Rect.Y+y, ch, nil, st)
	}
}

// VRule draws a vertical divider of the given height.
func (c *Canvas) VRule(x, y, h int, st tcell.Style) {
	ch := '│'
	if !c.T.UnicodeBox {
		ch = '|'
	}
	for i := 0; i < h; i++ {
		if x >= c.Rect.W || y+i >= c.Rect.H {
			break
		}
		c.S.SetContent(c.Rect.X+x, c.Rect.Y+y+i, ch, nil, st)
	}
}

// Box draws a frame around the canvas. Used only for dialogs and input focus —
// never around the main layout (spec tui-design-system / 边框与分隔线).
func (c *Canvas) Box(st tcell.Style) {
	if c.Rect.W < 2 || c.Rect.H < 2 {
		return
	}
	h, v := '─', '│'
	tl, tr, bl, br := '┌', '┐', '└', '┘'
	if !c.T.UnicodeBox {
		h, v = '-', '|'
		tl, tr, bl, br = '+', '+', '+', '+'
	}
	r := c.Rect
	c.put(r.X, r.Y, tl, st)
	c.put(r.X+r.W-1, r.Y, tr, st)
	c.put(r.X, r.Y+r.H-1, bl, st)
	c.put(r.X+r.W-1, r.Y+r.H-1, br, st)
	for x := r.X + 1; x < r.X+r.W-1; x++ {
		c.put(x, r.Y, h, st)
		c.put(x, r.Y+r.H-1, h, st)
	}
	for y := r.Y + 1; y < r.Y+r.H-1; y++ {
		c.put(r.X, y, v, st)
		c.put(r.X+r.W-1, y, v, st)
	}
}

// BoxTitle draws a box with a label embedded in the top edge, so a dialog reads
// as one object rather than a frame with text floating nearby.
func (c *Canvas) BoxTitle(title string, border, titleStyle tcell.Style) {
	c.Box(border)
	if title == "" {
		return
	}
	label := " " + title + " "
	if layout.StringWidth(label) > c.Rect.W-4 {
		label = layout.Truncate(label, c.Rect.W-4)
	}
	c.Text(2, 0, label, titleStyle)
}

func (c *Canvas) put(x, y int, r rune, st tcell.Style) {
	c.S.SetContent(x, y, r, nil, st)
}

// Label draws a section heading: accent text, no bold, one blank row below.
func (c *Canvas) Label(y int, s string) int {
	return c.Text(layout.GutterX, y, s, c.T.Heading())
}

// Badge draws a small count in brackets, the way the reference config screen
// shows state — legible without shouting.
func (c *Canvas) Badge(x, y int, n int, st tcell.Style) int {
	s := "(" + itoa(n) + ")"
	return c.Text(x, y, s, st)
}

// runeWidth measures one rune in cells; control characters take none.
//
// It delegates to layout so that everything in the interface measures with one
// rule, the same one the terminal renders with.
func runeWidth(r rune) int {
	if r < 0x20 || r == 0x7f {
		return 0
	}
	return layout.RuneWidth(r)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
