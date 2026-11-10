package anim

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// Depth is how much colour the terminal can show, which decides how a shimmer is
// drawn. It mirrors theme.ColorCapacity without importing it, so this package
// stays free of the theme dependency.
type Depth int

const (
	// DepthMono has no usable brightness steps: a gradient cannot be shown.
	DepthMono Depth = iota
	// Depth256 has a limited palette: brightness is quantised to a few steps.
	Depth256
	// DepthTrue can blend smoothly.
	DepthTrue
)

// Levels is how many brightness steps a 256-colour terminal gets. Five is enough
// to read as a moving highlight; more would just alias onto the same palette
// entries (design.md decision 5).
const Levels = 5

// Blend mixes two colours by t in 0..1.
//
// It works on RGB, which is exact on a true-colour terminal. Colours that are not
// RGB (the terminal default, palette indices) are returned unblended, since there
// is nothing meaningful to interpolate.
func Blend(from, to tcell.Color, t float64) tcell.Color {
	if t <= 0 {
		return from
	}
	if t >= 1 {
		return to
	}
	if !from.IsRGB() || !to.IsRGB() {
		if t >= 0.5 {
			return to
		}
		return from
	}
	fr, fg, fb := from.RGB()
	tr, tg, tb := to.RGB()
	mix := func(a, b int32) int32 {
		return int32(float64(a) + (float64(b)-float64(a))*t + 0.5)
	}
	return tcell.NewRGBColor(mix(fr, tr), mix(fg, tg), mix(fb, tb))
}

// Quantise snaps a 0..1 brightness to one of Levels discrete values.
func Quantise(b float64) float64 {
	if b <= 0 {
		return 0
	}
	if b >= 1 {
		return 1
	}
	step := 1.0 / float64(Levels-1)
	n := int(b/step + 0.5)
	return float64(n) * step
}

// ShimmerColor returns the foreground colour of one cell of a shimmering line.
//
// The result is the resting colour with the highlight blended in. On a 256-colour
// terminal the blend is quantised; on a mono terminal it returns the resting
// colour unchanged, because a mono terminal cannot show brightness at all and
// "interpolating" would produce noise. Mono gets a position marker instead, see
// ShimmerMarker.
func ShimmerColor(rest, highlight tcell.Color, brightness float64, d Depth) tcell.Color {
	switch d {
	case DepthMono:
		return rest
	case Depth256:
		return Blend(rest, highlight, Quantise(brightness))
	default:
		return Blend(rest, highlight, brightness)
	}
}

// ShimmerMarker returns the index of the single cell to emphasise on a mono
// terminal, or -1 when the band is at rest. The marker is simply the cell nearest
// the band's centre, so the sweep is still visible as a moving point.
func (s Shimmer) ShimmerMarker(n int, t time.Duration) int {
	c, on := s.Center(n, t)
	if !on {
		return -1
	}
	i := int(c + 0.5)
	if i < 0 || i >= n {
		return -1
	}
	return i
}
