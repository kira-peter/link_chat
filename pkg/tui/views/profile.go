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

// The profile page (spec tui-chat-client / 主页页面): the person, centred, and
// under it their moments. One's own profile can be edited in place.

// ProfileEditField is which field of the edit form has the keyboard.
type ProfileEditField int

const (
	EditNickname ProfileEditField = iota
	EditBio
	EditAvatar
	EditColor
	EditGender
	EditAge
	editFieldCount
)

// ProfileEdit is the edit form's state.
type ProfileEdit struct {
	Active   bool
	Field    ProfileEditField
	Nickname []rune
	Bio      []rune
	Avatar   []rune
	Color    string
	// Gender is "", "male" or "female"; Age is typed digits, empty to keep
	// it private.
	Gender string
	Age    []rune
	Busy   bool
}

// ProfileButton is one action button on someone else's profile.
type ProfileButton int

const (
	BtnMessage ProfileButton = iota
	BtnAdd
	BtnBlock
)

// ProfileButtons are the buttons someone else's profile shows: message for a
// friend, add for anyone else, and block for both.
func ProfileButtons(p *protocol.Profile) []ProfileButton {
	if p == nil || p.IsSelf {
		return nil
	}
	if p.IsFriend {
		return []ProfileButton{BtnMessage, BtnBlock}
	}
	return []ProfileButton{BtnAdd, BtnBlock}
}

// Next and Prev move between the form's fields, wrapping.
func (f ProfileEditField) Next() ProfileEditField { return (f + 1) % editFieldCount }
func (f ProfileEditField) Prev() ProfileEditField { return (f + editFieldCount - 1) % editFieldCount }

// ProfileModel is what the profile page draws.
type ProfileModel struct {
	Profile *protocol.Profile
	Loading bool
	Err     string
	Edit    ProfileEdit
	// Button is the chosen action button.
	Button int
	// Scroll is how many rows of the moment list are scrolled off.
	Scroll       int
	Notice       string
	NoticeErr    bool
	Focused      bool
	NameInFlight bool
	Now          int64
}

// ProfilePage draws the profile page.
type ProfilePage struct {
	T theme.Tokens
}

// profileHeadRows is how tall the person block is: avatar, name, @username,
// facts, and the action buttons.
const profileHeadRows = 13

// ProfileMomentRows is how many rows of moments fit in a page of height h.
func ProfileMomentRows(h int) int { return maxInt(1, h-profileHeadRows-3) }

// Render draws the page.
func (v ProfilePage) Render(s tcell.Screen, r widget.Rect, m ProfileModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	title := i18n.ProfileTitle.Get(lang)
	if m.Profile != nil {
		title = m.Profile.User.DisplayName()
	}
	pageTitle(c, v.T, r, title, m.Focused, m.NameInFlight)
	centre := func(y int, text string, st tcell.Style) {
		text = layout.Truncate(text, r.W-2)
		c.Text((r.W-layout.StringWidth(text))/2, y, text, st)
	}
	switch {
	case m.Err != "":
		centre(r.H/2, m.Err, v.T.DangerStyle())
		return
	case m.Profile == nil:
		centre(r.H/2, i18n.MomentsLoading.Get(lang), v.T.Faint())
		return
	}
	p := m.Profile
	u := p.User
	if m.Edit.Active {
		// The preview follows what is typed.
		u.Nickname, u.Avatar, u.Color = string(m.Edit.Nickname), string(m.Edit.Avatar), m.Edit.Color
		if u.Avatar == "" {
			u.Avatar, _ = protocol.DefaultAvatar(u.Username)
		}
	}

	// A large avatar: the glyph in the middle of a three-row tile of its colour.
	tile := AvatarStyle(v.T, u.Color, v.T.Base()).Reverse(true)
	tx := r.W/2 - 3
	for dy := 0; dy < 3; dy++ {
		c.Text(tx, 2+dy, "      ", tile)
	}
	c.Text(tx+2, 3, avatarText(u.Avatar), tile)

	centre(6, u.DisplayName(), v.T.Heading())
	sub := "@" + u.Username
	if p.CreatedAt > 0 {
		sub += " · " + fmt.Sprintf(i18n.ProfileJoined.Get(lang), i18n.Date(p.CreatedAt, lang))
	}
	switch {
	case p.IsSelf:
		sub += " · " + i18n.ProfileSelf.Get(lang)
	case p.IsFriend:
		sub += " · " + i18n.ProfileFriend.Get(lang)
	case p.RequestSent:
		sub += " · " + i18n.ProfilePending.Get(lang)
	default:
		sub += " · " + i18n.ProfileNotFriend.Get(lang)
	}
	centre(7, sub, v.T.Faint())
	if m.Edit.Active {
		u.Bio, u.Gender, u.Age = string(m.Edit.Bio), m.Edit.Gender, 0
		fmt.Sscan(string(m.Edit.Age), &u.Age)
	} else {
		u.Bio = p.Bio
	}
	if facts := UserFacts(u, lang); facts != "" {
		centre(8, facts, v.T.Muted())
	}
	if !m.Edit.Active {
		v.buttons(c, r.W, 10, p, m.Button, m.Focused, lang)
	}

	width := minInt(r.W-4, 84)
	x0 := (r.W - width) / 2
	if m.Edit.Active {
		v.editForm(c, x0, profileHeadRows+1, width, m.Edit, lang)
	} else {
		v.moments(c, x0, width, r.H, m, lang)
	}

	// Footer: feedback, or the keys.
	y := r.H - 1
	switch {
	case m.Notice != "":
		st := v.T.SuccessStyle()
		if m.NoticeErr {
			st = v.T.DangerStyle()
		}
		centre(y, m.Notice, st)
	case m.Edit.Active:
		centre(y, i18n.ProfileEditHint.Get(lang), v.T.Faint())
	case m.Focused:
		hint := i18n.ProfileHintOther
		switch {
		case p.IsSelf:
			hint = i18n.ProfileHintSelf
		case p.IsFriend || p.RequestSent:
			hint = i18n.ProfileHintFriend
		}
		centre(y, hint.Get(lang), v.T.Faint())
	}
}

