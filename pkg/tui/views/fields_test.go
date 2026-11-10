package views

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/theme"
)

// bgAt returns the background colour of one cell.
func bgAt(s tcell.SimulationScreen, x, y int) tcell.Color {
	s.Show()
	_, _, st, _ := s.GetContent(x, y)
	_, bg, _ := st.Decompose()
	return bg
}

// fieldExtent returns the first and last column of a row whose background is bg,
// scanning the whole screen. ok is false if no cell on that row has it.
func fieldExtent(s tcell.SimulationScreen, y int, bg tcell.Color) (first, last int, ok bool) {
	first, last = -1, -1
	for x := 0; x < panelW; x++ {
		if bgAt(s, x, y) == bg {
			if first < 0 {
				first = x
			}
			last = x
		}
	}
	return first, last, first >= 0
}

// ---------- 2.1 / 2.2 full-width backgrounds ----------

// A two-character name must not shorten the background: the username box is as
// wide as the password box beneath it, and their edges line up.
func TestLoginFieldsFillWidthWhateverTheContent(t *testing.T) {
	tk := distinctToks()
	tk.Input = tcell.NewRGBColor(5, 5, 5)
	tk.InputFocus = tcell.NewRGBColor(40, 60, 90)

	s := renderLogin(t, tk, LoginModel{
		Username: "ab", Password: "", Focus: LoginFocusSubmit,
	}, i18n.ZH)

	uy := rowOf(s, 'a')
	if uy < 0 {
		t.Fatalf("username not found:\n%s", screenString(s))
	}
	py := uy + 2 // password row sits two below the username row
	uf, ul, uok := fieldExtent(s, uy, tk.Input)
	pf, pl, pok := fieldExtent(s, py, tk.Input)
	if !uok || !pok {
		t.Fatalf("a field has no input background (user=%v pass=%v):\n%s", uok, pok, screenString(s))
	}
	if uf != pf || ul != pl {
		t.Fatalf("username box spans %d..%d but password box spans %d..%d", uf, ul, pf, pl)
	}
	if ul-uf+1 < 20 {
		t.Fatalf("username box is only %d cells wide: the fill is following the text", ul-uf+1)
	}
}

