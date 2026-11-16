package views

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// SettingsPage draws the settings page on the right-hand side.
type SettingsPage struct {
	T theme.Tokens
}

// SettingsPageModel is what the settings page needs.
type SettingsPageModel struct {
	Nav SettingsNav
	// Focused is true while the page has the keyboard. Without it the page is
	// shown as a preview of itself: no selection highlight, no wheel.
	Focused bool
	// Value is the text of a setting's current value.
	Value func(SettingID) string
	// Choices are the labels of a choice item's values, in order.
	Choices func(SettingID) []string
	// Preview is the chat layout and tokens the preview is drawn with: in the
	// choice mode they carry the candidate, so the preview shows it before it is
	// saved (spec tui-settings / 微型预览).
	Preview      Layout
	PreviewT     theme.Tokens
	PreviewLang  i18n.Lang
	NameInFlight bool
}

// Settings page geometry.
const (
	settingsCatW   = 16 // the category column, including its marker
	settingsGap    = 3  // between the columns, with a rule in the middle
	settingsLabelW = 14 // an item's label column
	previewMinW    = 34
	// previewMinH is the shortest preview worth drawing: the border and the last
	// ten rows of the sample, which hold the other person's bubble, a time line and
	// the user's reply.
	previewMinH = 12
)

// SettingsTitleX is where the page's title starts in r: centred, like a
// conversation's, so it lands at the same place when the list folds.
func SettingsTitleX(r widget.Rect, lang i18n.Lang) int {
	return r.X + maxInt(0, (r.W-layout.StringWidth(i18n.SettingsTitle.Get(lang)))/2)
}

// Render draws the page.
func (v SettingsPage) Render(s tcell.Screen, r widget.Rect, m SettingsPageModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())

	// Title, centred.
	title := i18n.SettingsTitle.Get(lang)
	titleStyle := v.T.Faint()
	if m.Focused {
		titleStyle = v.T.Heading()
	}
	if !m.NameInFlight {
		c.Text(SettingsTitleX(r, lang)-r.X, 0, title, titleStyle)
	}

	body := widget.Rect{X: r.X, Y: r.Y + 2, W: r.W, H: r.H - 2}
	if body.H <= 0 {
		return
	}
	catW := minInt(settingsCatW, body.W/3)
	left := widget.Rect{X: body.X, Y: body.Y, W: catW, H: body.H}
	right := widget.Rect{X: body.X + catW + settingsGap, Y: body.Y, W: body.W - catW - settingsGap, H: body.H}

	v.categories(s, left, m, lang)
	// The rule between the two columns.
	ruleX := body.X + catW + settingsGap/2
	for y := body.Y; y < body.Y+body.H; y++ {
		s.SetContent(ruleX, y, ruleGlyph(v.T), nil, v.T.BorderSoftStyle())
	}
	if right.W > 0 {
		if a, ok := ActionAt(m.Nav.Cat); ok {
			v.actionNote(s, right, a, lang)
		} else {
			v.items(s, right, m, lang)
		}
	}
}

func ruleGlyph(t theme.Tokens) rune {
	if t.UnicodeBox {
		return '│'
	}
	return '|'
}

// categories draws the left column: block titles, and under each its categories.
// A blank row separates the blocks.
func (v SettingsPage) categories(s tcell.Screen, r widget.Rect, m SettingsPageModel, lang i18n.Lang) {
	c := widget.NewCanvas(s, v.T, r)
	y, idx := 0, 0
	for bi, b := range SettingsCatalog {
		if bi > 0 {
			y++ // the gap between blocks
		}
		c.Text(0, y, layout.Truncate(b.Title.Get(lang), r.W), v.T.Faint())
		y++
		for _, cat := range b.Categories {
			label := layout.Truncate(cat.Title.Get(lang), r.W-2)
			on := idx == m.Nav.Cat
			style := v.T.Muted()
			switch {
			case on && m.Focused && m.Nav.Level == LevelCategory:
				c.TextPadded(0, y, "", v.T.Selected())
				style = v.T.Selected()
			case on:
				// The open category stays marked while its items have the keyboard.
				style = v.T.Body()
				c.Text(0, y, "›", v.T.Accented())
			}
			c.Text(2, y, label, style)
			y++
			idx++
		}
	}
	// The account operations, at the very bottom under a rule: they are not
	// settings but things that end this session, so they sit apart.
	bottom := r.H - len(AccountActions)
	if bottom <= y+1 {
		bottom = y + 2
	}
	c.Rule(bottom-1, v.T.BorderSoftStyle())
	for i, a := range AccountActions {
		yy := bottom + i
		label := layout.Truncate(a.Title.Get(lang), r.W-2)
		style := v.T.Muted()
		if idx+i == m.Nav.Cat && m.Focused && m.Nav.Level == LevelCategory {
			c.TextPadded(0, yy, "", v.T.Selected())
			style = v.T.Selected()
		}
		c.Text(2, yy, label, style)
	}
}

