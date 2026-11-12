package views

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/widget"
)

// ---------- the geometry ----------

func TestNameFlightPositionRunsFromStartToEnd(t *testing.T) {
	f := NameFlight{Name: "general", FromX: 4, FromY: 6, ToX: 2, ToY: 2}

	for _, c := range []struct {
		fold   float64
		wx, wy int
	}{{0, 4, 6}, {1, 2, 2}, {0.5, 3, 4}} {
		f.Fold = c.fold
		if x, y := f.Position(); x != c.wx || y != c.wy {
			t.Errorf("fold %.1f: at (%d,%d), want (%d,%d)", c.fold, x, y, c.wx, c.wy)
		}
	}
}

// It never overshoots, and moves in one direction only.
func TestNameFlightIsMonotonicAndStaysBetweenItsEnds(t *testing.T) {
	f := NameFlight{Name: "n", FromX: 30, FromY: 14, ToX: 3, ToY: 2}
	px, py := f.FromX, f.FromY
	for i := 0; i <= 100; i++ {
		f.Fold = float64(i) / 100
		x, y := f.Position()
		if x > px || y > py {
			t.Fatalf("fold %.2f: moved away from the title: (%d,%d) after (%d,%d)", f.Fold, x, y, px, py)
		}
		if x < f.ToX || y < f.ToY || x > f.FromX || y > f.FromY {
			t.Fatalf("fold %.2f: (%d,%d) is outside the box between its ends", f.Fold, x, y)
		}
		px, py = x, y
	}
}

// It is in the air only strictly between the ends; at either end its owner draws it.
func TestNameFlightIsAirborneOnlyBetweenTheEnds(t *testing.T) {
	for _, c := range []struct {
		fold float64
		want bool
	}{{-1, false}, {0, false}, {0.001, true}, {0.5, true}, {0.999, true}, {1, false}, {2, false}} {
		if got := (NameFlight{Fold: c.fold}).InFlight(); got != c.want {
			t.Errorf("fold %v: InFlight = %v, want %v", c.fold, got, c.want)
		}
	}
}

// Folded, the title is centred across the whole row — the top centre of the
// screen — and on the content's first row; open, it is centred across the narrower
// transcript, so it sits further right.
func TestTitleAnchorIsTheTopCentre(t *testing.T) {
	c := foldContent(100)
	cv := &client.Conversation{Conversation: protocol.Conversation{Name: "general", Kind: protocol.KindChannel, MemberCount: 4}}
	x0, y0 := TitleAnchor(c, 0, cv, i18n.ZH)
	x1, y1 := TitleAnchor(c, 1, cv, i18n.ZH)
	if y0 != c.Y || y1 != c.Y {
		t.Errorf("the title is on row %d/%d, want the content's first row %d", y0, y1, c.Y)
	}
	// "general · 4" is 11 cells; folded, the transcript is the row inside the gutters.
	main := MainRectAt(c, 1)
	if want := main.X + (main.W-11)/2; x1 != want {
		t.Errorf("folded, the title starts at %d, want %d (centred)", x1, want)
	}
	left := x1 - main.X
	right := main.X + main.W - (x1 + 11)
	if d := right - left; d < 0 || d > 1 {
		t.Errorf("folded, the title is not centred: %d cells left of it, %d right", left, right)
	}
	if x0 <= x1 {
		t.Errorf("open, the title (%d) should sit right of the folded one (%d)", x0, x1)
	}
	// Too wide to centre: it starts at the left edge rather than off it.
	long := &client.Conversation{Conversation: protocol.Conversation{Name: strings.Repeat("长", 80)}}
	if x, _ := TitleAnchor(c, 1, long, i18n.ZH); x != main.X {
		t.Errorf("an over-wide title starts at %d, want the left edge %d", x, main.X)
	}
}

// ---------- where the row is ----------

