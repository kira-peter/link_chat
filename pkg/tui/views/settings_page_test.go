package views

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

var settingsRect = widget.Rect{X: 2, Y: 2, W: 96, H: 30}

func testValues(id SettingID) string {
	return map[SettingID]string{
		SetUsername: "alice", SetServerAddr: "ws://x/ws", SetCredStore: "系统凭据库",
		SetRemember: "开启", SetTheme: "墨（深色）", SetMotion: "开启",
		SetBubbleWidth: "70%", SetDensity: "舒适", SetTimeFormat: "24 小时", SetLanguage: "中文",
	}[id]
}

func testChoices(id SettingID) []string {
	switch id {
	case SetBubbleWidth:
		return []string{"60%", "70%", "80%"}
	case SetLanguage:
		return []string{"中文", "English"}
	}
	return []string{"a", "b", "c", "d", "e"}
}

func renderSettings(t *testing.T, tk theme.Tokens, nav SettingsNav, lay Layout) tcell.SimulationScreen {
	t.Helper()
	s := newScreen(t, 100, 34)
	SettingsPage{T: tk}.Render(s, settingsRect, SettingsPageModel{
		Nav: nav, Focused: true, Value: testValues, Choices: testChoices,
		Preview: lay, PreviewT: tk, PreviewLang: i18n.ZH,
	}, i18n.ZH)
	s.Show()
	return s
}

func lines(s tcell.SimulationScreen) []string { return strings.Split(screenString(s), "\n") }

// rowWith is the screen row holding text, or -1.
func rowWith(s tcell.SimulationScreen, text string) int {
	for y, ln := range lines(s) {
		if strings.Contains(ln, text) {
			return y
		}
	}
	return -1
}

// leftRowWith is the row holding text in the settings page's left column only
// (the right column repeats the open category's name as its heading).
func leftRowWith(s tcell.SimulationScreen, text string) int {
	for y, ln := range lines(s) {
		col := []rune(ln)
		end := settingsRect.X + settingsCatW + 2
		if end > len(col) {
			end = len(col)
		}
		if strings.Contains(string(col[:end]), text) {
			return y
		}
	}
	return -1
}

// The left column has one heading per block with a blank row between blocks, and
// every category under its block.
func TestSettingsPageShowsBlocksAndCategories(t *testing.T) {
	s := renderSettings(t, toks(), SettingsNav{}, DefaultLayout)
	prev := -1
	for bi, b := range SettingsCatalog {
		y := leftRowWith(s, b.Title.Get(i18n.ZH))
		if y < 0 {
			t.Fatalf("block %q not shown:\n%s", b.Title.Get(i18n.ZH), screenString(s))
		}
		if bi > 0 && y-prev < 2 {
			t.Errorf("block %q is not separated from the one above", b.Title.Get(i18n.ZH))
		}
		for _, c := range b.Categories {
			cy := leftRowWith(s, c.Title.Get(i18n.ZH))
			if cy <= y {
				t.Errorf("category %q is not under its block heading", c.Title.Get(i18n.ZH))
			}
			prev = cy
		}
	}
}

// Selecting a category shows its items with their values.
func TestSettingsPageShowsTheSelectedCategorysItems(t *testing.T) {
	cat := catIndex(t, "聊天")
	s := renderSettings(t, toks(), SettingsNav{Cat: cat}, DefaultLayout)
	for _, w := range []string{"气泡最大宽度", "70%", "消息密度", "舒适", "时间格式", "24 小时"} {
		if rowWith(s, w) < 0 {
			t.Errorf("the chat category does not show %q", w)
		}
	}
	if rowWith(s, "alice") >= 0 {
		t.Error("another category's values are shown")
	}
}

