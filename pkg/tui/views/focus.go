package views

import (
	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// FocusMarkerRune is the glyph that says "the keyboard is here". A vertical bar is
// used because it is present or absent regardless of colour: a colour-blind user,
// a monochrome terminal and a greyscale screenshot all still show it, so the
// signal never rests on colour alone (spec tui-chat-client / 标记不依赖颜色).
const FocusMarkerRune = '▌'

// FocusMarkerASCII is the fallback when box-drawing glyphs are unavailable.
const FocusMarkerASCII = '|'

// FocusModel is what the focus overlay needs.
type FocusModel struct {
	// Focus is the region that owns the keyboard.
	Focus client.Focus
	// MarkerX is the marker's current horizontal position in cells. During a
	// transition it sits between the two regions; at rest it equals the focused
	// region's left edge.
	MarkerX float64
	// Active is true while the marker is part-way through a slide.
	Active bool
}

// FocusOverlay draws the cues that show which region has the keyboard, on top of
// an already-rendered frame.
//
// It is a separate layer, rather than something each view draws for itself, for
// two reasons. The marker moves between regions during a transition, so it does
// not belong to either of them. And keeping it out of the views means they need
// no animation parameters — they stay pure functions of their state.
type FocusOverlay struct {
	T theme.Tokens
}

// Render draws the marker, dims the unfocused title and colours the divider.
func (o FocusOverlay) Render(s tcell.Screen, content widget.Rect, m FocusModel, lang i18n.Lang) {
	if content.Empty() {
		return
	}
	side := SidebarRect(content)
	main := MainRect(content)

	// Title colour is chosen by each view from its own Focus field, not patched
	// afterwards: reading cells back and rewriting them truncates wide glyphs,
	// because the second cell of a CJK character reads back as a plain space.
	//
	// The divider is deliberately left alone. It is one cell wide, so it has no
	// left or right side to colour; "following" the focus would only change its
	// colour without telling anyone anything (design.md decision 8).
	o.drawMarker(s, content, side, main, m)
}

// drawMarker draws the bar at the marker's current position on the title row.
func (o FocusOverlay) drawMarker(s tcell.Screen, content, side, main widget.Rect, m FocusModel) {
	// The marker sits one cell left of the title text, in the gutter, so drawing
	// it never overwrites a character of the title.
	x := int(m.MarkerX+0.5) - 1
	y := content.Y
	if x < 0 {
		x = 0
	}
	if y < 0 || y >= content.Y+content.H {
		return
	}

	r := FocusMarkerRune
	if !o.T.UnicodeBox {
		r = FocusMarkerASCII
	}
	style := o.T.Base().Foreground(o.T.Accent)
	s.SetContent(x, y, r, nil, style)
}

// RenderStatic draws the marker for a single-pane layout, where one region fills
// the screen and so there is no second region to travel to.
func (o FocusOverlay) RenderStatic(s tcell.Screen, content widget.Rect) {
	if content.Empty() {
		return
	}
	o.drawMarkerAt(s, content, content.X+layout.GutterX)
}

// drawMarkerAt draws the bar one cell to the left of x on the title row.
func (o FocusOverlay) drawMarkerAt(s tcell.Screen, content widget.Rect, x int) {
	x--
	if x < 0 {
		x = 0
	}
	r := FocusMarkerRune
	if !o.T.UnicodeBox {
		r = FocusMarkerASCII
	}
	s.SetContent(x, content.Y, r, nil, o.T.Base().Foreground(o.T.Accent))
}
