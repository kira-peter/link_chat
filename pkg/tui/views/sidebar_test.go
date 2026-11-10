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
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

const (
	sideW = 40
	sideH = 18
)

// sidebarState builds a state with nDM direct messages and nCh channels, newest
// first by construction (higher index = older).
func sidebarState(nDM, nCh int) *client.AppState {
	st := client.NewAppState()
	for i := 0; i < nDM; i++ {
		c := st.EnsureConversation("d" + string(rune('a'+i)))
		c.Name, c.Kind, c.LastMessageAt = "dm"+string(rune('a'+i)), protocol.KindDM, int64(1000-i)
	}
	for i := 0; i < nCh; i++ {
		c := st.EnsureConversation("c" + string(rune('a'+i)))
		c.Name, c.Kind, c.LastMessageAt = "ch"+string(rune('a'+i)), protocol.KindChannel, int64(1000-i)
	}
	return st
}

func renderSidebar(t *testing.T, tk theme.Tokens, st *client.AppState, sel int, m func(*ListModel)) tcell.SimulationScreen {
	t.Helper()
	s := newScreen(t, sideW, sideH)
	rows := st.SidebarRows()
	if sel < 0 {
		sel = client.FirstSelectable(rows)
	}
	lm := ListModel{Rows: rows, Selected: sel, Focus: client.FocusList, Now: 2000}
	if m != nil {
		m(&lm)
	}
	List{T: tk}.Render(s, widget.Rect{X: 0, Y: 0, W: sideW, H: sideH}, lm, i18n.ZH)
	s.Show()
	return s
}

func rowOfText(s tcell.SimulationScreen, want string) int {
	for y := 0; y < sideH; y++ {
		if strings.Contains(rowText(s, y, sideW), want) {
			return y
		}
	}
	return -1
}

// ---------- 4.3 the three blocks ----------

// DMs above channels above the foot, each under its own small title.
func TestSidebarShowsThreeBlocksInOrder(t *testing.T) {
	s := renderSidebar(t, toks(), sidebarState(2, 2), -1, nil)

	dmH, dmA := rowOfText(s, "私聊"), rowOfText(s, "dma")
	chH, chA := rowOfText(s, "群聊"), rowOfText(s, "cha")
	set, help, about := rowOfText(s, "设置"), rowOfText(s, "帮助"), rowOfText(s, "关于")
	for name, y := range map[string]int{"私聊": dmH, "dma": dmA, "群聊": chH, "cha": chA, "设置": set, "帮助": help, "关于": about} {
		if y < 0 {
			t.Fatalf("%q is not on screen:\n%s", name, dump(s))
		}
	}
	if !(dmH < dmA && dmA < chH && chH < chA && chA < set && set < help && help < about) {
		t.Fatalf("blocks are out of order:\n%s", dump(s))
	}
	// Each header sits directly above its first row.
	if dmA != dmH+1 || chA != chH+1 {
		t.Fatalf("a header is not directly above its section:\n%s", dump(s))
	}
}

func dump(s tcell.SimulationScreen) string {
	var b strings.Builder
	for y := 0; y < sideH; y++ {
		b.WriteString(rowText(s, y, sideW))
		b.WriteByte('\n')
	}
	return b.String()
}

// The foot is anchored to the bottom of the sidebar: it does not move when the
// number of conversations changes, from none to a screenful.
func TestFootStaysAtTheBottomWhateverTheCount(t *testing.T) {
	var settingsY, aboutY []int
	for _, n := range [][2]int{{0, 0}, {1, 1}, {3, 4}, {10, 20}} {
		s := renderSidebar(t, toks(), sidebarState(n[0], n[1]), -1, nil)
		settingsY = append(settingsY, rowOfText(s, "设置"))
		aboutY = append(aboutY, rowOfText(s, "关于"))
		if aboutY[len(aboutY)-1] != sideH-1 {
			t.Fatalf("with %v conversations 关于 is on row %d, want the last row (%d):\n%s", n, aboutY[len(aboutY)-1], sideH-1, dump(s))
		}
	}
	for i := range settingsY {
		if settingsY[i] != settingsY[0] || aboutY[i] != aboutY[0] {
			t.Fatalf("the foot moved: settings rows %v, about rows %v", settingsY, aboutY)
		}
	}
}

// A long list scrolls, but the foot stays and the selected row stays visible.
func TestLongListScrollsAndKeepsTheFoot(t *testing.T) {
	st := sidebarState(8, 20)
	rows := st.SidebarRows()
	last := client.IndexOfConversation(rows, "ct") // the 20th channel, "cht"
	if last < 0 {
		t.Fatalf("fixture has no 20th channel")
	}
	s := renderSidebar(t, toks(), st, last, nil)

	if rowOfText(s, "cht") < 0 {
		t.Fatalf("the selected row scrolled out of view:\n%s", dump(s))
	}
	for _, w := range []string{"设置", "帮助", "关于"} {
		if rowOfText(s, w) < 0 {
			t.Fatalf("the foot lost %q while scrolling:\n%s", w, dump(s))
		}
	}
	// The first rows have scrolled away.
	if rowOfText(s, "dma") >= 0 {
		t.Fatalf("the top of the list is still showing after scrolling to the end:\n%s", dump(s))
	}
}

