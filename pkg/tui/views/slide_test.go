package views

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/tui/widget"
)

// fillRows writes a letter on every row of r: base, base+1, ...
func fillRows(s tcell.Screen, r widget.Rect, base rune) {
	for y := 0; y < r.H; y++ {
		for x := 0; x < r.W; x++ {
			s.SetContent(r.X+x, r.Y+y, base+rune(y), nil, tcell.StyleDefault)
		}
	}
}

func rowRune(s tcell.SimulationScreen, r widget.Rect, y int) rune {
	c, _, _, _ := s.GetContent(r.X, r.Y+y)
	return c
}

// The two pages are one strip: moving down the new page comes up from below,
// moving up it comes down from above, and every row of the area is one or the
// other — nothing is left blank or doubled.
func TestPageSlideIsAStripOfTheTwoPages(t *testing.T) {
	r := widget.Rect{X: 2, Y: 1, W: 4, H: 6}
	for _, c := range []struct {
		down  bool
		shift int
		want  string // what each row shows, top to bottom
	}{
		{true, 0, "abcdef"},
		{true, 2, "cdefAB"},
		{true, 6, "ABCDEF"},
		{false, 2, "EFabcd"},
		{false, 5, "BCDEFa"},
		{false, 6, "ABCDEF"},
	} {
		s := newScreen(t, 10, 10)
		fillRows(s, r, 'a')
		old := TakeRectShot(s, r)
		fillRows(s, r, 'A') // the new page, drawn as usual
		PaintPageSlide(s, old, c.shift, c.down)
		s.Show()
		got := ""
		for y := 0; y < r.H; y++ {
			got += string(rowRune(s, r, y))
		}
		if got != c.want {
			t.Errorf("down %v shift %d: rows %q, want %q", c.down, c.shift, got, c.want)
		}
	}
}

// Nothing outside the area is touched.
func TestPageSlideStaysInItsArea(t *testing.T) {
	r := widget.Rect{X: 2, Y: 1, W: 4, H: 6}
	s := newScreen(t, 10, 10)
	fillRows(s, widget.Rect{X: 0, Y: 0, W: 10, H: 10}, '0')
	fillRows(s, r, 'a')
	old := TakeRectShot(s, r)
	fillRows(s, r, 'A')
	PaintPageSlide(s, old, 3, true)
	s.Show()
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
				continue
			}
			if c, _, _, _ := s.GetContent(x, y); c != '0'+rune(y) {
				t.Fatalf("cell (%d,%d) outside the area changed to %q", x, y, c)
			}
		}
	}
}
