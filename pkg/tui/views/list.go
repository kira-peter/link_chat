package views

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// List renders the conversation sidebar.
//
// The sidebar has three blocks, top to bottom: direct messages, channels, and a
// fixed foot with settings, help and about. The first two scroll together as one
// run; the foot never moves, so the way back to settings is always in the same
// place however many conversations there are.
//
// Selection is a full-row background fill — not a pointer glyph, not bold — which
// is the single biggest thing that separates a considered terminal UI from a
// default-looking one (spec tui-design-system / 选中行整行高亮).
type List struct {
	T theme.Tokens
}

// ListModel is what the list needs from the app.
type ListModel struct {
	// Rows is the sidebar in display order (client.AppState.SidebarRows).
	Rows []client.Row
	// Selected is the cursor, an index into Rows. It points at a selectable row.
	Selected int
	ActiveID string
	Filter   []rune
	Focus    client.Focus
	Now      int64
	Username string

	// Shimmer sweeps a band of light across the selected row's text. Elapsed is how
	// long the sweep has been running, passed in so a render stays a pure function
	// of its inputs.
	Shimmer bool
	Elapsed time.Duration
	Depth   anim.Depth

	// FlyingID is the conversation whose name is currently travelling to or from the
	// transcript's title. Its row is drawn blank while it flies: the flight layer
	// draws the name, and drawing it here too would show it twice.
	FlyingID string
}

// footRows is how tall the fixed foot is: one row per entry.
const footRows = 3

// Render draws the sidebar into r.
func (l List) Render(s tcell.Screen, r widget.Rect, m ListModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	// Header: the sidebar's title with the conversation count, in the reference
	// screen's voice — small caps feel, accent colour, no shouting.
	headerH := 1
	if m.Filter != nil {
		headerH = 2
	}
	head, rest := r.Take(headerH)

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
	if n := countConversations(m.Rows); n > 0 {
		hc.Text(layout.StringWidth(label)+1, 0, i18n.Num(n), l.T.Faint())
	}
	if m.Filter != nil {
		fc := widget.NewCanvas(s, l.T, head)
		line := "/" + string(m.Filter) + "█"
		fc.Text(0, 1, layout.Truncate(line, head.W), l.T.Accented())
	}

	// Split the rest into the scrolling run and the fixed foot. On a very short
	// window the foot gives way before the conversations do.
	foot := footRows + 1 // the entries plus the rule above them
	if rest.H < foot+3 {
		foot = 0
	}
	body, footRect := rest.TakeBottom(foot)

	bodyRows, footEntries := splitFoot(m.Rows)

	bc := widget.NewCanvas(s, l.T, body)
	bc.Fill(l.T.Base())
	l.renderRun(bc, body, bodyRows, m, lang)

	if foot > 0 {
		fc := widget.NewCanvas(s, l.T, footRect)
		fc.Fill(l.T.Base())
		fc.Rule(0, l.T.BorderSoftStyle())
		for i, row := range footEntries {
			if i+1 >= footRect.H {
				break
			}
			selected := rowIndex(m.Rows, row) == m.Selected && m.Focus == client.FocusList
			l.renderEntry(fc, i+1, row, selected, m, lang)
		}
	}
}

// splitFoot separates the fixed foot entries from the scrolling rows above them.
func splitFoot(rows []client.Row) (run, foot []client.Row) {
	for i, r := range rows {
		if r.Kind == client.RowEntry {
			return rows[:i], rows[i:]
		}
	}
	return rows, nil
}

// rowIndex finds a row in the full sequence by identity of its content, since the
// foot is handed to the renderer as a sub-slice.
func rowIndex(rows []client.Row, want client.Row) int {
	for i, r := range rows {
		if r.Kind == want.Kind && r.Section == want.Section &&
			r.Entry == want.Entry && r.Conversation == want.Conversation {
			return i
		}
	}
	return -1
}

