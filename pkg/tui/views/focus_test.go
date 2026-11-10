package views

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// cellFg reads the foreground colour of one cell.
func cellFg(s tcell.SimulationScreen, x, y int) tcell.Color {
	s.Show()
	_, _, st, _ := s.GetContent(x, y)
	fg, _, _ := st.Decompose()
	return fg
}

// renderFocusFrame draws the list and transcript headers for a given focus.
func renderFocusFrame(t *testing.T, focus client.Focus, tk theme.Tokens) (tcell.SimulationScreen, widget.Rect) {
	t.Helper()
	const w, h = 84, 16
	s := newScreen(t, w, h)
	st := client.NewAppState()
	st.State = client.StateReady
	cv := st.EnsureConversation("c1")
	cv.Name, cv.Kind = "general", protocol.KindChannel
	st.ActiveID = "c1"

	content := Chrome{T: tk}.Frame(s, st, i18n.ZH, w, h)
	List{T: tk}.Render(s, SidebarRect(content), ListModel{
		Conversations: st.VisibleConversations(), ActiveID: "c1", Focus: focus,
	}, i18n.ZH)
	main := MainRect(content)
	tr, _ := ComposerRect(main, 1)
	Transcript{T: tk}.Render(s, tr, TranscriptModel{Conversation: st.Active(), Focus: focus}, i18n.ZH)
	return s, content
}

// The region with the keyboard is lit and the other recedes, so the focused one
// stands out by contrast and not only by its marker.
func TestTitleColourFollowsFocus(t *testing.T) {
	tk := toks()

	for _, c := range []struct {
		focus            client.Focus
		listLit, mainLit bool
	}{
		{client.FocusList, true, false},
		{client.FocusInput, false, true},
	} {
		s, content := renderFocusFrame(t, c.focus, tk)
		y := content.Y
		listFg := cellFg(s, SidebarRect(content).X, y)
		mainFg := cellFg(s, MainRect(content).X, y)

		if (listFg == tk.Accent) != c.listLit {
			t.Errorf("focus %v: list title lit = %v, want %v", c.focus, listFg == tk.Accent, c.listLit)
		}
		if (mainFg == tk.Accent) != c.mainLit {
			t.Errorf("focus %v: chat title lit = %v, want %v", c.focus, mainFg == tk.Accent, c.mainLit)
		}
	}
}

// The marker is a glyph, not a colour, so it survives a monochrome terminal, a
// greyscale screenshot, and a colour-blind reader.
func TestFocusMarkerIsAGlyphNotJustColour(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorMono, true)
	s := newScreen(t, 84, 16)
	content := ContentRect(84, 16)

	FocusOverlay{T: tk}.Render(s, content, FocusModel{
		Focus: client.FocusInput, MarkerX: float64(MainRect(content).X),
	}, i18n.ZH)

	x := int(float64(MainRect(content).X)+0.5) - 1
	s.Show()
	r, _, _, _ := s.GetContent(x, content.Y)
	if r != FocusMarkerRune {
		t.Fatalf("marker cell holds %q, want %q even without colour", r, FocusMarkerRune)
	}
}

// Without Unicode box support the marker must fall back to plain ASCII.
func TestFocusMarkerFallsBackToASCII(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorTrue, false)
	s := newScreen(t, 84, 16)
	content := ContentRect(84, 16)

	FocusOverlay{T: tk}.Render(s, content, FocusModel{
		Focus: client.FocusList, MarkerX: float64(SidebarRect(content).X),
	}, i18n.ZH)

	x := int(float64(SidebarRect(content).X)+0.5) - 1
	s.Show()
	r, _, _, _ := s.GetContent(x, content.Y)
	if r != FocusMarkerASCII {
		t.Fatalf("marker cell holds %q, want the ASCII fallback %q", r, FocusMarkerASCII)
	}
}

// The overlay must never overwrite a title character: the marker lives in the
// gutter to the left of the text. Re-writing cells once truncated the CJK title.
func TestFocusOverlayDoesNotEraseTitleText(t *testing.T) {
	tk := toks()
	s, content := renderFocusFrame(t, client.FocusList, tk)
	FocusOverlay{T: tk}.Render(s, content, FocusModel{
		Focus: client.FocusList, MarkerX: float64(SidebarRect(content).X),
	}, i18n.ZH)

	row := rowText(s, content.Y, 84)
	if !strings.Contains(row, "会话") {
		t.Fatalf("list title was erased by the overlay: %q", row)
	}
	if !strings.Contains(row, "general") {
		t.Fatalf("chat title was erased by the overlay: %q", row)
	}
}