// An empty field is still visibly a field.
func TestEmptyFieldStillHasBackground(t *testing.T) {
	tk := distinctToks()
	tk.Input = tcell.NewRGBColor(5, 5, 5)

	s := renderLogin(t, tk, LoginModel{Focus: LoginFocusSubmit}, i18n.ZH)
	found := 0
	for y := 0; y < panelH; y++ {
		if _, _, ok := fieldExtent(s, y, tk.Input); ok {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("found %d rows with an input background, want 2 (username and password):\n%s", found, screenString(s))
	}
}

// The focused field is a different colour from the resting one.
func TestFocusedFieldUsesFocusBackground(t *testing.T) {
	tk := distinctToks()
	tk.Input = tcell.NewRGBColor(5, 5, 5)
	tk.InputFocus = tcell.NewRGBColor(40, 60, 90)

	s := renderLogin(t, tk, LoginModel{Username: "ab", Focus: LoginFocusUsername}, i18n.ZH)
	uy := rowOf(s, 'a')
	if _, _, ok := fieldExtent(s, uy, tk.InputFocus); !ok {
		t.Fatalf("focused username does not use the focus background:\n%s", screenString(s))
	}
	if _, _, ok := fieldExtent(s, uy+2, tk.Input); !ok {
		t.Fatalf("unfocused password does not use the resting background:\n%s", screenString(s))
	}
	if !strings.ContainsRune(screenString(s), '█') {
		t.Fatalf("focused field has no caret:\n%s", screenString(s))
	}
}

func renderRegisterForm(t *testing.T, tk theme.Tokens, m RegisterModel) tcell.SimulationScreen {
	t.Helper()
	s := newScreen(t, panelW, panelH)
	Dialog{T: tk}.RenderRegister(s, panelW, panelH, m, i18n.ZH)
	s.Show()
	return s
}

func renderRecoverForm(t *testing.T, tk theme.Tokens, m RecoverModel) tcell.SimulationScreen {
	t.Helper()
	s := newScreen(t, panelW, panelH)
	Dialog{T: tk}.RenderRecover(s, panelW, panelH, m, i18n.ZH)
	s.Show()
	return s
}

// extents collects every row's input-background span, either colour.
func extents(s tcell.SimulationScreen, tk theme.Tokens) [][2]int {
	var out [][2]int
	for y := 0; y < panelH; y++ {
		f, l, ok := fieldExtent(s, y, tk.Input)
		if f2, l2, ok2 := fieldExtent(s, y, tk.InputFocus); ok2 {
			f, l, ok = f2, l2, true
		}
		if ok {
			out = append(out, [2]int{f, l})
		}
	}
	return out
}

func allSame(t *testing.T, what string, sp [][2]int, wantRows int) {
	t.Helper()
	if len(sp) != wantRows {
		t.Fatalf("%s: found %d field rows, want %d: %v", what, len(sp), wantRows, sp)
	}
	for _, e := range sp[1:] {
		if e != sp[0] {
			t.Fatalf("%s: field extents differ: %v", what, sp)
		}
	}
}

// Registration: four fields of very different content, one shared extent.
func TestRegisterFieldsAreTheSameWidth(t *testing.T) {
	tk := distinctToks()
	tk.Input = tcell.NewRGBColor(5, 5, 5)
	tk.InputFocus = tcell.NewRGBColor(40, 60, 90)

	s := renderRegisterForm(t, tk, RegisterModel{
		Username: "ab", Password: "x", Question: "", Answer: "a much longer answer than the others", Field: 0,
	})
	allSame(t, "register", extents(s, tk), 4)
}

// Recovery: every step's fields share an extent, including the two-field step.
func TestRecoverFieldsAreTheSameWidth(t *testing.T) {
	tk := distinctToks()
	tk.Input = tcell.NewRGBColor(5, 5, 5)
	tk.InputFocus = tcell.NewRGBColor(40, 60, 90)

	s := renderRecoverForm(t, tk, RecoverModel{Step: RecoverStepUsername, Username: "ab"})
	allSame(t, "recover step 1", extents(s, tk), 1)

	s = renderRecoverForm(t, tk, RecoverModel{Step: RecoverStepAnswer, Question: "Q?", Answer: "a"})
	allSame(t, "recover step 2", extents(s, tk), 1)

	s = renderRecoverForm(t, tk, RecoverModel{Step: RecoverStepPassword, NewPass: "abc", Confirm: "", Field: 0})
	allSame(t, "recover step 3", extents(s, tk), 2)
}

// The new-channel and DM prompts use the same field, so they fill too.
func TestPromptFieldFillsWidth(t *testing.T) {
	tk := distinctToks()
	tk.Input = tcell.NewRGBColor(5, 5, 5)
	tk.InputFocus = tcell.NewRGBColor(40, 60, 90)

	s := newScreen(t, panelW, panelH)
	Dialog{T: tk}.RenderPrompt(s, panelW, panelH, PromptModel{Title: "t", Label: "name", Value: "ab"}, i18n.ZH)
	s.Show()
	sp := extents(s, tk)
	if len(sp) != 1 {
		t.Fatalf("prompt: %d field rows, want 1: %v", len(sp), sp)
	}
	if w := sp[0][1] - sp[0][0] + 1; w < 15 {
		t.Fatalf("prompt field is only %d cells wide with a 2-character value", w)
	}
}

// ---------- 2.3 overflow ----------

// Content wider than the box shows its tail and does not move the box's edges or
// spill over the border.
func TestFieldOverflowKeepsTailAndEdges(t *testing.T) {
	tk := distinctToks()
	tk.Input = tcell.NewRGBColor(5, 5, 5)
	tk.InputFocus = tcell.NewRGBColor(40, 60, 90)

	long := strings.Repeat("abcdefghij", 8) + "TAIL"
	s := renderLogin(t, tk, LoginModel{Username: long, Focus: LoginFocusUsername}, i18n.ZH)
	short := renderLogin(t, tk, LoginModel{Username: "ab", Focus: LoginFocusUsername}, i18n.ZH)

	uy := rowOf(s, 'T')
	if uy < 0 {
		t.Fatalf("the tail of the content is not visible:\n%s", screenString(s))
	}
	lf, ll, _ := fieldExtent(s, uy, tk.InputFocus)
	sf, sl, _ := fieldExtent(short, rowOf(short, 'a'), tk.InputFocus)
	if lf != sf || ll != sl {
		t.Fatalf("overflow moved the box edges: %d..%d vs %d..%d", lf, ll, sf, sl)
	}
	// Nothing leaks past the right edge of the box onto the panel border.
	if r, _, _, _ := s.GetContent(ll+1, uy); r == 'T' || r == 'L' {
		t.Fatalf("content spilled past the field: %q", r)
	}
	// The label beside it survives.
	if !strings.Contains(rowText(s, uy, panelW), i18n.LoginUsername.Get(i18n.ZH)) {
		t.Fatalf("overflow overwrote the label:\n%s", screenString(s))
	}
}

func TestTailFit(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abcdef", 3, "def"},
		{"abc", 10, "abc"},
		{"", 5, ""},
		{"你好世界", 4, "世界"},
		{"你好世界", 5, "世界"}, // a wide glyph that would straddle the edge is dropped, not split
		{"ab你", 3, "b你"},
	}
	for _, c := range cases {
		if got := tailFit(c.in, c.w); got != c.want {
			t.Errorf("tailFit(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
}

// ---------- 2.4 mono ----------

// With no colour to show a field's extent, both ends are marked by glyphs, and
// every field in the form is still the same width.
func TestMonoFieldsAreMarkedByEdgeGlyphs(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorMono, true)
	s := renderLogin(t, tk, LoginModel{Username: "ab", Focus: LoginFocusSubmit}, i18n.ZH)

	var spans [][2]int
	for y := 0; y < panelH; y++ {
		// Scan by screen cell, not by string index: a wide glyph in the label
		// ("用户名") is one rune but two cells, so indexing the row text would put
		// the two rows' columns a cell apart even though the boxes are aligned.
		open, close := -1, -1
		for x := 0; x < panelW; x++ {
			r, _, _, _ := s.GetContent(x, y)
			if r == '[' && open < 0 {
				open = x
			}
			if r == ']' {
				close = x
			}
		}
		if open >= 0 && close > open {
			spans = append(spans, [2]int{open, close})
		}
	}
	if len(spans) != 2 {
		t.Fatalf("found %d bracketed fields, want 2:\n%s", len(spans), screenString(s))
	}
	if spans[0] != spans[1] {
		t.Fatalf("mono fields differ in extent: %v", spans)
	}
}
