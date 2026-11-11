package views

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

const (
	bubW = 60
	bubH = 24
)

// twoSided is a conversation with one message from each side.
func twoSided() *client.Conversation {
	cv := &client.Conversation{}
	cv.ID, cv.Name, cv.Kind = "c1", "general", protocol.KindChannel
	cv.Messages = []protocol.Message{
		{ID: "1", AuthorID: "bob", AuthorName: "bob", Body: "THEIRS", CreatedAt: at(10, 0, 0)},
		{ID: "2", AuthorID: me, AuthorName: "me", Body: "MINE", CreatedAt: at(10, 0, 30)},
	}
	return cv
}

func renderBubbles(t *testing.T, tk theme.Tokens, cv *client.Conversation, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := newScreen(t, w, h)
	Transcript{T: tk}.Render(s, widget.Rect{X: 0, Y: 0, W: w, H: h}, TranscriptModel{
		Conversation: cv, UserID: me, Now: at(23, 0, 0), Focus: client.FocusInput,
	}, i18n.ZH)
	s.Show()
	return s
}

// findText returns the (x, y) of the first cell of the first occurrence of word.
func findText(s tcell.SimulationScreen, word string) (int, int, bool) {
	w, h := s.Size()
	for y := 0; y < h; y++ {
		row := rowText(s, y, w)
		if i := strings.Index(row, word); i >= 0 {
			// rowText skips continuation cells, so convert the byte index back to a
			// column by walking the screen until the cell holds the word's first rune.
			first := []rune(word)[0]
			for x := 0; x < w; x++ {
				if r, _, _, _ := s.GetContent(x, y); r == first {
					return x, y, true
				}
			}
		}
	}
	return 0, 0, false
}

// borderFg reads the colour of the bubble's border cell that sits just left of the
// text (the vertical edge on the text's own row).
func borderFg(t *testing.T, s tcell.SimulationScreen, word string) tcell.Color {
	t.Helper()
	x, y, ok := findText(s, word)
	if !ok {
		t.Fatalf("%q not on screen", word)
	}
	// The text starts one padding cell inside the left border.
	fg, _ := styleAt(s, x-bubblePad-bubbleBorder, y)
	return fg
}

// ---------- 6.5 border colours ----------

// Each side's border is its own token colour, and the two are different.
func TestEachSideUsesItsOwnBorderColour(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorTrue, true)
	s := renderBubbles(t, tk, twoSided(), bubW, bubH)

	theirs, mine := borderFg(t, s, "THEIRS"), borderFg(t, s, "MINE")
	if theirs != tk.BubbleOther {
		t.Errorf("their border is %v, want the other-side token %v", theirs, tk.BubbleOther)
	}
	if mine != tk.BubbleSelf {
		t.Errorf("my border is %v, want the self token %v", mine, tk.BubbleSelf)
	}
	if theirs == mine {
		t.Errorf("both sides share one border colour %v", theirs)
	}
}

// The same holds in the light theme.
func TestBorderColoursInThePaperTheme(t *testing.T) {
	tk := theme.NewTokens(theme.Paper, theme.ColorTrue, true)
	s := renderBubbles(t, tk, twoSided(), bubW, bubH)
	if got := borderFg(t, s, "MINE"); got != tk.BubbleSelf {
		t.Errorf("paper: my border is %v, want %v", got, tk.BubbleSelf)
	}
	if got := borderFg(t, s, "THEIRS"); got != tk.BubbleOther {
		t.Errorf("paper: their border is %v, want %v", got, tk.BubbleOther)
	}
}

// 256 colours keeps the two apart.
func TestBorderColoursIn256(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.Color256, true)
	s := renderBubbles(t, tk, twoSided(), bubW, bubH)
	if borderFg(t, s, "MINE") == borderFg(t, s, "THEIRS") {
		t.Fatal("256-colour borders collapsed to one colour")
	}
}

// ---------- 6.5 no Unicode ----------

