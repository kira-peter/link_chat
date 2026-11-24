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

// The friends page, a foot entry of its own, in three tabs: contacts, friend
// requests, and the plaza (people who list themselves for anyone to add, with
// a field to add anyone by username on top).

// FriendsTab is one tab of the friends page.
type FriendsTab int

const (
	TabContacts FriendsTab = iota
	TabRequests
	TabPlaza
	friendsTabCount
)

// Next and Prev step through the tabs, wrapping round.
func (t FriendsTab) Next() FriendsTab { return (t + 1) % friendsTabCount }
func (t FriendsTab) Prev() FriendsTab { return (t + friendsTabCount - 1) % friendsTabCount }

// FriendsModel is what the friends page draws.
type FriendsModel struct {
	Tab      FriendsTab
	Selected [friendsTabCount]int
	Scroll   [friendsTabCount]int

	Friends   []protocol.User
	RemovedBy []protocol.User
	Blocked   []protocol.User
	Requests  []protocol.FriendRequest
	Plaza     []protocol.User

	// Name is the plaza tab's username field; NoteFor and Note the note being
	// written before a request goes out.
	Name    []rune
	Note    []rune
	NoteFor string
	Busy    bool

	Confirm      string
	Notice       string
	NoticeErr    bool
	Focused      bool
	NameInFlight bool
}

// FriendsPage draws the friends page.
type FriendsPage struct {
	T theme.Tokens
}

// ContactRow says what contacts row i is: a friend, someone who removed you,
// or a blocked user; -1 for the others.
type ContactRow struct {
	Friend  int
	Removed int
	Blocked int
}

// ContactRowAt resolves contacts row i.
func ContactRowAt(m FriendsModel, i int) ContactRow {
	r := ContactRow{Friend: -1, Removed: -1, Blocked: -1}
	if i < len(m.Friends) {
		r.Friend = i
		return r
	}
	i -= len(m.Friends)
	if i < len(m.RemovedBy) {
		r.Removed = i
		return r
	}
	i -= len(m.RemovedBy)
	if i < len(m.Blocked) {
		r.Blocked = i
	}
	return r
}

// FriendsRowCount is how many selectable rows tab t has.
func FriendsRowCount(m FriendsModel, t FriendsTab) int {
	switch t {
	case TabRequests:
		return len(m.Requests)
	case TabPlaza:
		return 1 + len(m.Plaza)
	}
	return len(m.Friends) + len(m.RemovedBy) + len(m.Blocked)
}

// FriendsBodyTop is the first row under the tab bar.
const FriendsBodyTop = 5

// FriendsBodyRows is how many rows fit under the tab bar in a page of height h.
func FriendsBodyRows(h int) int { return maxInt(1, h-FriendsBodyTop-2) }

// segmentedBar draws labels as one boxed bar on rows y..y+2 of a page w wide,
// the chosen one filled (as the moments page does).
func segmentedBar(c *widget.Canvas, t theme.Tokens, w, y int, labels []string, sel int, focused bool) {
	cellW := 0
	for _, l := range labels {
		cellW = maxInt(cellW, layout.StringWidth(l)+4)
	}
	total := cellW*len(labels) + 1
	x := maxInt(0, (w-total)/2)
	frame := t.BorderStyle()
	top, bottom := "┌", "└"
	for i := range labels {
		top += strings.Repeat("─", cellW-1)
		bottom += strings.Repeat("─", cellW-1)
		if i < len(labels)-1 {
			top, bottom = top+"┬", bottom+"┴"
		}
	}
	c.Text(x, y, top+"┐", frame)
	c.Text(x, y+2, bottom+"┘", frame)
	c.Text(x, y+1, "│", frame)
	for i, l := range labels {
		cx := x + 1 + i*cellW
		pad := cellW - 1 - layout.StringWidth(l)
		text := strings.Repeat(" ", pad/2) + l + strings.Repeat(" ", pad-pad/2)
		st := t.Muted()
		if i == sel {
			st = t.Selected().Bold(true)
			if !focused {
				st = t.SelectedMuted()
			}
		}
		c.Text(cx, y+1, text, st)
		c.Text(cx+cellW-1, y+1, "│", frame)
	}
}

