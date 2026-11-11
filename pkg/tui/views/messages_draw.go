package views

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// Drawing items.
//
// The transcript scrolls by whole rows, and a bubble is several rows tall, so the
// top or bottom of the window can cut a bubble in half. Drawing a bubble as one
// block would then need clipping logic of its own. Instead every item is drawn one
// row at a time: asked for "row 2 of this bubble", it paints just that row. The
// window then draws exactly the rows that fall inside it and a half-visible bubble
// needs no special case.

// borderSet is the glyphs a bubble border is drawn with.
type borderSet struct {
	tl, tr, bl, br rune
	h, v           rune
}

// borderGlyphs picks the border characters for a bubble.
//
// Unicode terminals get box-drawing lines; others get ASCII. When colour cannot
// tell the two sides apart (mono), the other side's border is dashed, so the sides
// stay distinguishable by line style as well as by position
// (spec tui-chat-client / 单色终端仍可区分双方).
func borderGlyphs(t theme.Tokens, own bool) borderSet {
	if !t.UnicodeBox {
		return borderSet{'+', '+', '+', '+', '-', '|'}
	}
	if t.BubbleDashed() && !own {
		return borderSet{'┌', '┐', '└', '┘', '┄', '┆'}
	}
	return borderSet{'┌', '┐', '└', '┘', '─', '│'}
}

// itemRow paints one row of an item onto the canvas, whose width is the whole
// conversation area. row counts from the item's top.
func (v Transcript) itemRow(c *widget.Canvas, y int, it Item, row int) {
	switch it.Kind {
	case ItemBlank:
		return
	case ItemName:
		c.Text(it.X, y, it.Lines[0], v.T.Muted())
	case ItemTime, ItemSystem, ItemDeleted:
		c.Text(it.X, y, it.Lines[0], v.T.Faint())
	case ItemBubble:
		v.bubbleRow(c, y, it, row)
	}
}

// bubbleRow paints one row of a bubble: its top border, one line of text, or its
// bottom border with the footer on it.
func (v Transcript) bubbleRow(c *widget.Canvas, y int, it Item, row int) {
	style := v.T.BubbleOtherStyle()
	if it.Own {
		style = v.T.BubbleSelfStyle()
	}

	// Too narrow to afford a border: the text stands alone, still coloured by side
	// so the two speakers stay apart.
	if !it.Boxed {
		if row >= 0 && row < len(it.Lines) {
			c.Text(it.X, y, it.Lines[row], style)
		}
		return
	}

	g := borderGlyphs(v.T, it.Own)
	last := len(it.Lines) + 1
	inner := it.W - 2

	switch row {
	case 0:
		c.Text(it.X, y, string(g.tl)+strings.Repeat(string(g.h), inner)+string(g.tr), style)
	case last:
		c.Text(it.X, y, string(g.bl)+strings.Repeat(string(g.h), inner)+string(g.br), style)
		if it.Footer != "" {
			// The footer is set into the border a cell in from the corner, on the
			// side away from the screen edge, so it never touches a corner.
			foot := " " + it.Footer + " "
			fw := layout.StringWidth(foot)
			if fw <= inner {
				fx := it.X + it.W - 1 - fw
				if it.Side == SideLeft {
					fx = it.X + 1
				}
				c.Text(fx, y, foot, v.T.Faint())
			}
		}
	default:
		text := it.Lines[row-1]
		c.Text(it.X, y, string(g.v), style)
		c.Text(it.X+1, y, strings.Repeat(" ", inner), v.T.Body())
		c.Text(it.X+1+bubblePad, y, text, v.T.Body())
		c.Text(it.X+it.W-1, y, string(g.v), style)
	}
}

// drawWindow paints the rows [start, start+height) of the items onto body.
func (v Transcript) drawWindow(s tcell.Screen, body widget.Rect, items []Item, start int) {
	bc := widget.NewCanvas(s, v.T, body)
	bc.Fill(v.T.Base())

	top := 0 // the row index of the current item's first row within all rows
	for _, it := range items {
		h := it.Height()
		// Skip items wholly above the window.
		if top+h <= start {
			top += h
			continue
		}
		// Stop once past the window.
		if top >= start+body.H {
			break
		}
		for row := 0; row < h; row++ {
			abs := top + row
			if abs < start || abs >= start+body.H {
				continue
			}
			v.itemRow(bc, abs-start, it, row)
		}
		top += h
	}
}
