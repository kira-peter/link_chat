package views

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// renderStyled is renderBubbles with a layout.
func renderStyled(t *testing.T, tk theme.Tokens, lay Layout) tcell.SimulationScreen {
	t.Helper()
	s := newScreen(t, bubW, bubH)
	Transcript{T: tk}.Render(s, widget.Rect{X: 0, Y: 0, W: bubW, H: bubH}, TranscriptModel{
		Conversation: twoSided(), UserID: me, Now: at(23, 0, 0), Focus: client.FocusInput, Layout: lay,
	}, i18n.ZH)
	s.Show()
	return s
}

func fgOf(s tcell.SimulationScreen, x, y int) tcell.Color {
	_, _, st, _ := s.GetContent(x, y)
	fg, _, _ := st.Decompose()
	return fg
}

// With the border off every message is its text alone, still on its own side
// and in its side's colour (spec tui-settings / 消息样式).
func TestMessagesWithoutBorders(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorTrue, true)
	lay := DefaultLayout
	lay.NoBorder = true
	s := renderStyled(t, tk, lay)
	if strings.ContainsAny(screenString(s), "┌┐└┘") {
		t.Fatalf("a border is still drawn:\n%s", screenString(s))
	}
	tx, ty, ok1 := findText(s, "THEIRS")
	mx, my, ok2 := findText(s, "MINE")
	if !ok1 || !ok2 || tx >= mx {
		t.Fatalf("the sides are lost without borders:\n%s", screenString(s))
	}
	if fgOf(s, tx, ty) != tk.BubbleOther || fgOf(s, mx, my) != tk.BubbleSelf {
		t.Error("without borders the text is not in its side's colour")
	}
}

// With the time off the bottom border carries no time; the time line between
// groups stays.
func TestMessagesWithoutTime(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorTrue, true)
	lay := DefaultLayout
	lay.NoTime = true
	s := renderStyled(t, tk, lay)
	clock := i18n.ClockOnly(at(10, 0, 0))
	_, y, ok := findText(s, "THEIRS")
	if !ok {
		t.Fatal("no bubble")
	}
	if bottom := rowText(s, y+1, bubW); strings.Contains(bottom, clock) {
		t.Fatalf("the time is still on the border: %q", bottom)
	}
	if strings.Count(screenString(s), clock) != 1 {
		t.Fatalf("the time line between groups is gone, or the time is elsewhere:\n%s", screenString(s))
	}
}

// Each side's text and border take the chosen colours; "default" is the theme's.
func TestMessageColours(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorTrue, true)
	lay := DefaultLayout
	lay.SelfText, lay.OtherText, lay.SelfBorder, lay.OtherBorder = "amber", "green", "rose", "blue"
	s := renderStyled(t, tk, lay)

	want := func(name string) tcell.Color { return tcell.NewHexColor(messageColorHex[name]) }
	tx, ty, _ := findText(s, "THEIRS")
	mx, my, _ := findText(s, "MINE")
	if fgOf(s, tx, ty) != want("green") || fgOf(s, mx, my) != want("amber") {
		t.Errorf("text colours %v / %v, want green / amber", fgOf(s, tx, ty), fgOf(s, mx, my))
	}
	if borderFg(t, s, "THEIRS") != want("blue") || borderFg(t, s, "MINE") != want("rose") {
		t.Errorf("border colours %v / %v, want blue / rose", borderFg(t, s, "THEIRS"), borderFg(t, s, "MINE"))
	}

	// Defaults: body text, the theme's side colours.
	d := renderStyled(t, tk, DefaultLayout)
	tx, ty, _ = findText(d, "THEIRS")
	if fgOf(d, tx, ty) != tk.Text || borderFg(t, d, "MINE") != tk.BubbleSelf {
		t.Error("the default colours are not the theme's")
	}
}

// A monochrome terminal has no colours to show: the theme's greys and line
// styles stay whatever is chosen.
func TestMessageColoursIgnoredInMono(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorMono, true)
	lay := DefaultLayout
	lay.SelfBorder, lay.OtherBorder = "rose", "blue"
	s := renderStyled(t, tk, lay)
	if borderFg(t, s, "THEIRS") != tk.BubbleOther || borderFg(t, s, "MINE") != tk.BubbleSelf {
		t.Error("mono shows a chosen colour")
	}
}

// Every colour choice has a name in both languages, and "default" comes first.
func TestMessageColourNames(t *testing.T) {
	if MessageColors[0] != "default" {
		t.Fatal("the default colour is not the first choice")
	}
	seen := map[string]bool{}
	for _, c := range MessageColors {
		for _, l := range []i18n.Lang{i18n.ZH, i18n.EN} {
			n := MessageColorName(c, l)
			if n == "" || (c != "default" && n == MessageColorName("default", l)) || seen[string(l)+n] {
				t.Errorf("colour %q has no name of its own in %s: %q", c, l, n)
			}
			seen[string(l)+n] = true
		}
	}
}

// Without borders the sample is shorter than the shortest cut-down preview, yet
// it is whole, so the preview is still drawn — with the messages, minus their
// frames.
func TestPreviewStaysWithoutBorders(t *testing.T) {
	cat := catIndex(t, "消息样式")
	lay := DefaultLayout
	lay.NoBorder = true
	for _, nav := range []SettingsNav{
		{Cat: cat, Level: LevelItem, Item: 0},
		{Cat: cat, Level: LevelChoose, Item: 0, Choice: 1, Original: 0},
	} {
		s := renderSettings(t, toks(), nav, lay)
		text := screenString(s)
		var all strings.Builder
		for y := 0; y < 34; y++ {
			all.WriteString(rowText(s, y, 100))
			all.WriteByte(10)
		}
		text = all.String()
		for _, w := range []string{"预览", "长一点的话", "看起来不错"} {
			if !strings.Contains(text, w) {
				t.Fatalf("level %v: with borders hidden the preview has no %q:\n%s", nav.Level, w, text)
			}
		}
		if _, _, top, _, ok := previewBox(t, s); ok {
			// Inside the preview box no bubble border is drawn.
			if strings.Count(text, "┌") > 1+strings.Count(text, "┌─ 预览") {
				t.Fatalf("level %v: a bubble border is still drawn under row %d:\n%s", nav.Level, top, text)
			}
		}
	}
}