func countConversations(rows []client.Row) int {
	n := 0
	for _, r := range rows {
		if r.Kind == client.RowConversation {
			n++
		}
	}
	return n
}

// runWindowStart is the index of the first row of the scrolling run that is on
// screen: the window follows the cursor so the selected row is always visible.
//
// Drawing and the flying name both need to know where a row sits, so the rule is
// here once. If each worked it out separately the name would leave from a row that
// is not the one on screen.
func runWindowStart(selected, runLen, visible int) int {
	start := 0
	if selected >= 0 && selected < runLen && selected >= visible {
		start = selected - visible + 1
	}
	if start > runLen-visible {
		start = runLen - visible
	}
	if start < 0 {
		start = 0
	}
	return start
}

// RowLine returns the line, within the sidebar's body, on which the conversation's
// row is drawn, and whether it is on screen at all. contentH is the height of the
// content area the sidebar fills and filtering whether the filter line is showing.
//
// It mirrors what Render does, so the flying name starts on the row the user was
// just looking at. A row scrolled out of view reports false.
func RowLine(rows []client.Row, selected int, convID string, contentH int, filtering bool, lang i18n.Lang) (line int, ok bool) {
	headerH := 1
	if filtering {
		headerH = 2
	}
	rest := contentH - headerH
	foot := footRows + 1
	if rest < foot+3 {
		foot = 0
	}
	visible := rest - foot
	run, _ := splitFoot(rows)
	idx := client.IndexOfConversation(run, convID)
	if idx < 0 || visible <= 0 {
		return 0, false
	}
	start := runWindowStart(selected, len(run), visible)
	if idx < start || idx >= start+visible {
		return 0, false
	}
	return headerH + (idx - start), true
}

// renderRun draws the headers and conversations, scrolled so the selected row is
// on screen.
func (l List) renderRun(c *widget.Canvas, body widget.Rect, run []client.Row, m ListModel, lang i18n.Lang) {
	visible := body.H
	if visible <= 0 || len(run) == 0 {
		return
	}

	// Keep the cursor inside the window. The cursor indexes the full row sequence;
	// the run is a prefix of it, so while the cursor is in the foot the run simply
	// shows from the top.
	start := runWindowStart(m.Selected, len(run), visible)

	for i := start; i < len(run) && i-start < visible; i++ {
		y := i - start
		row := run[i]
		switch row.Kind {
		case client.RowHeader:
			l.renderHeader(c, y, row, lang)
		case client.RowEmpty:
			l.renderEmptyRow(c, y, row, lang)
		case client.RowConversation:
			selected := i == m.Selected && m.Focus == client.FocusList
			active := row.Conversation.ID == m.ActiveID
			l.renderRow(c, y, row.Conversation, selected, active, m, lang)
		}
	}
}

// renderHeader draws a section title. It is quieter than a conversation: it
// labels a group, it is not something to pick.
func (l List) renderHeader(c *widget.Canvas, y int, row client.Row, lang i18n.Lang) {
	title := i18n.SectionChannelTitle.Get(lang)
	if row.Section == client.SectionDM {
		title = i18n.SectionDMTitle.Get(lang)
	}
	c.Text(0, y, layout.Truncate(title, c.Rect.W), l.T.Faint())
}

// renderEmptyRow draws the hint under a section with nothing in it.
func (l List) renderEmptyRow(c *widget.Canvas, y int, row client.Row, lang i18n.Lang) {
	hint := i18n.SectionChannelEmpty.Get(lang)
	if row.Section == client.SectionDM {
		hint = i18n.SectionDMEmpty.Get(lang)
	}
	c.Text(2, y, layout.Truncate(hint, c.Rect.W-3), l.T.Faint())
}

