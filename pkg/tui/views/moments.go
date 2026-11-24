package views

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// The moments page (spec tui-chat-client / 朋友圈页面): a tab bar, then the
// chosen tab as a column of blocks, one of which is selected. Everything here is
// a pure function of MomentsModel; the app owns the state and the network.

// MomentsTab is one of the page's tabs.
type MomentsTab int

const (
	TabTimeline MomentsTab = iota
	TabNotices
	tabCount
)

// MomentsTabs is the tab order.
var MomentsTabs = []MomentsTab{TabTimeline, TabNotices}

// Next and Prev step through the tabs, wrapping round.
func (t MomentsTab) Next() MomentsTab { return (t + 1) % tabCount }

func (t MomentsTab) Prev() MomentsTab { return (t + tabCount - 1) % tabCount }

// FoldAfter is how many comments show before the rest fold away.
const FoldAfter = 3

// CommentToggle marks the "show all / show fewer" row among a moment's
// comment rows.
const CommentToggle = -1

// CommentRows lists what can be selected under a moment, in order: comment
// indexes, and CommentToggle for the fold row. More than FoldAfter comments
// fold to the first two plus the toggle; expanded shows them all with the
// toggle last.
func CommentRows(mo protocol.Moment, expanded bool) []int {
	n := len(mo.Comments)
	var out []int
	if n <= FoldAfter {
		for i := 0; i < n; i++ {
			out = append(out, i)
		}
		return out
	}
	shown := FoldAfter - 1
	if expanded {
		shown = n
	}
	for i := 0; i < shown; i++ {
		out = append(out, i)
	}
	return append(out, CommentToggle)
}

// InputKind is what the input strip is collecting.
type InputKind int

const (
	InputNone InputKind = iota
	InputPost
	InputComment
	InputReply
)

// MomentsInput is the input strip at the bottom of the page.
type MomentsInput struct {
	Kind    InputKind
	Text    []rune
	Private bool
	// ReplyName is who a reply goes to.
	ReplyName string
	Busy      bool
}

// Active reports whether an input is open.
func (in MomentsInput) Active() bool { return in.Kind != InputNone }

// MomentsModel is everything the moments page draws.
type MomentsModel struct {
	Tab MomentsTab
	// Selected is the selected block of each tab; Scroll its first visible row.
	Selected [tabCount]int
	Scroll   [tabCount]int

	SelfID  string
	Moments []protocol.Moment
	// MoreMoments is true while older moments remain; LoadingMore while they
	// are being fetched; Loaded once the first page has arrived.
	MoreMoments bool
	LoadingMore bool
	Loaded      bool

	Requests      []protocol.FriendRequest
	Notifications []protocol.Notification

	Friends []protocol.User
	Blocked []protocol.User
	// RemovedBy are former friends who removed you; Plaza people listed for
	// anyone to add.
	RemovedBy []protocol.User
	Plaza     []protocol.User

	// CommentSel is which comment row of the selected moment is chosen (an
	// index into CommentRows), -1 for the moment itself.
	CommentSel int
	// Expanded are the moments whose comments are unfolded.
	Expanded map[string]bool

	Input MomentsInput
	// Confirm is a question waiting for y; empty for none.
	Confirm string
	// Notice is a short line of feedback, NoticeErr when it is an error.
	Notice    string
	NoticeErr bool

	Unsupported  bool
	Focused      bool
	NameInFlight bool
	Now          int64
}

// MomentsPage draws the moments page.
type MomentsPage struct {
	T theme.Tokens
}

// block is one selectable unit: its lines and how each is drawn.
type block struct {
	lines []line
}

// line is one row: segments drawn left to right.
type line struct {
	segs []seg
}

type seg struct {
	text  string
	style tcell.Style
	// avatar, when set, draws a user's avatar instead of text.
	avatar *protocol.User
}

func (l *line) add(text string, st tcell.Style) *line {
	l.segs = append(l.segs, seg{text: text, style: st})
	return l
}

func (l *line) addAvatar(u protocol.User) *line {
	uu := u
	l.segs = append(l.segs, seg{avatar: &uu})
	return l
}

func textLine(text string, st tcell.Style) line { return line{segs: []seg{{text: text, style: st}}} }

// pageBodyTop is the first row under the title and tab bar.
const pageBodyTop = 5

// MomentsBodyRows is how many rows the tab's content has in a page of height h,
// with or without the input strip.
func MomentsBodyRows(h int, m MomentsModel) int {
	return maxInt(1, h-pageBodyTop-footerRows(m))
}