// Render draws the page.
func (v FriendsPage) Render(s tcell.Screen, r widget.Rect, m FriendsModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	pageTitle(c, v.T, r, i18n.EntryFriends.Get(lang), m.Focused, m.NameInFlight)
	labels := []string{i18n.FriendsTabContacts.Get(lang), i18n.FriendsTabRequests.Get(lang), i18n.FriendsTabPlaza.Get(lang)}
	if n := len(m.Requests); n > 0 {
		labels[1] += fmt.Sprintf(" %d", n)
	}
	segmentedBar(c, v.T, r.W, 1, labels, int(m.Tab), m.Focused)

	width := minInt(r.W-4, 84)
	x0 := (r.W - width) / 2
	mp := MomentsPage{T: v.T}
	var rows []line
	switch m.Tab {
	case TabRequests:
		rows = v.requestRows(m, lang)
	case TabPlaza:
		rows = v.plazaRows(m, width, lang)
	default:
		rows = v.contactRows(m, lang)
	}
	sel := m.Selected[m.Tab]
	if FriendsRowCount(m, m.Tab) == 0 {
		sel = -1
	}
	body := FriendsBodyRows(r.H)
	start := clampInt(m.Scroll[m.Tab], 0, maxInt(0, len(rows)-body))
	for i := 0; i < body && start+i < len(rows); i++ {
		mp.drawLine(c, x0, FriendsBodyTop+i, width, rows[start+i], start+i == sel && m.Focused)
	}

	top := r.H - 2
	c.Text(x0, top, strings.Repeat("─", width), v.T.BorderSoftStyle())
	switch {
	case m.Confirm != "":
		c.Text(x0, top+1, layout.Truncate(m.Confirm, width), v.T.WarningStyle())
	case m.NoteFor != "":
		label := fmt.Sprintf(i18n.FriendsNoteFor.Get(lang), m.NoteFor)
		lw := layout.StringWidth(label) + 2
		c.Text(x0, top+1, label, v.T.Muted())
		Dialog{T: v.T}.fieldRow(c, x0+lw, top+1, width-lw, string(m.Note), !m.Busy)
	case m.Notice != "":
		st := v.T.SuccessStyle()
		if m.NoticeErr {
			st = v.T.DangerStyle()
		}
		c.Text(x0, top+1, layout.Truncate(m.Notice, width), st)
	case m.Focused:
		hint := i18n.FriendsHintList
		switch m.Tab {
		case TabRequests:
			hint = i18n.FriendsHintRequests
		case TabPlaza:
			hint = i18n.FriendsHintAdd
		}
		c.Text(x0, top+1, layout.Truncate(hint.Get(lang), width), v.T.Faint())
	}
}

// personCols are the column widths of one list of people, so every row's
// name, @username and what follows start at the same column whatever the
// names' lengths (CJK included: widths are display cells, not runes).
type personCols struct {
	name, handle int
}

// colsFor measures the widest name and @username in us. Columns are capped so
// one very long name cannot push the rest off the screen.
func colsFor(us []protocol.User) personCols {
	const maxName, maxHandle = 16, 18
	var c personCols
	for _, u := range us {
		c.name = maxInt(c.name, layout.StringWidth(u.DisplayName()))
		if u.Nickname != "" {
			c.handle = maxInt(c.handle, layout.StringWidth("@"+u.Username))
		}
	}
	c.name, c.handle = minInt(c.name, maxName), minInt(c.handle, maxHandle)
	return c
}

// cell is s fitted to exactly w display cells: truncated, or padded.
func cell(s string, w int) string {
	return layout.PadRight(layout.Truncate(s, w), w)
}

// personLine is the shared row for one user in columns: avatar, name,
// @username (blank when they have no nickname), then two spaces before
// whatever the caller adds.
func (v FriendsPage) personLine(u protocol.User, c personCols) line {
	ln := line{}
	ln.addAvatar(u).add(" ", v.T.Base()).add(cell(u.DisplayName(), c.name), v.T.Body())
	if c.handle > 0 {
		h := ""
		if u.Nickname != "" {
			h = "@" + u.Username
		}
		ln.add("  "+cell(h, c.handle), v.T.Faint())
	}
	return *ln.add("  ", v.T.Base())
}

func (v FriendsPage) contactRows(m FriendsModel, lang i18n.Lang) []line {
	var all []protocol.User
	all = append(append(append(all, m.Friends...), m.RemovedBy...), m.Blocked...)
	cols := colsFor(all)
	var out []line
	for _, u := range m.Friends {
		ln := v.personLine(u, cols)
		if u.Online {
			ln.add(i18n.FriendsOnline.Get(lang), v.T.SuccessStyle())
		} else {
			ln.add(i18n.FriendsOffline.Get(lang), v.T.Faint())
		}
		out = append(out, ln)
	}
	for _, u := range m.RemovedBy {
		ln := v.personLine(u, cols)
		out = append(out, *ln.add(i18n.FriendsRemovedYou.Get(lang), v.T.DangerStyle()))
	}
	for _, u := range m.Blocked {
		ln := v.personLine(u, cols)
		out = append(out, *ln.add(i18n.FriendsBlocked.Get(lang), v.T.Faint()))
	}
	if len(out) == 0 {
		out = append(out, textLine(i18n.FriendsNoContacts.Get(lang), v.T.Faint()))
	}
	return out
}

