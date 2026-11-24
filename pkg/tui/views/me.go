package views

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// The "me" page: the user's own card, and a menu to everything about the
// account — editing the profile, their moments, settings, help, about, and
// switching account or signing out.

// MeItem is one menu row of the "me" page.
type MeItem int

const (
	MeEditProfile MeItem = iota
	MeMyMoments
	MeSettings
	MeHelp
	MeAbout
	MeSwitchAccount
	MeSignOut
)

// MeItems are the menu rows, top to bottom.
var MeItems = []MeItem{MeEditProfile, MeMyMoments, MeSettings, MeHelp, MeAbout, MeSwitchAccount, MeSignOut}

// meGapBefore is the item a blank row precedes: the account actions sit apart.
const meGapBefore = MeSwitchAccount

// MeModel is what the "me" page draws.
type MeModel struct {
	User         protocol.User
	Selected     int
	Focused      bool
	NameInFlight bool
}

// MePage draws the "me" page.
type MePage struct {
	T theme.Tokens
}

func meLabel(it MeItem, lang i18n.Lang) string {
	switch it {
	case MeEditProfile:
		return i18n.MeEditProfile.Get(lang)
	case MeMyMoments:
		return i18n.MeMyMoments.Get(lang)
	case MeSettings:
		return i18n.SettingsTitle.Get(lang)
	case MeHelp:
		return i18n.HelpPageTitle.Get(lang)
	case MeAbout:
		return i18n.AboutTitle.Get(lang)
	case MeSwitchAccount:
		return i18n.SetSwitchAccount.Get(lang)
	}
	return i18n.SetLogout.Get(lang)
}

// Render draws the card and the menu.
func (v MePage) Render(s tcell.Screen, r widget.Rect, m MeModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())
	pageTitle(c, v.T, r, i18n.EntryMe.Get(lang), m.Focused, m.NameInFlight)
	centre := func(y int, text string, st tcell.Style) {
		text = layout.Truncate(text, r.W-2)
		c.Text((r.W-layout.StringWidth(text))/2, y, text, st)
	}

	u := m.User
	if u.Avatar == "" {
		u.Avatar, u.Color = protocol.DefaultAvatar(u.Username)
	}
	tile := AvatarStyle(v.T, u.Color, v.T.Base()).Reverse(true)
	tx := r.W/2 - 3
	for dy := 0; dy < 3; dy++ {
		c.Text(tx, 2+dy, "      ", tile)
	}
	c.Text(tx+2, 3, avatarText(u.Avatar), tile)
	centre(6, u.DisplayName(), v.T.Heading())
	sub := "@" + u.Username
	if facts := UserFacts(u, lang); facts != "" {
		sub += " · " + facts
	}
	centre(7, sub, v.T.Faint())

	width := minInt(r.W-4, 40)
	x0 := (r.W - width) / 2
	c.Text(x0, 9, strings.Repeat("─", width), v.T.BorderSoftStyle())
	mp := MomentsPage{T: v.T}
	y := 10
	for i, it := range MeItems {
		if it == meGapBefore {
			y++
		}
		st := v.T.Body()
		if it == MeSignOut {
			st = v.T.DangerStyle()
		}
		mp.drawLine(c, x0, y, width, textLine(meLabel(it, lang), st), i == m.Selected && m.Focused)
		y++
	}
}