func footerRows(m MomentsModel) int {
	switch {
	case m.Input.Kind == InputPost:
		return 6
	case m.Input.Active():
		return 3
	}
	return 2
}

// Render draws the page.
func (v MomentsPage) Render(s tcell.Screen, r widget.Rect, m MomentsModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	pageTitle(c, v.T, r, i18n.MomentsTitle.Get(lang), m.Focused, m.NameInFlight)
	if m.Unsupported {
		msg := i18n.MomentsUnsupported.Get(lang)
		c.Text((r.W-layout.StringWidth(msg))/2, r.H/2, msg, v.T.Faint())
		return
	}
	v.tabBar(c, r.W, m, lang)

	width := minInt(r.W-4, 84)
	x0 := (r.W - width) / 2
	blocks := v.Blocks(m, width, lang)
	rows := MomentsBodyRows(r.H, m)
	sel := m.Selected[m.Tab]
	y := pageBodyTop
	skip := m.Scroll[m.Tab]
	for i, b := range blocks {
		for _, ln := range b.lines {
			if skip > 0 {
				skip--
				continue
			}
			if y >= pageBodyTop+rows {
				break
			}
			v.drawLine(c, x0, y, width, ln, i == sel && m.Focused)
			y++
		}
	}
	v.footer(c, r, x0, width, m, lang)
}

// tabBar draws the tabs as one segmented bar.
func (v MomentsPage) tabBar(c *widget.Canvas, w int, m MomentsModel, lang i18n.Lang) {
	labels := []string{i18n.MomentsTabTimeline.Get(lang), i18n.MomentsTabNotices.Get(lang)}
	if n := unreadNotices(m.Notifications); n > 0 {
		labels[1] += fmt.Sprintf(" %d", n)
	}
	segmentedBar(c, v.T, w, 1, labels, int(m.Tab), m.Focused)
}

func unreadNotices(ns []protocol.Notification) int {
	n := 0
	for _, x := range ns {
		if !x.Read {
			n++
		}
	}
	return n
}

// drawLine draws one line at x, y within width, with the selection fill when
// selected.
func (v MomentsPage) drawLine(c *widget.Canvas, x, y, width int, ln line, selected bool) {
	bg := v.T.Base()
	if selected {
		bg = v.T.Selected()
		c.Text(x-1, y, strings.Repeat(" ", width+2), bg)
		c.Text(x-1, y, "▌", v.T.Accented().Background(selectedBg(v.T)))
	}
	cx := x
	for _, sg := range ln.segs {
		if cx >= x+width {
			break
		}
		if sg.avatar != nil {
			drawUserAvatar(c, v.T, cx, y, *sg.avatar, bg)
			cx += AvatarCells
			continue
		}
		text := layout.Truncate(sg.text, x+width-cx)
		st := sg.style
		if selected {
			fg, _, attrs := st.Decompose()
			st = tcell.StyleDefault.Foreground(fg).Background(selectedBg(v.T)).Attributes(attrs)
		}
		c.Text(cx, y, text, st)
		cx += layout.StringWidth(text)
	}
}

func selectedBg(t theme.Tokens) tcell.Color {
	_, bg, _ := t.Selected().Decompose()
	return bg
}

// Blocks builds the selectable blocks of the current tab at width.
func (v MomentsPage) Blocks(m MomentsModel, width int, lang i18n.Lang) []block {
	switch m.Tab {
	case TabNotices:
		return v.noticeBlocks(m, lang)
	}
	return v.timelineBlocks(m, width, lang)
}

// BlockCount is how many selectable blocks the current tab has.
func (v MomentsPage) BlockCount(m MomentsModel, width int, lang i18n.Lang) int {
	return len(v.Blocks(m, width, lang))
}

// BlockSpan is the first row and height of block i, for scrolling.
func (v MomentsPage) BlockSpan(m MomentsModel, width int, lang i18n.Lang, i int) (int, int) {
	y := 0
	for k, b := range v.Blocks(m, width, lang) {
		if k == i {
			return y, len(b.lines)
		}
		y += len(b.lines)
	}
	return y, 0
}

// MomentsContentWidth is the width of the page's column in a page of width w.
func MomentsContentWidth(w int) int { return minInt(w-4, 84) }

// ---------- timeline ----------

// TimelineMomentIndex maps a selected block of the timeline to a moment index:
// block 0 is the compose row.
func TimelineMomentIndex(sel int) int { return sel - 1 }

