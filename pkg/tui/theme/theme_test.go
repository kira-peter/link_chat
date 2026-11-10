package theme

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// luminance is the WCAG relative luminance of an RGB colour, used to check that a
// border actually shows against the page.
func luminance(c tcell.Color) float64 {
	r, g, b := c.RGB()
	lin := func(v int32) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func contrast(a, b tcell.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Both themes must give the two sides of a conversation different border colours,
// each visible against the page.
func TestBubbleTokensDifferAndShow(t *testing.T) {
	for name, p := range Registry {
		if p.BubbleSelf == p.BubbleOther {
			t.Errorf("%s: both sides share one border colour", name)
		}
		for side, c := range map[string]tcell.Color{"self": p.BubbleSelf, "other": p.BubbleOther} {
			if cr := contrast(c, p.Bg); cr < 3 {
				t.Errorf("%s: %s border contrast %.2f against the page is below 3", name, side, cr)
			}
		}
	}
}

// A field must be visible as a field: both its states differ from the dialog
// surface and from each other.
func TestInputTokensDifferFromSurface(t *testing.T) {
	for name, p := range Registry {
		if p.Input == p.InputFocus {
			t.Errorf("%s: resting and focused fields look the same", name)
		}
		if p.Input == p.Surface || p.InputFocus == p.Surface {
			t.Errorf("%s: a field blends into the dialog surface", name)
		}
	}
}

// 256-colour terminals keep the two sides apart.
func TestBubbleTokensSurvive256(t *testing.T) {
	for name, p := range Registry {
		tk := NewTokens(p, Color256, true)
		if tk.BubbleSelf == tk.BubbleOther {
			t.Errorf("%s: 256-colour borders collapsed to one colour", name)
		}
		if tk.Input == tk.InputFocus {
			t.Errorf("%s: 256-colour fields collapsed to one colour", name)
		}
	}
}

// Mono: two greys, and the line style differs so the sides stay distinguishable.
func TestBubbleTokensInMono(t *testing.T) {
	for name, p := range Registry {
		tk := NewTokens(p, ColorMono, true)
		if tk.BubbleSelf == tk.BubbleOther {
			t.Errorf("%s: mono borders are the same grey", name)
		}
		if !tk.BubbleDashed() {
			t.Errorf("%s: mono should tell the sides apart by line style too", name)
		}
		if !tk.FieldEdges() {
			t.Errorf("%s: mono fields need edge glyphs, there is no background to show them", name)
		}
	}
	if NewTokens(Ink, ColorTrue, true).BubbleDashed() {
		t.Error("a colour terminal does not need dashed borders")
	}
	if NewTokens(Ink, ColorTrue, true).FieldEdges() {
		t.Error("a colour terminal does not need field edge glyphs")
	}
}

func TestInputStylesCarryTheirBackground(t *testing.T) {
	tk := NewTokens(Ink, ColorTrue, true)
	_, bg, _ := tk.InputStyle().Decompose()
	if bg != Ink.Input {
		t.Errorf("InputStyle background = %v, want %v", bg, Ink.Input)
	}
	_, bg, _ = tk.InputFocusStyle().Decompose()
	if bg != Ink.InputFocus {
		t.Errorf("InputFocusStyle background = %v, want %v", bg, Ink.InputFocus)
	}
}