// Which column has the keyboard is visible: the selection fill moves with it.
func TestSettingsSelectionFollowsTheLevel(t *testing.T) {
	cat := catIndex(t, "聊天")
	selectedRow := func(nav SettingsNav) string {
		s := renderSettings(t, toks(), nav, DefaultLayout)
		w, h := s.Size()
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				_, _, st, _ := s.GetContent(x, y)
				if _, bg, _ := st.Decompose(); bg == toks().SelectedBg {
					return lines(s)[y]
				}
			}
		}
		return ""
	}
	if got := selectedRow(SettingsNav{Cat: cat, Level: LevelCategory}); !strings.Contains(got, "聊天") {
		t.Errorf("at the category level the highlighted row is %q", got)
	}
	if got := selectedRow(SettingsNav{Cat: cat, Level: LevelItem, Item: 1}); !strings.Contains(got, "消息密度") {
		t.Errorf("at the item level the highlighted row is %q", got)
	}
}

// rightTop is the first row of the settings page's right column in renderSettings.
var rightTop = settingsRect.Y + 2

// itemRow is the screen row of item i of a category at rest, and while it is
// being chosen.
func itemRow(i int, choosing bool) int {
	return rightTop + ItemsRow + i
}

// Entering the choice mode moves nothing: the category title, the item being
// chosen and the preview are on the same cells before and after, for every item.
func TestEnteringTheChoiceModeMovesNothing(t *testing.T) {
	for _, name := range []string{"聊天", "消息样式"} {
		cat := catIndex(t, name)
		for item := range Categories()[cat].Items {
			before := renderSettings(t, toks(), SettingsNav{Cat: cat, Level: LevelItem, Item: item}, DefaultLayout)
			after := renderSettings(t, toks(), SettingsNav{Cat: cat, Level: LevelChoose, Item: item}, DefaultLayout)
			label := Categories()[cat].Items[item].Label.Get(i18n.ZH)
			for _, w := range []string{label, "预览"} {
				bx, by, ok1 := findText(before, w)
				ax, ay, ok2 := findText(after, w)
				if !ok1 || !ok2 || bx != ax || by != ay {
					t.Errorf("%s item %d: %q at (%d,%d) %v before, (%d,%d) %v while choosing", name, item, w, bx, by, ok1, ax, ay, ok2)
				}
			}
			colX := settingsRect.X + minInt(settingsCatW, settingsRect.W/3) + settingsGap
			if r, _, _, _ := after.GetContent(colX, rightTop); r != []rune(name)[0] {
				t.Errorf("%s item %d: the category title left the column's first row", name, item)
			}
		}
	}
}

// The preview is the real transcript drawing: same items, same cells.
func TestPreviewDrawsWhatTheTranscriptWould(t *testing.T) {
	pr := widget.Rect{X: 2, Y: 2, W: 50, H: PreviewHeight(50, DefaultLayout, i18n.ZH)}
	s := newScreen(t, 60, 20)
	MiniPreview{T: toks()}.Render(s, pr, DefaultLayout, i18n.ZH)
	s.Show()

	inner := widget.Rect{X: pr.X + 2, Y: pr.Y + 1, W: pr.W - 4, H: pr.H - 2}
	ref := newScreen(t, 60, 20)
	cv := previewConversation(i18n.ZH)
	items := LayoutMessagesWith(cv.Messages, inner.W, previewMe, cv.Messages[1].CreatedAt, i18n.ZH, DefaultLayout)
	if TotalHeight(items) > inner.H {
		t.Fatalf("the sample conversation (%d rows) does not fit the preview (%d)", TotalHeight(items), inner.H)
	}
	Transcript{T: toks()}.drawWindow(ref, inner, items, 0)
	ref.Show()

	for y := inner.Y; y < inner.Y+inner.H; y++ {
		for x := inner.X; x < inner.X+inner.W; x++ {
			a, _, sa, _ := s.GetContent(x, y)
			b, _, sb, _ := ref.GetContent(x, y)
			if a != b || sa != sb {
				t.Fatalf("cell (%d,%d) differs from the transcript's drawing: %q vs %q", x, y, a, b)
			}
		}
	}
	// It contains the parts it promises.
	text := screenString(s)
	for _, w := range []string{"预览", "carol", "20:26"} {
		if !strings.Contains(text, w) {
			t.Errorf("the preview has no %q:\n%s", w, text)
		}
	}
}