// Without box-drawing support the borders are ASCII and nothing else changes.
func TestBubblesFallBackToASCII(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorTrue, false)
	s := renderBubbles(t, tk, twoSided(), bubW, bubH)

	var all strings.Builder
	for y := 0; y < bubH; y++ {
		all.WriteString(rowText(s, y, bubW) + "\n")
	}
	if strings.ContainsAny(all.String(), "┌┐└┘─│┄┆") {
		t.Fatalf("a box-drawing glyph was drawn on a terminal without them:\n%s", all.String())
	}
	if !strings.Contains(all.String(), "+--") || !strings.Contains(all.String(), "| THEIRS") {
		t.Fatalf("no ASCII border found:\n%s", all.String())
	}
	// Sides and text unchanged: theirs on the left edge, mine on the right.
	tx, _, _ := findText(s, "THEIRS")
	mx, _, _ := findText(s, "MINE")
	if tx >= mx {
		t.Fatalf("the sides moved: theirs at column %d, mine at %d", tx, mx)
	}
}

// ---------- 6.5 mono ----------

// With no colour the sides are told apart by line style as well as position: the
// other side is dashed, mine solid.
func TestMonoTellsTheSidesApartByLineStyle(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorMono, true)
	s := renderBubbles(t, tk, twoSided(), bubW, bubH)

	tx, ty, _ := findText(s, "THEIRS")
	mx, my, _ := findText(s, "MINE")
	left, _, _, _ := s.GetContent(tx-bubblePad-bubbleBorder, ty)
	right, _, _, _ := s.GetContent(mx-bubblePad-bubbleBorder, my)

	if left != '┆' {
		t.Errorf("the other side's edge is %q, want a dashed glyph", left)
	}
	if right != '│' {
		t.Errorf("my edge is %q, want a solid glyph", right)
	}
	// And their greys differ.
	if borderFg(t, s, "THEIRS") == borderFg(t, s, "MINE") {
		t.Error("mono borders are the same grey")
	}
}

// A colour terminal never uses the dashed glyphs: they are a mono fallback.
func TestColourTerminalsStaySolid(t *testing.T) {
	s := renderBubbles(t, theme.NewTokens(theme.Ink, theme.ColorTrue, true), twoSided(), bubW, bubH)
	var all strings.Builder
	for y := 0; y < bubH; y++ {
		all.WriteString(rowText(s, y, bubW))
	}
	if strings.ContainsAny(all.String(), "┄┆") {
		t.Fatal("dashed glyphs appeared on a colour terminal")
	}
}

// ---------- the footer ----------

// The time sits on the bottom border, inside the corners, and the border is still
// whole on both sides of it.
func TestFooterIsSetIntoTheBottomBorder(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorTrue, true)
	s := renderBubbles(t, tk, twoSided(), bubW, bubH)
	clock := i18n.ClockOnly(at(10, 0, 0))

	_, y, ok := findText(s, "THEIRS")
	if !ok {
		t.Fatal("no bubble")
	}
	bottom := rowText(s, y+1, bubW)
	if !strings.Contains(bottom, clock) {
		t.Fatalf("the footer %q is not on the bottom border: %q", clock, bottom)
	}
	if !strings.HasPrefix(strings.TrimLeft(bottom, " "), "└") || !strings.Contains(bottom, "┘") {
		t.Fatalf("the bottom border lost its corners: %q", bottom)
	}
}

// An edited message says so on its footer.
func TestEditedMessageSaysSoOnItsFooter(t *testing.T) {
	cv := twoSided()
	cv.Messages[0].EditedAt = at(10, 5, 0)
	s := renderBubbles(t, theme.NewTokens(theme.Ink, theme.ColorTrue, true), cv, bubW, bubH)
	_, y, _ := findText(s, "THEIRS")
	if got := rowText(s, y+1, bubW); !strings.Contains(got, i18n.MessageEdited.Get(i18n.ZH)) {
		t.Fatalf("the edited marker is missing from the footer: %q", got)
	}
}

// ---------- 6.6 very narrow ----------