// actionNote draws what an account operation will do, in the right column, when
// the cursor rests on it.
func (v SettingsPage) actionNote(s tcell.Screen, r widget.Rect, a AccountAction, lang i18n.Lang) {
	c := widget.NewCanvas(s, v.T, r)
	for _, x := range AccountActions {
		if x.Action != a {
			continue
		}
		c.Text(0, 0, x.Title.Get(lang), v.T.Heading())
		for i, ln := range layout.Wrap(x.Note.Get(lang), minInt(r.W-1, 56)) {
			c.Text(1, 2+i, ln, v.T.Muted())
		}
		hint := i18n.ActionHint
		if a == ActionSwitchAccount {
			hint = i18n.SwitchHint
		}
		c.Text(1, 2+len(layout.Wrap(x.Note.Get(lang), minInt(r.W-1, 56)))+1, hint.Get(lang), v.T.Accented())
	}
}

// items draws the right column: the category's title, its items with their
// values and, for a category that changes how chat looks, the preview centred in
// the space below them.
//
// The choice mode opens a wheel on the item being chosen (spec tui-settings /
// 选择模式):
//
//   - the rows within wheelClear of that item, above and below, are hidden
//     whatever the number of candidates, so a two-way switch hides as much as a
//     long list does; the wheel shows at most wheelAbove candidates each way, so
//     there is always a blank row between it and the nearest other item;
//   - the rest of the right column — the category's title and the items left
//     showing — goes faint, so the wheel is the one thing that stands out there.
//     The category column beside it is left as it is;
//   - the list always starts wheelAbove rows below the title, and the preview
//     always stays wheelClear rows below the list, so even the first or last
//     item's wheel fits and nothing moves when the mode is entered or left.
func (v SettingsPage) items(s tcell.Screen, r widget.Rect, m SettingsPageModel, lang i18n.Lang) {
	cats := Categories()
	if m.Nav.Cat < 0 || m.Nav.Cat >= len(cats) {
		return
	}
	cat := cats[m.Nav.Cat]
	c := widget.NewCanvas(s, v.T, r)
	labelW := minInt(settingsLabelW, r.W/2)
	valueX := labelW + 2
	choosing := Choosing(m)

	first := ItemsRow
	titleStyle := v.T.Heading()
	if choosing {
		titleStyle = v.T.Faint()
	}
	// The title stays on the first row: the list scrolls under it.
	c.Text(0, 0, cat.Title.Get(lang), titleStyle)
	for i, it := range cat.Items {
		y := first + i
		if y >= r.H {
			break
		}
		d := i - m.Nav.Item
		if choosing && d != 0 && d >= -wheelClear && d <= wheelClear {
			continue // inside the wheel's clear space
		}
		on := m.Focused && m.Nav.Level == LevelItem && i == m.Nav.Item
		label := layout.Truncate(it.Label.Get(lang), labelW)
		val := ""
		if m.Value != nil && !(choosing && d == 0) {
			val = m.Value(it.ID)
		}
		labelStyle, valStyle := v.T.Body(), v.T.Muted()
		if it.Kind == SettingReadOnly {
			labelStyle, valStyle = v.T.Muted(), v.T.Faint()
		}
		switch {
		case choosing && d == 0:
			// The item being chosen keeps its label as it was.
		case choosing:
			labelStyle, valStyle = v.T.Faint(), v.T.Faint()
		case on:
			c.TextPadded(0, y, "", v.T.Selected())
			labelStyle, valStyle = v.T.Selected(), v.T.SelectedMuted()
		}
		c.Text(1, y, label, labelStyle)
		if val != "" {
			c.Text(valueX, y, layout.Truncate(val, r.W-valueX-1), valStyle)
		}
	}
	if choosing {
		it := cat.Items[m.Nav.Item]
		v.wheel(s, widget.Rect{X: r.X + valueX, Y: r.Y + first + m.Nav.Item, W: r.W - valueX - 1},
			m.Choices(it.ID), m.Nav.Choice)
	}

	if cat.Preview {
		// Under the list and the clear space a wheel on its last item keeps: the
		// same in both modes.
		used := first + len(cat.Items) + wheelClear
		area := widget.Rect{X: r.X, Y: r.Y + used, W: r.W, H: r.H - used}
		pw := minInt(area.W, 60)
		full := PreviewHeight(pw, m.Preview, m.PreviewLang)
		ph := minInt(full, area.H)
		// The whole sample fits: draw it, however short it is — without borders
		// it is shorter than previewMinH and still complete. Cut down to the room
		// left, it must keep at least previewMinH rows, or it is left out.
		if pw >= previewMinW && (ph == full || ph >= previewMinH) {
			pr := widget.Rect{
				X: area.X + (area.W-pw)/2,
				Y: area.Y + (area.H-ph)/2,
				W: pw, H: ph,
			}
			MiniPreview{T: m.PreviewT}.Render(s, pr, m.Preview, m.PreviewLang)
		}
	}
}