// An empty section keeps its header and shows how to fill it; the other section is
// unaffected.
func TestEmptySectionsShowAHint(t *testing.T) {
	s := renderSidebar(t, toks(), sidebarState(0, 2), -1, nil)
	if y := rowOfText(s, "私聊"); y < 0 || !strings.Contains(rowText(s, y+1, sideW), "Alt+O") {
		t.Fatalf("an empty DM block should show the Alt+O hint under its header:\n%s", dump(s))
	}
	if rowOfText(s, "cha") < 0 {
		t.Fatalf("the channels vanished with the DMs:\n%s", dump(s))
	}

	s = renderSidebar(t, toks(), sidebarState(2, 0), -1, nil)
	if y := rowOfText(s, "群聊"); y < 0 || !strings.Contains(rowText(s, y+1, sideW), "Alt+N") {
		t.Fatalf("an empty channel block should show the Alt+N hint under its header:\n%s", dump(s))
	}
}

// A window too short for the foot gives it up rather than crowding out the
// conversations.
func TestTinyHeightDropsTheFootFirst(t *testing.T) {
	s := newScreen(t, sideW, 5)
	st := sidebarState(1, 1)
	rows := st.SidebarRows()
	List{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: sideW, H: 5}, ListModel{
		Rows: rows, Selected: client.FirstSelectable(rows), Focus: client.FocusList,
	}, i18n.ZH)
	var all strings.Builder
	for y := 0; y < 5; y++ {
		all.WriteString(rowText(s, y, sideW))
	}
	if strings.Contains(all.String(), "设置") {
		t.Fatalf("the foot was kept in a 5-row window:\n%s", all.String())
	}
	if !strings.Contains(all.String(), "dma") {
		t.Fatalf("the conversations were crowded out:\n%s", all.String())
	}
}

// The selected foot entry gets the same full-row fill as a conversation.
func TestSelectedEntryHasTheRowFill(t *testing.T) {
	tk := toks()
	st := sidebarState(1, 1)
	rows := st.SidebarRows()
	help := -1
	for i, r := range rows {
		if r.Kind == client.RowEntry && r.Entry == client.EntryHelp {
			help = i
		}
	}
	s := renderSidebar(t, tk, st, help, nil)
	y := rowOfText(s, "帮助")
	_, selBg, _ := tk.Selected().Decompose()
	filled := 0
	for x := 0; x < sideW; x++ {
		if bgAt(s, x, y) == selBg {
			filled++
		}
	}
	if filled < sideW-1 {
		t.Fatalf("the selected entry's fill covers %d of %d cells", filled, sideW)
	}
}

// Without the keyboard, nothing in the sidebar is drawn as selected.
func TestNoRowIsSelectedWithoutFocus(t *testing.T) {
	tk := toks()
	_, selBg, _ := tk.Selected().Decompose()
	s := renderSidebar(t, tk, sidebarState(1, 1), -1, func(m *ListModel) { m.Focus = client.FocusInput })
	for y := 0; y < sideH; y++ {
		for x := 0; x < sideW; x++ {
			if bgAt(s, x, y) == selBg {
				t.Fatalf("cell (%d,%d) has the selection fill though the list has no focus", x, y)
			}
		}
	}
}

// ---------- 4.7 the selection sweep ----------

// sweepFg returns the foreground colour of every glyph of a word on a row.
func sweepFg(t *testing.T, s tcell.SimulationScreen, y int, word string) []tcell.Color {
	t.Helper()
	var out []tcell.Color
	start := strings.Index(rowText(s, y, sideW), word)
	if start < 0 {
		t.Fatalf("%q not on row %d", word, y)
	}
	for i := range word {
		out = append(out, cellFg(s, start+i, y))
	}
	return out
}

func distinct(c []tcell.Color) int {
	seen := map[tcell.Color]bool{}
	for _, x := range c {
		seen[x] = true
	}
	return len(seen)
}

