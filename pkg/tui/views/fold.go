package views

import (
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/widget"
)

// Folding.
//
// The sidebar can be folded away so the conversation gets the full width. How far
// it has folded is a single number, fold, running from 0 (fully open) to 1 (fully
// folded). Everything that changes during the fold is read from that one number:
// the sidebar's width, the transcript's width and left edge, and the position of
// the conversation name as it flies from the list to the transcript's title.
// Because they are all the same number, they cannot disagree — there is no moment
// at which the sidebar has shrunk but the transcript has not yet grown
// (design.md decision 1).

// foldContentFrom is the fold value above which the sidebar's contents are no
// longer drawn. A sidebar squeezed to a few cells wide cannot show a name or a
// header legibly; drawing it anyway shows a flickering sliver of truncated text.
// Past this point only the shrinking blank space remains, so the list looks like
// it is being drawn away rather than crushed (design.md risk 1).
const foldContentFrom = 0.6

// clamp01 limits a fold value to its valid range.
func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// sidebarWidthAt is the sidebar's width at a given fold, in whole cells.
func sidebarWidthAt(total int, fold float64) int {
	w := float64(layout.SidebarWidth(total)) * (1 - clamp01(fold))
	return int(w + 0.5)
}

// SidebarRectAt is the sidebar's rectangle at the given fold. At fold 1 it is
// empty (zero width).
func SidebarRectAt(content widget.Rect, fold float64) widget.Rect {
	return widget.Rect{
		X: content.X + layout.GutterX,
		Y: content.Y,
		W: sidebarWidthAt(content.W, fold),
		H: content.H,
	}
}

// MainRectAt is the conversation's rectangle at the given fold.
//
// Unfolded it sits right of the sidebar, its gap and the divider. As the sidebar
// narrows it follows, and fully folded it starts at the left gutter and spans the
// whole row. The column gap shrinks away with the sidebar: a gap kept at full
// width beside no sidebar would leave a stripe of empty space on the left.
func MainRectAt(content widget.Rect, fold float64) widget.Rect {
	f := clamp01(fold)
	sw := sidebarWidthAt(content.W, f)
	gap := int(float64(layout.ColumnGap)*(1-f) + 0.5)
	x := content.X + layout.GutterX + sw + gap
	w := content.W - (x - content.X) - layout.GutterX
	if w < 0 {
		w = 0
	}
	return widget.Rect{X: x, Y: content.Y, W: w, H: content.H}
}

// SidebarContentVisible reports whether the sidebar's contents are drawn at this
// fold. See foldContentFrom.
func SidebarContentVisible(fold float64) bool {
	return clamp01(fold) < foldContentFrom
}

// FoldDividerX is the column of the vertical rule at the given fold, and whether
// it is drawn at all. It travels with the sidebar's right edge and disappears once
// the sidebar is gone.
func FoldDividerX(content widget.Rect, fold float64) (x int, visible bool) {
	f := clamp01(fold)
	sw := sidebarWidthAt(content.W, f)
	if sw <= 0 {
		return 0, false
	}
	x = content.X + layout.GutterX + sw + 1
	if x >= content.X+content.W {
		return 0, false
	}
	return x, true
}
