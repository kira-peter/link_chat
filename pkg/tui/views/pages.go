package views

import (
	"fmt"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// The help, about and channel-preview pages. Like the settings page and a
// conversation they are drawn on the right-hand side, title centred on the first
// row, and they fold and unfold with the rest of the main interface
// (spec tui-chat-client / 左侧底部入口).

// PageTitleX is where a page's title starts in r: centred.
func PageTitleX(r widget.Rect, title string) int {
	return r.X + maxInt(0, (r.W-layout.StringWidth(title))/2)
}

func pageTitle(c *widget.Canvas, t theme.Tokens, r widget.Rect, title string, focused, inFlight bool) {
	if inFlight {
		return
	}
	st := t.Faint()
	if focused {
		st = t.Heading()
	}
	c.Text(PageTitleX(r, title)-r.X, 0, title, st)
}

// HelpPage draws the keyboard help.
type HelpPage struct {
	T theme.Tokens
}

// HelpPageModel is what the help page needs.
type HelpPageModel struct {
	Focused      bool
	NameInFlight bool
	// Scroll is how many rows of the help are scrolled off the top.
	Scroll int
	// WheelKey is the quick wheel key as it reads; empty means F1.
	WheelKey string
}

// helpLines is the help as rows: a heading per group, then key and description
// rows, and a blank row between groups.
type helpLine struct {
	heading, key, desc string
}

func helpLines(lang i18n.Lang, wheelKey string) []helpLine {
	var out []helpLine
	group := func(title string, rows []i18n.HelpRow) {
		if len(out) > 0 {
			out = append(out, helpLine{})
		}
		out = append(out, helpLine{heading: title})
		for _, r := range rows {
			key := r.Key
			if key == "F1" && wheelKey != "" {
				key = wheelKey
			}
			out = append(out, helpLine{key: key, desc: r.Desc.Get(lang)})
		}
	}
	group(i18n.HelpGlobal.Get(lang), i18n.HelpGlobalRows)
	group(i18n.HelpList.Get(lang), i18n.HelpListRows)
	group(i18n.HelpCompose.Get(lang), i18n.HelpComposeRows)
	group(i18n.HelpMoments.Get(lang), i18n.HelpMomentsRows)
	group(i18n.HelpFriends.Get(lang), i18n.HelpFriendsRows)
	group(i18n.HelpWheel.Get(lang), i18n.HelpWheelRows)
	return out
}

// HelpRowCount is how many rows the help has, for clamping its scroll.
func HelpRowCount(lang i18n.Lang) int { return len(helpLines(lang, "")) }

// HelpVisibleRows is how many rows of help fit in a page of height h.
func HelpVisibleRows(h int) int { return maxInt(0, h-2) }

// Render draws the help, scrolled by m.Scroll rows.
func (v HelpPage) Render(s tcell.Screen, r widget.Rect, m HelpPageModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	pageTitle(c, v.T, r, i18n.HelpPageTitle.Get(lang), m.Focused, m.NameInFlight)

	ls := helpLines(lang, m.WheelKey)
	visible := HelpVisibleRows(r.H)
	start := clampInt(m.Scroll, 0, maxInt(0, len(ls)-visible))
	// Two columns, centred as a block in the page: the keys, then what they do.
	const keyW = 14
	blockW := minInt(r.W, 64)
	x := (r.W - blockW) / 2
	for i := 0; i < visible && start+i < len(ls); i++ {
		ln := ls[start+i]
		y := 2 + i
		switch {
		case ln.heading != "":
			c.Text(x, y, ln.heading, v.T.Heading())
		case ln.key != "":
			c.Text(x+1, y, layout.PadRight(ln.key, keyW), v.T.Accented())
			c.Text(x+1+keyW+1, y, layout.Truncate(ln.desc, blockW-keyW-2), v.T.Muted())
		}
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// AboutPage draws the about page.
type AboutPage struct {
	T theme.Tokens
}

// Render draws the application's name, one line about it and its version, as a
// block centred in the page.
func (v AboutPage) Render(s tcell.Screen, r widget.Rect, version string, focused, inFlight bool, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	pageTitle(c, v.T, r, i18n.AboutTitle.Get(lang), focused, inFlight)

	mid := r.H / 2
	centre := func(y int, text string, st tcell.Style) {
		text = layout.Truncate(text, r.W)
		c.Text((r.W-layout.StringWidth(text))/2, y, text, st)
	}
	centre(mid-2, i18n.AppName.Get(lang), v.T.Accented())
	centre(mid, i18n.AboutBlurb.Get(lang), v.T.Muted())
	centre(mid+2, i18n.AboutVer.Get(lang)+"  "+version, v.T.Faint())
}

// ChannelPreviewPage draws a channel the user has not joined: what it is, and how
// to join. It has no messages to show — the server keeps a channel's history for
// its members — and looking at it does not join it
// (spec tui-chat-client / 未加入的频道只预览).
type ChannelPreviewPage struct {
	T theme.Tokens
}

// Render draws the preview.
func (v ChannelPreviewPage) Render(s tcell.Screen, r widget.Rect, cv *client.Conversation, now int64, focused, inFlight bool, lang i18n.Lang) {
	if r.Empty() || cv == nil {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	pageTitle(c, v.T, r, cv.Name, focused, inFlight)

	mid := r.H / 2
	centre := func(y int, text string, st tcell.Style) {
		text = layout.Truncate(text, r.W)
		c.Text((r.W-layout.StringWidth(text))/2, y, text, st)
	}
	topic := cv.Topic
	topicStyle := v.T.Body()
	if topic == "" {
		topic, topicStyle = i18n.PreviewNoTopic.Get(lang), v.T.Faint()
	}
	lines := layout.Wrap(topic, r.W)
	visible := min(len(lines), max(1, mid-3))
	for i, line := range lines[:visible] {
		centre(mid-2-visible+i, line, topicStyle)
	}
	meta := fmt.Sprintf(i18n.PreviewMembers.Get(lang), cv.MemberCount)
	if cv.LastMessageAt > 0 {
		meta += "  ·  " + fmt.Sprintf(i18n.PreviewLast.Get(lang), i18n.RelTime(cv.LastMessageAt, now, lang))
	}
	centre(mid-1, meta, v.T.Muted())
	centre(mid+1, i18n.PreviewNotYet.Get(lang), v.T.Faint())
	centre(mid+3, i18n.PreviewJoin.Get(lang), v.T.Accented())
}

// NewPage is the page of a section's "new" button: start a direct message, or
// create a channel. Entering it folds the list and puts the keyboard in the form.
type NewPage struct {
	T theme.Tokens
}

// NewPageModel is what the new page draws.
type NewPageModel struct {
	// Channel is true for "new channel", false for "direct message".
	Channel      bool
	Value        string
	Topic        string
	Private      bool
	EditingID    string
	Field        NewChannelField
	Err          string
	Busy         bool
	Focused      bool
	NameInFlight bool
}

type NewChannelField int

const (
	ChannelNameField NewChannelField = iota
	ChannelTopicField
	ChannelVisibilityField
	ChannelConfirmField
	ChannelCancelField
	ChannelFieldCount
)

// NewPageTitle is the page's title, which is also what flies to it at a fold.
func NewPageTitle(channel bool, lang i18n.Lang) string {
	if channel {
		return i18n.NewChannel.Get(lang)
	}
	return i18n.OpenDM.Get(lang)
}

// newFieldW is the width of the page's field: wide enough for a long name, and
// no wider, so the block reads as one thing in the middle of the page.
const newFieldW = 36

// Render draws the title, a line about what the page does, the label and field,
// and under it the hint for what to press — or the error, or that it is busy.
func (v NewPage) Render(s tcell.Screen, r widget.Rect, m NewPageModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	title := NewPageTitle(m.Channel, lang)
	if m.EditingID != "" {
		title = i18n.EditChannel.Get(lang)
	}
	pageTitle(c, v.T, r, title, m.Focused, m.NameInFlight)
	if m.Channel {
		v.renderChannelForm(c, m, lang)
		return
	}

	mid := r.H / 2
	centre := func(y int, text string, st tcell.Style) {
		text = layout.Truncate(text, r.W)
		c.Text((r.W-layout.StringWidth(text))/2, y, text, st)
	}
	top := max(1, mid-1)
	if top > 2 {
		centre(top-2, i18n.NewDMBlurb.Get(lang), v.T.Muted())
	}
	label := i18n.PeerName.Get(lang)
	lw := min(layout.StringWidth(label), r.W/2)
	fw := min(newFieldW, max(0, r.W-lw-3))
	x := max(0, (r.W-(lw+2+fw))/2)
	c.Text(x, top, layout.Truncate(label, lw), v.T.Muted())
	Dialog{T: v.T}.fieldRow(c, x+lw+2, top, fw, m.Value, formFocus(m, ChannelNameField))
	hint, style := v.formHint(m, i18n.NewPageSubmit, lang)
	centre(top+2, hint, style)
}

func formFocus(m NewPageModel, field NewChannelField) bool {
	return m.Focused && !m.Busy && m.Field == field
}

func (v NewPage) formHint(m NewPageModel, hint i18n.T, lang i18n.Lang) (string, tcell.Style) {
	switch {
	case m.Busy && m.EditingID != "":
		return i18n.SavingChannel.Get(lang), v.T.Faint()
	case m.Busy:
		return i18n.NewPageBusy.Get(lang), v.T.Faint()
	case m.Err != "":
		return m.Err, v.T.DangerStyle()
	case m.Focused:
		return hint.Get(lang), v.T.Faint()
	}
	return i18n.NewPageEnter.Get(lang), v.T.Accented()
}

func (v NewPage) renderChannelForm(page *widget.Canvas, m NewPageModel, lang i18n.Lang) {
	room := page.Rect.H - 1
	w := min(54, max(1, page.Rect.W-4))
	if layout.RuneWidth('─') != 1 {
		v.T.UnicodeBox = false
	}
	hint, style := v.formHint(m, i18n.NewChannelSubmit, lang)
	hints := layout.Wrap(hint, w)
	hintH := min(len(hints), 2)
	if room < 8 && m.Err == "" && !m.Busy {
		hintH = 0
	}
	gap, switchH, buttonH := 0, 1, 1
	if room >= 12 {
		buttonH = 3
	}
	if room >= 14 {
		switchH = 3
	}
	if room >= 16 {
		gap = 1
	}
	// Reserve the labels, switch and actions before expanding the description.
	fixed := 2 + gap + 1 + gap + switchH + hintH + buttonH
	topicH := min(7, max(1, room-fixed))
	top := 1 + max(0, (room-fixed-topicH)/2)
	form := widget.Rect{X: page.Rect.X + (page.Rect.W-w)/2, Y: page.Rect.Y + top, W: w, H: max(0, page.Rect.H-top)}
	c := widget.NewCanvas(page.S, v.T, form)

	c.Text(0, 0, i18n.ChannelName.Get(lang), v.T.Body())
	Dialog{T: v.T}.fieldRow(c, 0, 1, w, m.Value, formFocus(m, ChannelNameField))

	y := 2 + gap
	count := fmt.Sprintf("%d / %d", utf8.RuneCountInString(m.Topic), protocol.ChannelTopicMaxRunes)
	labelWidth := w - layout.StringWidth(count) - 1
	c.Text(0, y, layout.Truncate(i18n.ChannelTopic.Get(lang), labelWidth), v.T.Body())
	c.TextRight(y, count, v.T.Faint())
	v.descriptionField(c, y+1, topicH, m.Topic, formFocus(m, ChannelTopicField))

	y += 1 + topicH + gap
	v.publicSwitch(c, y, switchH, m, lang)
	y += switchH
	for _, line := range hints[:hintH] {
		c.Text(0, y, line, style)
		y++
	}

	action := i18n.ConfirmCreateChannel
	if m.EditingID != "" {
		action = i18n.SaveChannel
	}
	v.channelActions(c, y, buttonH, action.Get(lang), i18n.Cancel.Get(lang), m)
}

func (v NewPage) descriptionField(c *widget.Canvas, y, h int, text string, focused bool) {
	if y >= c.Rect.H {
		return
	}
	rect := widget.Rect{X: c.Rect.X, Y: c.Rect.Y + y, W: c.Rect.W, H: min(h, c.Rect.H-y)}
	box := widget.NewCanvas(c.S, v.T, rect)
	fill, border := v.T.InputStyle(), v.T.BorderSoftStyle()
	if focused {
		fill, border = v.T.InputFocusStyle(), v.T.Accented()
		text += "█"
	}
	box.Fill(fill)
	inner := rect
	if rect.H >= 3 && rect.W >= 4 {
		fg, _, _ := border.Decompose()
		box.Box(fill.Foreground(fg))
		inner = rect.InsetXY(2, 1)
	}
	rows := layout.Wrap(text, inner.W)
	if focused && len(rows) > inner.H {
		rows = rows[len(rows)-inner.H:]
	}
	txt := widget.NewCanvas(c.S, v.T, inner)
	for i, row := range rows[:min(len(rows), inner.H)] {
		txt.Text(0, i, row, fill)
	}
}

func (v NewPage) publicSwitch(c *widget.Canvas, y, h int, m NewPageModel, lang i18n.Lang) {
	const trackW = 8
	x := max(0, c.Rect.W-trackW)
	labelStyle := v.T.Body()
	if formFocus(m, ChannelVisibilityField) {
		labelStyle = v.T.Accented().Bold(true)
	}
	c.Text(0, y+(h-1)/2, layout.Truncate(i18n.ChannelPublicSwitch.Get(lang), max(0, x-1)), labelStyle)
	detail, style := i18n.ChannelPublicDetail, v.T.SuccessStyle()
	knob := "●"
	if !v.T.UnicodeBox || layout.RuneWidth('●') != 1 {
		knob = "o"
	}
	body := "    " + knob + " "
	if m.Private {
		detail, style = i18n.ChannelPrivateDetail, v.T.Muted()
		body = " " + knob + "    "
	}
	if h == 1 {
		c.Text(x, y, "("+body+")", style)
		return
	}
	c.Text(0, y+2, layout.Truncate(detail.Get(lang), max(0, x-1)), v.T.Faint())
	if v.T.UnicodeBox {
		c.Text(x, y, "╭──────╮", style)
		c.Text(x, y+1, "│"+body+"│", style)
		c.Text(x, y+2, "╰──────╯", style)
		return
	}
	c.Text(x, y, "+------+", style)
	c.Text(x, y+1, "|"+body+"|", style)
	c.Text(x, y+2, "+------+", style)
}

func (v NewPage) channelActions(c *widget.Canvas, y, h int, action, cancel string, m NewPageModel) {
	const gap = 2
	w := min(ConfirmButtonWidth(action, cancel), (c.Rect.W-gap)/2)
	if w <= 0 || y >= c.Rect.H {
		return
	}
	x := c.Rect.W - 2*w - gap
	for i, label := range []string{action, cancel} {
		focused := formFocus(m, ChannelConfirmField+NewChannelField(i))
		if h == 3 && y+3 <= c.Rect.H && w >= 3 {
			Dialog{T: v.T}.frameButton(c, x, y, w, layout.Truncate(label, w-2), ButtonState{Focused: focused})
		} else {
			style := v.T.InputStyle()
			if focused {
				style = v.T.Selected().Bold(true)
			}
			label = layout.Truncate(label, w)
			padding := (w - layout.StringWidth(label)) / 2
			c.TextFill(x, y, w, fmt.Sprintf("%*s%s", padding, "", label), style)
		}
		x += w + gap
	}
}