// Mid-pass, the selected row's text has bands of different brightness while its
// background fill is exactly as wide as without the sweep.
func TestSelectionSweepRecoloursTextNotTheFill(t *testing.T) {
	tk := distinctToks()
	tk.SelectedFg = tcell.NewRGBColor(120, 140, 160)
	st := sidebarState(0, 1)

	still := renderSidebar(t, tk, st, -1, nil)
	mid := renderSidebar(t, tk, st, -1, func(m *ListModel) {
		m.Shimmer, m.Depth = true, anim.DepthTrue
		m.Elapsed = anim.ListShimmer.Travel / 2
	})

	y := rowOfText(still, "cha")
	if y != rowOfText(mid, "cha") {
		t.Fatalf("the sweep moved the row")
	}
	stillFg, midFg := sweepFg(t, still, y, "cha"), sweepFg(t, mid, y, "cha")
	if distinct(stillFg) != 1 {
		t.Fatalf("a resting selected row is not one colour: %v", stillFg)
	}
	changed := false
	for i := range stillFg {
		if stillFg[i] != midFg[i] {
			changed = true
		}
	}
	if !changed {
		t.Fatalf("a mid-pass sweep left every glyph at %v", stillFg)
	}

	// The fill: identical cells carry the selection background in both frames.
	_, selBg, _ := tk.Selected().Decompose()
	for x := 0; x < sideW; x++ {
		if (bgAt(still, x, y) == selBg) != (bgAt(mid, x, y) == selBg) {
			t.Fatalf("the sweep changed the extent of the selection fill at column %d", x)
		}
	}
	// And the text did not move.
	if rowText(still, y, sideW) != rowText(mid, y, sideW) {
		t.Fatalf("the sweep moved text:\n%q\n%q", rowText(still, y, sideW), rowText(mid, y, sideW))
	}
}

// The sweep applies to a foot entry as well as a conversation.
func TestSelectionSweepWorksOnFootEntries(t *testing.T) {
	tk := distinctToks()
	tk.SelectedFg = tcell.NewRGBColor(120, 140, 160)
	st := sidebarState(0, 0)
	rows := st.SidebarRows()
	sel := client.FirstSelectable(rows) // Settings

	still := renderSidebar(t, tk, st, sel, nil)
	mid := renderSidebar(t, tk, st, sel, func(m *ListModel) {
		m.Shimmer, m.Depth = true, anim.DepthTrue
		m.Elapsed = anim.ListShimmer.Travel / 2
	})
	y := rowOfText(still, "设置")
	same := true
	for x := 0; x < sideW; x++ {
		if cellFg(still, x, y) != cellFg(mid, x, y) {
			same = false
		}
	}
	if same {
		t.Fatalf("the sweep did not touch the selected entry")
	}
}

// Rows that are not selected never shimmer, whatever the flag says.
func TestSweepOnlyTouchesTheSelectedRow(t *testing.T) {
	tk := distinctToks()
	st := sidebarState(0, 3)
	rows := st.SidebarRows()
	sel := client.FirstSelectable(rows)

	off := renderSidebar(t, tk, st, sel, nil)
	on := renderSidebar(t, tk, st, sel, func(m *ListModel) {
		m.Shimmer, m.Depth = true, anim.DepthTrue
		m.Elapsed = anim.ListShimmer.Travel / 2
	})
	selY := rowOfText(on, "cha")
	for y := 0; y < sideH; y++ {
		if y == selY {
			continue
		}
		for x := 0; x < sideW; x++ {
			if cellFg(off, x, y) != cellFg(on, x, y) {
				t.Fatalf("an unselected row changed colour at (%d,%d)", x, y)
			}
		}
	}
}

// The three degradations: no sweep flag is the static fill; no focus is no fill at
// all; mono marks exactly one glyph instead of blending colour.
func TestSelectionSweepDegrades(t *testing.T) {
	tk := distinctToks()
	tk.SelectedFg = tcell.NewRGBColor(120, 140, 160)
	st := sidebarState(0, 1)
	mid := anim.ListShimmer.Travel / 2

	// Animation off: static, whatever the clock says.
	off := renderSidebar(t, tk, st, -1, func(m *ListModel) {
		m.Shimmer, m.Depth, m.Elapsed = false, anim.DepthTrue, mid
	})
	y := rowOfText(off, "cha")
	if distinct(sweepFg(t, off, y, "cha")) != 1 {
		t.Fatalf("with animation off the row is still being swept")
	}

	// Mono: the colour is untouched and exactly one glyph carries the marker.
	mono := renderSidebar(t, tk, st, -1, func(m *ListModel) {
		m.Shimmer, m.Depth, m.Elapsed = true, anim.DepthMono, mid
	})
	if distinct(sweepFg(t, mono, y, "cha")) != 1 {
		t.Fatalf("mono blended colours")
	}
	marked := 0
	for x := 0; x < sideW; x++ {
		_, _, st, _ := mono.GetContent(x, y)
		if _, _, at := st.Decompose(); at&tcell.AttrUnderline != 0 {
			marked++
		}
	}
	if marked != 1 {
		t.Fatalf("mono marked %d glyphs, want exactly 1", marked)
	}

	// Between passes: nothing lit.
	gap := renderSidebar(t, tk, st, -1, func(m *ListModel) {
		m.Shimmer, m.Depth = true, anim.DepthTrue
		m.Elapsed = anim.ListShimmer.Travel + anim.ListShimmer.Pause/2
	})
	if distinct(sweepFg(t, gap, y, "cha")) != 1 {
		t.Fatalf("the band is visible during the rest between passes")
	}
}

var _ = time.Second
