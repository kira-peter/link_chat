package views

import (
	"testing"

	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/widget"
)

func foldContent(w int) widget.Rect { return widget.Rect{X: 0, Y: 2, W: w, H: 20} }

// At fold 0 the layout is exactly what it was before folding existed, so nothing
// that predates it moves by a cell.
func TestFoldZeroMatchesTheUnfoldedLayout(t *testing.T) {
	for _, w := range []int{60, 84, 100, 140, 200} {
		c := foldContent(w)
		side := SidebarRectAt(c, 0)
		if want := layout.SidebarWidth(w); side.W != want {
			t.Errorf("width %d: sidebar %d, want %d", w, side.W, want)
		}
		if side.X != c.X+layout.GutterX {
			t.Errorf("width %d: sidebar starts at %d", w, side.X)
		}
		main := MainRectAt(c, 0)
		if want := c.X + layout.GutterX + side.W + layout.ColumnGap; main.X != want {
			t.Errorf("width %d: main starts at %d, want %d", w, main.X, want)
		}
		if main.X+main.W != c.X+c.W-layout.GutterX {
			t.Errorf("width %d: main ends at %d, want the right gutter", w, main.X+main.W)
		}
	}
}

// Fully folded: no sidebar at all, and the conversation owns the whole row.
func TestFoldOneGivesTheConversationTheWholeRow(t *testing.T) {
	for _, w := range []int{60, 100, 140} {
		c := foldContent(w)
		if side := SidebarRectAt(c, 1); side.W != 0 || !side.Empty() {
			t.Errorf("width %d: a folded sidebar is %d cells wide", w, side.W)
		}
		main := MainRectAt(c, 1)
		if main.X != c.X+layout.GutterX {
			t.Errorf("width %d: folded main starts at %d, want the left gutter %d", w, main.X, c.X+layout.GutterX)
		}
		if main.W != c.W-2*layout.GutterX {
			t.Errorf("width %d: folded main is %d wide, want %d", w, main.W, c.W-2*layout.GutterX)
		}
	}
}

// The sidebar only ever gets narrower as fold rises, and the conversation only
// ever gets wider; and the two always tile the row with the same total.
func TestFoldIsMonotonicAndTilesTheRow(t *testing.T) {
	c := foldContent(100)
	prevSide, prevMain := 1<<30, -1
	for i := 0; i <= 100; i++ {
		f := float64(i) / 100
		side, main := SidebarRectAt(c, f), MainRectAt(c, f)

		if side.W > prevSide {
			t.Fatalf("fold %.2f: sidebar grew from %d to %d", f, prevSide, side.W)
		}
		if main.W < prevMain {
			t.Fatalf("fold %.2f: conversation shrank from %d to %d", f, prevMain, main.W)
		}
		prevSide, prevMain = side.W, main.W

		// They never overlap and the conversation always ends at the right gutter.
		if side.X+side.W > main.X {
			t.Fatalf("fold %.2f: sidebar (ends %d) overlaps the conversation (starts %d)", f, side.X+side.W, main.X)
		}
		if main.X+main.W != c.X+c.W-layout.GutterX {
			t.Fatalf("fold %.2f: the conversation ends at %d, not the right gutter", f, main.X+main.W)
		}
		// The gap between them is at most the unfolded gap plus the divider's cell:
		// no stripe of empty space ever opens up.
		if gap := main.X - (side.X + side.W); gap > layout.ColumnGap {
			t.Fatalf("fold %.2f: a %d-cell gap opened between the columns", f, gap)
		}
	}
	if prevSide != 0 {
		t.Fatalf("fold 1 left a %d-cell sidebar", prevSide)
	}
}

// Out-of-range values behave like the nearest end rather than producing a
// negative width.
func TestFoldIsClamped(t *testing.T) {
	c := foldContent(100)
	if a, b := SidebarRectAt(c, -3), SidebarRectAt(c, 0); a != b {
		t.Errorf("fold -3 is not treated as 0: %v vs %v", a, b)
	}
	if a, b := SidebarRectAt(c, 7), SidebarRectAt(c, 1); a != b {
		t.Errorf("fold 7 is not treated as 1: %v vs %v", a, b)
	}
	if a, b := MainRectAt(c, 7), MainRectAt(c, 1); a != b {
		t.Errorf("main at fold 7 is not main at fold 1: %v vs %v", a, b)
	}
}

// The sidebar's contents are drawn until a threshold and not after, so a sliver of
// a few cells never shows a flickering truncated word.
func TestSidebarContentVisibleOnlyBeforeTheThreshold(t *testing.T) {
	for _, c := range []struct {
		fold float64
		want bool
	}{{0, true}, {0.3, true}, {0.59, true}, {0.6, false}, {0.8, false}, {1, false}} {
		if got := SidebarContentVisible(c.fold); got != c.want {
			t.Errorf("fold %.2f: visible = %v, want %v", c.fold, got, c.want)
		}
	}
}

// The divider travels with the sidebar's edge and is gone once it has folded.
func TestDividerFollowsTheSidebarAndVanishes(t *testing.T) {
	c := foldContent(100)
	x0, ok0 := FoldDividerX(c, 0)
	if !ok0 {
		t.Fatal("no divider when fully open")
	}
	if want := c.X + layout.GutterX + layout.SidebarWidth(100) + 1; x0 != want {
		t.Fatalf("open divider at %d, want %d", x0, want)
	}
	xMid, okMid := FoldDividerX(c, 0.5)
	if !okMid || xMid >= x0 {
		t.Fatalf("the divider did not move left with the sidebar: %d -> %d (visible %v)", x0, xMid, okMid)
	}
	if _, ok := FoldDividerX(c, 1); ok {
		t.Fatal("a divider is drawn beside a sidebar that is gone")
	}
}

// The four values the design names, checked as concrete rectangles at width 100.
func TestFoldRectsAtTheDesignedSteps(t *testing.T) {
	c := foldContent(100)
	sw := layout.SidebarWidth(100) // 25 at this width

	for _, s := range []struct {
		fold     float64
		sideW    int
		mainFrom int
	}{
		{0, sw, 2 + sw + 2},
		{0.3, int(float64(sw)*0.7 + 0.5), 0},
		{0.6, int(float64(sw)*0.4 + 0.5), 0},
		{1, 0, 2},
	} {
		side, main := SidebarRectAt(c, s.fold), MainRectAt(c, s.fold)
		if side.W != s.sideW {
			t.Errorf("fold %.1f: sidebar %d wide, want %d", s.fold, side.W, s.sideW)
		}
		if s.mainFrom != 0 && main.X != s.mainFrom {
			t.Errorf("fold %.1f: conversation starts at %d, want %d", s.fold, main.X, s.mainFrom)
		}
		if main.X+main.W != 98 {
			t.Errorf("fold %.1f: conversation ends at %d, want 98", s.fold, main.X+main.W)
		}
	}
}