// Below the threshold the border is dropped and the text stands alone, still
// coloured by side so the speakers stay apart.
func TestNarrowAreaDropsTheBorderButKeepsTheSides(t *testing.T) {
	tk := theme.NewTokens(theme.Ink, theme.ColorTrue, true)
	const w = 20 // under boxlessBelow
	s := renderBubbles(t, tk, twoSided(), w, 16)

	var all strings.Builder
	for y := 0; y < 16; y++ {
		all.WriteString(rowText(s, y, w) + "\n")
	}
	if strings.ContainsAny(all.String(), "┌┐└┘│") {
		t.Fatalf("a border was drawn in a %d-cell area:\n%s", w, all.String())
	}
	tx, ty, ok1 := findText(s, "THEIRS")
	mx, my, ok2 := findText(s, "MINE")
	if !ok1 || !ok2 {
		t.Fatalf("a message is missing:\n%s", all.String())
	}
	if tx >= mx {
		t.Errorf("the sides are not left/right: theirs at %d, mine at %d", tx, mx)
	}
	if tf, _ := styleAt(s, tx, ty); tf != tk.BubbleOther {
		t.Errorf("their text colour is %v, want %v", tf, tk.BubbleOther)
	}
	if mf, _ := styleAt(s, mx, my); mf != tk.BubbleSelf {
		t.Errorf("my text colour is %v, want %v", mf, tk.BubbleSelf)
	}
}

// The threshold is exact: one cell either side.
func TestBoxlessThresholdIsExact(t *testing.T) {
	boxed := func(w int) bool {
		items := LayoutMessages(twoSided().Messages, w, me, at(23, 0, 0), i18n.ZH)
		for _, it := range items {
			if it.Kind == ItemBubble {
				return it.Boxed
			}
		}
		return false
	}
	if boxed(27) {
		t.Error("width 27 is under the threshold but is boxed")
	}
	if !boxed(28) {
		t.Error("width 28 is the threshold and should be boxed")
	}
}

// An unboxed bubble is as tall as its text: no border rows.
func TestUnboxedBubbleHasNoBorderRows(t *testing.T) {
	items := LayoutMessages(twoSided().Messages, 20, me, at(23, 0, 0), i18n.ZH)
	for _, it := range items {
		if it.Kind == ItemBubble && it.Height() != len(it.Lines) {
			t.Errorf("an unboxed bubble is %d rows for %d lines", it.Height(), len(it.Lines))
		}
	}
}

// ---------- 6.7 scrolling ----------

// manyMessages builds n alternating messages, each its own group.
func manyMessages(n int) *client.Conversation {
	cv := &client.Conversation{}
	cv.ID, cv.Name, cv.Kind = "c1", "general", protocol.KindChannel
	for i := 0; i < n; i++ {
		author, name := "bob", "bob"
		if i%2 == 1 {
			author, name = me, "me"
		}
		cv.Messages = append(cv.Messages, protocol.Message{
			ID: string(rune('a' + i)), AuthorID: author, AuthorName: name,
			Body: "msg" + strings.Repeat("x", 0) + string(rune('A'+i)), CreatedAt: at(10, i, 0),
		})
	}
	return cv
}

// Render reports the total height, and it is what scrolling clamps against.
func TestRenderReportsTheTotalHeight(t *testing.T) {
	cv := manyMessages(6)
	s := newScreen(t, bubW, bubH)
	got := Transcript{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: bubW, H: bubH}, TranscriptModel{
		Conversation: cv, UserID: me, Now: at(23, 0, 0), Focus: client.FocusInput,
	}, i18n.ZH)
	want := TotalHeight(LayoutMessages(cv.Messages, bubW, me, at(23, 0, 0), i18n.ZH))
	if got != want {
		t.Fatalf("Render reported %d rows, the layout is %d", got, want)
	}
}

// Pinned to the bottom, the newest message is on screen and the oldest is not.
func TestPinnedToTheBottomShowsTheNewest(t *testing.T) {
	cv := manyMessages(12)
	s := renderBubbles(t, toks(), cv, bubW, 14)
	if _, _, ok := findText(s, "msgL"); !ok { // the 12th message
		t.Fatal("the newest message is not visible when pinned to the bottom")
	}
	if _, _, ok := findText(s, "msgA"); ok {
		t.Fatal("the oldest message is visible although it should have scrolled away")
	}
}

