package views

import (
	"fmt"
	"strings"

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
// Three rules carry most of the quality here:
//
//  1. A run of messages from one author within MergeWindow seconds shows the
//     author and time once, then bare text — the way every good chat client does
//     it, and the reason a busy channel stays readable.
//  2. Continuation lines align under the body, never under the author, so a
//     wrapped message reads as one paragraph.
//  3. Timestamps appear only where they add information (first message, after a
//     gap, after an author change), never on every line.
type Transcript struct {
	T theme.Tokens
}

// MergeWindow is how long consecutive messages from one author are grouped.
const MergeWindow = 2 * 60 * 1000

// AuthorColumn is the fixed width reserved for the author name, so every body
// starts at the same x.
const AuthorColumn = 12

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

	lines := v.buildLines(cv.Messages, body.W, m, lang)

	// Bottom-anchored: the newest line sits on the last row, and scrolling moves
	// a window up from there.
	viewH := body.H
	total := len(lines)
	start := total - viewH - m.ScrollFromBottom
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + viewH
	if end > total {
		end = total
	}

	bc := widget.NewCanvas(s, v.T, body)
	bc.Fill(v.T.Base())
	for i := start; i < end; i++ {
		ln := lines[i]
		bc.Text(0, i-start, ln.text, ln.style)
	}

	// Top affordances, drawn over the content gutter.
	if m.LoadingOlder {
		top := widget.NewCanvas(s, v.T, body.Row(0))
		top.Fill(v.T.Base())
		top.Text(layout.GutterX, 0, i18n.LoadingOlder.Get(lang), v.T.Faint())
	} else if !cv.HasMore && start == 0 && total > 0 {
		top := widget.NewCanvas(s, v.T, body.Row(0))
		top.Fill(v.T.Base())
		top.Text(layout.GutterX, 0, i18n.ReachedStart.Get(lang), v.T.Faint())
	}

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

// renderedLine is one row of the transcript after wrapping.
type renderedLine struct {
	text  string
	style tcell.Style
}

// buildLines turns messages into rows.
func (v Transcript) buildLines(msgs []protocol.Message, width int, m TranscriptModel, lang i18n.Lang) []renderedLine {
	var out []renderedLine
	bodyX := AuthorColumn + 2
	if bodyX > width-10 {
		bodyX = width / 3
	}
	bodyW := width - bodyX
	if bodyW < 8 {
		bodyW = 8
	}

	var prev *protocol.Message
	for i := range msgs {
		msg := &msgs[i]

		// System events (joins) render as a dimmed centred line, visually
		// distinct from conversation (spec tui-chat-client / 系统事件样式区分).
		if isSystemMessage(msg) {
			out = append(out, v.systemLine(msg, width, lang))
			prev = nil
			continue
		}

		// A time divider appears after a long gap, which is where a timestamp
		// actually carries information.
		if prev == nil || msg.CreatedAt-prev.CreatedAt > 5*60*1000 {
			label := i18n.ClockOnly(msg.CreatedAt)
			if !sameDayLabel(msg.CreatedAt, m.Now) {
				label = i18n.FullTime(msg.CreatedAt)
			}
			out = append(out, renderedLine{text: dividerLine(label, width, v.T), style: v.T.Faint()})
		}

		if msg.Deleted {
			out = append(out, renderedLine{
				text:  pad(bodyX) + i18n.MessageDeleted.Get(lang),
				style: v.T.Faint(),
			})
			prev = nil
			continue
		}

		merged := prev != nil &&
			prev.AuthorID == msg.AuthorID &&
			msg.CreatedAt-prev.CreatedAt <= MergeWindow &&
			!prev.Deleted

		if merged {
			// Continuation: body only, aligned under the first line's body.
			for _, ln := range layout.Wrap(msg.Body, bodyW) {
				out = append(out, renderedLine{text: pad(bodyX) + ln, style: v.T.Body()})
			}
			if msg.EditedAt > 0 {
				out = append(out, renderedLine{text: pad(bodyX) + i18n.MessageEdited.Get(lang), style: v.T.Faint()})
			}
		} else {
			if prev != nil {
				// Breathing room between author groups.
				out = append(out, renderedLine{text: "", style: v.T.Base()})
			}
			// Author line: name in its fixed column, clock right after it.
			authorStyle := v.T.Muted()
			if msg.AuthorID == m.UserID {
				authorStyle = v.T.Accented()
			}
			name := msg.AuthorName
			if name == "" {
				name = "?"
			}
			nameCell := layout.PadRight(layout.Truncate(name, AuthorColumn), AuthorColumn)
			clockCell := i18n.ClockOnly(msg.CreatedAt)
			out = append(out, renderedLine{
				text:  nameCell + "  " + clockCell,
				style: authorStyle,
			})
			for _, ln := range layout.Wrap(msg.Body, bodyW) {
				out = append(out, renderedLine{text: pad(bodyX) + ln, style: v.T.Body()})
			}
			if msg.EditedAt > 0 {
				out = append(out, renderedLine{text: pad(bodyX) + i18n.MessageEdited.Get(lang), style: v.T.Faint()})
			}
		}
		prev = msg
	}
	return out
}

// dividerLine renders "── 14:32 ──────" while respecting the ASCII fallback.
func dividerLine(label string, width int, t theme.Tokens) string {
	ch := "─"
	if !t.UnicodeBox {
		ch = "-"
	}
	lab := " " + label + " "
	lw := layout.StringWidth(lab)
	if width <= lw+4 {
		return lab
	}
	side := (width - lw) / 2
	return strings.Repeat(ch, side) + lab + strings.Repeat(ch, width-lw-side)
}

func (v Transcript) systemLine(msg *protocol.Message, width int, lang i18n.Lang) renderedLine {
	what := msg.Body
	switch msg.Body {
	case "joined":
		what = i18n.JoinedChannel.Get(lang)
	case "left":
		what = i18n.LeftChannel.Get(lang)
	}
	line := "· " + msg.AuthorName + " " + what
	if x := (width - layout.StringWidth(line)) / 2; x > 0 {
		line = pad(x) + line
	}
	return renderedLine{text: line, style: v.T.Faint()}
}

// isSystemMessage treats the membership markers the server writes into history
// as events rather than messages.
func isSystemMessage(m *protocol.Message) bool {
	return m.Body == "joined" || m.Body == "left"
}

func pad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
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

var _ = fmt.Sprintf
