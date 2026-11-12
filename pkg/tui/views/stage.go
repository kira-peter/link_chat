package views

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// The sign-in stage.
//
// Before the user has signed in there is nothing behind the panel worth showing:
// no conversations, no connection, no shortcuts that work. So the panel stands on
// its own stage — the theme's background and a sparse field of faint dots drifting
// slowly across it, like a night sky (spec tui-login-stage / 登录舞台只显示面板与背景).

// Stage draws the stage background.
type Stage struct {
	T theme.Tokens
}

// StageModel is what the stage needs.
type StageModel struct {
	// Panel is the panel the dots keep clear of. The dots also keep a one-cell
	// margin around it so none sits against its border.
	Panel widget.Rect
	// Elapsed is how long the dots have been drifting. It is held at zero when
	// animation is off, which gives the same still field every frame.
	Elapsed time.Duration
	// Fade dims the field, 1 fully visible and 0 gone. The entrance uses it to let
	// the dots go while the panel turns into a line.
	Fade float64
}

// dotArea is how many cells of screen there are per dot: sparse enough to read as
// a sky, not a texture.
const dotArea = 60

// dotCellsPerSecond caps how fast a dot moves. At this speed a dot moves under one
// cell between two frames of the stage's 100ms clock, so the drift looks continuous
// rather than jumpy (spec tui-login-stage / 点在缓慢移动).
const dotCellsPerSecond = 1.2

// dot is one point of the field: where it starts and how it drifts, in cells and
// cells per second.
type dot struct {
	x, y   float64
	vx, vy float64
	near   bool // nearer dots are drawn with the brighter glyph
}

// dotsFor lays out the field for a w×h screen. It is a pure function of the size:
// a small linear congruential generator with a fixed seed, so the same screen
// always gets the same sky and a test can say exactly where every dot is.
func dotsFor(w, h int) []dot {
	n := w * h / dotArea
	out := make([]dot, 0, n)
	seed := uint32(0x9E3779B9) ^ uint32(w*131+h)
	next := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed>>8) / float64(1<<24)
	}
	for i := 0; i < n; i++ {
		d := dot{x: next() * float64(w), y: next() * float64(h)}
		// A slow drift down and to the right, each dot at its own pace; vertical
		// motion is halved because a terminal cell is about twice as tall as wide.
		speed := 0.3 + 0.7*next()
		d.vx = dotCellsPerSecond * speed
		d.vy = dotCellsPerSecond * speed * (0.2 + 0.3*next())
		d.near = next() < 0.35
		out = append(out, d)
	}
	return out
}

// DotPositions is where each dot of a w×h field is after elapsed, wrapped around
// the screen. Exposed for tests; drawing goes through Render.
func DotPositions(w, h int, elapsed time.Duration) [][2]int {
	if w <= 0 || h <= 0 {
		return nil
	}
	t := elapsed.Seconds()
	ds := dotsFor(w, h)
	out := make([][2]int, len(ds))
	for i, d := range ds {
		out[i] = [2]int{wrap(d.x+d.vx*t, w), wrap(d.y+d.vy*t, h)}
	}
	return out
}

func wrap(v float64, n int) int {
	i := int(v) % n
	if i < 0 {
		i += n
	}
	return i
}

// StageHasRoom reports whether there is any space around the panel for the field.
// On a screen the panel all but fills, the dots would only crowd its border.
func StageHasRoom(w, h int, panel widget.Rect) bool {
	keep := grow(panel, 1)
	return keep.X > 0 || keep.Y > 0 || keep.X+keep.W < w || keep.Y+keep.H < h
}

func grow(r widget.Rect, n int) widget.Rect {
	return widget.Rect{X: r.X - n, Y: r.Y - n, W: r.W + 2*n, H: r.H + 2*n}
}

func inside(r widget.Rect, x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Render fills the screen with the background and draws the dots. The panel is
// drawn after this, on top.
func (v Stage) Render(s tcell.Screen, w, h int, m StageModel) {
	c := widget.NewCanvas(s, v.T, widget.Rect{X: 0, Y: 0, W: w, H: h})
	c.Fill(v.T.Base())
	if m.Fade <= 0 || !StageHasRoom(w, h, m.Panel) {
		return
	}
	keep := grow(m.Panel, 1)
	far := v.T.Faint()
	near := v.T.Muted()
	if m.Fade < 0.5 {
		// Half gone: every dot is drawn at the faintest step, and then not at all.
		near = far
	}
	ds := dotsFor(w, h)
	for i, p := range DotPositions(w, h, m.Elapsed) {
		x, y := p[0], p[1]
		if inside(keep, x, y) {
			continue
		}
		if m.Fade < 0.5 && i%2 == 1 {
			// Fading: thin the field out as well as dimming it.
			continue
		}
		r, st := '.', far
		if ds[i].near {
			r, st = '·', near
		}
		s.SetContent(x, y, r, nil, st)
	}
}
