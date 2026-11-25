package views

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/widget"
)

func fanScreen(t *testing.T, w, h int, m FanModel) (tcell.SimulationScreen, []string) {
	s := newScreen(t, w, h)
	c := widget.NewCanvas(s, toks(), widget.Rect{W: w, H: h})
	c.Fill(toks().Base())
	FanWheel{T: toks()}.Render(s, widget.Rect{W: w, H: h}, m, i18n.ZH)
	rows := make([]string, h)
	for y := range rows {
		rows[y] = rowText(s, y, w)
	}
	return s, rows
}

func fanRows(t *testing.T, w, h int, m FanModel) []string {
	_, rows := fanScreen(t, w, h, m)
	return rows
}

func fanRowOf(rows []string, s string) int {
	for y, r := range rows {
		if strings.Contains(r, s) {
			return y
		}
	}
	return -1
}

// fanCol is the display column where glyph starts on row, or -1.
func fanCol(row, glyph string) int {
	i := strings.Index(row, glyph)
	if i < 0 {
		return -1
	}
	return layout.StringWidth(row[:i])
}

var fanLabels = []string{"发消息", "查看主页", "删除好友", "拉黑", "帮助"}

// fanMid selects the middle of fanLabels, 删除好友, with two items either side.
const fanMid = 2

func TestFanOpensRightOfALeftAnchor(t *testing.T) {
	anchor := widget.Rect{X: 2, Y: 10, W: 20, H: 1}
	s, rows := fanScreen(t, 100, 24, FanModel{Labels: fanLabels, Sel: fanMid, Anchor: anchor})
	y := fanRowOf(rows, "删除好友")
	x := fanCol(rows[y], "删除好友")
	if y != 10 || x < anchor.X+anchor.W {
		t.Fatalf("the selection is not beside its anchor:\n%s", strings.Join(rows, "\n"))
	}
	// Yellow and bold, no frame.
	_, _, st, _ := s.GetContent(x, y)
	if fg, _, attrs := st.Decompose(); fg != toks().Warning || attrs&tcell.AttrBold == 0 {
		t.Fatal("the selection is not in bold yellow")
	}
	if strings.ContainsAny(strings.Join(rows, ""), "┏┃┗") {
		t.Fatal("the selection still has a frame")
	}
	// The line reaches the label with one space before it.
	if fanCol(rows[y], "─ 删除好友") < 0 {
		t.Fatalf("the line does not reach the label:\n%s", rows[y])
	}
	// The list in order, without wrapping: earlier items above, later below.
	if !(fanRowOf(rows, "查看主页") < y && fanRowOf(rows, "拉黑") > y) {
		t.Fatalf("order round the selection:\n%s", strings.Join(rows, "\n"))
	}
	// Left-aligned on the arc: the further from the middle, the further back.
	if !(fanCol(rows[fanRowOf(rows, "发消息")], "发消息") <= fanCol(rows[fanRowOf(rows, "查看主页")], "查看主页") &&
		fanCol(rows[fanRowOf(rows, "查看主页")], "查看主页") <= x) {
		t.Fatalf("the labels do not start on the arc:\n%s", strings.Join(rows, "\n"))
	}
}

func TestFanOnTheLeftAlignsTheirEnds(t *testing.T) {
	anchor := widget.Rect{X: 70, Y: 10, W: 20, H: 1}
	rows := fanRows(t, 100, 24, FanModel{Labels: fanLabels, Sel: fanMid, Anchor: anchor})
	end := func(l string) int { return fanCol(rows[fanRowOf(rows, l)], l) + layout.StringWidth(l) }
	// Mirror of the right side: the selection reaches furthest out, the others
	// curve back towards the anchor, so their ends move right.
	if !(end("删除好友") <= anchor.X && end("查看主页") >= end("删除好友") && end("发消息") >= end("查看主页")) {
		t.Fatalf("the labels do not end on the arc:\n%s", strings.Join(rows, "\n"))
	}
}

func TestFanOpensUpFromTheBottom(t *testing.T) {
	anchor := widget.Rect{X: 30, Y: 22, W: 40, H: 1}
	rows := fanRows(t, 100, 24, FanModel{Labels: fanLabels, Sel: fanMid, Anchor: anchor})
	y := fanRowOf(rows, "删除好友")
	if y < 0 || y >= anchor.Y {
		t.Fatalf("the fan did not open upwards:\n%s", strings.Join(rows, "\n"))
	}
	// The line runs unbroken from the anchor up to the row under the label.
	x := fanCol(rows[y+1], "▏")
	for r := y + 1; r < anchor.Y; r++ {
		if fanCol(rows[r], "▏") != x {
			t.Fatalf("the line breaks on row %d:\n%s", r, strings.Join(rows, "\n"))
		}
	}
	// Right side starts on the arc, left side ends on it.
	if fanCol(rows[fanRowOf(rows, "拉黑")], "拉黑") <= x || fanCol(rows[fanRowOf(rows, "查看主页")], "查看主页")+layout.StringWidth("查看主页") > x {
		t.Fatalf("the sides are not either side of the line:\n%s", strings.Join(rows, "\n"))
	}
}