// renderEntry draws one foot entry (settings, help, about).
func (l List) renderEntry(c *widget.Canvas, y int, row client.Row, selected bool, m ListModel, lang i18n.Lang) {
	name := entryLabel(row.Entry, lang)
	style := l.T.Muted()
	if selected {
		style = l.T.Selected()
		c.TextPadded(0, y, "", l.T.Selected())
	}
	l.putName(c, 2, y, layout.Truncate(name, c.Rect.W-3), style, selected, m)
}

// putName writes a row's text. On the selected row with the sweep running, each
// glyph takes a foreground blended toward the highlight by how close the band is;
// the background stays the selection fill, so the band changes brightness only and
// never the extent of the highlighted row (spec tui-design-system / 行样式与选中态).
//
// Anywhere else it is plain text in style.
func (l List) putName(c *widget.Canvas, x, y int, text string, style tcell.Style, selected bool, m ListModel) {
	if !selected || !m.Shimmer {
		c.Text(x, y, text, style)
		return
	}

	band := anim.ListShimmer
	n := layout.StringWidth(text)
	rest, bg, attrs := style.Decompose()
	hi := l.T.Text

	// Mono has no brightness steps to blend, so the band becomes a single moving
	// glyph instead.
	marker := -1
	if m.Depth == anim.DepthMono {
		marker = band.ShimmerMarker(n, m.Elapsed)
	}

	cell := 0
	for _, r := range text {
		w := layout.RuneWidth(r)
		if w == 0 {
			continue
		}
		st := tcell.StyleDefault.Foreground(rest).Background(bg).Attributes(attrs)
		switch {
		case m.Depth == anim.DepthMono:
			if cell == marker || (w == 2 && cell+1 == marker) {
				st = st.Bold(true).Underline(true)
			}
		default:
			b := band.Brightness(cell, n, m.Elapsed)
			st = tcell.StyleDefault.
				Foreground(anim.ShimmerColor(rest, hi, b, m.Depth)).
				Background(bg).Attributes(attrs)
		}
		c.Text(x+cell, y, string(r), st)
		cell += w
	}
}

// entryLabel is the display name of a foot entry.
func entryLabel(e client.EntryID, lang i18n.Lang) string {
	switch e {
	case client.EntrySettings:
		return i18n.EntrySettings.Get(lang)
	case client.EntryHelp:
		return i18n.EntryHelp.Get(lang)
	default:
		return i18n.EntryAbout.Get(lang)
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
	// While the name is in the air its slot is left empty: the flight layer draws
	// it, and the row's marker, unread count and time stay put so only the name
	// moves.
	if m.FlyingID == "" || cv.ID != m.FlyingID {
		l.putName(c, nameX, y, layout.Truncate(name, nameAvail), nameStyle, selected, m)
	}

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

// SidebarRect for the main layout, with the sidebar fully open.
func SidebarRect(content widget.Rect) widget.Rect { return SidebarRectAt(content, 0) }

// MainRect is the region right of the sidebar, after the gutter and the vertical
// rule that separates the two, with the sidebar fully open.
func MainRect(content widget.Rect) widget.Rect { return MainRectAt(content, 0) }

// DrawDivider paints the thin vertical rule between sidebar and transcript, with
// the sidebar fully open.
func DrawDivider(s tcell.Screen, t theme.Tokens, content widget.Rect) {
	DrawDividerAt(s, t, content, 0)
}

// DrawDividerAt paints the rule at the given fold. It moves with the sidebar's
// edge and is not drawn once the sidebar has folded away.
func DrawDividerAt(s tcell.Screen, t theme.Tokens, content widget.Rect, fold float64) {
	x, ok := FoldDividerX(content, fold)
	if !ok {
		return
	}
	c := widget.NewCanvas(s, t, widget.Rect{X: x, Y: content.Y, W: 1, H: content.H})
	c.VRule(0, 0, content.H, t.BorderSoftStyle())
}

// ListWidthForTest exposes the sidebar width to external diagnostics.
func ListWidthForTest(total int) int { return layout.SidebarWidth(total) }
