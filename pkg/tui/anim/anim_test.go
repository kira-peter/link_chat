package anim

import (
	"math"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

const eps = 1e-9

// The band must slide in from beyond the left edge and leave beyond the right, so
// it never pops into existence on a glyph (design.md decision 3).
func TestShimmerEntersAndLeavesOutsideTheText(t *testing.T) {
	s := DefaultShimmer
	const n = 20

	start, on := s.Center(n, 0)
	if !on {
		t.Fatal("band should be active at the start of a pass")
	}
	if start >= 0 {
		t.Fatalf("band starts at %v, want left of the first cell", start)
	}
	// At that moment the first glyph must be effectively dark.
	if b := s.Brightness(0, n, 0); b > 0.05 {
		t.Fatalf("first glyph already lit at pass start: %v", b)
	}

	// Just before the pass ends the centre is right of the last cell.
	end, on := s.Center(n, s.Travel-time.Millisecond)
	if !on {
		t.Fatal("band should still be active just before travel ends")
	}
	if end <= float64(n-1) {
		t.Fatalf("band ends at %v, want right of the last cell %d", end, n-1)
	}
	if b := s.Brightness(n-1, n, s.Travel-time.Millisecond); b > 0.05 {
		t.Fatalf("last glyph still lit as the pass ends: %v", b)
	}
}

// The centre must advance linearly: a constant-speed sweep is what reads as a
// "slide".
func TestShimmerCenterAdvancesLinearly(t *testing.T) {
	s := DefaultShimmer
	const n = 24
	var prev float64
	var step float64
	for i, ms := range []int{100, 300, 500, 700, 900} {
		c, on := s.Center(n, time.Duration(ms)*time.Millisecond)
		if !on {
			t.Fatalf("band inactive at %dms", ms)
		}
		if i > 0 {
			d := c - prev
			if d <= 0 {
				t.Fatalf("band moved backwards: %v -> %v", prev, c)
			}
			if i == 1 {
				step = d
			} else if math.Abs(d-step) > 1e-6 {
				t.Fatalf("speed not constant: step %v then %v", step, d)
			}
		}
		prev = c
	}
}

// During the rest period nothing is lit: the pause is what makes it breathe.
func TestShimmerRestsBetweenPasses(t *testing.T) {
	s := DefaultShimmer
	const n = 16
	mid := s.Travel + s.Pause/2
	if _, on := s.Center(n, mid); on {
		t.Fatal("band visible during the rest period")
	}
	for i := 0; i < n; i++ {
		if b := s.Brightness(i, n, mid); b != 0 {
			t.Fatalf("cell %d lit during rest: %v", i, b)
		}
	}
}

// After a full cycle the pass begins again from the same place.
func TestShimmerLoops(t *testing.T) {
	s := DefaultShimmer
	const n = 18
	a, _ := s.Center(n, 400*time.Millisecond)
	b, _ := s.Center(n, 400*time.Millisecond+s.Cycle())
	c, _ := s.Center(n, 400*time.Millisecond+3*s.Cycle())
	if math.Abs(a-b) > eps || math.Abs(a-c) > eps {
		t.Fatalf("pass does not repeat: %v %v %v", a, b, c)
	}
}

// The profile is a bell: brightest at the centre, falling off symmetrically.
func TestShimmerBrightnessIsBellShaped(t *testing.T) {
	s := Shimmer{Sigma: 2, Travel: time.Second, Pause: time.Second}
	const n = 21
	// Pick a moment where the centre sits exactly on cell 10.
	pad := s.pad()
	frac := (10 + pad) / (float64(n) + 2*pad)
	at := time.Duration(frac * float64(s.Travel))

	peak := s.Brightness(10, n, at)
	if peak < 0.999 {
		t.Fatalf("peak brightness %v, want ~1", peak)
	}
	for d := 1; d <= 5; d++ {
		l := s.Brightness(10-d, n, at)
		r := s.Brightness(10+d, n, at)
		if math.Abs(l-r) > 1e-3 {
			t.Fatalf("asymmetric at distance %d: %v vs %v", d, l, r)
		}
		if d > 1 && l >= s.Brightness(10-(d-1), n, at) {
			t.Fatalf("brightness does not fall off with distance at %d", d)
		}
	}
}

func TestShimmerHandlesDegenerateInput(t *testing.T) {
	s := DefaultShimmer
	if _, on := s.Center(0, 0); on {
		t.Fatal("empty text should have no band")
	}
	if b := s.Brightness(0, 0, 0); b != 0 {
		t.Fatalf("brightness on empty text: %v", b)
	}
	zero := Shimmer{}
	if _, on := zero.Center(10, time.Second); on {
		t.Fatal("zero-config shimmer should be inert, not divide by zero")
	}
}

// ---------- slide ----------

func TestSlideEndpointsAreExact(t *testing.T) {
	now := time.Unix(1000, 0)
	s := NewSlide(2)
	s.MoveTo(30, now, 180*time.Millisecond, true)

	if got := s.At(now); got != 2 {
		t.Fatalf("start = %v, want 2", got)
	}
	if got := s.At(now.Add(180 * time.Millisecond)); got != 30 {
		t.Fatalf("end = %v, want 30", got)
	}
	if got := s.At(now.Add(time.Second)); got != 30 {
		t.Fatalf("after end = %v, want 30", got)
	}
	if !s.Done(now.Add(200 * time.Millisecond)) {
		t.Fatal("slide should be done after its duration")
	}
	if s.Done(now.Add(50 * time.Millisecond)) {
		t.Fatal("slide reports done too early")
	}
}

// Ease-out means most of the distance is covered early.
func TestSlideEasesOut(t *testing.T) {
	now := time.Unix(1000, 0)
	s := NewSlide(0)
	s.MoveTo(100, now, 200*time.Millisecond, true)

	half := s.At(now.Add(100 * time.Millisecond))
	if half <= 50 {
		t.Fatalf("halfway through, position %v: ease-out should be past the midpoint", half)
	}
	// And it must never overshoot.
	for ms := 0; ms <= 200; ms += 10 {
		p := s.At(now.Add(time.Duration(ms) * time.Millisecond))
		if p < 0 || p > 100 {
			t.Fatalf("position %v outside [0,100] at %dms", p, ms)
		}
	}
}

// Retargeting mid-flight must start from where the marker is, not snap back.
func TestSlideRetargetStartsFromCurrentPosition(t *testing.T) {
	now := time.Unix(1000, 0)
	s := NewSlide(0)
	s.MoveTo(100, now, 200*time.Millisecond, true)

	mid := now.Add(80 * time.Millisecond)
	before := s.At(mid)
	s.MoveTo(0, mid, 200*time.Millisecond, true)
	after := s.At(mid)

	if math.Abs(before-after) > eps {
		t.Fatalf("retarget jumped: %v -> %v", before, after)
	}
	if s.Target() != 0 {
		t.Fatalf("target = %v, want 0", s.Target())
	}
	// And it heads back.
	if later := s.At(mid.Add(100 * time.Millisecond)); later >= before {
		t.Fatalf("did not turn around: %v then %v", before, later)
	}
}

func TestSlideDisabledAnimationIsInstant(t *testing.T) {
	now := time.Unix(1000, 0)
	s := NewSlide(0)
	s.MoveTo(50, now, 200*time.Millisecond, false)
	if got := s.At(now); got != 50 {
		t.Fatalf("with animation off, position = %v, want 50 immediately", got)
	}
	if !s.Done(now) {
		t.Fatal("instant move should be done")
	}
}

func TestEaseOutCubicBounds(t *testing.T) {
	if EaseOutCubic(-1) != 0 || EaseOutCubic(0) != 0 {
		t.Fatal("low bound")
	}
	if EaseOutCubic(1) != 1 || EaseOutCubic(2) != 1 {
		t.Fatal("high bound")
	}
	prev := 0.0
	for i := 1; i <= 100; i++ {
		v := EaseOutCubic(float64(i) / 100)
		if v < prev {
			t.Fatalf("not monotonic at %d", i)
		}
		prev = v
	}
}

// ---------- colour ----------

func TestBlendEndpointsAndMidpoint(t *testing.T) {
	a := tcell.NewRGBColor(0, 0, 0)
	b := tcell.NewRGBColor(200, 100, 50)
	if Blend(a, b, 0) != a || Blend(a, b, 1) != b {
		t.Fatal("endpoints not exact")
	}
	r, g, bl := Blend(a, b, 0.5).RGB()
	if r != 100 || g != 50 || bl != 25 {
		t.Fatalf("midpoint = %d,%d,%d", r, g, bl)
	}
}

func TestBlendNonRGBDoesNotPanic(t *testing.T) {
	c := Blend(tcell.ColorDefault, tcell.NewRGBColor(255, 0, 0), 0.7)
	_ = c
}

func TestQuantiseGivesFiveSteps(t *testing.T) {
	seen := map[float64]bool{}
	for i := 0; i <= 200; i++ {
		seen[Quantise(float64(i)/200)] = true
	}
	if len(seen) != Levels {
		t.Fatalf("got %d distinct levels, want %d: %v", len(seen), Levels, seen)
	}
}

// A mono terminal must never receive a blended colour: there are no brightness
// steps to show, and interpolating would produce noise.
func TestShimmerColorByDepth(t *testing.T) {
	rest := tcell.NewRGBColor(90, 100, 110)
	hi := tcell.NewRGBColor(240, 250, 255)

	if got := ShimmerColor(rest, hi, 0.9, DepthMono); got != rest {
		t.Fatal("mono depth blended a colour")
	}

	// True colour varies continuously.
	a := ShimmerColor(rest, hi, 0.31, DepthTrue)
	b := ShimmerColor(rest, hi, 0.33, DepthTrue)
	if a == b {
		t.Fatal("true colour did not distinguish nearby brightnesses")
	}

	// 256 colour collapses nearby values onto the same step.
	c := ShimmerColor(rest, hi, 0.31, Depth256)
	d := ShimmerColor(rest, hi, 0.33, Depth256)
	if c != d {
		t.Fatal("256 colour should quantise nearby brightnesses together")
	}
}

// The mono fallback is a moving point: it must be present during a pass, move
// forward, and be absent at rest.
func TestShimmerMarkerForMono(t *testing.T) {
	s := DefaultShimmer
	const n = 20
	if m := s.ShimmerMarker(n, s.Travel+s.Pause/2); m != -1 {
		t.Fatalf("marker %d during rest", m)
	}
	last := -2
	seenInside := false
	for ms := 0; ms < int(s.Travel/time.Millisecond); ms += 50 {
		m := s.ShimmerMarker(n, time.Duration(ms)*time.Millisecond)
		if m < 0 {
			continue
		}
		seenInside = true
		if m < last {
			t.Fatalf("marker moved backwards: %d -> %d", last, m)
		}
		last = m
	}
	if !seenInside {
		t.Fatal("marker never appeared on the text")
	}
}
