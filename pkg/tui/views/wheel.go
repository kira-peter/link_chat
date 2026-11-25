package views

import (
	"math"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// The quick wheel (spec tui-quick-wheel / 扇形样式): the actions fan out from the
// selected thing like the ribs of a hand fan, towards whichever side has the
// most room. The selected action is always in the middle of the fan, in yellow
// and in full; the rest fade with their distance from it, and the list wraps
// round. The rest
// of the screen is dimmed, except the selected thing itself.

// WheelKeyName is how a wheel key setting reads.
func WheelKeyName(k string) string {
	switch k {
	case "ctrl_g":
		return "Ctrl+G"
	case "ctrl_k":
		return "Ctrl+K"
	case "ctrl_o":
		return "Ctrl+O"
	case "f1":
		return "F1"
	}
	return "Ctrl+Space"
}

// FindSelection is the rectangle of the selected thing drawn in region: the
// largest run of rows painted with the selection background, or the focused
// input field, or failing both the middle of the region. Every page marks its
// selection with the same fill, so reading it back from the screen finds it on
// any page without each page reporting where it drew it.
func FindSelection(s tcell.Screen, region widget.Rect, t theme.Tokens) widget.Rect {
	for _, bg := range []tcell.Color{t.SelectedBg, t.InputFocus} {
		if r, ok := findFill(s, region, bg); ok {
			return r
		}
	}
	return widget.Rect{X: region.X + region.W/2, Y: region.Y + region.H/2, W: 1, H: 1}
}

func findFill(s tcell.Screen, region widget.Rect, bg tcell.Color) (widget.Rect, bool) {
	type run struct{ top, bottom, left, right, cells int }
	var best, cur run
	open := false
	flush := func() {
		if open && cur.cells > best.cells {
			best = cur
		}
		open = false
	}
	for y := region.Y; y < region.Y+region.H; y++ {
		left, right, cells := -1, -1, 0
		for x := region.X; x < region.X+region.W; x++ {
			_, _, st, _ := s.GetContent(x, y)
			if _, b, _ := st.Decompose(); b == bg {
				if left < 0 {
					left = x
				}
				right, cells = x, cells+1
			}
		}
		if cells == 0 {
			flush()
			continue
		}
		if !open {
			cur, open = run{top: y, left: left, right: right}, true
		}
		cur.bottom, cur.cells = y, cur.cells+cells
		cur.left, cur.right = minInt(cur.left, left), maxInt(cur.right, right)
	}
	flush()
	if best.cells == 0 {
		return widget.Rect{}, false
	}
	return widget.Rect{X: best.left, Y: best.top, W: best.right - best.left + 1, H: best.bottom - best.top + 1}, true
}

// FanModel is what the fan shows.
type FanModel struct {
	Labels []string
	Sel    int
	// Anchor is the selected thing on screen; it stays undimmed, as do Keep.
	Anchor widget.Rect
	Keep   []widget.Rect
	// Motion animates the opening and closing; without it the fan is drawn
	// fully open.
	Motion bool
	// Since is the time since the wheel opened or, while Closing, since it
	// closed.
	Since   time.Duration
	Closing bool
	Shimmer bool
	Depth   anim.Depth
	// Colors, when set, are each item's own colour (a colour setting's
	// candidates are printed in that colour); zero keeps the usual one.
	Colors []tcell.Color
	// Muted draws the fan held still with only its selected item and its line:
	// it is the parent of a submenu that has the keyboard, and its other items
	// would be in the submenu's way.
	Muted bool
	// Sideways keeps the fan to the left or right of its anchor.
	Sideways bool
	// Shallow keeps the arc to a gentle curve however many items there are: a
	// submenu of many candidates stays a tidy column.
	Shallow bool
	// Prefer opens the fan that way when it fits: a submenu goes on in the
	// direction its parent opened, so the line keeps going one way.
	Prefer FanDir
}

// The timing of the opening and the closing (spec tui-quick-wheel / 转盘动画).
//
// Opening: the line grows out of the selected thing; then the items light up
// ring by ring, the middle one first and the outer ones clearly later, each to
// its own brightness, the middle brightest; the background dims all along.
//
// Closing is the reverse: every item starts fading at once by the same amount,
// so the dim outer ones are gone first and the middle one last; then the line
// draws back into the thing; then the background brightens.
const (
	fanLineIn   = 140 * time.Millisecond
	fanRingGap  = 70 * time.Millisecond
	fanRingsMax = 420 * time.Millisecond // all the rings' delays together, at most
	fanItemIn   = 110 * time.Millisecond
	fanItemsOut = 180 * time.Millisecond
	fanLineOut  = 130 * time.Millisecond
	fanBgOut    = 120 * time.Millisecond
)

// fanRings is how many rings of items there are beside the middle one.
func fanRings(n int) int { return n - 1 - (n-1)/2 }

func fanGap(n int) time.Duration {
	if k := fanRings(n); k > 0 {
		if g := fanRingsMax / time.Duration(k); g < fanRingGap {
			return g
		}
	}
	return fanRingGap
}

// FanOpenDuration is how long opening n items takes.
func FanOpenDuration(n int) time.Duration {
	return fanLineIn + time.Duration(fanRings(n))*fanGap(n) + fanItemIn
}

// FanCloseDuration is how long closing takes.
func FanCloseDuration() time.Duration { return fanItemsOut + fanLineOut + fanBgOut }

// FanFoldDuration is how long a submenu takes to fold back into its item: its
// items fade and its line draws back; there is no background to restore.
func FanFoldDuration() time.Duration { return fanItemsOut + fanLineOut }

// fanLevel is the full brightness of an item k rings from the middle: 1 in the
// middle, falling evenly to 0.3 at the outermost ring.
func fanLevel(k, rings int) float64 {
	return 1 - 0.7*float64(k)/float64(maxInt(rings, 3))
}

func fanClamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func fanEase(p float64) float64 { p = fanClamp(p); return 1 - (1-p)*(1-p) }

// fanFrame is the animation at one instant.
type fanFrame struct {
	line, dim float64
	ring      func(k int) float64 // brightness of the items k rings out
	settled   bool                // fully open and still
}

func fanFrameAt(m FanModel) fanFrame {
	n := len(m.Labels)
	rings := fanRings(n)
	if !m.Motion {
		return fanFrame{line: 1, dim: 1, settled: true, ring: func(k int) float64 { return fanLevel(k, rings) }}
	}
	t := m.Since
	frac := func(d, of time.Duration) float64 { return fanClamp(float64(d) / float64(of)) }
	if m.Closing {
		q := frac(t, fanItemsOut)
		return fanFrame{
			ring: func(k int) float64 { return math.Max(0, fanLevel(k, rings)-q) },
			line: 1 - frac(t-fanItemsOut, fanLineOut),
			dim:  1 - frac(t-fanItemsOut-fanLineOut, fanBgOut),
		}
	}
	gap := fanGap(n)
	open := FanOpenDuration(n)
	return fanFrame{
		line: fanEase(float64(t) / float64(fanLineIn)),
		dim:  fanEase(float64(t) / float64(open)),
		ring: func(k int) float64 {
			return fanLevel(k, rings) * fanEase(float64(t-fanLineIn-time.Duration(k)*gap)/float64(fanItemIn))
		},
		settled: t >= open,
	}
}

// FanWheel draws the fan.
type FanWheel struct{ T theme.Tokens }

// FanDir is the way the fan opens.
type FanDir int

const (
	// FanNone is no preference, the zero value of FanModel.Prefer.
	FanNone FanDir = iota
	FanRight
	FanLeft
	FanUp
	FanDown
	// FanColumn is the fallback when no side has room: a plain list in the
	// middle of the screen.
	FanColumn
)

const fanDim = 0.82

// fanMidGap is the distance, in cells, from a vertical line to the nearest
// items either side of it: the width of two Chinese characters, the same for
// any labels, so the fan keeps its shape as it turns.
const fanMidGap = 4

// fanItem is one placed label: its text starts at x on row y.
type fanItem struct {
	x, y int
	text string
	d    int // signed distance from the selection, wrapping
}

// fanPoint is one cell of the line from the anchor to the selection.
type fanPoint struct {
	x, y int
	r    rune
}

// fanPlan is where everything goes.
type fanPlan struct {
	dir   FanDir
	items []fanItem // the selection among them, at d == 0
	// line runs from the anchor to the selection, in that order, so a part of
	// it is a prefix.
	line []fanPoint
}

// fanDist is item i's signed distance from sel. The list does not wrap: the
// items before the selection are on one side, those after on the other, and
// the ends are ends (turning past one starts again from the other).
func fanDist(i, sel, n int) int { return i - sel }

// arc is how far a circle of radius r curves back from its widest point at a
// height h off the middle, in columns (twice the rows, for the cells' shape).
func arc(r, h int) int {
	v := float64(r*r - h*h)
	if v < 0 {
		v = 0
	}
	return int(math.Round(2 * (float64(r) - math.Sqrt(v))))
}

// planFan lays the labels out round anchor inside c.
//
// Beside the anchor (left or right), the labels are aligned on the arc by the
// end nearer the anchor: opening right, each label starts on the arc; opening
// left, each ends on it.
//
// Above or below the anchor, the selection is centred at the tip of the line
// and the others alternate either side, stepping back towards the anchor; each
// is aligned on the arc by the end nearer the line, and the arc starts just
// past the selection's ends so the sides sit close to it. The line keeps a stem
// of a few rows below the lowest item, so it never runs between labels.
//
// When neither way fits, the line rises (or drops) clear of the anchor first and
// then turns, and the fan opens sideways from the corner.
func planFan(labels []string, sel int, anchor, c widget.Rect, sideways, shallow bool, prefer FanDir) fanPlan {
	n := len(labels)
	width := make([]int, n)
	maxW := 0
	for i, l := range labels {
		width[i] = layout.StringWidth(l)
		maxW = maxInt(maxW, width[i])
	}
	// The selection is always in the middle and the list runs from it either
	// way without wrapping. The fan keeps the size it had (half the items each
	// side), so the shade under it does not grow; near an end the slots past it
	// stay empty, and items beyond the far edge wait their turn.
	l, p := (n-1)/2, n-1-(n-1)/2 // rows kept before and after the selection
	r := maxInt(l, p) + 1
	ay := anchor.Y + anchor.H/2
	// The anchor's middle as the vertical line sees it: the line is drawn on a
	// cell's left edge, so this rounds up, matching how a label is centred on
	// the line. A submenu above an item then carries on its parent's line.
	ax := anchor.X + (anchor.W+1)/2

	// side lays a sideways fan whose line leaves (sx, sy) heading out, and
	// reports whether it fits. prefix is line already laid from the anchor.
	side := func(dir FanDir, sx, sy int, prefix []fanPoint) (fanPlan, bool) {
		const reach = 8
		out := 1
		if dir == FanLeft {
			out = -1
		}
		need := reach + 2*r + maxW + 1
		if dir == FanRight && sx+need > c.X+c.W || dir == FanLeft && sx-need < c.X-1 || n > c.H {
			return fanPlan{}, false
		}
		pl := fanPlan{dir: dir, line: append([]fanPoint(nil), prefix...)}
		cy := clampInt(sy, c.Y+l, c.Y+c.H-1-p)
		for i := range labels {
			d := fanDist(i, sel, n)
			if d < -l || d > p {
				continue
			}
			back := arc(r, d)
			if shallow {
				back = arc(4, minInt(absInt(d), 3))
			}
			x := sx + reach - back
			if dir == FanLeft {
				x = sx - reach + back - width[i] + 1
			}
			pl.items = append(pl.items, fanItem{x: x, y: cy + d, text: labels[i], d: d})
		}
		// Out along sy, then up or down to the selection's row if the fan had to
		// move, then on to one cell short of the label.
		bend := sx + out
		end := sx + out*(reach-2)
		x := sx
		if sy != cy {
			for ; x != bend; x += out {
				pl.line = append(pl.line, fanPoint{x, sy, '─'})
			}
			step := 1
			if cy < sy {
				step = -1
			}
			pl.line = append(pl.line, fanPoint{bend, sy, corner(out, 0, 0, step)})
			for y := sy + step; y != cy; y += step {
				pl.line = append(pl.line, fanPoint{bend, y, '│'})
			}
			pl.line = append(pl.line, fanPoint{bend, cy, corner(0, step, out, 0)})
			x = bend + out
		}
		for ; x != end+out; x += out {
			pl.line = append(pl.line, fanPoint{x, cy, '─'})
		}
		return pl, true
	}

	// vertical lays a fan above (step -1) or below (step 1) the anchor.
	vertical := func(dir FanDir, step int) (fanPlan, bool) {
		const stem = 3
		k := maxInt(l, p)
		base := anchor.Y - 1
		if step > 0 {
			base = anchor.Y + anchor.H
		}
		apex := base + step*(stem+k)
		if apex < c.Y || apex >= c.Y+c.H {
			return fanPlan{}, false
		}
		selW := width[sel]
		// The sides keep a fixed distance from the line whatever the labels:
		// fanMidGap cells (two Chinese characters) between the line and ring 1,
		// further out along the arc for the outer rings. The selection is
		// centred on the line above them.
		offset := func(a int) int {
			return int(math.Round(2 * math.Sqrt(float64(r*r-(r-a)*(r-a)))))
		}
		reachOut := fanMidGap + offset(k) - offset(1) + maxW
		if selW/2+1 > reachOut {
			reachOut = selW/2 + 1
		}
		if 2*reachOut+1 > c.W {
			return fanPlan{}, false
		}
		x0 := clampInt(ax, c.X+reachOut, c.X+c.W-1-reachOut)
		pl := fanPlan{dir: dir}
		// The line is a thin bar '▏' on the left edge of cell x0. An even-width
		// label centres on that edge exactly; an odd one sits half a cell left.
		left := x0 - (selW+1)/2
		for i := range labels {
			d := fanDist(i, sel, n)
			if d < -k || d > k {
				continue
			}
			if d == 0 {
				pl.items = append(pl.items, fanItem{x: left, y: apex, text: labels[i]})
				continue
			}
			a := absInt(d)
			y := apex - step*a
			gap := fanMidGap + offset(a) - offset(1)
			// The line is on the left edge of cell x0, so the right side starts
			// a cell nearer to keep both sides the same distance from it.
			x := x0 + gap - 1
			if d < 0 {
				x = x0 - gap - width[i] + 1
			}
			pl.items = append(pl.items, fanItem{x: x, y: y, text: labels[i], d: d})
		}
		// If the anchor is off to one side, the line runs over first.
		x, y := ax, base
		if ax != x0 {
			hs := 1
			if x0 < ax {
				hs = -1
			}
			pl.line = append(pl.line, fanPoint{ax, base, corner(0, step, hs, 0)})
			for x = ax + hs; x != x0; x += hs {
				pl.line = append(pl.line, fanPoint{x, base, '─'})
			}
			pl.line = append(pl.line, fanPoint{x0, base, corner(hs, 0, 0, step)})
			y = base + step
		}
		for ; y != apex; y += step {
			pl.line = append(pl.line, fanPoint{x0, y, '▏'})
		}
		return pl, true
	}

	// Try the sides with the most room first.
	space := map[FanDir]int{
		FanRight: c.X + c.W - (anchor.X + anchor.W),
		FanLeft:  anchor.X - c.X,
		FanUp:    2 * (anchor.Y - c.Y),
		FanDown:  2 * (c.Y + c.H - (anchor.Y + anchor.H)),
	}
	order := []FanDir{FanRight, FanLeft, FanUp, FanDown}
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && space[order[j]] > space[order[j-1]]; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	if prefer != FanNone {
		// The preferred way first: a submenu carries on its parent's line.
		// Then the rest in their usual order.
		rest := []FanDir{prefer}
		for _, d := range order {
			if d != prefer {
				rest = append(rest, d)
			}
		}
		order = rest
	}
	for _, dir := range order {
		if sideways && (dir == FanUp || dir == FanDown) {
			continue
		}
		var pl fanPlan
		ok := false
		switch dir {
		case FanRight:
			pl, ok = side(FanRight, anchor.X+anchor.W, ay, nil)
		case FanLeft:
			pl, ok = side(FanLeft, anchor.X-1, ay, nil)
		case FanUp:
			pl, ok = vertical(FanUp, -1)
		case FanDown:
			pl, ok = vertical(FanDown, 1)
		}
		if ok {
			return pl
		}
	}

	// Up or down clear of the anchor, then a turn to whichever side has room.
	for _, step := range []int{-1, 1} {
		base := anchor.Y - 1
		if step > 0 {
			base = anchor.Y + anchor.H
		}
		turn := base + step*2
		if turn < c.Y || turn >= c.Y+c.H {
			continue
		}
		for _, out := range []int{1, -1} {
			var prefix []fanPoint
			for y := base; y != turn; y += step {
				prefix = append(prefix, fanPoint{ax, y, '│'})
			}
			prefix = append(prefix, fanPoint{ax, turn, corner(0, step, out, 0)})
			dir := FanRight
			if out < 0 {
				dir = FanLeft
			}
			if pl, ok := side(dir, ax+out, turn, prefix); ok {
				return pl
			}
		}
	}

	// Nothing fits: a plain column in the middle, as many items as there is room
	// for, centred on the selection.
	pl := fanPlan{dir: FanColumn}
	cx := c.X + c.W/2 - maxW/2
	cy := c.Y + c.H/2
	for i := range labels {
		d := fanDist(i, sel, n)
		if y := cy + d; y >= c.Y && y < c.Y+c.H {
			pl.items = append(pl.items, fanItem{x: cx, y: y, text: labels[i], d: d})
		}
	}
	return pl
}

// corner is the box glyph joining two line ends: (ix, iy) is the direction the
// line comes in from (as a step towards this cell), (ox, oy) the way it leaves.
// One of each pair is zero.
func corner(ix, iy, ox, oy int) rune {
	// The arms the glyph has: where it came from (opposite the step) and where
	// it goes.
	up := iy > 0 || oy < 0
	down := iy < 0 || oy > 0
	left := ix > 0 || ox < 0
	right := ix < 0 || ox > 0
	switch {
	case down && right:
		return '┌'
	case down && left:
		return '┐'
	case up && right:
		return '└'
	case up && left:
		return '┘'
	case up || down:
		return '│'
	}
	return '─'
}

// Render dims the screen, keeps the anchor, and draws the fan in c.
// It returns the way the fan opened, which decides its keys.
func (v FanWheel) Render(s tcell.Screen, c widget.Rect, m FanModel, lang i18n.Lang) FanDir {
	if len(m.Labels) == 0 || c.Empty() {
		return FanRight
	}
	v.Shade(s, v.Cover(c, m), append([]widget.Rect{m.Anchor}, m.Keep...), fanFrameAt(m).dim)
	_, dir := v.Draw(s, c, m)
	return dir
}

// Draw draws the fan in c without touching the rest of the screen, and returns
// where the selected item is, for a submenu to grow from, and the way the fan
// opened: the direction of the line into the selection.
func (v FanWheel) Draw(s tcell.Screen, c widget.Rect, m FanModel) (widget.Rect, FanDir) {
	if len(m.Labels) == 0 || c.Empty() {
		return widget.Rect{}, FanRight
	}
	fr := fanFrameAt(m)
	pl := planFan(m.Labels, m.Sel, m.Anchor, c, m.Sideways, m.Shallow, m.Prefer)
	var selRect widget.Rect
	for _, it := range pl.items {
		if it.d == 0 {
			selRect = widget.Rect{X: it.x, Y: it.y, W: layout.StringWidth(it.text), H: 1}
		}
	}
	if m.Muted {
		fr.ring = func(k int) float64 {
			if k == 0 {
				return 1
			}
			return 0
		}
		fr.settled = false
	}
	colorOf := func(i int, def tcell.Color) tcell.Color {
		if i < len(m.Colors) && m.Colors[i] != 0 {
			return m.Colors[i]
		}
		return def
	}

	base := tcell.StyleDefault.Background(v.T.Bg)
	rgb := v.T.Bg.IsRGB()
	// lit is col at brightness b over the background. Without true colour there
	// is no blending: a glyph is drawn or not, in a brighter or fainter colour.
	lit := func(col tcell.Color, b float64) (tcell.Color, bool) {
		if b <= 0.02 {
			return 0, false
		}
		if rgb && col.IsRGB() {
			return anim.Blend(v.T.Bg, col, b), true
		}
		if b < 0.2 {
			return 0, false
		}
		if col == v.T.Text && b < 0.75 {
			if b < 0.45 {
				return v.T.TextFaint, true
			}
			return v.T.TextMuted, true
		}
		return col, true
	}
	put := func(x, y int, r rune, st tcell.Style) {
		if x >= c.X && x < c.X+c.W && y >= c.Y && y < c.Y+c.H {
			s.SetContent(x, y, r, nil, st)
		}
	}
	text := func(x, y int, str string, st tcell.Style) {
		for _, r := range str {
			put(x, y, r, st)
			x += maxInt(1, layout.RuneWidth(r))
		}
	}

	// The line, as far as it has grown.
	shown := int(math.Ceil(fr.line * float64(len(pl.line))))
	if !v.T.UnicodeBox {
		for i := range pl.line {
			switch pl.line[i].r {
			case '─':
				pl.line[i].r = '-'
			case '│', '▏':
				pl.line[i].r = '|'
			default:
				pl.line[i].r = '+'
			}
		}
	}
	// The line is unbroken: each of its cells is cleared first, including a
	// wide character from the page that straddles it, so nothing shows through
	// or cuts it.
	clearCell := func(x, y int) {
		if x < c.X || x >= c.X+c.W || y < c.Y || y >= c.Y+c.H {
			return
		}
		if x > 0 {
			if _, _, _, w := s.GetContent(x-1, y); w == 2 {
				s.SetContent(x-1, y, ' ', nil, base)
			}
		}
		if _, _, _, w := s.GetContent(x, y); w == 2 {
			s.SetContent(x+1, y, ' ', nil, base)
		}
		s.SetContent(x, y, ' ', nil, base)
	}
	for _, pt := range pl.line[:shown] {
		clearCell(pt.x, pt.y)
		put(pt.x, pt.y, pt.r, base.Foreground(v.T.Accent))
	}
	// It is drawn again last, over anything the labels put on it.
	defer func() {
		for _, pt := range pl.line[:shown] {
			clearCell(pt.x, pt.y)
			put(pt.x, pt.y, pt.r, base.Foreground(v.T.Accent))
		}
	}()

	// The items: the others fade with their distance; the selection is the
	// warning yellow in bold, its whole name, with the band of light once open.
	n := len(m.Labels)
	for _, it := range pl.items {
		b := fr.ring(absInt(it.d))
		w := layout.StringWidth(it.text)
		idx := ((m.Sel+it.d)%n + n) % n
		if it.d != 0 {
			if col, ok := lit(colorOf(idx, v.T.Text), b); ok {
				put(it.x-1, it.y, ' ', base)
				text(it.x, it.y, it.text, base.Foreground(col))
				put(it.x+w, it.y, ' ', base)
			}
			continue
		}
		col, ok := lit(colorOf(idx, v.T.Warning), b)
		if !ok {
			continue
		}
		st := base.Foreground(col).Bold(true)
		if idx < len(m.Colors) && m.Colors[idx] != 0 {
			// A colour candidate keeps its colour; it is marked by bold and
			// underline instead of yellow.
			st = st.Underline(true)
		}
		put(it.x-1, it.y, ' ', base)
		put(it.x+w, it.y, ' ', base)
		if m.Shimmer && fr.settled && !m.Closing {
			cv := widget.NewCanvas(s, v.T, c)
			shimmerText(cv, it.x-c.X, it.y-c.Y, it.text, st, v.T.Text, m.Since, m.Depth)
		} else {
			text(it.x, it.y, it.text, st)
		}
	}
	return selRect, pl.dir
}

// FanKeys are the keys of a fan that opened dir: next and prev turn it, out
// goes on along the line (opens a submenu, or runs the item), back goes back
// against the line (to the level before, or closes the wheel).
//
// A fan beside its anchor is a column, so ↑↓ turn it; one above or below has
// its items either side of the line, so ←→ do.
func FanKeys(dir FanDir) (next, prev, out, back tcell.Key) {
	switch dir {
	case FanLeft:
		return tcell.KeyDown, tcell.KeyUp, tcell.KeyLeft, tcell.KeyRight
	case FanUp:
		return tcell.KeyRight, tcell.KeyLeft, tcell.KeyUp, tcell.KeyDown
	case FanDown:
		return tcell.KeyRight, tcell.KeyLeft, tcell.KeyDown, tcell.KeyUp
	}
	return tcell.KeyDown, tcell.KeyUp, tcell.KeyRight, tcell.KeyLeft
}

// Bounds is the rectangle the fan's items and line cover in c.
func (v FanWheel) Bounds(c widget.Rect, m FanModel) widget.Rect {
	if len(m.Labels) == 0 || c.Empty() {
		return widget.Rect{}
	}
	pl := planFan(m.Labels, m.Sel, m.Anchor, c, m.Sideways, m.Shallow, m.Prefer)
	x0, y0, x1, y1 := c.X+c.W, c.Y+c.H, c.X-1, c.Y-1
	grow := func(x, y, w int) {
		x0, y0 = minInt(x0, x), minInt(y0, y)
		x1, y1 = maxInt(x1, x+w-1), maxInt(y1, y)
	}
	for _, it := range pl.items {
		if m.Muted && it.d != 0 {
			continue // not drawn
		}
		grow(it.x-1, it.y, layout.StringWidth(it.text)+2)
	}
	for _, pt := range pl.line {
		grow(pt.x, pt.y, 1)
	}
	if x1 < x0 {
		return widget.Rect{}
	}
	return widget.Rect{X: x0, Y: y0, W: x1 - x0 + 1, H: y1 - y0 + 1}
}

// Cover is the area a fan can take whichever item is selected, so the shade
// under it keeps one size as the fan turns: the box round its items in every
// layout, and a strip one cell either side of its line.
func (v FanWheel) Cover(c widget.Rect, m FanModel) []widget.Rect {
	m.Muted = false
	var box widget.Rect
	var line []widget.Rect
	seen := map[[2]int]bool{}
	for i := range m.Labels {
		m.Sel = i
		pl := planFan(m.Labels, m.Sel, m.Anchor, c, m.Sideways, m.Shallow, m.Prefer)
		for _, it := range pl.items {
			r := widget.Rect{X: it.x - 1, Y: it.y, W: layout.StringWidth(it.text) + 2, H: 1}
			box = unionRect(box, r)
		}
		for _, pt := range pl.line {
			if !seen[[2]int{pt.x, pt.y}] {
				seen[[2]int{pt.x, pt.y}] = true
				line = append(line, widget.Rect{X: pt.x - 1, Y: pt.y, W: 3, H: 1})
			}
		}
	}
	return append([]widget.Rect{box}, line...)
}

// Trail is what a muted fan still shows, for shading under it: its line and
// its selected item, as it is now.
func (v FanWheel) Trail(c widget.Rect, m FanModel) []widget.Rect {
	pl := planFan(m.Labels, m.Sel, m.Anchor, c, m.Sideways, m.Shallow, m.Prefer)
	var out []widget.Rect
	for _, it := range pl.items {
		if it.d == 0 {
			out = append(out, widget.Rect{X: it.x - 1, Y: it.y, W: layout.StringWidth(it.text) + 2, H: 1})
		}
	}
	for _, pt := range pl.line {
		out = append(out, widget.Rect{X: pt.x - 1, Y: pt.y, W: 3, H: 1})
	}
	return out
}

func unionRect(a, b widget.Rect) widget.Rect {
	if a.W == 0 {
		return b
	}
	if b.W == 0 {
		return a
	}
	x0, y0 := minInt(a.X, b.X), minInt(a.Y, b.Y)
	x1, y1 := maxInt(a.X+a.W, b.X+b.W), maxInt(a.Y+a.H, b.Y+b.H)
	return widget.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// fanFeather is how far the shade reaches past its area, in rows (a column
// counts as half a row, for the cells' shape).
const fanFeather = 3.0

// Shade darkens areas right down to the background, and the cells around them
// less the further they are from the nearest, out to fanFeather; keep is left
// as it is. strength scales it all, for the animation.
func (v FanWheel) Shade(s tcell.Screen, areas []widget.Rect, keep []widget.Rect, strength float64) {
	var area widget.Rect
	for _, a := range areas {
		area = unionRect(area, a)
	}
	if area.W == 0 || strength <= 0 {
		return
	}
	// dist is how far (x, y) is from the nearest area, 0 inside one.
	dist := func(x, y int) float64 {
		best := math.Inf(1)
		for _, a := range areas {
			if a.W == 0 {
				continue
			}
			dx := float64(maxInt(0, maxInt(a.X-x, x-(a.X+a.W-1)))) / 2
			dy := float64(maxInt(0, maxInt(a.Y-y, y-(a.Y+a.H-1))))
			best = math.Min(best, math.Sqrt(dx*dx+dy*dy))
		}
		return best
	}
	sw, sh := s.Size()
	fx := int(fanFeather * 2)
	fy := int(fanFeather)
	for y := maxInt(0, area.Y-fy); y < minInt(sh, area.Y+area.H+fy); y++ {
		for x := maxInt(0, area.X-fx); x < minInt(sw, area.X+area.W+fx); {
			mainc, comb, st, width := s.GetContent(x, y)
			if width < 1 {
				width = 1
			}
			kept := false
			for _, k := range keep {
				if x >= k.X && x < k.X+k.W && y >= k.Y && y < k.Y+k.H {
					kept = true
				}
			}
			t := (1 - dist(x, y)/fanFeather) * strength
			if !kept && t > 0 {
				f, b, attrs := st.Decompose()
				s.SetContent(x, y, mainc, comb, tcell.StyleDefault.Foreground(v.fade(f, t)).Background(v.fade(b, t)).Attributes(attrs&^tcell.AttrBold))
			}
			x += width
		}
	}
}

// dim fades every cell outside the anchor towards the background.
func (v FanWheel) dim(s tcell.Screen, keep []widget.Rect, t float64) {
	w, h := s.Size()
	if t <= 0 {
		return
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; {
			mainc, comb, st, width := s.GetContent(x, y)
			if width < 1 {
				width = 1
			}
			kept := false
			for _, a := range keep {
				if x >= a.X && x < a.X+a.W && y >= a.Y && y < a.Y+a.H {
					kept = true
				}
			}
			if !kept {
				f, b, attrs := st.Decompose()
				f, b = v.fade(f, t), v.fade(b, t)
				s.SetContent(x, y, mainc, comb, tcell.StyleDefault.Foreground(f).Background(b).Attributes(attrs&^tcell.AttrBold))
			}
			x += width
		}
	}
}

func (v FanWheel) fade(c tcell.Color, t float64) tcell.Color {
	if c.IsRGB() && v.T.Bg.IsRGB() {
		return anim.Blend(c, v.T.Bg, t)
	}
	// Without true colour there is nothing to blend: text goes faint and fills
	// go to the background.
	if c == v.T.Bg || c == tcell.ColorDefault {
		return c
	}
	if c == v.T.SelectedBg || c == v.T.InputFocus || c == v.T.Surface {
		return v.T.Bg
	}
	return v.T.TextFaint
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