func flightRows(nCh int) ([]client.Row, *client.AppState) {
	st := client.NewAppState()
	for i := 0; i < nCh; i++ {
		id := "c" + string(rune('a'+i))
		c := st.EnsureConversation(id)
		c.Name, c.Kind, c.LastMessageAt = "ch"+string(rune('a'+i)), protocol.KindChannel, int64(1000-i)
	}
	return st.SidebarRows(), st
}

// RowLine agrees with where Render actually draws the row, so the name leaves from
// the row the user was looking at. Checked against the real screen rather than
// against a second copy of the arithmetic.
func TestRowLineMatchesWhereTheRowIsDrawn(t *testing.T) {
	for _, n := range []int{1, 3, 6} {
		rows, _ := flightRows(n)
		for _, filter := range []bool{false, true} {
			for i := 0; i < n; i++ {
				id := "c" + string(rune('a'+i))
				name := "ch" + string(rune('a'+i))
				sel := client.IndexOfConversation(rows, id)

				s := newScreen(t, sideW, sideH)
				var flt []rune
				if filter {
					flt = []rune{}
				}
				List{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: sideW, H: sideH}, ListModel{
					Rows: rows, Selected: sel, Focus: client.FocusList, Filter: flt,
				}, i18n.ZH)

				line, ok := RowLine(rows, sel, id, sideH, filter, i18n.ZH)
				if !ok {
					t.Fatalf("n=%d filter=%v %s: reported off screen", n, filter, id)
				}
				if got := rowText(s, line, sideW); !strings.Contains(got, name) {
					t.Errorf("n=%d filter=%v: row %d is %q, want it to hold %q", n, filter, line, got, name)
				}
			}
		}
	}
}

// With a long list the window scrolls: a row far above the cursor is off screen and
// has no line, and one inside the window does.
func TestRowLineReportsOffScreenRows(t *testing.T) {
	rows, _ := flightRows(30)
	last := client.IndexOfConversation(rows, "c"+string(rune('a'+29)))

	if _, ok := RowLine(rows, last, "ca", sideH, false, i18n.ZH); ok {
		t.Error("the first channel is scrolled off but was reported on screen")
	}
	if _, ok := RowLine(rows, last, "c"+string(rune('a'+29)), sideH, false, i18n.ZH); !ok {
		t.Error("the selected row must always be on screen")
	}
	if _, ok := RowLine(rows, last, "nope", sideH, false, i18n.ZH); ok {
		t.Error("an unknown conversation was reported on screen")
	}
}

// ---------- drawing ----------

func renderFlightFrame(t *testing.T, fold float64, nameInFlight, flying bool) tcell.SimulationScreen {
	t.Helper()
	const w, h = 100, 24
	s := newScreen(t, w, h)
	tk := toks()
	st := client.NewAppState()
	st.State = client.StateReady
	cv := st.EnsureConversation("c1")
	cv.Name, cv.Kind = "uniquename", protocol.KindChannel
	st.ActiveID = "c1"

	content := ContentRect(w, h)
	side := SidebarRectAt(content, fold)
	main := MainRectAt(content, fold)

	rows := st.SidebarRows()
	lm := ListModel{Rows: rows, Selected: client.FirstSelectable(rows), ActiveID: "c1", Focus: client.FocusList}
	if flying {
		lm.FlyingID = "c1"
	}
	if SidebarContentVisible(fold) {
		List{T: tk}.Render(s, side, lm, i18n.ZH)
	}
	tr, _ := ComposerRect(main, 1)
	Transcript{T: tk}.Render(s, tr, TranscriptModel{
		Conversation: st.Active(), Focus: client.FocusInput, NameInFlight: nameInFlight,
	}, i18n.ZH)

	if flying {
		line, ok := RowLine(rows, lm.Selected, "c1", content.H, false, i18n.ZH)
		if !ok {
			t.Fatal("the open conversation is not on screen")
		}
		fx, fy := SidebarRowAnchor(content, line)
		tx, ty := TitleAnchor(content, 1, st.Active(), i18n.ZH)
		FlightLayer{T: tk}.Render(s, content, NameFlight{
			Name: "uniquename", Fold: fold, FromX: fx, FromY: fy, ToX: tx, ToY: ty,
		}, client.FocusInput, i18n.ZH)
	}
	s.Show()
	return s
}