func (v FriendsPage) requestRows(m FriendsModel, lang i18n.Lang) []line {
	var from []protocol.User
	for _, r := range m.Requests {
		from = append(from, r.From)
	}
	cols := colsFor(from)
	var out []line
	for _, r := range m.Requests {
		ln := v.personLine(r.From, cols)
		if r.Note != "" {
			ln.add("「"+r.Note+"」", v.T.Muted())
		}
		out = append(out, ln)
	}
	if len(out) == 0 {
		out = append(out, textLine(i18n.MomentsNoRequests.Get(lang), v.T.Faint()))
	}
	return out
}

// genderText and ageText are the plaza's gender and age cells; "" when the
// user keeps them private.
func genderText(u protocol.User, lang i18n.Lang) string {
	switch u.Gender {
	case "male":
		return i18n.GenderMale.Get(lang)
	case "female":
		return i18n.GenderFemale.Get(lang)
	}
	return ""
}

func ageText(u protocol.User, lang i18n.Lang) string {
	if u.Age > 0 {
		return fmt.Sprintf(i18n.AgeYears.Get(lang), u.Age)
	}
	return ""
}

// UserFacts is gender, age and bio as one short string, leaving out what the
// user keeps private.
func UserFacts(u protocol.User, lang i18n.Lang) string {
	var parts []string
	for _, p := range []string{genderText(u, lang), ageText(u, lang), u.Bio} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

// plazaRows lays the plaza out in columns: avatar, name, @username, gender,
// age, signature. A column nobody fills takes no room; a private value
// leaves its cell blank so the signature still lines up.
func (v FriendsPage) plazaRows(m FriendsModel, width int, lang i18n.Lang) []line {
	field := string(m.Name)
	if m.Focused && m.Selected[TabPlaza] == 0 && m.NoteFor == "" {
		field += "█"
	}
	if field == "" {
		field = " "
	}
	out := []line{*(&line{}).add(i18n.MomentsAddFriend.Get(lang)+"  ", v.T.Accented()).add(field, v.T.InputFocusStyle())}
	if len(m.Plaza) == 0 {
		return append(out, textLine(i18n.FriendsPlazaEmpty.Get(lang), v.T.Faint()))
	}
	cols := colsFor(m.Plaza)
	gw, aw := 0, 0
	for _, u := range m.Plaza {
		gw = maxInt(gw, layout.StringWidth(genderText(u, lang)))
		aw = maxInt(aw, layout.StringWidth(ageText(u, lang)))
	}
	for _, u := range m.Plaza {
		ln := v.personLine(u, cols)
		if gw > 0 {
			ln.add(cell(genderText(u, lang), gw)+"  ", v.T.Muted())
		}
		if aw > 0 {
			ln.add(cell(ageText(u, lang), aw)+"  ", v.T.Muted())
		}
		ln.add(u.Bio, v.T.Faint())
		out = append(out, ln)
	}
	return out
}

// ---------- the "new direct message" page ----------

// DMPickModel is the new-DM page: friends without an open DM.
type DMPickModel struct {
	Friends      []protocol.User
	Selected     int
	Err          string
	Busy         bool
	Focused      bool
	NameInFlight bool
}

// DMPickPage draws the new-DM page.
type DMPickPage struct {
	T theme.Tokens
}

// Render draws the list, or a note that every friend already has a DM.
func (v DMPickPage) Render(s tcell.Screen, r widget.Rect, m DMPickModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	pageTitle(c, v.T, r, i18n.OpenDM.Get(lang), m.Focused, m.NameInFlight)
	width := minInt(r.W-4, 60)
	x0 := (r.W - width) / 2
	mp := MomentsPage{T: v.T}
	c.Text(x0, 2, layout.Truncate(i18n.DMPickBlurb.Get(lang), width), v.T.Muted())
	if len(m.Friends) == 0 {
		c.Text(x0, 4, layout.Truncate(i18n.DMPickEmpty.Get(lang), width), v.T.Faint())
		return
	}
	body := maxInt(1, r.H-7)
	start := clampInt(m.Selected-body+1, 0, maxInt(0, len(m.Friends)-body))
	cols := colsFor(m.Friends)
	for i := 0; i < body && start+i < len(m.Friends); i++ {
		u := m.Friends[start+i]
		ln := FriendsPage{T: v.T}.personLine(u, cols)
		if u.Online {
			ln.add(i18n.FriendsOnline.Get(lang), v.T.SuccessStyle())
		} else {
			ln.add(i18n.FriendsOffline.Get(lang), v.T.Faint())
		}
		mp.drawLine(c, x0, 4+i, width, ln, start+i == m.Selected && m.Focused)
	}
	foot := i18n.DMPickHint.Get(lang)
	st := v.T.Faint()
	switch {
	case m.Busy:
		foot = i18n.NewPageBusy.Get(lang)
	case m.Err != "":
		foot, st = m.Err, v.T.DangerStyle()
	case !m.Focused:
		foot, st = i18n.NewPageEnter.Get(lang), v.T.Accented()
	}
	c.Text(x0, r.H-2, layout.Truncate(foot, width), st)
}