func TestFanShowsEveryItemInFull(t *testing.T) {
	labels := []string{}
	for _, s := range []string{"甲", "乙", "丙", "丁", "戊", "己", "庚", "辛", "壬", "癸", "子", "丑", "寅"} {
		labels = append(labels, s+s)
	}
	labels[3] = "一个很长很长的操作名称"
	// From the middle the whole list is in view, each label in full.
	rows := fanRows(t, 120, 30, FanModel{Labels: labels, Sel: len(labels) / 2, Anchor: widget.Rect{X: 2, Y: 14, W: 20, H: 1}})
	all := strings.Join(rows, "\n")
	for _, l := range labels {
		if !strings.Contains(all, l) {
			t.Fatalf("%s missing:\n%s", l, all)
		}
	}
}

// The list has ends: on its first item nothing comes before it.
func TestFanDoesNotWrap(t *testing.T) {
	anchor := widget.Rect{X: 2, Y: 10, W: 20, H: 1}
	rows := fanRows(t, 100, 24, FanModel{Labels: fanLabels, Anchor: anchor})
	y := fanRowOf(rows, "发消息")
	for r := 0; r < y; r++ {
		if strings.TrimSpace(rows[r]) != "" {
			t.Fatalf("something above the first item:\n%s", strings.Join(rows, "\n"))
		}
	}
	if fanRowOf(rows, "查看主页") != y+1 || fanRowOf(rows, "帮助") >= 0 && fanRowOf(rows, "帮助") < y {
		t.Fatalf("the list is not in order from its first item:\n%s", strings.Join(rows, "\n"))
	}
}

func TestFanHasNoKeyHint(t *testing.T) {
	rows := fanRows(t, 100, 24, FanModel{Labels: fanLabels, Anchor: widget.Rect{X: 2, Y: 10, W: 20, H: 1}})
	if all := strings.Join(rows, ""); strings.Contains(all, "Enter") || strings.Contains(all, "Esc") {
		t.Fatal("the key hint is still shown")
	}
}

func TestWheelKeyNames(t *testing.T) {
	for k, want := range map[string]string{"ctrl_space": "Ctrl+Space", "ctrl_g": "Ctrl+G", "f1": "F1", "": "Ctrl+Space"} {
		if got := WheelKeyName(k); got != want {
			t.Errorf("%q reads %q", k, got)
		}
	}
}

// Opening: the line first, then the middle item, then the outer rings clearly
// later. Closing: the outer items go first, then the middle, then the line.
// The selection is 删除好友, with 查看主页/拉黑 one ring out and 发消息/帮助 two.
func TestFanAnimationOrder(t *testing.T) {
	anchor := widget.Rect{X: 2, Y: 10, W: 20, H: 1}
	at := func(since time.Duration, closing bool) string {
		return strings.Join(fanRows(t, 100, 24, FanModel{Labels: fanLabels, Sel: fanMid, Anchor: anchor, Motion: true, Since: since, Closing: closing}), "\n")
	}
	if f := at(fanLineIn/2, false); strings.Contains(f, "删除好友") || !strings.Contains(f, "─") {
		t.Fatalf("mid-line: items already shown or no line:\n%s", f)
	}
	f := at(fanLineIn+fanItemIn/2, false)
	if !strings.Contains(f, "删除好友") || strings.Contains(f, "查看主页") || strings.Contains(f, "发消息") {
		t.Fatalf("the middle item should light before the others:\n%s", f)
	}
	f = at(fanLineIn+fanRingGap+fanItemIn/2, false)
	if !strings.Contains(f, "查看主页") || strings.Contains(f, "发消息") {
		t.Fatalf("the second ring should light before the third:\n%s", f)
	}
	if f := at(FanOpenDuration(len(fanLabels)), false); !strings.Contains(f, "发消息") {
		t.Fatalf("fully open but an item is missing:\n%s", f)
	}
	f = at(fanItemsOut*3/4, true)
	if strings.Contains(f, "发消息") || !strings.Contains(f, "删除好友") {
		t.Fatalf("closing: outer items should go before the middle one:\n%s", f)
	}
	if f := at(fanItemsOut+fanLineOut/2, true); strings.Contains(f, "删除好友") || !strings.Contains(f, "─") {
		t.Fatalf("closing: the line should outlast the items:\n%s", f)
	}
	if f := at(FanCloseDuration(), true); strings.Contains(f, "─") {
		t.Fatalf("closed but the line is still there:\n%s", f)
	}
}