// buttons draws the action buttons as one segmented bar.
func (v ProfilePage) buttons(c *widget.Canvas, w, y int, p *protocol.Profile, sel int, focused bool, lang i18n.Lang) {
	btns := ProfileButtons(p)
	if len(btns) == 0 {
		return
	}
	var labels []string
	for _, b := range btns {
		switch b {
		case BtnMessage:
			labels = append(labels, i18n.ProfileBtnMessage.Get(lang))
		case BtnAdd:
			l := i18n.ProfileBtnAdd.Get(lang)
			if p.RequestSent {
				l = i18n.ProfilePending.Get(lang)
			}
			labels = append(labels, l)
		case BtnBlock:
			labels = append(labels, i18n.ProfileBtnBlock.Get(lang))
		}
	}
	segmentedBar(c, v.T, w, y, labels, sel, focused)
}

func (v ProfilePage) moments(c *widget.Canvas, x0, width, h int, m ProfileModel, lang i18n.Lang) {
	top := profileHeadRows + 1
	c.Text(x0, top-1, strings.Repeat("─", width), v.T.BorderSoftStyle())
	if len(m.Profile.Moments) == 0 {
		msg := i18n.ProfileNoMoments.Get(lang)
		c.Text(x0+(width-layout.StringWidth(msg))/2, top+1, msg, v.T.Faint())
		return
	}
	mp := MomentsPage{T: v.T}
	var lines []line
	for _, mo := range m.Profile.Moments {
		lines = append(lines, mp.momentLines(mo, width, m.Now, -1, true, lang)...)
	}
	rows := ProfileMomentRows(h)
	start := clampInt(m.Scroll, 0, maxInt(0, len(lines)-rows))
	for i := 0; i < rows && start+i < len(lines); i++ {
		mp.drawLine(c, x0, top+i, width, lines[start+i], false)
	}
}

// ProfileLineCount is how many rows the profile's moments take at width, for
// clamping the scroll.
func ProfileLineCount(t theme.Tokens, p *protocol.Profile, width int, now int64, lang i18n.Lang) int {
	if p == nil {
		return 0
	}
	mp := MomentsPage{T: t}
	n := 0
	for _, mo := range p.Moments {
		n += len(mp.momentLines(mo, width, now, -1, true, lang))
	}
	return n
}

func (v ProfilePage) editForm(c *widget.Canvas, x0, y, width int, e ProfileEdit, lang i18n.Lang) {
	c.Text(x0, y, i18n.ProfileEditTitle.Get(lang), v.T.Heading())
	labels := []string{i18n.ProfileNickname.Get(lang), i18n.ProfileBio.Get(lang), i18n.ProfileAvatar.Get(lang), i18n.ProfileColor.Get(lang),
		i18n.ProfileGender.Get(lang), i18n.ProfileAge.Get(lang)}
	lw := 0
	for _, l := range labels {
		lw = maxInt(lw, layout.StringWidth(l))
	}
	lw += 2
	field := Dialog{T: v.T}
	values := []string{string(e.Nickname), string(e.Bio), string(e.Avatar)}
	for i, l := range labels {
		row := y + 2 + i
		st := v.T.Muted()
		if ProfileEditField(i) == e.Field {
			st = v.T.Accented()
		}
		c.Text(x0, row, l, st)
		if i < len(values) {
			fw := width - lw
			if ProfileEditField(i) == EditAvatar {
				fw = minInt(fw, 8)
			}
			field.fieldRow(c, x0+lw, row, fw, values[i], !e.Busy && ProfileEditField(i) == e.Field)
			continue
		}
		x := x0 + lw
		switch ProfileEditField(i) {
		case EditGender:
			for _, g := range protocol.Genders {
				name := GenderName(g, lang)
				text, st := " "+name+" ", v.T.Muted()
				if g == e.Gender {
					text, st = "["+name+"]", v.T.Accented()
				}
				c.Text(x, row, text, st)
				x += layout.StringWidth(text) + 1
			}
			continue
		case EditAge:
			field.fieldRow(c, x, row, minInt(width-lw, 8), string(e.Age), !e.Busy && e.Field == EditAge)
			c.Text(x+9, row, i18n.ProfileAgeHint.Get(lang), v.T.Faint())
			continue
		}
		// The colours as swatches, the chosen one bracketed.
		for _, col := range protocol.AvatarColors {
			text := " " + MessageColorName(col, lang) + " "
			if col == e.Color {
				text = "[" + MessageColorName(col, lang) + "]"
			}
			c.Text(x, row, text, AvatarStyle(v.T, col, v.T.Base()).Bold(col == e.Color))
			x += layout.StringWidth(text) + 1
		}
	}
}

// GenderName is how a gender value reads; "" is "not shown".
func GenderName(g string, lang i18n.Lang) string {
	switch g {
	case "male":
		return i18n.GenderMale.Get(lang)
	case "female":
		return i18n.GenderFemale.Get(lang)
	}
	return i18n.GenderHidden.Get(lang)
}
