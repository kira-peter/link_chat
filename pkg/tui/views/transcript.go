package views

import (
	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// Transcript renders the message stream.
//
// Messages are laid out as bubbles — other people's on the left, the current
// user's on the right — and three rules carry most of the quality:
//
//  1. A run of messages from one author within MergeWindow shows the author once,
//     above the first bubble, then bubbles alone — the way every good chat client
//     does it, and the reason a busy channel stays readable. Your own side never
//     shows a name: you know who you are.
//  2. Text wraps inside its bubble by display width, so a long message stays a
//     paragraph and CJK and emoji are never split.
//  3. Times appear only where they add information (the first message, after a
//     silence), as a quiet centred line, never as a rule across the screen.
//
// The layout itself is in messages.go and is a pure function; this file draws it.
type Transcript struct {
	T theme.Tokens
}

// MergeWindow is how long consecutive messages from one author are grouped.
const MergeWindow = 2 * 60 * 1000

// TranscriptModel is what the view needs from the app.
type TranscriptModel struct {
	Conversation *client.Conversation
	UserID       string
	// ScrollFromBottom is how many lines up from the newest the viewport sits.
	// Zero means pinned to the bottom, which is the normal state.
	ScrollFromBottom int
	Now              int64
	HasNew           bool
	LoadingOlder     bool
	Focus            client.Focus
	// NameInFlight is true while the conversation's name is travelling between the
	// sidebar and this title. The flight layer is drawing it then, so the title
	// leaves its own slot blank instead of showing the same name twice.
	NameInFlight bool
}

// Render draws the transcript and returns the total number of rendered lines,
// which the caller uses to clamp scrolling.
func (v Transcript) Render(s tcell.Screen, r widget.Rect, m TranscriptModel, lang i18n.Lang) int {
	if r.Empty() {
		return 0
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())

	if m.Conversation == nil {
		v.renderWelcome(c, lang)
		return 0
	}
	cv := m.Conversation

	// Header: conversation name, member count, topic. This is the one place a
	// heading is allowed to be larger than the body — by colour, not by bold.
	head, body := r.Take(2)
	v.renderHeader(s, head, cv, m, lang)

	if len(cv.Messages) == 0 {
		v.renderEmptyState(s, body, lang)
		return 0
	}

	// Lay the conversation out as items (bubbles, time lines, …), then draw the
	// window of rows that is on screen. The total height is the sum of the items'
	// heights, so scrolling stays in whole rows exactly as before.
	items := LayoutMessages(cv.Messages, body.W, m.UserID, m.Now, lang)
	total := TotalHeight(items)

	// The note at the top ("loading earlier messages", "start of conversation") gets
	// a row of its own above the messages instead of being drawn over them. With
	// bubbles the top row of the window can be a border or a time line, and a note
	// painted on it would erase real content.
	//
	// The row costs the window one line, so a note is only worth showing if the
	// window still reaches the top with that line gone. Otherwise showing it would
	// scroll the first row of the conversation out of view to make room for a note
	// saying "this is the start" about a start that is no longer on screen. Both
	// notes follow that rule: while loading, the older messages are about to appear
	// above anyway.
	note := ""
	view := body
	start := windowStart(total, view.H, m.ScrollFromBottom)

	wanted := ""
	switch {
	case m.LoadingOlder:
		wanted = i18n.LoadingOlder.Get(lang)
	case !cv.HasMore && start == 0 && total > 0:
		wanted = i18n.ReachedStart.Get(lang)
	}
	if wanted != "" && body.H > 1 {
		_, shorter := body.Take(1)
		shortStart := windowStart(total, shorter.H, m.ScrollFromBottom)
		// "Loading" is shown whenever the window is at the top of what is loaded;
		// "start of conversation" only when the shorter window still shows it all.
		if (m.LoadingOlder && start == 0) || shortStart == 0 {
			note, view, start = wanted, shorter, shortStart
		}
	}
	if note != "" {
		nc := widget.NewCanvas(s, v.T, body.Row(0))
		nc.Fill(v.T.Base())
		nc.Text(layout.GutterX, 0, note, v.T.Faint())
	}

	v.drawWindow(s, view, items, start)

	// New-message hint: offered, never forced. Yanking someone out of history
	// because a message arrived is the rudest thing a chat client can do
	// (spec tui-chat-client / 新消息不打断阅读).
	if m.HasNew {
		bottom := widget.NewCanvas(s, v.T, body.Row(body.H-1))
		bottom.Fill(v.T.Base())
		hint := "↓ " + i18n.NewBelow.Get(lang)
		x := (body.W - layout.StringWidth(hint)) / 2
		bottom.Text(maxInt(0, x), 0, hint, v.T.Accented())
	}

	return total
}

// windowStart is the index of the first row shown. The newest row sits on the last
// line of the view and scrolling moves the window up from there.
func windowStart(total, viewH, scrollFromBottom int) int {
	start := total - viewH - scrollFromBottom
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	return start
}

// renderHeader draws the conversation title block.
func (v Transcript) renderHeader(s tcell.Screen, r widget.Rect, cv *client.Conversation, m TranscriptModel, lang i18n.Lang) {
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())

	name := cv.Name
	if name == "" {
		name = i18n.OpenDM.Get(lang)
	}
	// Accent only while the composer side has the keyboard; otherwise it recedes,
	// so the focused region is the one that reads as lit.
	titleStyle := v.T.Faint()
	if m.Focus == client.FocusInput {
		titleStyle = v.T.Heading()
	}
	// While the name is flying here the flight layer owns it. The slot is skipped,
	// but the width is still counted so the member count does not jump when the
	// name lands.
	var x int
	if m.NameInFlight {
		x = layout.StringWidth(name)
	} else {
		x = c.Text(0, 0, name, titleStyle)
	}

	// Meta on the same row, dimmed, after a gap: "# general  · 3 members".
	if cv.Kind == protocol.KindChannel {
		meta := "· " + i18n.Num(cv.MemberCount)
		x += 1
		c.Text(x, 0, meta, v.T.Faint())
	}

	if cv.Topic != "" {
		c.Text(0, 1, layout.Truncate(cv.Topic, r.W), v.T.Muted())
	}
}

