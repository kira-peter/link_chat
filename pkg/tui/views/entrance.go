package views

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// The sign-in entrance, drawn as a layer over the main interface.
//
// The main interface is drawn first, in full and exactly as it would be with no
// entrance at all. This layer then covers what should not be seen yet: during the
// collapse and the stretch, everything (the panel is turning into a line on the
// stage); during the opening, the rows outside the two travelling halves of the
// rule. Because the main interface underneath is untouched, the last frame of the
// entrance is the main interface cell for cell (spec tui-login-stage /
// 结束后与直接显示的主界面一致).

// EntranceOverlay draws the entrance layer.
type EntranceOverlay struct {
	T theme.Tokens
}

// EntranceModel is one frame of the entrance.
type EntranceModel struct {
	Phase anim.EntrancePhase
	// P is how far into Phase the entrance is, 0..1.
	P float64
	// Panel is where the sign-in panel was when the entrance began.
	Panel widget.Rect
	// StageElapsed keeps the dots where they were drifting, so they fade out in
	// place instead of jumping.
	StageElapsed time.Duration
	// Depth is the terminal's colour tier, which picks how the bright rule is drawn.
	Depth anim.Depth
	// Under, when set, is what the opening shows between its two halves instead of
	// whatever is on the screen beneath. The sign-out exit uses it: by the time the
	// exit plays the state has already moved to the sign-in stage, so the main
	// interface it is closing over only exists as a picture taken when it began.
	Under *Snapshot
	// Reveal, for the sign-out exit, uncovers the real sign-in panel drawn beneath
	// instead of drawing an empty outline: the panel grows out of the line with its
	// contents, and when it is fully grown it is the panel itself, cell for cell.
	Reveal bool
}

// Snapshot is a picture of a screen's cells, taken so that something no longer
// in the state can still be shown for the length of an animation.
type Snapshot struct {
	W, H  int
	cells []snapCell
}

type snapCell struct {
	r     rune
	comb  []rune
	style tcell.Style
}

// TakeSnapshot reads every cell of the screen.
func TakeSnapshot(s tcell.Screen) *Snapshot {
	w, h := s.Size()
	sn := &Snapshot{W: w, H: h, cells: make([]snapCell, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, comb, st, _ := s.GetContent(x, y)
			sn.cells[y*w+x] = snapCell{r, comb, st}
		}
	}
	return sn
}

// paintRows copies rows [top, bot] of the snapshot onto the screen.
func (sn *Snapshot) paintRows(s tcell.Screen, top, bot int) {
	for y := maxInt(0, top); y <= bot && y < sn.H; y++ {
		for x := 0; x < sn.W; x++ {
			c := sn.cells[y*sn.W+x]
			s.SetContent(x, y, c.r, c.comb, c.style)
		}
	}
}

// Render draws the layer. In PhaseDone it draws nothing.
func (v EntranceOverlay) Render(s tcell.Screen, w, h int, m EntranceModel) {
	if w <= 0 || h <= 0 {
		return
	}
	stage := Stage{T: v.T}
	mid := m.Panel.Y + m.Panel.H/2
	// The opening splits from halfway between the two rules it lands on, so both
	// halves have the same distance to go and arrive together. The stretch glides
	// the line there from the panel's middle row as it widens.
	topEnd, botEnd := ChromeRuleRows(h)
	centre := (topEnd + botEnd) / 2
	switch m.Phase {
	case anim.PhaseCollapse:
		if m.Reveal {
			v.revealPanel(s, w, h, m)
			return
		}
		stage.Render(s, w, h, StageModel{Panel: m.Panel, Elapsed: m.StageElapsed, Fade: 1})
		v.collapsingPanel(s, m.Panel, m.P)

	case anim.PhaseStretch:
		stage.Render(s, w, h, StageModel{Panel: m.Panel, Elapsed: m.StageElapsed, Fade: 1 - m.P})
		from, to := m.Panel.W, w-2*layout.GutterX
		lw := from + int(float64(to-from)*m.P+0.5)
		y := mid + int(float64(centre-mid)*m.P+0.5)
		v.rule(s, (w-lw)/2, y, lw, 0, m.Depth)

	case anim.PhaseOpen:
		top := centre - int(float64(centre-topEnd)*m.P+0.5)
		bot := centre + int(float64(botEnd-centre)*m.P+0.5)
		if bot == top && bot < h-1 {
			// The two halves start as one rule; give them their own rows at once so
			// the opening is visible from the first frame.
			bot++
		}
		bg := v.T.Base()
		for y := 0; y < h; y++ {
			if y >= top && y <= bot {
				continue
			}
			widget.NewCanvas(s, v.T, widget.Rect{X: 0, Y: y, W: w, H: 1}).Fill(bg)
		}
		if m.Under != nil && m.Under.W == w && m.Under.H == h {
			m.Under.paintRows(s, top, bot)
		}
		x, lw := layout.GutterX, w-2*layout.GutterX
		v.rule(s, x, top, lw, m.P, m.Depth)
		v.rule(s, x, bot, lw, m.P, m.Depth)
	}
}

