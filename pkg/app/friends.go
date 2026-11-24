package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// The friends page (contacts, requests, plaza) and the "new direct message"
// picker. Friend data lives in a.ui.Moments.Model, shared with the moments
// page; these hold only what each page has selected.

// friendsState is the friends page's own state.
type friendsState struct {
	Tab      views.FriendsTab
	Selected [3]int
	Scroll   [3]int
	Name     []rune
	Note     []rune
	NoteFor  string
	Busy     bool
	Confirm  string
	confirm  func()
}

// dmPickState is the new-DM page's own state.
type dmPickState struct {
	Selected int
	Err      string
	Busy     bool
}

// friendsModel is the friends page as the view draws it.
func (a *App) friendsModel(focused, nameInFlight bool) views.FriendsModel {
	f := a.ui.Friends
	m := a.ui.Moments.Model
	return views.FriendsModel{
		Tab: f.Tab, Selected: f.Selected, Scroll: f.Scroll,
		Friends: m.Friends, RemovedBy: m.RemovedBy, Blocked: m.Blocked, Requests: m.Requests, Plaza: m.Plaza,
		Name: f.Name, Note: f.Note, NoteFor: f.NoteFor, Busy: f.Busy,
		Confirm: f.Confirm, Notice: m.Notice, NoticeErr: m.NoticeErr,
		Focused: focused, NameInFlight: nameInFlight,
	}
}

// syncFriendsPage keeps each tab's selection on a row after the lists change.
// Caller holds a.mu.
func (a *App) syncFriendsPage() {
	f := &a.ui.Friends
	m := a.friendsModel(false, false)
	for t := views.TabContacts; t <= views.TabPlaza; t++ {
		f.Selected[t] = clamp(f.Selected[t], 0, maxInt(0, views.FriendsRowCount(m, t)-1))
	}
}

func (a *App) friendsBodyRows() int {
	_, h := a.scr.Size()
	return views.FriendsBodyRows(views.ContentRect(0, h).H)
}

// scrollFriends keeps the selected row in view.
func (a *App) scrollFriends() {
	f := &a.ui.Friends
	sel, s := f.Selected[f.Tab], &f.Scroll[f.Tab]
	rows := a.friendsBodyRows()
	if sel < *s {
		*s = sel
	}
	if sel >= *s+rows {
		*s = sel - rows + 1
	}
}

func (a *App) askFriends(q string, run func()) {
	a.ui.Friends.Confirm = q
	a.ui.Friends.confirm = run
}

// switchFriendsTab moves to t and fetches what it shows.
func (a *App) switchFriendsTab(t views.FriendsTab) {
	a.ui.Friends.Tab = t
	a.refreshFriendsIfStale()
}

func (a *App) handleFriendsKey(ctx context.Context, ev *tcell.EventKey) {
	f := &a.ui.Friends
	if f.Confirm != "" {
		run := f.confirm
		f.Confirm, f.confirm = "", nil
		if ev.Key() == tcell.KeyRune && (ev.Rune() == 'y' || ev.Rune() == 'Y') && run != nil {
			run()
		}
		return
	}
	if f.Busy {
		return
	}
	a.ui.Moments.Model.Notice = ""
	if f.NoteFor != "" {
		a.handleNoteKey(ev)
		return
	}
	m := a.friendsModel(true, false)
	n := views.FriendsRowCount(m, f.Tab)
	switch ev.Key() {
	case tcell.KeyEscape:
		a.moveFocus(client.FocusList)
		return
	case tcell.KeyLeft, tcell.KeyBacktab:
		a.switchFriendsTab(f.Tab.Prev())
		return
	case tcell.KeyRight, tcell.KeyTab:
		a.switchFriendsTab(f.Tab.Next())
		return
	case tcell.KeyUp:
		f.Selected[f.Tab] = clamp(f.Selected[f.Tab]-1, 0, maxInt(0, n-1))
		a.scrollFriends()
		return
	case tcell.KeyDown:
		f.Selected[f.Tab] = clamp(f.Selected[f.Tab]+1, 0, maxInt(0, n-1))
		a.scrollFriends()
		return
	}
	switch f.Tab {
	case views.TabRequests:
		a.requestsKey(ev)
	case views.TabPlaza:
		a.plazaKey(ev)
	default:
		a.contactsKey(ctx, ev)
	}
}