// renderEmptyState fills an empty conversation with a quiet prompt.
func (v Transcript) renderEmptyState(s tcell.Screen, r widget.Rect, lang i18n.Lang) {
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	midY := r.H / 2
	title := i18n.NoMessages.Get(lang)
	sub := i18n.NoMessagesSub.Get(lang)
	c.Text((r.W-layout.StringWidth(title))/2, midY, title, v.T.Muted())
	c.Text((r.W-layout.StringWidth(sub))/2, midY+1, sub, v.T.Faint())
}

func (v Transcript) renderWelcome(c *widget.Canvas, lang i18n.Lang) {
	c.Fill(v.T.Base())
	title := i18n.AppName.Get(lang)
	sub := i18n.Tagline.Get(lang)
	midY := c.Rect.H / 2
	c.Text((c.Rect.W-layout.StringWidth(title))/2, midY, title, v.T.Heading())
	c.Text((c.Rect.W-layout.StringWidth(sub))/2, midY+1, sub, v.T.Faint())
}

// isSystemMessage treats the membership markers the server writes into history
// as events rather than messages.
func isSystemMessage(m *protocol.Message) bool {
	return m.Body == "joined" || m.Body == "left"
}

func sameDayLabel(millis, now int64) bool {
	a := i18n.FullTime(millis)
	b := i18n.FullTime(now)
	return len(a) >= 10 && len(b) >= 10 && a[:10] == b[:10]
}

// ComposerRect splits the main region into transcript and composer.
//
// The composer grows with its content up to a cap, so a multi-line draft is
// visible without letting a long paste swallow the conversation
// (spec tui-chat-client / 输入区与编辑键位).
func ComposerRect(main widget.Rect, inputLines int) (transcript, composer widget.Rect) {
	h := inputLines
	if h < layout.InputMinRows {
		h = layout.InputMinRows
	}
	if h > layout.InputMaxRows {
		h = layout.InputMaxRows
	}
	// One row of gap, one border row top and bottom for the composer box.
	need := h + 3
	if need > main.H-4 {
		need = main.H / 3
	}
	return widget.Rect{X: main.X, Y: main.Y, W: main.W, H: main.H - need},
		widget.Rect{X: main.X, Y: main.Y + main.H - need, W: main.W, H: need}
}