// countName counts how many times the name's first glyphs appear anywhere on screen.
func countName(s tcell.SimulationScreen) int {
	w, h := s.Size()
	n := 0
	for y := 0; y < h; y++ {
		n += strings.Count(rowText(s, y, w), "uniquename")
	}
	return n
}

// Mid-flight the name is on screen exactly once: not in its sidebar row, not in the
// title, only in the air.
func TestTheNameIsShownExactlyOnceWhileFlying(t *testing.T) {
	for _, fold := range []float64{0.1, 0.3, 0.5, 0.59} {
		s := renderFlightFrame(t, fold, true, true)
		if got := countName(s); got != 1 {
			w, h := s.Size()
			var b strings.Builder
			for y := 0; y < h; y++ {
				b.WriteString(rowText(s, y, w) + "\n")
			}
			t.Fatalf("fold %.2f: the name is on screen %d times, want 1:\n%s", fold, got, b.String())
		}
	}
}

// Past the sidebar-content threshold the name is still only in the air.
func TestTheNameIsShownOnceAfterTheListHasGone(t *testing.T) {
	for _, fold := range []float64{0.6, 0.8, 0.95} {
		if got := countName(renderFlightFrame(t, fold, true, true)); got != 1 {
			t.Fatalf("fold %.2f: the name is on screen %d times, want 1", fold, got)
		}
	}
}

// At rest the name is shown once by its owner and the flight layer adds nothing:
// open, it is in the sidebar and the title (two homes, drawn by two owners); folded,
// only in the title.
func TestAtRestTheOwnersDrawTheNameAndTheFlightLayerDoesNot(t *testing.T) {
	open := renderFlightFrame(t, 0, false, false)
	if got := countName(open); got != 2 {
		t.Fatalf("open: the name appears %d times, want 2 (the sidebar row and the title)", got)
	}
	folded := renderFlightFrame(t, 1, false, false)
	if got := countName(folded); got != 1 {
		t.Fatalf("folded: the name appears %d times, want 1 (the title)", got)
	}

	// Even with the flag set, the flight layer is silent at the two ends.
	for _, fold := range []float64{0, 1} {
		layerOnly := newScreen(t, 100, 24)
		FlightLayer{T: toks()}.Render(layerOnly, ContentRect(100, 24), NameFlight{
			Name: "uniquename", Fold: fold, FromX: 4, FromY: 6, ToX: 2, ToY: 2,
		}, client.FocusInput, i18n.ZH)
		layerOnly.Show()
		if countName(layerOnly) != 0 {
			t.Errorf("fold %v: the flight layer drew the name at a resting end", fold)
		}
	}
}

// The name in the air sits where the geometry says it does.
func TestTheFlyingNameIsWhereTheGeometrySaysItIs(t *testing.T) {
	const fold = 0.4
	s := renderFlightFrame(t, fold, true, true)

	content := ContentRect(100, 24)
	st := client.NewAppState()
	c := st.EnsureConversation("c1")
	c.Name, c.Kind = "uniquename", protocol.KindChannel
	rows := st.SidebarRows()
	line, _ := RowLine(rows, client.FirstSelectable(rows), "c1", content.H, false, i18n.ZH)
	fx, fy := SidebarRowAnchor(content, line)
	tx, ty := TitleAnchor(content, 1, c, i18n.ZH)
	wx, wy := NameFlight{Fold: fold, FromX: fx, FromY: fy, ToX: tx, ToY: ty}.Position()

	if got := rowText(s, wy, 100); !strings.Contains(got, "uniquename") {
		t.Fatalf("expected the name on row %d, found %q", wy, got)
	}
	if r, _, _, _ := s.GetContent(wx, wy); r != 'u' {
		t.Fatalf("expected the name's first glyph at (%d,%d), found %q", wx, wy, r)
	}
}

