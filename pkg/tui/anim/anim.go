// Package anim holds the animation maths: how bright a glyph is at a given
// moment, where a sliding marker sits part-way through a move.
//
// Everything here is a pure function of its arguments. Time is passed in rather
// than read from a clock, which is what lets a test freeze a moment and assert
// exactly what every cell looks like — and lets the renderer stay a pure
// "state in, cells out" function (design.md decision 3).
package anim

import (
	"math"
	"time"
)

// Shimmer describes a band of light that sweeps across a line of text, the way
// the "slide to unlock" label on an early iPhone did.
//
// The text itself never moves. Only brightness travels: a bell-shaped band
// enters from the left, crosses, leaves on the right, then rests for a moment
// before the next pass. The rest matters — a sweep with no pause reads as
// anxious, a pause reads as breathing (design.md decision 3).
type Shimmer struct {
	// Sigma is the band's width in cells. Too small looks like a dot, too large
	// like the whole line flushing.
	Sigma float64
	// Travel is how long one pass takes.
	Travel time.Duration
	// Pause is the rest between passes.
	Pause time.Duration
}

// DefaultShimmer is tuned to feel like the reference: a soft band, one pass in a
// little over a second, then a short rest.
var DefaultShimmer = Shimmer{
	Sigma:  2.6,
	Travel: 1500 * time.Millisecond,
	Pause:  900 * time.Millisecond,
}

// Cycle is the length of one full pass plus its rest.
func (s Shimmer) Cycle() time.Duration { return s.Travel + s.Pause }

// pad is how far outside the text the band starts and ends, so it slides in from
// beyond the edge instead of popping into existence on the first glyph.
func (s Shimmer) pad() float64 { return 3 * s.Sigma }

// Center returns where the band's middle is at elapsed time t, in cell units
// relative to the start of a line of n cells. The second result is false during
// the rest period, when no band is visible.
func (s Shimmer) Center(n int, t time.Duration) (float64, bool) {
	if n <= 0 || s.Travel <= 0 {
		return 0, false
	}
	cycle := s.Cycle()
	if cycle <= 0 {
		return 0, false
	}
	phase := t % cycle
	if phase < 0 {
		phase += cycle
	}
	if phase >= s.Travel {
		return 0, false
	}
	frac := float64(phase) / float64(s.Travel)
	pad := s.pad()
	return -pad + frac*(float64(n)+2*pad), true
}

// Brightness returns how lit the i-th cell of an n-cell line is at time t, in
// the range 0..1.
//
// The profile is a Gaussian — exp(-d²/2σ²) — because that is what makes the band
// look soft. A hard-edged block of light looks like a cursor, not a shimmer.
func (s Shimmer) Brightness(i, n int, t time.Duration) float64 {
	c, on := s.Center(n, t)
	if !on || s.Sigma <= 0 {
		return 0
	}
	d := float64(i) - c
	return math.Exp(-(d * d) / (2 * s.Sigma * s.Sigma))
}

// Slide moves a value from one position to another with an ease-out curve: fast
// at the start, settling at the end. That is what makes a focus marker feel
// pulled into place rather than dragged at a constant, mechanical speed.
//
// A Slide can be retargeted mid-flight. The new move starts from wherever the
// marker currently is, so mashing the arrow keys does not snap it back to an old
// start before moving on (spec tui-chat-client / 连续切换以最新为准).
type Slide struct {
	from, to float64
	start    time.Time
	dur      time.Duration
	active   bool
}

// NewSlide returns a resting slide at position p.
func NewSlide(p float64) Slide { return Slide{from: p, to: p} }

// MoveTo begins (or redirects) a move to target.
//
// If animation is disabled the move completes instantly, which is the contract
// for the global animation switch.
func (s *Slide) MoveTo(target float64, now time.Time, dur time.Duration, animated bool) {
	cur := s.At(now)
	if !animated || dur <= 0 {
		s.from, s.to, s.active = target, target, false
		return
	}
	if cur == target && !s.active {
		return
	}
	s.from, s.to = cur, target
	s.start, s.dur, s.active = now, dur, true
}

// At returns the position at time now.
func (s Slide) At(now time.Time) float64 {
	if !s.active || s.dur <= 0 {
		return s.to
	}
	t := float64(now.Sub(s.start)) / float64(s.dur)
	if t >= 1 {
		return s.to
	}
	if t <= 0 {
		return s.from
	}
	return s.from + (s.to-s.from)*EaseOutCubic(t)
}

// Done reports whether the slide has settled at its target by time now.
func (s Slide) Done(now time.Time) bool {
	if !s.active {
		return true
	}
	return now.Sub(s.start) >= s.dur
}

// Target returns where the slide is heading.
func (s Slide) Target() float64 { return s.to }

// EaseOutCubic maps 0..1 to 0..1 with a fast start and a slow finish.
func EaseOutCubic(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	u := 1 - t
	return 1 - u*u*u
}

// ButtonShimmer is the sweep used on a focused button.
//
// A button label is only a few cells wide, so the input hint's wide band (σ≈2.6)
// would light the whole word at once and read as a flash rather than a sweep. This
// one is narrower, and — because the caller passes the button's full inner width,
// padding included — the band travels across the empty padding before reaching the
// label and out again, which is what gives it room to look like it is *moving*
// (design.md decision 5).
//
// It is a separate value rather than a tweak to DefaultShimmer so retuning one
// cannot change the other.
var ButtonShimmer = Shimmer{
	Sigma:  1.3,
	Travel: 1100 * time.Millisecond,
	Pause:  700 * time.Millisecond,
}

// ListShimmer is the sweep used on the selected row of the conversation list.
//
// A list row is longer than a button label and only one row at a time is lit, so
// the band is narrow and slow: it should read as a quiet glint passing over the
// name, not as the row blinking. It is its own value so retuning it cannot change
// the button's or the input hint's.
var ListShimmer = Shimmer{
	Sigma:  1.8,
	Travel: 1600 * time.Millisecond,
	Pause:  1400 * time.Millisecond,
}