// Choosing reports whether the page is in the choice mode with a wheel to draw.
func Choosing(m SettingsPageModel) bool {
	cats := Categories()
	if !m.Focused || m.Nav.Level != LevelChoose || m.Choices == nil ||
		m.Nav.Cat < 0 || m.Nav.Cat >= len(cats) {
		return false
	}
	return m.Nav.Item >= 0 && m.Nav.Item < len(cats[m.Nav.Cat].Items)
}

// ItemsRow is the row of a category's first item, counted from the top of the
// right column: the category's title, then wheelAbove rows kept free so that a
// wheel on the first item has its two candidates above it. The row under the
// title takes the third clear row's place: the title is not an item.
const ItemsRow = 1 + wheelAbove

// wheelAbove is how many candidates the wheel shows above and below the current
// one: the two fading steps.
const wheelAbove = 2

// wheelClear is how many rows are kept clear above and below the item being
// chosen: the wheel's reach and one blank row beyond it.
const wheelClear = wheelAbove + 1

// wheel draws the candidates stacked about row r.Y: the current one in the
// middle, bold in the warning colour so it cannot be missed, and the others
// fading with their distance from it (spec tui-design-system / 候选值逐级变淡).
func (v SettingsPage) wheel(s tcell.Screen, r widget.Rect, choices []string, cur int) {
	w := 0
	for _, ch := range choices {
		w = maxInt(w, layout.StringWidth(ch))
	}
	w = minInt(w+4, r.W)
	for d := -wheelAbove; d <= wheelAbove; d++ {
		i := cur + d
		if i < 0 || i >= len(choices) {
			continue
		}
		row := widget.NewCanvas(s, v.T, widget.Rect{X: r.X, Y: r.Y + d, W: w, H: 1})
		text := layout.Truncate(choices[i], w-4)
		switch d {
		case 0:
			st := v.T.WarningStyle().Bold(true)
			if v.T.ColorCap == theme.ColorMono {
				st = v.T.Base().Bold(true).Reverse(true)
				row.Text(0, 0, "‹", st)
				row.Text(w-1, 0, "›", st)
			}
			row.Text(2, 0, text, st)
		case -1, 1:
			row.Text(2, 0, text, v.T.Muted())
		default:
			row.Text(2, 0, text, v.T.Faint())
		}
	}
}

// MiniPreview is the small chat shown under the settings that change how chat
// looks. It is drawn with the same layout and drawing code as the real
// conversation, so what it shows is what the user will see.
type MiniPreview struct {
	T theme.Tokens
}

// previewConversation is the sample the preview shows: someone else's message, a
// time line, and the user's own reply.
func previewConversation(lang i18n.Lang) *client.Conversation {
	base := time.Date(2026, 1, 1, 20, 0, 0, 0, time.Local).UnixMilli()
	// The first message is long enough to reach the bubble's width cap, so the
	// width setting visibly changes it.
	other := "这是一句长一点的话，长到会碰到气泡的宽度上限"
	mine := "看起来不错"
	if lang == i18n.EN {
		other = "A longer line, long enough to reach the bubble width cap"
		mine = "Looks good"
	}
	cv := &client.Conversation{}
	cv.Messages = []protocol.Message{
		{ID: "p1", AuthorID: "them", AuthorName: "carol", Body: other, CreatedAt: base},
		{ID: "p2", AuthorID: previewMe, AuthorName: "me", Body: mine, CreatedAt: base + 26*60*1000},
	}
	return cv
}

const previewMe = "preview-me"

// PreviewHeight is how tall the preview box must be at width w to show the whole
// sample conversation with layout lay: its rows plus the border. A candidate that
// changes the wrapping (a narrower bubble, say) changes the height with it, so
// the sample is never cut off.
func PreviewHeight(w int, lay Layout, lang i18n.Lang) int {
	cv := previewConversation(lang)
	inner := w - 4
	if inner <= 0 {
		return 2
	}
	items := LayoutMessagesWith(cv.Messages, inner, previewMe, cv.Messages[1].CreatedAt, lang, lay)
	return TotalHeight(items) + 2
}

// Render draws the preview box: a border in the general border colour, and inside
// it the sample conversation laid out with lay.
func (v MiniPreview) Render(s tcell.Screen, r widget.Rect, lay Layout, lang i18n.Lang) {
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	c.BoxTitle(i18n.PreviewTitle.Get(lang), v.T.BorderStyle(), v.T.Faint())
	inner := widget.Rect{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: r.H - 2}
	if inner.Empty() {
		return
	}
	cv := previewConversation(lang)
	items := LayoutMessagesWith(cv.Messages, inner.W, previewMe, cv.Messages[1].CreatedAt, lang, lay)
	// The whole sample when it fits; otherwise its end, which keeps both speakers
	// and the time line between them.
	Transcript{T: v.T}.drawWindow(s, inner, items, maxInt(0, TotalHeight(items)-inner.H))
}
