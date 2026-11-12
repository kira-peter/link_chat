package views

import (
	"testing"
	"time"

	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

func stageScreen(t *testing.T, tk theme.Tokens, w, h int, m StageModel) [][]rune {
	t.Helper()
	s := newScreen(t, w, h)
	Stage{T: tk}.Render(s, w, h, m)
	s.Show()
	out := make([][]rune, h)
	for y := 0; y < h; y++ {
		out[y] = make([]rune, w)
		for x := 0; x < w; x++ {
			r, _, _, _ := s.GetContent(x, y)
			out[y][x] = r
		}
	}
	return out
}

func isDot(r rune) bool { return r == '.' || r == '·' }

// The same screen and the same time give exactly the same sky.
func TestStageDotsAreDeterministic(t *testing.T) {
	const w, h = 100, 30
	m := StageModel{Panel: LoginPanelRect(w, h), Elapsed: 1700 * time.Millisecond, Fade: 1}
	a := stageScreen(t, toks(), w, h, m)
	b := stageScreen(t, toks(), w, h, m)
	n := 0
	for y := range a {
		for x := range a[y] {
			if a[y][x] != b[y][x] {
				t.Fatalf("cell (%d,%d) differs between two identical renders", x, y)
			}
			if isDot(a[y][x]) {
				n++
			}
		}
	}
	if n < 10 {
		t.Fatalf("only %d dots on a %dx%d stage; the field is missing", n, w, h)
	}
}

// No dot inside the panel or in the one-cell margin around it.
func TestStageDotsKeepClearOfThePanel(t *testing.T) {
	const w, h = 100, 30
	panel := LoginPanelRect(w, h)
	keep := widget.Rect{X: panel.X - 1, Y: panel.Y - 1, W: panel.W + 2, H: panel.H + 2}
	for ms := 0; ms <= 20000; ms += 250 {
		sc := stageScreen(t, toks(), w, h, StageModel{Panel: panel, Elapsed: time.Duration(ms) * time.Millisecond, Fade: 1})
		for y := keep.Y; y < keep.Y+keep.H; y++ {
			for x := keep.X; x < keep.X+keep.W; x++ {
				if isDot(sc[y][x]) {
					t.Fatalf("at %dms a dot sits at (%d,%d), inside the panel's clear zone %v", ms, x, y, keep)
				}
			}
		}
	}
}

// The dots drift: after two seconds the field has changed, and between two frames
// 100ms apart no dot jumps more than one cell.
func TestStageDotsDriftSlowly(t *testing.T) {
	const w, h = 100, 30
	if a, b := DotPositions(w, h, 0), DotPositions(w, h, 2*time.Second); equalPositions(a, b) {
		t.Fatal("after two seconds no dot has moved")
	}
	for ms := 0; ms < 30000; ms += 100 {
		a := DotPositions(w, h, time.Duration(ms)*time.Millisecond)
		b := DotPositions(w, h, time.Duration(ms+100)*time.Millisecond)
		for i := range a {
			dx := abs(b[i][0] - a[i][0])
			dy := abs(b[i][1] - a[i][1])
			// Wrapping round the edge is a jump of the screen's size, not a step.
			if dx == w-1 {
				dx = 1
			}
			if dy == h-1 {
				dy = 1
			}
			if dx > 1 || dy > 1 {
				t.Fatalf("dot %d jumped (%d,%d) cells between %dms and %dms", i, dx, dy, ms, ms+100)
			}
		}
	}
}

func equalPositions(a, b [][2]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// A panel that all but fills the screen leaves no sky: only the panel is drawn.
func TestStageDrawsNoDotsWithoutRoom(t *testing.T) {
	const w, h = 30, 12
	panel := widget.Rect{X: 0, Y: 0, W: w, H: h}
	if StageHasRoom(w, h, panel) {
		t.Fatal("a panel filling the screen still reports room for dots")
	}
	sc := stageScreen(t, toks(), w, h, StageModel{Panel: panel, Fade: 1})
	for y := range sc {
		for x := range sc[y] {
			if isDot(sc[y][x]) {
				t.Fatalf("a dot at (%d,%d) with no room for the field", x, y)
			}
		}
	}
}

// Monochrome terminals draw the same glyphs: the dots are characters, not colour.
func TestStageDotsSurviveMonochrome(t *testing.T) {
	const w, h = 100, 30
	mono := theme.NewTokens(theme.Ink, theme.ColorMono, false)
	sc := stageScreen(t, mono, w, h, StageModel{Panel: LoginPanelRect(w, h), Fade: 1})
	n := 0
	for y := range sc {
		for x := range sc[y] {
			if isDot(sc[y][x]) {
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("a monochrome stage has no dots")
	}
}
