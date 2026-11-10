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

// List renders the conversation sidebar.
//
// The row layout is: a one-cell status marker, the name, and a right-aligned
// timestamp or unread count. Selection is a full-row background fill — not a
// pointer glyph, not bold — which is the single biggest thing that separates a
// considered terminal UI from a default-looking one
// (spec tui-design-system / 选中行整行高亮).
type List struct {
	T theme.Tokens
}

// ListModel is what the list needs from the app.
type ListModel struct {
	Conversations []*client.Conversation
	Selected      int
	ActiveID      string
	Filter        []rune
	Focus         client.Focus
	Now           int64
	Username      string
}

// Render draws the list into r and returns the visible window's first index,
// which the caller needs to keep the selection on screen.
func (l List) Render(s tcell.Screen, r widget.Rect, m ListModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	// Header: section label with the count, in the reference screen's voice —
	// small caps feel, accent colour, no shouting.
	headerH := 1
	if m.Filter != nil {
		headerH = 2
	}
	head, body := r.Take(headerH)

	hc := widget.NewCanvas(s, l.T, head)
	hc.Fill(l.T.Base())
	label := i18n.ListTitle.Get(lang)
	// The title takes the accent only while the list has the keyboard, so the
	// focused region is the one that stands out and the other one goes quiet.
	titleStyle := l.T.Faint()
	if m.Focus == client.FocusList {
		titleStyle = l.T.Heading()
	}
	hc.Text(0, 0, label, titleStyle)
	if n := len(m.Conversations); n > 0 {
		count := i18n.Num(n)
		hc.Text(layout.StringWidth(label)+1, 0, count, l.T.Faint())
	}

	if m.Filter != nil {
		fc := widget.NewCanvas(s, l.T, head)
		line := "/" + string(m.Filter) + "█"
		fc.Text(0, 1, layout.Truncate(line, head.W), l.T.Accented())
	}

	if len(m.Conversations) == 0 {
		l.renderEmpty(s, body, lang)
		return
	}

	// Keep the selected row inside the window.
	visible := body.H
	start := 0
	if m.Selected >= visible {
		start = m.Selected - visible + 1
	}
	if start > len(m.Conversations)-visible {
		start = len(m.Conversations) - visible
	}
	if start < 0 {
		start = 0
	}

	bc := widget.NewCanvas(s, l.T, body)
	bc.Fill(l.T.Base())

	for i := start; i < len(m.Conversations) && i-start < visible; i++ {
		cv := m.Conversations[i]
		y := i - start
		selected := i == m.Selected && m.Focus == client.FocusList
		active := cv.ID == m.ActiveID

		l.renderRow(bc, y, cv, selected, active, m, lang)
	}

	// Position indicator, only when it carries information.
	if len(m.Conversations) > visible {
		pc := widget.NewCanvas(s, l.T, head)
		pos := i18n.Num(m.Selected+1) + "/" + i18n.Num(len(m.Conversations))
		pc.TextRight(0, pos, l.T.Faint())
	}
}

// renderRow paints one conversation.
func (l List) renderRow(c *widget.Canvas, y int, cv *client.Conversation, selected, active bool, m ListModel, lang i18n.Lang) {
	nameStyle := l.T.Body()
	metaStyle := l.T.Faint()
	if selected {
		nameStyle = l.T.Selected()
		metaStyle = l.T.SelectedMuted()
	}
	if cv.Unread > 0 && !selected {
		nameStyle = l.T.Emphasis()
	}

	// Full-row background for selection.
	if selected {
		c.TextPadded(0, y, "", l.T.Selected())
	}

	// Left marker column: a vertical bar marks the open conversation, a middot
	// marks a DM. Channels need no marker — most entries are channels, and
	// marking the majority is noise.
	marker := ""
	switch {
	case active:
		marker = "│"
	case cv.Kind == protocol.KindDM:
		marker = "·"
	}
	if marker != "" {
		c.Text(0, y, marker, markerStyle(l.T, selected, active))
	}

	// Name.
	nameX := 2
	nameAvail := c.Rect.W - nameX - 1
	name := cv.Name
	if name == "" {
		name = defaultDMName(cv, m.Username, lang)
	}
	c.Text(nameX, y, layout.Truncate(name, nameAvail), nameStyle)

	// Right gutter: unread count beats a timestamp, because it is actionable.
	if cv.Unread > 0 {
		badge := i18n.Num(cv.Unread)
		if selected {
			c.TextRight(y, badge, l.T.SelectedMuted())
		} else {
			c.TextRight(y, badge, l.T.Accented())
		}
		return
	}
	if cv.LastMessageAt > 0 {
		ts := i18n.RelTime(cv.LastMessageAt, m.Now, lang)
		c.TextRight(y, ts, metaStyle)
	}
}

func markerStyle(t theme.Tokens, selected, active bool) tcell.Style {
	if selected {
		return t.Selected()
	}
	if active {
		return t.Accented()
	}
	return t.Faint()
}

// defaultDMName names a DM that the server sent without a name (the peer's
// username is filled in by the DM-open reply, but a freshly listed one may not
// have it yet).
func defaultDMName(cv *client.Conversation, self string, lang i18n.Lang) string {
	return i18n.OpenDM.Get(lang)
}

// renderEmpty draws the first-run state with an actionable hint rather than a
// bare "nothing here" (spec tui-chat-client / 空列表).
func (l List) renderEmpty(s tcell.Screen, r widget.Rect, lang i18n.Lang) {
	c := widget.NewCanvas(s, l.T, r)
	c.Fill(l.T.Base())
	c.Text(layout.GutterX, 1, i18n.ListEmpty.Get(lang), l.T.Muted())
	for i, line := range layout.Wrap(i18n.ListEmptySub.Get(lang), r.W-layout.GutterX) {
		c.Text(layout.GutterX, 3+i, line, l.T.Faint())
	}
}

// SidebarRect for the main layout.
func SidebarRect(content widget.Rect) widget.Rect {
	w := layout.SidebarWidth(content.W)
	return widget.Rect{X: content.X + layout.GutterX, Y: content.Y, W: w, H: content.H}
}

// MainRect is the region right of the sidebar, after the gutter and the vertical
// rule that separates the two.
func MainRect(content widget.Rect) widget.Rect {
	sw := layout.SidebarWidth(content.W)
	x := content.X + layout.GutterX + sw + layout.ColumnGap
	w := content.W - (x - content.X) - layout.GutterX
	if w < 0 {
		w = 0
	}
	return widget.Rect{X: x, Y: content.Y, W: w, H: content.H}
}

// DrawDivider paints the thin vertical rule between sidebar and transcript.
func DrawDivider(s tcell.Screen, t theme.Tokens, content widget.Rect) {
	sw := layout.SidebarWidth(content.W)
	x := content.X + layout.GutterX + sw + 1
	if x >= content.X+content.W {
		return
	}
	c := widget.NewCanvas(s, t, widget.Rect{X: x, Y: content.Y, W: 1, H: content.H})
	c.VRule(0, 0, content.H, t.BorderSoftStyle())
}

// ListWidthForTest exposes the sidebar width to external diagnostics.
func ListWidthForTest(total int) int { return layout.SidebarWidth(total) }