func (v MomentsPage) timelineBlocks(m MomentsModel, width int, lang i18n.Lang) []block {
	out := []block{{lines: []line{textLine(i18n.MomentsCompose.Get(lang), v.T.Accented()), {}}}}
	if !m.Loaded {
		out = append(out, block{lines: []line{textLine(i18n.MomentsLoading.Get(lang), v.T.Faint())}})
		return out
	}
	if len(m.Moments) == 0 {
		out = append(out, block{lines: []line{textLine(i18n.MomentsEmpty.Get(lang), v.T.Faint())}})
		return out
	}
	sel := TimelineMomentIndex(m.Selected[TabTimeline])
	for i, mo := range m.Moments {
		cs := -1
		if i == sel {
			cs = m.CommentSel
		}
		out = append(out, block{lines: v.momentLines(mo, width, m.Now, cs, m.Expanded[mo.ID], lang)})
	}
	switch {
	case m.LoadingMore:
		out = append(out, block{lines: []line{textLine(i18n.MomentsLoadingMore.Get(lang), v.T.Faint())}})
	case !m.MoreMoments:
		out = append(out, block{lines: []line{textLine(i18n.MomentsEnd.Get(lang), v.T.Faint())}})
	}
	return out
}

// momentLines lays out one moment: header, body, likes, comments, a gap.
// commentSel marks a chosen comment row (an index into CommentRows), -1 for
// none.
func (v MomentsPage) momentLines(mo protocol.Moment, width int, now int64, commentSel int, expanded bool, lang i18n.Lang) []line {
	var out []line
	head := line{}
	head.addAvatar(mo.Author).add(" ", v.T.Base()).add(mo.Author.DisplayName(), v.T.Heading())
	meta := "  " + i18n.RelTime(mo.CreatedAt, now, lang)
	if mo.Visibility == protocol.VisibilityPrivate {
		meta += " · " + i18n.MomentsPrivate.Get(lang)
	}
	head.add(meta, v.T.Faint())
	out = append(out, head)

	indent := strings.Repeat(" ", AvatarCells+1)
	for _, para := range strings.Split(mo.Body, "\n") {
		for _, w := range layout.Wrap(para, width-len(indent)) {
			out = append(out, textLine(indent+w, v.T.Body()))
		}
	}
	if len(mo.Likes) > 0 {
		var names []string
		for _, u := range mo.Likes {
			names = append(names, u.DisplayName())
		}
		st := v.T.Muted()
		if mo.Liked {
			st = v.T.Accented()
		}
		for i, w := range layout.Wrap(i18n.MomentsLikedBy.Get(lang)+strings.Join(names, "、"), width-len(indent)) {
			if i > 0 {
				w = "  " + w
			}
			out = append(out, textLine(indent+w, st))
		}
	}
	for row, ci := range CommentRows(mo, expanded) {
		mark := "  "
		st := v.T.Muted()
		if row == commentSel {
			mark, st = "› ", v.T.Accented()
		}
		if ci == CommentToggle {
			label := fmt.Sprintf(i18n.MomentsExpand.Get(lang), len(mo.Comments)-(FoldAfter-1))
			if expanded {
				label = i18n.MomentsCollapse.Get(lang)
			}
			ts := v.T.Accented()
			if row != commentSel {
				ts = v.T.Faint()
			}
			out = append(out, *(&line{}).add(indent+mark, st).add(label, ts))
			continue
		}
		cm := mo.Comments[ci]
		who := cm.Author.DisplayName()
		if cm.ReplyTo != nil {
			who += " " + i18n.MomentsReply.Get(lang) + " " + cm.ReplyTo.DisplayName()
		}
		text := who + i18n.MomentsSaid.Get(lang) + cm.Body
		for k, w := range layout.Wrap(text, width-len(indent)-2) {
			ln := line{}
			if k == 0 {
				ln.add(indent+mark, st)
			} else {
				ln.add(indent+"  ", st)
			}
			ln.add(w, v.T.Body())
			out = append(out, ln)
		}
	}
	return append(out, line{})
}

// ---------- notices ----------

// NoticeTarget says what notices block i is. Friend requests live on the
// friends page now, so request is always -1.
func NoticeTarget(m MomentsModel, i int) (request, notification int) {
	if i < len(m.Notifications) {
		return -1, i
	}
	return -1, -1
}

