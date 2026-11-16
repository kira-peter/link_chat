package views

import (
	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/tui/widget"
)

// The page slide: when the sidebar's cursor moves to another row, the page on
// the right does not just repaint. The old page and the new one are laid out
// one above the other, like two pictures on a strip, and the strip moves until
// the new one fills the area: moving down the list, the new page comes up from
// below; moving up, it comes down from above (spec tui-chat-client / 切换页面的平移).

// RectShot is a copy of the cells in one rectangle of the screen.
type RectShot struct {
	R     widget.Rect
	cells []snapCell
}

// TakeRectShot copies the cells of r as they are on s now.
func TakeRectShot(s tcell.Screen, r widget.Rect) *RectShot {
	if r.Empty() {
		return nil
	}
	sh := &RectShot{R: r, cells: make([]snapCell, r.W*r.H)}
	for y := 0; y < r.H; y++ {
		for x := 0; x < r.W; x++ {
			ch, comb, st, _ := s.GetContent(r.X+x, r.Y+y)
			sh.cells[y*r.W+x] = snapCell{ch, comb, st}
		}
	}
	return sh
}

// row paints row sy of the shot onto screen row dy.
func (sh *RectShot) row(s tcell.Screen, sy, dy int) {
	for x := 0; x < sh.R.W; x++ {
		c := sh.cells[sy*sh.R.W+x]
		s.SetContent(sh.R.X+x, dy, c.r, c.comb, c.style)
	}
}

// PaintPageSlide draws the strip of old and new at shift rows, 0..r.H, over r.
// The new page must already be drawn in r: it is read back from the screen
// before anything is painted over it. down says which way the cursor went.
//
// At shift 0 the old page fills r; at r.H the new one does.
func PaintPageSlide(s tcell.Screen, old *RectShot, shift int, down bool) {
	if old == nil {
		return
	}
	r := old.R
	if shift <= 0 {
		shift = 0
	}
	if shift >= r.H {
		return // the new page, already on screen, is the whole picture
	}
	cur := TakeRectShot(s, r)
	for y := 0; y < r.H; y++ {
		if down {
			// The strip is old then new, moving up: row y shows strip row y+shift.
			if k := y + shift; k < r.H {
				old.row(s, k, r.Y+y)
			} else {
				cur.row(s, k-r.H, r.Y+y)
			}
		} else {
			// The strip is new then old, moving down: row y shows strip row
			// y + (H - shift).
			if k := y + r.H - shift; k < r.H {
				cur.row(s, k, r.Y+y)
			} else {
				old.row(s, k-r.H, r.Y+y)
			}
		}
	}
}