// Scrolling up brings older messages in and the window moves by whole rows; at the
// top the first message and its time line are there. A bubble cut by the window's
// edge still draws the rows that are visible.
func TestScrollingUpRevealsOlderMessages(t *testing.T) {
	cv := manyMessages(12)
	total := TotalHeight(LayoutMessages(cv.Messages, bubW, me, at(23, 0, 0), i18n.ZH))
	const viewH = 14 - 2

	s := newScreen(t, bubW, 14)
	Transcript{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: bubW, H: 14}, TranscriptModel{
		Conversation: cv, UserID: me, Now: at(23, 0, 0), ScrollFromBottom: total - viewH,
		Focus: client.FocusInput,
	}, i18n.ZH)
	s.Show()
	if _, _, ok := findText(s, "msgA"); !ok {
		t.Fatal("scrolled to the top, the oldest message is not visible")
	}
	if _, _, ok := findText(s, "msgL"); ok {
		t.Fatal("scrolled to the top, the newest message is still visible")
	}
}

// A bubble that the window's edge cuts in half still draws the rows that are inside
// the window. Walk every offset, and at each one whose top edge or bottom edge falls
// inside a bubble, require that bubble's visible rows on screen. Drawing only whole
// bubbles would blank the half-visible one and make content pop in and out as the
// user scrolls a row at a time.
func TestABubbleCutByTheWindowStillDrawsItsVisibleRows(t *testing.T) {
	cv := manyMessages(12)
	items := LayoutMessages(cv.Messages, bubW, me, at(23, 0, 0), i18n.ZH)
	total := TotalHeight(items)
	const bodyH = 7
	const screenH = bodyH + 2

	// For each bubble, the absolute row of its text line, so a test can ask whether
	// that row is inside the window and whether the text is on screen.
	type spot struct {
		word string
		row  int // absolute row of the line holding the word
	}
	var spots []spot
	row := 0
	for _, it := range items {
		if it.Kind == ItemBubble {
			spots = append(spots, spot{word: it.Lines[0], row: row + 1}) // +1: below the top border
		}
		row += it.Height()
	}

	cut := 0
	for off := 0; off+bodyH <= total; off++ {
		s := newScreen(t, bubW, screenH)
		Transcript{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: bubW, H: screenH}, TranscriptModel{
			Conversation: cv, UserID: me, Now: at(23, 0, 0), ScrollFromBottom: off, Focus: client.FocusInput,
		}, i18n.ZH)
		s.Show()

		top := total - bodyH - off // first visible absolute row
		for _, sp := range spots {
			inside := sp.row >= top && sp.row < top+bodyH
			_, _, on := findText(s, sp.word)
			if inside && !on {
				t.Fatalf("offset %d: %q's line (row %d) is inside the window [%d,%d) but is not drawn",
					off, sp.word, sp.row, top, top+bodyH)
			}
			if !inside && on {
				t.Fatalf("offset %d: %q's line (row %d) is outside the window [%d,%d) but is drawn",
					off, sp.word, sp.row, top, top+bodyH)
			}
			// Count the cases that are genuinely cuts: the text line is in the window
			// while the bubble's top border is above it, or its bottom border below.
			if inside && (sp.row-1 < top || sp.row+1 >= top+bodyH) {
				cut++
			}
		}
	}
	if cut == 0 {
		t.Fatal("no offset cut a bubble in half; the test is not exercising the case")
	}
}

// Every scroll offset draws something sane: no row outside the body is touched and
// no half-bubble leaves a stray corner without its edge.
func TestEveryScrollOffsetDrawsWithinTheBody(t *testing.T) {
	cv := manyMessages(10)
	total := TotalHeight(LayoutMessages(cv.Messages, bubW, me, at(23, 0, 0), i18n.ZH))

	for off := 0; off <= total; off++ {
		s := newScreen(t, bubW, 14)
		Transcript{T: toks()}.Render(s, widget.Rect{X: 0, Y: 2, W: bubW, H: 12}, TranscriptModel{
			Conversation: cv, UserID: me, Now: at(23, 0, 0), ScrollFromBottom: off, Focus: client.FocusInput,
		}, i18n.ZH)
		s.Show()
		for y := 0; y < 2; y++ {
			if strings.TrimSpace(rowText(s, y, bubW)) != "" {
				t.Fatalf("offset %d: drew above the transcript on row %d: %q", off, y, rowText(s, y, bubW))
			}
		}
	}
}