func (v MomentsPage) noticeBlocks(m MomentsModel, lang i18n.Lang) []block {
	var out []block
	// Name and action in columns, so every row's time lines up.
	nameW, whatW := 0, 0
	for _, n := range m.Notifications {
		nameW = maxInt(nameW, layout.StringWidth(n.Actor.DisplayName()))
		whatW = maxInt(whatW, layout.StringWidth(i18n.NoticeText(n.Kind).Get(lang)))
	}
	nameW = minInt(nameW, 16)
	indent := strings.Repeat(" ", AvatarCells+1)
	for _, n := range m.Notifications {
		st := v.T.Muted()
		if !n.Read {
			st = v.T.Body()
		}
		ln := line{}
		ln.addAvatar(n.Actor).add(" ", v.T.Base()).add(cell(n.Actor.DisplayName(), nameW), v.T.Heading()).
			add("  "+cell(i18n.NoticeText(n.Kind).Get(lang), whatW), st).add("  "+i18n.RelTime(n.CreatedAt, m.Now, lang), v.T.Faint())
		lines := []line{ln}
		if n.Body != "" {
			lines = append(lines, textLine(indent+"「"+n.Body+"」", st))
		}
		if n.Excerpt != "" {
			lines = append(lines, textLine(indent+n.Excerpt, v.T.Faint()))
		}
		out = append(out, block{lines: lines})
	}
	if len(out) == 0 {
		out = append(out, block{lines: []line{textLine(i18n.MomentsNoNotices.Get(lang), v.T.Faint())}})
	}
	return out
}

// ---------- footer ----------

// footer draws the hint line, or the input strip, or a confirmation.
func (v MomentsPage) footer(c *widget.Canvas, r widget.Rect, x0, width int, m MomentsModel, lang i18n.Lang) {
	top := r.H - footerRows(m)
	c.Text(x0, top, strings.Repeat("─", width), v.T.BorderSoftStyle())
	y := top + 1
	switch {
	case m.Confirm != "":
		c.Text(x0, y, layout.Truncate(m.Confirm, width), v.T.WarningStyle())
	case m.Input.Active():
		v.inputStrip(c, x0, y, width, m, lang)
	case m.Notice != "":
		st := v.T.SuccessStyle()
		if m.NoticeErr {
			st = v.T.DangerStyle()
		}
		c.Text(x0, y, layout.Truncate(m.Notice, width), st)
	case m.Focused:
		c.Text(x0, y, layout.Truncate(tabHint(m.Tab, lang), width), v.T.Faint())
	}
}

func tabHint(t MomentsTab, lang i18n.Lang) string {
	switch t {
	case TabNotices:
		return i18n.MomentsHintNotices.Get(lang)
	}
	return i18n.MomentsHintTimeline.Get(lang)
}

func (v MomentsPage) inputStrip(c *widget.Canvas, x0, y, width int, m MomentsModel, lang i18n.Lang) {
	in := m.Input
	field := Dialog{T: v.T}
	switch in.Kind {
	case InputPost:
		vis := i18n.MomentsFriends.Get(lang)
		if in.Private {
			vis = i18n.MomentsPrivate.Get(lang)
		}
		c.Text(x0, y, i18n.MomentsComposeTitle.Get(lang)+" · "+vis, v.T.Heading())
		lines := layout.Wrap(string(in.Text), width-2)
		if len(lines) == 0 {
			lines = []string{""}
		}
		// The last three lines of the draft, so the caret's line shows.
		if len(lines) > 3 {
			lines = lines[len(lines)-3:]
		}
		for i := 0; i < 3; i++ {
			text := ""
			if i < len(lines) {
				text = lines[i]
			}
			field.fieldRow(c, x0, y+1+i, width, text, !in.Busy && i == minInt(len(lines), 3)-1)
		}
		c.Text(x0, y+4, layout.Truncate(i18n.MomentsComposeHint.Get(lang), width), v.T.Faint())
	default:
		title := i18n.MomentsCommentTitle.Get(lang)
		if in.Kind == InputReply {
			title = fmt.Sprintf(i18n.MomentsReplyTitle.Get(lang), in.ReplyName)
		}
		lw := layout.StringWidth(title) + 2
		c.Text(x0, y, title, v.T.Muted())
		field.fieldRow(c, x0+lw, y, width-lw, string(in.Text), !in.Busy)
		c.Text(x0, y+1, layout.Truncate(i18n.MomentsInputHint.Get(lang), width), v.T.Faint())
	}
}