func (a *App) contactsKey(ctx context.Context, ev *tcell.EventKey) {
	m := a.friendsModel(true, false)
	row := views.ContactRowAt(m, a.ui.Friends.Selected[views.TabContacts])
	var u *protocol.User
	switch {
	case row.Friend >= 0:
		u = &m.Friends[row.Friend]
	case row.Removed >= 0:
		u = &m.RemovedBy[row.Removed]
	case row.Blocked >= 0:
		u = &m.Blocked[row.Blocked]
	}
	if u == nil {
		return
	}
	who := *u
	if ev.Key() == tcell.KeyEnter {
		if row.Blocked < 0 {
			a.openProfile(who.Username)
		}
		return
	}
	if ev.Key() != tcell.KeyRune {
		return
	}
	switch ev.Rune() {
	case 'm':
		if row.Friend >= 0 {
			a.messageUser(ctx, who.Username)
		}
	case 'd':
		if row.Friend >= 0 || row.Removed >= 0 {
			a.askFriends(fmt.Sprintf(i18n.MomentsConfirmRemove.Get(a.lang), who.DisplayName()), func() {
				socialDo(a, protocol.OpFriendRemove, protocol.FriendUserReq{Username: who.Username}, func(struct{}) {
					a.dropFriend(who.ID)
					a.refreshFriends()
					a.momentsNotice(i18n.MomentsDone.Get(a.lang), false)
				})
			})
		}
	case 'b':
		if row.Blocked < 0 {
			a.askBlockFriend(who)
		}
	case 'u':
		if row.Blocked >= 0 {
			socialDo(a, protocol.OpFriendUnblock, protocol.FriendUserReq{Username: who.Username}, func(struct{}) {
				a.refreshFriends()
				a.momentsNotice(i18n.MomentsDone.Get(a.lang), false)
			})
		}
	}
}

func (a *App) askBlockFriend(who protocol.User) {
	a.askFriends(fmt.Sprintf(i18n.MomentsConfirmBlock.Get(a.lang), who.DisplayName()), func() {
		socialDo(a, protocol.OpFriendBlock, protocol.FriendUserReq{Username: who.Username}, func(struct{}) {
			a.dropFriend(who.ID)
			a.refreshFriends()
			a.momentsNotice(i18n.MomentsDone.Get(a.lang), false)
		})
	})
}

func (a *App) requestsKey(ev *tcell.EventKey) {
	m := &a.ui.Moments.Model
	i := a.ui.Friends.Selected[views.TabRequests]
	if i < 0 || i >= len(m.Requests) {
		return
	}
	fr := m.Requests[i]
	if ev.Key() == tcell.KeyEnter {
		a.openProfile(fr.From.Username)
		return
	}
	if ev.Key() != tcell.KeyRune {
		return
	}
	switch ev.Rune() {
	case 'a', 'x':
		a.respondRequest(fr, ev.Rune() == 'a')
	}
}

// respondRequest accepts or declines fr, taking it off the list at once.
func (a *App) respondRequest(fr protocol.FriendRequest, accept bool) {
	m := &a.ui.Moments.Model
	m.Requests = dropRequestsFrom(m.Requests, fr.From.ID)
	a.ui.Moments.Unread.Requests = len(m.Requests)
	a.syncFriendsPage()
	if !accept {
		socialDo(a, protocol.OpFriendRespond, protocol.FriendRespondReq{RequestID: fr.ID}, func(struct{}) {})
		return
	}
	socialDo(a, protocol.OpFriendRespond, protocol.FriendRespondReq{RequestID: fr.ID, Accept: true}, func(res protocol.FriendRequestRes) {
		if res.Friend != nil {
			m := &a.ui.Moments.Model
			m.Friends = upsertUser(m.Friends, *res.Friend)
		}
		a.momentsNotice(i18n.MomentsFriendAdded.Get(a.lang), false)
		a.refreshFriends()
	})
}