// revealPanel is the collapse played backwards for the sign-out exit: the real
// sign-in panel and stage are already drawn beneath, and this covers all of the
// panel except a band about its middle row that widens to the panel's full height.
// At the start of the phase (P 1) only the middle row shows, as the line the
// stretch left there; at its end (P 0) nothing is covered.
func (v EntranceOverlay) revealPanel(s tcell.Screen, w, h int, m EntranceModel) {
	panel := m.Panel
	hgt := panel.H - int(float64(panel.H-1)*m.P+0.5)
	if hgt >= panel.H {
		return
	}
	mid := panel.Y + panel.H/2
	top := mid - hgt/2
	bot := top + hgt - 1
	if hgt <= 1 {
		// Only the line: draw it over the panel's middle row.
		v.coverPanelRows(s, panel, panel.Y, panel.Y+panel.H-1, m)
		v.rule(s, panel.X, mid, panel.W, 0, anim.DepthTrue)
		return
	}
	v.coverPanelRows(s, panel, panel.Y, top-1, m)
	v.coverPanelRows(s, panel, bot+1, panel.Y+panel.H-1, m)
	// The uncovered band's top and bottom edges are the panel's frame, so it reads
	// as a box opening rather than a slice cut out of one.
	edge := v.T.SurfaceStyle().Foreground(v.T.Border)
	glyphs := []rune{'┌', '┐', '└', '┘', '─'}
	if !v.T.UnicodeBox {
		glyphs = []rune{'+', '+', '+', '+', '-'}
	}
	for x := panel.X; x < panel.X+panel.W; x++ {
		tr, br := glyphs[4], glyphs[4]
		switch x {
		case panel.X:
			tr, br = glyphs[0], glyphs[2]
		case panel.X + panel.W - 1:
			tr, br = glyphs[1], glyphs[3]
		}
		if top != panel.Y {
			s.SetContent(x, top, tr, nil, edge)
		}
		if bot != panel.Y+panel.H-1 {
			s.SetContent(x, bot, br, nil, edge)
		}
	}
}

// coverPanelRows paints rows [from, to] of the panel's rectangle as the stage's
// background, dots included, so the covered part looks like the panel is not
// there yet.
func (v EntranceOverlay) coverPanelRows(s tcell.Screen, panel widget.Rect, from, to int, m EntranceModel) {
	if to < from {
		return
	}
	for y := from; y <= to; y++ {
		widget.NewCanvas(s, v.T, widget.Rect{X: panel.X, Y: y, W: panel.W, H: 1}).Fill(v.T.Base())
	}
}

// collapsingPanel draws the panel's outline squashed about its middle row. Its
// contents are gone from the first frame: the panel is now a shape on its way to
// becoming a line, and half a form squeezed into fewer rows would only read as
// broken text.
func (v EntranceOverlay) collapsingPanel(s tcell.Screen, panel widget.Rect, p float64) {
	hgt := panel.H - int(float64(panel.H-1)*p+0.5)
	if hgt < 1 {
		hgt = 1
	}
	mid := panel.Y + panel.H/2
	if hgt == 1 {
		v.rule(s, panel.X, mid, panel.W, 0, anim.DepthTrue)
		return
	}
	r := widget.Rect{X: panel.X, Y: mid - hgt/2, W: panel.W, H: hgt}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.SurfaceStyle())
	c.Box(v.T.BorderStyle())
}

// rule draws one bright line of the entrance. settle runs 0..1 across the opening:
// on a full-colour terminal the line's colour eases from the bright accent to the
// soft border colour of the rules it is about to become, so the landing is seamless.
// With fewer colours brightness cannot carry the effect, so the line is drawn heavy
// instead, which also tells it apart from an ordinary rule.
func (v EntranceOverlay) rule(s tcell.Screen, x, y, w int, settle float64, depth anim.Depth) {
	if w <= 0 {
		return
	}
	glyph := "─"
	bright := anim.Blend(v.T.Accent, tcell.NewRGBColor(255, 255, 255), 0.35)
	st := v.T.Base().Foreground(anim.Blend(bright, v.T.BorderSoft, settle))
	if depth != anim.DepthTrue || v.T.ColorCap != theme.ColorTrue {
		glyph = "━"
		st = v.T.Base().Foreground(v.T.Accent)
	}
	if !v.T.UnicodeBox {
		glyph = "="
	}
	widget.NewCanvas(s, v.T, widget.Rect{X: x, Y: y, W: w, H: 1}).Text(0, 0, strings.Repeat(glyph, w), st)
}
