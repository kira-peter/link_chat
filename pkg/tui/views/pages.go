package views

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
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
	centre(mid-3, topic, topicStyle)
	meta := fmt.Sprintf(i18n.PreviewMembers.Get(lang), cv.MemberCount)
	if cv.LastMessageAt > 0 {
		meta += "  ·  " + fmt.Sprintf(i18n.PreviewLast.Get(lang), i18n.RelTime(cv.LastMessageAt, now, lang))
	}
	centre(mid-1, meta, v.T.Muted())
	centre(mid+1, i18n.PreviewNotYet.Get(lang), v.T.Faint())
	centre(mid+3, i18n.PreviewJoin.Get(lang), v.T.Accented())
}

// NewPage is the page of a section's "new" button: start a direct message, or
// create a channel. Its one field is the whole page — like a conversation it is
// entered with → or Enter, which folds the list and puts the keyboard in the
// field (spec tui-chat-client / 新建按钮的页面).
type NewPage struct {
	T theme.Tokens
}

// NewPageModel is what the new page draws.
type NewPageModel struct {
	// Channel is true for "new channel", false for "direct message".
	Channel      bool
	Value        string
	Err          string
	Busy         bool
	Focused      bool
	NameInFlight bool
}

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
	pageTitle(c, v.T, r, NewPageTitle(m.Channel, lang), m.Focused, m.NameInFlight)

	blurb, label := i18n.NewDMBlurb, i18n.PeerName
	if m.Channel {
		blurb, label = i18n.NewChannelBlurb, i18n.ChannelName
	}
	mid := r.H / 2
	centre := func(y int, text string, st tcell.Style) {
		text = layout.Truncate(text, r.W)
		c.Text((r.W-layout.StringWidth(text))/2, y, text, st)
	}
	centre(mid-3, blurb.Get(lang), v.T.Muted())

	// The label and field as one centred row.
	lw := layout.StringWidth(label.Get(lang))
	fw := minInt(newFieldW, maxInt(8, r.W-lw-6))
	x := maxInt(0, (r.W-(lw+2+fw))/2)
	c.Text(x, mid-1, label.Get(lang), v.T.Muted())
	Dialog{T: v.T}.fieldRow(c, x+lw+2, mid-1, fw, m.Value, m.Focused && !m.Busy)

	switch {
	case m.Busy:
		centre(mid+1, i18n.NewPageBusy.Get(lang), v.T.Faint())
	case m.Err != "":
		centre(mid+1, m.Err, v.T.DangerStyle())
	case m.Focused:
		centre(mid+1, i18n.NewPageSubmit.Get(lang), v.T.Faint())
	default:
		centre(mid+1, i18n.NewPageEnter.Get(lang), v.T.Accented())
	}
}