// The preview follows the layout it is given, so a candidate shows before it is
// saved.
func TestPreviewFollowsTheCandidateLayout(t *testing.T) {
	cat := catIndex(t, "聊天")
	a := screenString(renderSettings(t, toks(), SettingsNav{Cat: cat}, DefaultLayout))
	b := screenString(renderSettings(t, toks(), SettingsNav{Cat: cat}, Layout{BubblePercent: 70, Clock24: false}))
	if a == b || !strings.Contains(b, "下午 8:26") {
		t.Fatalf("the preview did not change with a 12-hour candidate:\n%s", b)
	}
}

// No room: no preview, and the items are not squeezed.
func TestPreviewOmittedWithoutRoom(t *testing.T) {
	cat := catIndex(t, "外观")
	s := newScreen(t, 100, 14)
	SettingsPage{T: toks()}.Render(s, widget.Rect{X: 2, Y: 0, W: 96, H: 12}, SettingsPageModel{
		Nav: SettingsNav{Cat: cat}, Focused: true, Value: testValues, Choices: testChoices,
		Preview: DefaultLayout, PreviewT: toks(), PreviewLang: i18n.ZH,
	}, i18n.ZH)
	s.Show()
	text := screenString(s)
	if strings.Contains(text, "预览") {
		t.Fatalf("a preview is squeezed into a short page:\n%s", text)
	}
	for _, w := range []string{"主题", "动画"} {
		if !strings.Contains(text, w) {
			t.Errorf("item %q lost on a short page", w)
		}
	}
}

func TestNoPreviewForCategoriesThatDoNotChangeChat(t *testing.T) {
	for _, name := range []string{"账号", "通用"} {
		s := renderSettings(t, toks(), SettingsNav{Cat: catIndex(t, name)}, DefaultLayout)
		if strings.Contains(screenString(s), "预览") {
			t.Errorf("category %q shows a preview", name)
		}
	}
	_ = fmt.Sprint()
}

// realChoices has the chat settings' real number of candidates (3, 2, 2), which
// decides how much room the list keeps for a wheel.
func realChoices(id SettingID) []string {
	if id == SetBubbleWidth {
		return []string{"60%", "70%", "80%"}
	}
	return []string{"x", "y"}
}

// On a short page the preview shrinks to what is left and shows the end of the
// sample — still the other person's bubble, a time line and the reply — and below
// that it is left out rather than cut. With two rows kept above the list and
// three below it for the wheel, the chat preview first fits at 80x27.
func TestPreviewShrinksOnAShortPageAndThenGoes(t *testing.T) {
	render := func(h int) string {
		s := newScreen(t, 80, h)
		main := MainRectAt(ContentRect(80, h), 1)
		SettingsPage{T: toks()}.Render(s, main, SettingsPageModel{
			Nav: SettingsNav{Cat: catIndex(t, "聊天")}, Focused: true, Value: testValues, Choices: realChoices,
			Preview: DefaultLayout, PreviewT: toks(), PreviewLang: i18n.ZH,
		}, i18n.ZH)
		s.Show()
		return screenString(s)
	}
	text := render(27)
	for _, w := range []string{"预览", "长一点的话", "20:26", "看起来不错"} {
		if !strings.Contains(text, w) {
			t.Fatalf("at 80x27 the chat preview has no %q:\n%s", w, text)
		}
	}
	if strings.Count(text, "└") < 3 {
		t.Fatalf("a bubble or the box is cut off:\n%s", text)
	}
	short := render(26)
	if strings.Contains(short, "预览") {
		t.Fatalf("at 80x26 a preview is squeezed in:\n%s", short)
	}
	for _, w := range []string{"气泡最大宽度", "消息密度", "时间格式"} {
		if !strings.Contains(short, w) {
			t.Fatalf("at 80x26 the item %q is lost", w)
		}
	}
}

// previewBox finds the preview's frame on screen: its corners.
func previewBox(t *testing.T, s tcell.SimulationScreen) (left, top, right, bottom int, ok bool) {
	t.Helper()
	x, y, found := findText(s, "预览")
	if !found {
		return 0, 0, 0, 0, false
	}
	top = y
	for left = x; left > 0; left-- {
		if r, _, _, _ := s.GetContent(left, top); r == '┌' {
			break
		}
	}
	w, h := s.Size()
	for right = x; right < w; right++ {
		if r, _, _, _ := s.GetContent(right, top); r == '┐' {
			break
		}
	}
	for bottom = top + 1; bottom < h; bottom++ {
		if r, _, _, _ := s.GetContent(left, bottom); r == '└' {
			break
		}
	}
	return left, top, right, bottom, true
}