// The divider is deliberately neutral: it is one cell wide and so has no left or
// right side to colour (design.md decision 8). Pin that so it is not reintroduced
// as decoration that carries no information.
func TestDividerColourDoesNotChangeWithFocus(t *testing.T) {
	tk := toks()
	var colours []tcell.Color
	for _, f := range []client.Focus{client.FocusList, client.FocusInput} {
		s := newScreen(t, 84, 16)
		content := ContentRect(84, 16)
		DrawDivider(s, tk, content)
		FocusOverlay{T: tk}.Render(s, content, FocusModel{Focus: f, MarkerX: 2}, i18n.ZH)
		x := content.X + layout.GutterX + layout.SidebarWidth(content.W) + 1
		colours = append(colours, cellFg(s, x, content.Y+4))
	}
	if colours[0] != colours[1] {
		t.Fatalf("divider colour changed with focus: %v vs %v", colours[0], colours[1])
	}
}

// ---------- composer shimmer ----------

// columnOf returns the screen column where needle starts in a rendered row.
//
// strings.Index gives a byte offset, and box-drawing glyphs are three bytes but
// one column, so using it directly as a column reads the wrong cells. Counting
// display width of everything before the match gives the real column.
func columnOf(row, needle string) int {
	idx := strings.Index(row, needle)
	if idx < 0 {
		return -1
	}
	return layout.StringWidth(row[:idx])
}

// hintColours renders the empty composer and returns the colour of each glyph of
// the hint, in order.
func hintColours(t *testing.T, elapsed time.Duration, depth anim.Depth) []tcell.Color {
	t.Helper()
	tk := toks()
	s := newScreen(t, 70, 8)
	hint := i18n.ComposeHint.Get(i18n.EN)

	Composer{T: tk}.Render(s, widget.Rect{X: 0, Y: 0, W: 70, H: 5}, ComposerModel{
		Focus: client.FocusInput, Placeholder: hint, Shimmer: true,
		Elapsed: elapsed, Depth: depth,
	}, i18n.EN)

	s.Show()
	var out []tcell.Color
	for y := 0; y < 5; y++ {
		row := rowText(s, y, 70)
		if col := columnOf(row, hint); col >= 0 {
			for i := 0; i < len(hint); i++ {
				out = append(out, cellFg(s, col+i, y))
			}
			return out
		}
	}
	t.Fatalf("hint %q not found on screen", hint)
	return nil
}

// brightness reports how far a colour is from rest towards the highlight, 0..1.
func brightness(c, rest, hi tcell.Color) float64 {
	cr, _, _ := c.RGB()
	rr, _, _ := rest.RGB()
	hr, _, _ := hi.RGB()
	if hr == rr {
		return 0
	}
	return float64(cr-rr) / float64(hr-rr)
}

// A frozen moment must produce a bell of brightness: the glyphs under the band
// are lit and the ones beyond it are at rest.
func TestShimmerPaintsABellAcrossTheHint(t *testing.T) {
	cols := hintColours(t, 750*time.Millisecond, anim.DepthTrue)
	if len(cols) < 17 {
		t.Fatalf("read %d glyph colours", len(cols))
	}
	tk := toks()
	rest := tk.TextFaint

	distinct := map[tcell.Color]bool{}
	for _, c := range cols {
		distinct[c] = true
	}
	if len(distinct) < 4 {
		t.Fatalf("only %d distinct colours: a band should show a smooth gradient", len(distinct))
	}

	// The brightest glyph should be in the interior, not at either end.
	at := 0
	best := brightness(cols[0], rest, tk.Accent)
	for i, c := range cols {
		if b := brightness(c, rest, tk.Accent); b > best {
			best, at = b, i
		}
	}
	if at == 0 || at == len(cols)-1 {
		t.Fatalf("brightest glyph at the edge (%d): the band is not inside the text", at)
	}
	if best < 0.9 {
		t.Fatalf("peak brightness %.2f, want close to 1", best)
	}
}