// plazaKey runs the plaza tab: the name field on row 0, the people after.
func (a *App) plazaKey(ev *tcell.EventKey) {
	f := &a.ui.Friends
	m := a.friendsModel(true, false)
	sel := f.Selected[views.TabPlaza]
	if sel == 0 {
		switch ev.Key() {
		case tcell.KeyEnter:
			if name := strings.TrimSpace(string(f.Name)); name != "" {
				f.NoteFor = name
			}
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(f.Name) > 0 {
				f.Name = f.Name[:len(f.Name)-1]
			}
		case tcell.KeyRune:
			f.Name = append(f.Name, ev.Rune())
		}
		return
	}
	if i := sel - 1; i < len(m.Plaza) && ev.Key() == tcell.KeyEnter {
		a.openProfile(m.Plaza[i].Username)
	}
}

// handleNoteKey takes the note that goes with a request.
func (a *App) handleNoteKey(ev *tcell.EventKey) {
	f := &a.ui.Friends
	switch ev.Key() {
	case tcell.KeyEscape:
		f.NoteFor, f.Note = "", nil
	case tcell.KeyEnter:
		a.sendFriendRequest(f.NoteFor, strings.TrimSpace(string(f.Note)))
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(f.Note) > 0 {
			f.Note = f.Note[:len(f.Note)-1]
		}
	case tcell.KeyRune:
		f.Note = append(f.Note, ev.Rune())
	}
}

// sendFriendRequest asks username to be a friend. It runs from the friends
// page and from a profile's add button.
func (a *App) sendFriendRequest(username, note string) {
	f := &a.ui.Friends
	f.Busy = true
	socialDo(a, protocol.OpFriendRequest, protocol.FriendRequestReq{Username: username, Note: note}, func(res protocol.FriendRequestRes) {
		f := &a.ui.Friends
		f.Busy, f.NoteFor, f.Note, f.Name = false, "", nil, nil
		msg := i18n.MomentsRequestSent
		added := res.Result == protocol.FriendRequestAdded
		if added {
			msg = i18n.MomentsFriendAdded
			if res.Friend != nil {
				m := &a.ui.Moments.Model
				m.Friends = upsertUser(m.Friends, *res.Friend)
			}
		}
		if p := a.ui.Moments.Profile.Profile; p != nil && strings.EqualFold(p.User.Username, username) {
			p.IsFriend, p.RequestSent = added, !added
		}
		a.momentsNotice(msg.Get(a.lang), false)
		a.refreshFriends()
	})
}

// ---------- new direct message ----------

// dmCandidates are the friends with no direct message open yet.
func (a *App) dmCandidates() []protocol.User {
	open := map[string]bool{}
	for _, cv := range a.st.Conversations {
		if cv.Kind == protocol.KindDM {
			open[strings.ToLower(cv.Name)] = true
		}
	}
	var out []protocol.User
	for _, u := range a.ui.Moments.Model.Friends {
		if !open[strings.ToLower(u.Username)] {
			out = append(out, u)
		}
	}
	return out
}

func (a *App) dmPickModel(focused, nameInFlight bool) views.DMPickModel {
	d := a.ui.DMPick
	friends := a.dmCandidates()
	return views.DMPickModel{
		Friends: friends, Selected: clamp(d.Selected, 0, maxInt(0, len(friends)-1)),
		Err: d.Err, Busy: d.Busy, Focused: focused, NameInFlight: nameInFlight,
	}
}

func (a *App) handleDMPickKey(ctx context.Context, ev *tcell.EventKey) {
	d := &a.ui.DMPick
	if d.Busy {
		return
	}
	friends := a.dmCandidates()
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyLeft:
		a.moveFocus(client.FocusList)
	case tcell.KeyUp:
		d.Selected = clamp(d.Selected-1, 0, maxInt(0, len(friends)-1))
		d.Err = ""
	case tcell.KeyDown:
		d.Selected = clamp(d.Selected+1, 0, maxInt(0, len(friends)-1))
		d.Err = ""
	case tcell.KeyEnter:
		if len(friends) == 0 {
			return
		}
		u := friends[clamp(d.Selected, 0, len(friends)-1)]
		*d = dmPickState{}
		a.messageUser(ctx, u.Username)
	}
}