// The preview is centred in the space left of the right column: equal margins
// left and right, and above and below, within a cell.
func TestPreviewIsCentredInTheSpaceLeft(t *testing.T) {
	cat := catIndex(t, "聊天")
	for _, nav := range []SettingsNav{
		{Cat: cat},
		{Cat: cat, Level: LevelChoose, Item: 0, Choice: 1, Original: 1},
	} {
		s := renderSettings(t, toks(), nav, DefaultLayout)
		l, top, r, b, ok := previewBox(t, s)
		if !ok {
			t.Fatalf("level %v: no preview:\n%s", nav.Level, screenString(s))
		}
		catW := minInt(settingsCatW, settingsRect.W/3)
		colL := settingsRect.X + catW + settingsGap
		colR := settingsRect.X + settingsRect.W - 1
		if d := (l - colL) - (colR - r); d < -1 || d > 1 {
			t.Errorf("level %v: %d cells left of the preview, %d right", nav.Level, l-colL, colR-r)
		}
		// Vertically: the space under the list and the clear space a wheel on its
		// last item keeps, down to the page's end — the same in both modes.
		areaTop := rightTop + ItemsRow + len(Categories()[cat].Items) + wheelClear
		areaBottom := settingsRect.Y + settingsRect.H - 1
		if d := (top - areaTop) - (areaBottom - b); d < -1 || d > 1 {
			t.Errorf("level %v: %d rows above the preview, %d below", nav.Level, top-areaTop, areaBottom-b)
		}
	}
}

// While choosing, the wheel and the preview are both on screen, and the preview
// shows the candidate.
func TestPreviewStaysWhileChoosing(t *testing.T) {
	cat := catIndex(t, "聊天")
	s := renderSettings(t, toks(), SettingsNav{Cat: cat, Level: LevelChoose, Item: 2, Choice: 1}, Layout{BubblePercent: 70, Clock24: false})
	text := screenString(s)
	if !strings.Contains(text, "预览") || !strings.Contains(text, "下午 8:26") {
		t.Fatalf("while choosing the 12-hour candidate the preview is missing or not following it:\n%s", text)
	}
}

// The message style category has six items; its preview first fits at 80x30.
func TestMessageStylePreviewFitsAt30Rows(t *testing.T) {
	render := func(h int) string {
		s := newScreen(t, 80, h)
		main := MainRectAt(ContentRect(80, h), 1)
		SettingsPage{T: toks()}.Render(s, main, SettingsPageModel{
			Nav: SettingsNav{Cat: catIndex(t, "消息样式")}, Focused: true, Value: testValues, Choices: realChoices,
			Preview: DefaultLayout, PreviewT: toks(), PreviewLang: i18n.ZH,
		}, i18n.ZH)
		s.Show()
		return screenString(s)
	}
	if !strings.Contains(render(30), "预览") {
		t.Fatal("at 80x30 the message style category has no preview")
	}
	if strings.Contains(render(29), "预览") {
		t.Fatal("at 80x29 a preview is squeezed in")
	}
}

// The choice mode draws the page as it was with the item selected: the
// candidates open as a fan over it, drawn by the app, so nothing on the page
// moves or changes.
func TestTheChoiceModeLeavesThePageAsItWas(t *testing.T) {
	for _, name := range []string{"聊天", "消息样式", "外观"} {
		cat := catIndex(t, name)
		for item := range Categories()[cat].Items {
			before := renderSettings(t, toks(), SettingsNav{Cat: cat, Level: LevelItem, Item: item}, DefaultLayout)
			after := renderSettings(t, toks(), SettingsNav{Cat: cat, Level: LevelChoose, Item: item}, DefaultLayout)
			if screenString(before) != screenString(after) {
				t.Errorf("%s item %d: the page changed on entering the choice mode", name, item)
			}
		}
	}
}