// During the rest period the hint is plain resting colour: the pause is what makes
// the shimmer breathe.
func TestShimmerIsDarkDuringRest(t *testing.T) {
	cols := hintColours(t, anim.DefaultShimmer.Travel+300*time.Millisecond, anim.DepthTrue)
	rest := toks().TextFaint
	for i, c := range cols {
		if c != rest {
			t.Fatalf("glyph %d is lit during the rest period: %v", i, c)
		}
	}
}

// Brightness moves, the text does not: frames at different times must show
// identical characters in identical cells (spec tui-design-system / 动画不改变布局).
func TestShimmerNeverMovesTheText(t *testing.T) {
	render := func(el time.Duration) string {
		tk := toks()
		s := newScreen(t, 70, 8)
		Composer{T: tk}.Render(s, widget.Rect{X: 0, Y: 0, W: 70, H: 5}, ComposerModel{
			Focus: client.FocusInput, Placeholder: i18n.ComposeHint.Get(i18n.EN),
			Shimmer: true, Elapsed: el, Depth: anim.DepthTrue,
		}, i18n.EN)
		var b strings.Builder
		for y := 0; y < 5; y++ {
			b.WriteString(rowText(s, y, 70))
			b.WriteByte('\n')
		}
		return b.String()
	}
	a := render(100 * time.Millisecond)
	b := render(900 * time.Millisecond)
	c := render(2300 * time.Millisecond)
	if a != b || b != c {
		t.Fatalf("text layout changed between frames:\n%s\n---\n%s", a, b)
	}
}

// A 256-colour terminal quantises the band to a handful of steps.
func TestShimmerQuantisesOn256Colours(t *testing.T) {
	cols := hintColours(t, 750*time.Millisecond, anim.Depth256)
	distinct := map[tcell.Color]bool{}
	for _, c := range cols {
		distinct[c] = true
	}
	// Levels brightness steps, and level 0 is the resting colour itself, so the
	// hint can show at most Levels distinct colours in total.
	if len(distinct) > anim.Levels {
		t.Fatalf("256-colour band used %d colours, want at most %d", len(distinct), anim.Levels)
	}
}

// Mono cannot show brightness, so the band becomes a single moving mark.
func TestShimmerOnMonoMarksOneCell(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorMono, true)
	s := newScreen(t, 70, 8)
	hint := i18n.ComposeHint.Get(i18n.EN)
	Composer{T: tk}.Render(s, widget.Rect{X: 0, Y: 0, W: 70, H: 5}, ComposerModel{
		Focus: client.FocusInput, Placeholder: hint, Shimmer: true,
		Elapsed: 750 * time.Millisecond, Depth: anim.DepthMono,
	}, i18n.EN)

	s.Show()
	underlined := 0
	for y := 0; y < 5; y++ {
		row := rowText(s, y, 70)
		col := columnOf(row, hint)
		if col < 0 {
			continue
		}
		for i := 0; i < len(hint); i++ {
			_, _, st, _ := s.GetContent(col+i, y)
			if _, _, attrs := st.Decompose(); attrs&tcell.AttrUnderline != 0 {
				underlined++
			}
		}
	}
	if underlined != 1 {
		t.Fatalf("mono mode marked %d cells, want exactly 1", underlined)
	}
}

// Without focus the hint is plain, so an unfocused composer costs no animation.
func TestShimmerOffWithoutFocus(t *testing.T) {
	tk := toks()
	s := newScreen(t, 70, 8)
	hint := i18n.ComposeHint.Get(i18n.EN)
	Composer{T: tk}.Render(s, widget.Rect{X: 0, Y: 0, W: 70, H: 5}, ComposerModel{
		Focus: client.FocusList, Placeholder: hint, Shimmer: true,
		Elapsed: 750 * time.Millisecond, Depth: anim.DepthTrue,
	}, i18n.EN)
	s.Show()

	distinct := map[tcell.Color]bool{}
	for y := 0; y < 5; y++ {
		row := rowText(s, y, 70)
		if col := columnOf(row, hint); col >= 0 {
			for i := 0; i < len(hint); i++ {
				distinct[cellFg(s, col+i, y)] = true
			}
		}
	}
	if len(distinct) != 1 {
		t.Fatalf("an unfocused hint shows %d colours, want one flat colour", len(distinct))
	}
}
