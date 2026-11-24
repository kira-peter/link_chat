package app

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// The "me" page: a menu over the user's own things. Settings, help and about
// open from it and go back to it on Esc.

// meState is the "me" page's own state.
type meState struct {
	Selected int
	// Self is the signed-in user's public profile, fetched when the page shows.
	Self protocol.User
}

func (a *App) meModel(focused, nameInFlight bool) views.MeModel {
	u := a.ui.Me.Self
	if u.Username == "" {
		u.Username, u.ID = a.st.Username, a.st.UserID
	}
	return views.MeModel{User: u, Selected: a.ui.Me.Selected, Focused: focused, NameInFlight: nameInFlight}
}

// refreshSelf fetches the signed-in user's profile for the card.
func (a *App) refreshSelf() {
	if a.st.Username == "" {
		return
	}
	socialDo(a, protocol.OpProfileGet, protocol.ProfileGetReq{Username: a.st.Username}, func(p protocol.Profile) {
		u := p.User
		u.Bio = p.Bio
		a.ui.Me.Self = u
	})
}

func (a *App) handleMeKey(ctx context.Context, ev *tcell.EventKey) {
	me := &a.ui.Me
	n := len(views.MeItems)
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyLeft:
		a.moveFocus(client.FocusList)
	case tcell.KeyUp:
		me.Selected = (me.Selected + n - 1) % n
	case tcell.KeyDown:
		me.Selected = (me.Selected + 1) % n
	case tcell.KeyEnter, tcell.KeyRight:
		a.openMeItem(views.MeItems[clamp(me.Selected, 0, n-1)])
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'j':
			me.Selected = (me.Selected + 1) % n
		case 'k':
			me.Selected = (me.Selected + n - 1) % n
		}
	}
}

func (a *App) openMeItem(it views.MeItem) {
	switch it {
	case views.MeEditProfile:
		a.openOwnProfile(true)
	case views.MeMyMoments:
		a.openOwnProfile(false)
	case views.MeSettings:
		a.ui.SettingsNav.Level = views.LevelCategory
		a.ui.Page = client.PageSettings
	case views.MeHelp:
		a.ui.Page = client.PageHelp
	case views.MeAbout:
		a.ui.Page = client.PageAbout
	case views.MeSwitchAccount:
		a.switchAccount()
	case views.MeSignOut:
		a.openLogoutConfirm()
	}
	a.syncMarker()
}

// openOwnProfile opens the user's own profile from "me", straight into the
// edit form when edit is true.
func (a *App) openOwnProfile(edit bool) {
	a.openProfile(a.st.Username)
	a.ui.Moments.ProfileBack = client.PageMe
	if i := client.IndexOfEntry(a.st.SidebarRows(), client.EntryMe); i >= 0 {
		a.ui.Selected = i
	}
	a.ui.Moments.editOnLoad = edit
}

// startOwnEdit opens the edit form on the user's own loaded profile.
func (a *App) startOwnEdit() {
	pm := &a.ui.Moments.Profile
	p := pm.Profile
	if p == nil || !p.IsSelf {
		return
	}
	color := p.User.Color
	if color == "" {
		_, color = protocol.DefaultAvatar(p.User.Username)
	}
	age := ""
	if p.User.Age > 0 {
		age = fmt.Sprint(p.User.Age)
	}
	pm.Edit = views.ProfileEdit{
		Active: true, Nickname: []rune(p.User.Nickname), Bio: []rune(p.Bio),
		Avatar: []rune(p.User.Avatar), Color: color,
		Gender: p.User.Gender, Age: []rune(age),
	}
}

// backToMe leaves a page reached through "me" for the "me" page itself.
func (a *App) backToMe() {
	a.ui.Page = client.PageMe
	a.syncMarker()
}