// A name that would run past the screen's right edge is clipped, not wrapped or
// dropped.
func TestAFlyingNameIsClippedAtTheScreenEdge(t *testing.T) {
	s := newScreen(t, 30, 12)
	FlightLayer{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: 30, H: 12}, NameFlight{
		Name: "a-rather-long-conversation-name", Fold: 0.5, FromX: 40, FromY: 5, ToX: 20, ToY: 5,
	}, client.FocusInput, i18n.ZH)
	s.Show()
	x, y := NameFlight{Fold: 0.5, FromX: 40, FromY: 5, ToX: 20, ToY: 5}.Position()
	if x != 30 {
		t.Fatalf("fixture expected x=30, got %d", x)
	}
	// Starting at the very edge there is no room at all: nothing is drawn and
	// nothing panics.
	if got := rowText(s, y, 30); strings.TrimSpace(got) != "" {
		t.Fatalf("something was drawn off the edge: %q", got)
	}

	// Closer in, the name is cut at the edge.
	s2 := newScreen(t, 30, 12)
	FlightLayer{T: toks()}.Render(s2, widget.Rect{X: 0, Y: 0, W: 30, H: 12}, NameFlight{
		Name: "a-rather-long-conversation-name", Fold: 0.5, FromX: 30, FromY: 5, ToX: 10, ToY: 5,
	}, client.FocusInput, i18n.ZH)
	s2.Show()
	got := strings.TrimSpace(rowText(s2, 5, 30))
	// A clipped name ends in an ellipsis (that is what layout.Truncate does), so
	// what must be a prefix of the full name is the text before it.
	body := strings.TrimSuffix(got, "…")
	if body == "" || !strings.HasPrefix("a-rather-long-conversation-name", body) {
		t.Fatalf("the clipped name is %q, want a prefix of the full name followed by an ellipsis", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a clipped name should end in an ellipsis, got %q", got)
	}
}

// A name flying over a wide glyph must not leave half of it behind. Row 5 of the
// open sidebar holds the channel header "群聊"; a name that lands on its second
// character used to overwrite one of the glyph's two cells, producing a mangled
// 群 followed by the name's tail.
func TestAFlyingNameLeavesNoBrokenWideGlyph(t *testing.T) {
	for _, fold := range []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.59} {
		s := renderFlightFrame(t, fold, true, true)
		w, h := s.Size()
		for y := 0; y < h; y++ {
			row := rowText(s, y, w)
			// "群" must only ever appear as part of "群聊", never cut off from it.
			if strings.Contains(row, "群") && !strings.Contains(row, "群聊") {
				t.Fatalf("fold %.2f row %d: a wide glyph was split: %q", fold, y, row)
			}
			if strings.Contains(row, "私") && !strings.Contains(row, "私聊") {
				t.Fatalf("fold %.2f row %d: a wide glyph was split: %q", fold, y, row)
			}
		}
	}
}

// Off the vertical bounds, nothing is drawn (a row scrolled away, a short window).
func TestAFlyingNameOutsideTheBoundsIsNotDrawn(t *testing.T) {
	s := newScreen(t, 40, 10)
	FlightLayer{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: 40, H: 4}, NameFlight{
		Name: "name", Fold: 0.5, FromX: 5, FromY: 8, ToX: 5, ToY: 8,
	}, client.FocusInput, i18n.ZH)
	s.Show()
	for y := 0; y < 10; y++ {
		if strings.Contains(rowText(s, y, 40), "name") {
			t.Fatalf("a name outside the bounds was drawn on row %d", y)
		}
	}
}
