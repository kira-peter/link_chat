package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// Moments, friends and profiles on the client (spec tui-chat-client /
// 朋友圈页面, 主页页面). Key handling runs on the main loop like every other
// page; network calls run in goroutines that take a.mu to apply their result.

// momentsState is the client side of the moments feature.
type momentsState struct {
	Model views.MomentsModel
	// Next is the cursor for the next timeline page.
	Next *protocol.MomentCursor
	// Unread is what the sidebar badge shows.
	Unread protocol.UnreadRes
	// Profile is the profile page; ProfileBack the page it was opened from.
	Profile     views.ProfileModel
	ProfileBack client.Page
	// confirm runs when the user answers y to Model.Confirm.
	confirm func()
	// noticeUntil clears a notice after a moment.
	noticeUntil time.Time
	// friendsAt and noticesAt are when the lists were last fetched. Events
	// keep them current, so switching tabs refetches only when they are stale;
	// refetching on every switch spent the connection's request budget.
	friendsAt, noticesAt time.Time
	noticesRevision      uint64
	// editOnLoad opens the edit form as soon as the user's own profile loads
	// (the "edit profile" row of "me").
	editOnLoad bool
	// gen counts local changes to moments; touched and removed record the gen
	// at which each moment last changed here. A timeline page fetched before a
	// change must not undo it when its reply lands later.
	gen     int
	touched map[string]int
	removed map[string]int
}

// touch records that moment id changed locally now. Caller holds a.mu.
func (ms *momentsState) touch(id string, gone bool) {
	ms.gen++
	if ms.touched == nil {
		ms.touched, ms.removed = map[string]int{}, map[string]int{}
	}
	if gone {
		ms.removed[id] = ms.gen
	} else {
		ms.touched[id] = ms.gen
	}
}

// mergePage takes a fetched page, keeping the local copy of any moment changed
// since since, and leaving out any removed since then.
func (ms *momentsState) mergePage(page []protocol.Moment, since int) []protocol.Moment {
	local := map[string]protocol.Moment{}
	for _, m := range ms.Model.Moments {
		local[m.ID] = m
	}
	out := make([]protocol.Moment, 0, len(page))
	for _, m := range page {
		if ms.removed[m.ID] > since {
			continue
		}
		if l, ok := local[m.ID]; ok && ms.touched[m.ID] > since {
			m = l
		}
		out = append(out, m)
	}
	// A moment posted or pushed since the request went out may be newer than
	// anything in the page.
	for _, m := range ms.Model.Moments {
		if ms.touched[m.ID] > since && indexOfMoment(out, m.ID) < 0 {
			out = append([]protocol.Moment{m}, out...)
		}
	}
	return out
}

// refetchAfter is how long a fetched list counts as fresh.
const refetchAfter = 5 * time.Second

// socialCall runs one request. It is a field so tests can stand in for the
// server.
func (a *App) socialCall(op string, data any) (protocol.Frame, error) {
	return a.socialCallOn(a.tr.Load(), op, data)
}

func (a *App) socialCallOn(tr *client.Transport, op string, data any) (protocol.Frame, error) {
	if a.socialCallFn != nil {
		return a.socialCallFn(op, data)
	}
	if tr == nil {
		return protocol.Frame{}, fmt.Errorf("not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return tr.Call(ctx, op, data, 15*time.Second)
}

// socialDo runs a request in the background and hands its decoded result to
// apply under a.mu. A failure becomes the page's notice.
func socialDo[T any](a *App, op string, data any, apply func(T)) {
	tr := a.tr.Load()
	go func() {
		f, err := a.socialCallOn(tr, op, data)
		if !a.lockTransport(tr) {
			return
		}
		defer func() {
			a.mu.Unlock()
			a.markDirty()
		}()
		if err != nil {
			a.momentsNotice(i18n.ServerDown.Get(a.lang), true)
			return
		}
		if f.Type == protocol.TypeError {
			code := errorCode(f)
			if code == protocol.CodeInvalidArgument && isSocialOp(op) && !a.ui.Moments.Model.Loaded {
				// An older server does not know these requests at all.
				a.ui.Moments.Model.Unsupported = true
			}
			a.momentsNotice(i18n.ErrText(code, "").Get(a.lang), true)
			a.ui.Moments.Model.Input.Busy = false
			a.ui.Moments.Profile.Edit.Busy = false
			return
		}
		var out T
		if err := decodeData(f, &out); err != nil {
			return
		}
		apply(out)
	}()
}

func isSocialOp(op string) bool {
	return strings.HasPrefix(op, "moment.") || strings.HasPrefix(op, "friend.") || strings.HasPrefix(op, "profile.")
}

// momentsNotice shows a line of feedback on whichever social page is open.
// Caller holds a.mu or runs on the main loop.
func (a *App) momentsNotice(msg string, isErr bool) {
	ms := &a.ui.Moments
	ms.Model.Notice, ms.Model.NoticeErr = msg, isErr
	ms.Profile.Notice, ms.Profile.NoticeErr = msg, isErr
	ms.noticeUntil = nowTime().Add(3 * time.Second)
}

// tickMoments clears a stale notice.
func (a *App) tickMoments() {
	ms := &a.ui.Moments
	if !ms.noticeUntil.IsZero() && nowTime().After(ms.noticeUntil) {
		ms.Model.Notice, ms.Profile.Notice = "", ""
		ms.noticeUntil = time.Time{}
		a.markDirty()
	}
}

// ---------- loading ----------

// loadSocialAfterSignIn fetches what the sidebar badge and the avatars need.
func (a *App) loadSocialAfterSignIn() {
	a.refreshUnread()
	a.refreshFriends()
}

func (a *App) refreshUnread() {
	revision := a.ui.Moments.noticesRevision
	socialDo(a, protocol.OpMomentUnread, nil, func(u protocol.UnreadRes) {
		if a.ui.Moments.noticesRevision == revision {
			a.ui.Moments.Unread = u
		}
	})
}

func (a *App) refreshFriends() {
	a.ui.Moments.friendsAt = nowTime()
	socialDo(a, protocol.OpFriendList, nil, func(res protocol.FriendListRes) {
		m := &a.ui.Moments.Model
		m.Friends, m.Requests, m.Blocked = res.Friends, res.Incoming, res.Blocked
		m.RemovedBy, m.Plaza = res.RemovedBy, res.Plaza
		a.ui.Moments.Unread.Requests = len(res.Incoming)
		for _, u := range res.Friends {
			a.st.Users[u.ID] = u
		}
		a.clampMoments()
		a.syncFriendsPage()
	})
}

// loadTimeline fetches the first page, or the next one when more is true.
func (a *App) loadTimeline(more bool) {
	m := &a.ui.Moments.Model
	var req protocol.MomentTimelineReq
	if more {
		if a.ui.Moments.Next == nil || m.LoadingMore {
			return
		}
		m.LoadingMore = true
		req.Cursor = a.ui.Moments.Next
	}
	since := a.ui.Moments.gen
	socialDo(a, protocol.OpMomentTimeline, req, func(res protocol.MomentTimelineRes) {
		ms := &a.ui.Moments
		m := &ms.Model
		if more {
			for _, mo := range ms.mergePage(res.Moments, since) {
				if indexOfMoment(m.Moments, mo.ID) < 0 {
					m.Moments = append(m.Moments, mo)
				}
			}
		} else {
			m.Moments = ms.mergePage(res.Moments, since)
			ms.Unread.Moments = 0
		}
		m.Loaded, m.LoadingMore = true, false
		m.MoreMoments = res.Next != nil
		a.ui.Moments.Next = res.Next
		a.clampMoments()
	})
}

// refreshFriendsIfStale refetches the friend list unless it is fresh.
func (a *App) refreshFriendsIfStale() {
	if nowTime().Sub(a.ui.Moments.friendsAt) >= refetchAfter {
		a.refreshFriends()
	}
}

func (a *App) loadNotificationsIfStale() {
	if nowTime().Sub(a.ui.Moments.noticesAt) >= refetchAfter {
		a.loadNotifications()
	}
}

func (a *App) loadNotifications() {
	a.ui.Moments.noticesAt = nowTime()
	revision := a.ui.Moments.noticesRevision
	socialDo(a, protocol.OpMomentNotifications, nil, func(res protocol.NotificationsRes) {
		if a.ui.Moments.noticesRevision != revision {
			return
		}
		a.ui.Moments.Model.Notifications = res.Notifications
		a.clampMoments()
	})
}

// markNoticesRead clears the unread notifications, locally at once.
func (a *App) markNoticesRead() {
	if a.ui.Moments.Unread.Notifications == 0 {
		return
	}
	a.ui.Moments.Unread.Notifications = 0
	socialDo(a, protocol.OpMomentNotifRead, nil, func(struct{}) {})
}

// openMoments is Alt+P.
func (a *App) openMoments() {
	if a.st.State != client.StateReady {
		return
	}
	a.showEntryPage(client.EntryMoments)
}

// onMomentsShown runs when the moments page comes up: it loads what the
// visible tab shows.
func (a *App) onMomentsShown() {
	m := &a.ui.Moments.Model
	if !m.Loaded {
		a.loadTimeline(false)
	} else if m.Tab == views.TabTimeline && a.ui.Moments.Unread.Moments > 0 {
		a.loadTimeline(false)
	}
	if m.Tab == views.TabNotices {
		a.loadNotificationsIfStale()
		a.markNoticesRead()
	}
	a.refreshFriendsIfStale()
}

// ---------- events ----------

// onSocialEvent handles the social events. It reports whether name was one.
func (a *App) onSocialEvent(tr *client.Transport, name string, data json.RawMessage) bool {
	switch name {
	case protocol.EvPlazaChanged:
		if !a.lockTransport(tr) {
			return true
		}
		a.refreshFriends()
		a.mu.Unlock()
	case protocol.EvFriendRequested:
		var fr protocol.FriendRequest
		if json.Unmarshal(data, &fr) != nil {
			return true
		}
		if !a.lockTransport(tr) {
			return true
		}
		m := &a.ui.Moments.Model
		kept := []protocol.FriendRequest{fr}
		for _, r := range m.Requests {
			if r.ID != fr.ID {
				kept = append(kept, r)
			}
		}
		m.Requests = kept
		a.ui.Moments.Unread.Requests = len(kept)
		a.syncFriendsPage()
		a.mu.Unlock()
	case protocol.EvFriendAdded:
		var u protocol.User
		if json.Unmarshal(data, &u) != nil {
			return true
		}
		if !a.lockTransport(tr) {
			return true
		}
		m := &a.ui.Moments.Model
		m.Friends = upsertUser(m.Friends, u)
		m.Requests = dropRequestsFrom(m.Requests, u.ID)
		a.ui.Moments.Unread.Requests = len(m.Requests)
		a.st.Users[u.ID] = u
		if p := a.ui.Moments.Profile.Profile; p != nil && p.User.ID == u.ID {
			p.IsFriend, p.RequestSent = true, false
		}
		// Their moments are now ours to see.
		if m.Loaded {
			a.loadTimeline(false)
		}
		a.mu.Unlock()
	case protocol.EvFriendRemoved:
		var ev protocol.FriendRemovedEvent
		if json.Unmarshal(data, &ev) != nil {
			return true
		}
		if !a.lockTransport(tr) {
			return true
		}
		var gone *protocol.User
		for _, u := range a.ui.Moments.Model.Friends {
			if u.ID == ev.UserID {
				uu := u
				gone = &uu
			}
		}
		a.dropFriend(ev.UserID)
		if ev.ByThem && gone != nil {
			m := &a.ui.Moments.Model
			m.RemovedBy = upsertUser(m.RemovedBy, *gone)
		}
		a.mu.Unlock()
	case protocol.EvProfileUpdated:
		var ev protocol.ProfileUpdatedEvent
		if json.Unmarshal(data, &ev) != nil {
			return true
		}
		if !a.lockTransport(tr) {
			return true
		}
		a.applyProfile(ev)
		a.mu.Unlock()
	case protocol.EvMomentNew:
		var mo protocol.Moment
		if json.Unmarshal(data, &mo) != nil {
			return true
		}
		if !a.lockTransport(tr) {
			return true
		}
		m := &a.ui.Moments.Model
		if indexOfMoment(m.Moments, mo.ID) < 0 {
			if m.Loaded {
				a.ui.Moments.touch(mo.ID, false)
				m.Moments = append([]protocol.Moment{mo}, m.Moments...)
				// Keep the same moment selected as it shifts down.
				if m.Selected[views.TabTimeline] > 0 {
					m.Selected[views.TabTimeline]++
				}
			}
			if mo.Author.ID != a.st.UserID && !(a.page() == client.PageMoments && m.Tab == views.TabTimeline) {
				a.ui.Moments.Unread.Moments++
			}
		}
		a.mu.Unlock()
	case protocol.EvMomentUpdated:
		var mo protocol.Moment
		if json.Unmarshal(data, &mo) != nil {
			return true
		}
		if !a.lockTransport(tr) {
			return true
		}
		a.replaceMoment(mo)
		a.mu.Unlock()
	case protocol.EvMomentDeleted:
		var ev protocol.MomentDeletedEvent
		if json.Unmarshal(data, &ev) != nil {
			return true
		}
		if !a.lockTransport(tr) {
			return true
		}
		a.removeMoment(ev.MomentID)
		a.mu.Unlock()
	case protocol.EvMomentNotify:
		var n protocol.Notification
		if json.Unmarshal(data, &n) != nil {
			return true
		}
		if !a.lockTransport(tr) {
			return true
		}
		m := &a.ui.Moments.Model
		if a.page() == client.PageMoments && m.Tab == views.TabNotices && a.st.Focus == client.FocusInput {
			n.Read = true
			socialDo(a, protocol.OpMomentNotifRead, nil, func(struct{}) {})
		} else {
			a.ui.Moments.Unread.Notifications++
		}
		m.Notifications = append([]protocol.Notification{n}, m.Notifications...)
		a.mu.Unlock()
	default:
		return false
	}
	a.markDirty()
	return true
}

func upsertUser(list []protocol.User, u protocol.User) []protocol.User {
	for i := range list {
		if list[i].ID == u.ID {
			list[i] = u
			return list
		}
	}
	return append(list, u)
}

func dropRequestsFrom(list []protocol.FriendRequest, uid string) []protocol.FriendRequest {
	out := list[:0:0]
	for _, r := range list {
		if r.From.ID != uid {
			out = append(out, r)
		}
	}
	return out
}

func indexOfMoment(list []protocol.Moment, id string) int {
	for i, m := range list {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// dropFriend forgets a friend and their moments. Caller holds a.mu.
func (a *App) dropFriend(uid string) {
	m := &a.ui.Moments.Model
	var keep []protocol.User
	for _, u := range m.Friends {
		if u.ID != uid {
			keep = append(keep, u)
		}
	}
	m.Friends = keep
	var moments []protocol.Moment
	for _, mo := range m.Moments {
		if mo.Author.ID != uid {
			moments = append(moments, mo)
		}
	}
	m.Moments = moments
	if p := a.ui.Moments.Profile.Profile; p != nil && p.User.ID == uid {
		p.IsFriend, p.Moments = false, nil
	}
	a.ui.Moments.noticesRevision++
	a.ui.Moments.noticesAt = time.Time{}
	m.Notifications = nil
	a.ui.Moments.Unread.Notifications = 0
	if a.tr.Load() != nil || a.socialCallFn != nil {
		a.refreshUnread()
		if a.page() == client.PageMoments && m.Tab == views.TabNotices {
			a.loadNotifications()
		}
	}
	a.clampMoments()
}

// applyProfile puts a changed profile everywhere it is shown. Caller holds a.mu.
func (a *App) applyProfile(ev protocol.ProfileUpdatedEvent) {
	u := ev.User
	if old, ok := a.st.Users[u.ID]; ok {
		u.Online = old.Online
	}
	a.st.Users[u.ID] = u
	m := &a.ui.Moments.Model
	for i := range m.Friends {
		if m.Friends[i].ID == u.ID {
			u.Online = m.Friends[i].Online
			m.Friends[i] = u
		}
	}
	for i := range m.Moments {
		if m.Moments[i].Author.ID == u.ID {
			m.Moments[i].Author = u
		}
	}
	if p := a.ui.Moments.Profile.Profile; p != nil && p.User.ID == u.ID {
		p.User, p.Bio = u, ev.Bio
	}
}

// replaceMoment swaps in a moment's new state wherever it is shown.
func (a *App) replaceMoment(mo protocol.Moment) {
	a.ui.Moments.touch(mo.ID, false)
	m := &a.ui.Moments.Model
	if i := indexOfMoment(m.Moments, mo.ID); i >= 0 {
		m.Moments[i] = mo
		if TimelineSelected(m) == i && m.CommentSel >= len(mo.Comments) {
			m.CommentSel = len(mo.Comments) - 1
		}
	}
	if p := a.ui.Moments.Profile.Profile; p != nil {
		if i := indexOfMoment(p.Moments, mo.ID); i >= 0 {
			p.Moments[i] = mo
		}
	}
}

func (a *App) removeMoment(id string) {
	a.ui.Moments.touch(id, true)
	m := &a.ui.Moments.Model
	if i := indexOfMoment(m.Moments, id); i >= 0 {
		m.Moments = append(m.Moments[:i:i], m.Moments[i+1:]...)
	}
	if p := a.ui.Moments.Profile.Profile; p != nil {
		if i := indexOfMoment(p.Moments, id); i >= 0 {
			p.Moments = append(p.Moments[:i:i], p.Moments[i+1:]...)
		}
	}
	a.clampMoments()
}

// TimelineSelected is the index of the selected moment, or -1 on the compose
// row or past the end.
func TimelineSelected(m *views.MomentsModel) int {
	i := views.TimelineMomentIndex(m.Selected[views.TabTimeline])
	if i < 0 || i >= len(m.Moments) {
		return -1
	}
	return i
}

// ---------- layout helpers ----------

func (a *App) momentsWidth() int {
	w, h := a.scr.Size()
	return views.MomentsContentWidth(views.MainRectAt(views.ContentRect(w, h), 1).W)
}

func (a *App) momentsBodyRows() int {
	w, h := a.scr.Size()
	return views.MomentsBodyRows(views.MainRectAt(views.ContentRect(w, h), 1).H, a.ui.Moments.Model)
}

// clampMoments keeps every tab's selection on a block and in view.
func (a *App) clampMoments() {
	m := &a.ui.Moments.Model
	mp := views.MomentsPage{T: a.tokens}
	width := a.momentsWidth()
	cur := m.Tab
	for _, t := range views.MomentsTabs {
		m.Tab = t
		n := mp.BlockCount(*m, width, a.lang)
		m.Selected[t] = clamp(m.Selected[t], 0, maxInt(0, n-1))
	}
	m.Tab = cur
	a.scrollMomentsIntoView()
}

// scrollMomentsIntoView moves the scroll so the selected block shows,
// preferring its top.
func (a *App) scrollMomentsIntoView() {
	m := &a.ui.Moments.Model
	mp := views.MomentsPage{T: a.tokens}
	top, h := mp.BlockSpan(*m, a.momentsWidth(), a.lang, m.Selected[m.Tab])
	rows := a.momentsBodyRows()
	s := &m.Scroll[m.Tab]
	if top < *s {
		*s = top
	}
	if top+h > *s+rows {
		*s = top + h - rows
		if *s > top {
			*s = top
		}
	}
	if *s < 0 {
		*s = 0
	}
}

// ---------- keys: moments page ----------

func (a *App) handleMomentsKey(ctx context.Context, ev *tcell.EventKey) {
	ms := &a.ui.Moments
	m := &ms.Model
	if m.Unsupported {
		if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyLeft {
			a.moveFocus(client.FocusList)
		}
		return
	}
	if m.Confirm != "" {
		yes := ev.Key() == tcell.KeyRune && (ev.Rune() == 'y' || ev.Rune() == 'Y')
		run := ms.confirm
		m.Confirm, ms.confirm = "", nil
		if yes && run != nil {
			run()
		}
		return
	}
	if m.Input.Active() {
		a.handleMomentsInputKey(ev)
		return
	}
	m.Notice = ""

	switch ev.Key() {
	case tcell.KeyEscape:
		a.moveFocus(client.FocusList)
		return
	case tcell.KeyLeft, tcell.KeyBacktab:
		if m.Tab == views.TabTimeline {
			if ev.Key() == tcell.KeyLeft {
				a.moveFocus(client.FocusList)
			}
			return
		}
		a.switchMomentsTab(m.Tab.Prev())
		return
	case tcell.KeyRight, tcell.KeyTab:
		a.switchMomentsTab(m.Tab.Next())
		return
	case tcell.KeyUp:
		a.moveMomentsSelection(-1)
		return
	case tcell.KeyDown:
		a.moveMomentsSelection(1)
		return
	case tcell.KeyPgUp:
		a.moveMomentsSelection(-5)
		return
	case tcell.KeyPgDn:
		a.moveMomentsSelection(5)
		return
	case tcell.KeyEnter:
		a.momentsEnter()
		return
	case tcell.KeyRune:
	default:
		return
	}

	switch ev.Rune() {
	case 'j':
		a.moveMomentsSelection(1)
	case 'k':
		a.moveMomentsSelection(-1)
	case 'p':
		a.openProfile(a.st.Username)
	}
	switch m.Tab {
	case views.TabTimeline:
		a.timelineKey(ev.Rune())
	case views.TabNotices:
		a.noticesKey(ev.Rune())
	}
}

func (a *App) switchMomentsTab(t views.MomentsTab) {
	m := &a.ui.Moments.Model
	if t == m.Tab {
		return
	}
	m.Tab = t
	m.CommentSel = -1
	switch t {
	case views.TabNotices:
		a.loadNotificationsIfStale()
		a.markNoticesRead()
		a.refreshFriendsIfStale()
	case views.TabTimeline:
		if a.ui.Moments.Unread.Moments > 0 {
			a.loadTimeline(false)
		}
	}
	a.clampMoments()
}

// moveMomentsSelection moves by delta blocks. On the timeline a moment's
// comments are stepped through before moving on, so r can pick one.
func (a *App) moveMomentsSelection(delta int) {
	m := &a.ui.Moments.Model
	if m.Tab == views.TabTimeline && (delta == 1 || delta == -1) {
		if i := TimelineSelected(m); i >= 0 {
			n := len(views.CommentRows(m.Moments[i], m.Expanded[m.Moments[i].ID]))
			next := m.CommentSel + delta
			if next >= -1 && next < n && !(delta == -1 && m.CommentSel == -1) {
				m.CommentSel = next
				return
			}
		}
	}
	mp := views.MomentsPage{T: a.tokens}
	n := mp.BlockCount(*m, a.momentsWidth(), a.lang)
	old := m.Selected[m.Tab]
	m.Selected[m.Tab] = clamp(old+delta, 0, maxInt(0, n-1))
	m.CommentSel = -1
	if m.Tab == views.TabTimeline && delta == -1 {
		// Coming up into a moment lands on its last comment.
		if i := TimelineSelected(m); i >= 0 && m.Selected[m.Tab] != old {
			m.CommentSel = len(views.CommentRows(m.Moments[i], m.Expanded[m.Moments[i].ID])) - 1
		}
	}
	a.scrollMomentsIntoView()
	// Near the end of what is loaded, fetch more.
	if m.Tab == views.TabTimeline && m.MoreMoments && m.Selected[m.Tab] >= len(m.Moments)-2 {
		a.loadTimeline(true)
	}
}

func (a *App) momentsEnter() {
	m := &a.ui.Moments.Model
	switch m.Tab {
	case views.TabTimeline:
		if m.Selected[views.TabTimeline] == 0 {
			m.Input = views.MomentsInput{Kind: views.InputPost}
			return
		}
		if i := TimelineSelected(m); i >= 0 {
			// Enter on the fold row folds or unfolds; anywhere else it opens
			// the author's profile.
			if a.toggleFold(i) {
				return
			}
			a.openProfile(m.Moments[i].Author.Username)
		}
	case views.TabNotices:
		req, note := views.NoticeTarget(*m, m.Selected[views.TabNotices])
		switch {
		case req >= 0:
			a.openProfile(m.Requests[req].From.Username)
		case note >= 0:
			a.openProfile(m.Notifications[note].Actor.Username)
		}
	}
}

// selectedComment is the comment under the cursor of moment i, or nil on the
// moment itself or the fold row.
func selectedComment(m *views.MomentsModel, i int) *protocol.Comment {
	mo := m.Moments[i]
	rows := views.CommentRows(mo, m.Expanded[mo.ID])
	if m.CommentSel < 0 || m.CommentSel >= len(rows) || rows[m.CommentSel] == views.CommentToggle {
		return nil
	}
	return &mo.Comments[rows[m.CommentSel]]
}

// toggleFold folds or unfolds moment i's comments when the cursor is on its
// fold row, and reports whether it was.
func (a *App) toggleFold(i int) bool {
	m := &a.ui.Moments.Model
	mo := m.Moments[i]
	rows := views.CommentRows(mo, m.Expanded[mo.ID])
	if m.CommentSel < 0 || m.CommentSel >= len(rows) || rows[m.CommentSel] != views.CommentToggle {
		return false
	}
	if m.Expanded == nil {
		m.Expanded = map[string]bool{}
	}
	m.Expanded[mo.ID] = !m.Expanded[mo.ID]
	// The cursor stays on the fold row, wherever it now is.
	m.CommentSel = len(views.CommentRows(mo, m.Expanded[mo.ID])) - 1
	a.scrollMomentsIntoView()
	return true
}

func (a *App) timelineKey(r rune) {
	m := &a.ui.Moments.Model
	i := TimelineSelected(m)
	if i < 0 {
		return
	}
	mo := m.Moments[i]
	switch r {
	case 'l':
		a.toggleLike(mo)
	case 'c':
		m.Input = views.MomentsInput{Kind: views.InputComment}
	case 'r':
		if cm := selectedComment(m, i); cm != nil {
			m.Input = views.MomentsInput{Kind: views.InputReply, ReplyName: cm.Author.DisplayName()}
		}
	case 'd':
		if cm := selectedComment(m, i); cm != nil {
			if cm.Author.ID != a.st.UserID && mo.Author.ID != a.st.UserID {
				return
			}
			a.askMoments(i18n.MomentsConfirmDelCmt.Get(a.lang), func() {
				socialDo(a, protocol.OpMomentCommentDelete, protocol.CommentRef{CommentID: cm.ID}, func(pm protocol.Moment) { a.replaceMoment(pm) })
			})
			return
		}
		if mo.Author.ID != a.st.UserID {
			return
		}
		a.askMoments(i18n.MomentsConfirmDelete.Get(a.lang), func() {
			socialDo(a, protocol.OpMomentDelete, protocol.MomentRef{MomentID: mo.ID}, func(struct{}) { a.removeMoment(mo.ID) })
		})
	}
}

func (a *App) toggleLike(mo protocol.Moment) {
	socialDo(a, protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: mo.ID, Like: !mo.Liked}, func(pm protocol.Moment) {
		a.replaceMoment(pm)
	})
}

func (a *App) askMoments(q string, run func()) {
	a.ui.Moments.Model.Confirm = q
	a.ui.Moments.confirm = run
}

func (a *App) noticesKey(r rune) {
	m := &a.ui.Moments.Model
	req, _ := views.NoticeTarget(*m, m.Selected[views.TabNotices])
	if req < 0 {
		return
	}
	fr := m.Requests[req]
	switch r {
	case 'a', 'x':
		accept := r == 'a'
		// Gone from the list at once; the server confirms.
		m.Requests = append(m.Requests[:req:req], m.Requests[req+1:]...)
		a.ui.Moments.Unread.Requests = len(m.Requests)
		a.clampMoments()
		if accept {
			socialDo(a, protocol.OpFriendRespond, protocol.FriendRespondReq{RequestID: fr.ID, Accept: true}, func(res protocol.FriendRequestRes) {
				if res.Friend != nil {
					m := &a.ui.Moments.Model
					m.Friends = upsertUser(m.Friends, *res.Friend)
				}
				a.momentsNotice(i18n.MomentsFriendAdded.Get(a.lang), false)
			})
		} else {
			socialDo(a, protocol.OpFriendRespond, protocol.FriendRespondReq{RequestID: fr.ID}, func(struct{}) {})
		}
	}
}

func (a *App) askBlock(u protocol.User) {
	a.askMoments(fmt.Sprintf(i18n.MomentsConfirmBlock.Get(a.lang), u.DisplayName()), func() {
		socialDo(a, protocol.OpFriendBlock, protocol.FriendUserReq{Username: u.Username}, func(struct{}) {
			a.dropFriend(u.ID)
			a.refreshFriends()
			a.momentsNotice(i18n.MomentsDone.Get(a.lang), false)
		})
	})
}

// messageUser opens the direct message with username, as the "new message"
// page does.
func (a *App) messageUser(ctx context.Context, username string) {
	a.ui.NewPage = views.NewPageModel{Value: username}
	a.submitPrompt(ctx, false)
}

// ---------- the input strip ----------

func (a *App) handleMomentsInputKey(ev *tcell.EventKey) {
	in := &a.ui.Moments.Model.Input
	if in.Busy {
		return
	}
	text := &in.Text
	switch ev.Key() {
	case tcell.KeyEscape:
		*in = views.MomentsInput{}
	case tcell.KeyTab, tcell.KeyBacktab:
		if in.Kind == views.InputPost {
			in.Private = !in.Private
		}
	case tcell.KeyEnter:
		if in.Kind == views.InputPost && ev.Modifiers()&tcell.ModShift != 0 {
			*text = append(*text, '\n')
			return
		}
		a.submitMomentsInput()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(*text) > 0 {
			*text = (*text)[:len(*text)-1]
		}
	case tcell.KeyCtrlU:
		*text = nil
	case tcell.KeyRune:
		*text = append(*text, ev.Rune())
	}
}

func (a *App) submitMomentsInput() {
	m := &a.ui.Moments.Model
	in := &m.Input
	body := strings.TrimSpace(string(in.Text))
	if body == "" {
		return
	}
	switch in.Kind {
	case views.InputPost:
		vis := protocol.VisibilityFriends
		if in.Private {
			vis = protocol.VisibilityPrivate
		}
		in.Busy = true
		socialDo(a, protocol.OpMomentPost, protocol.MomentPostReq{Body: body, Visibility: vis}, func(mo protocol.Moment) {
			m := &a.ui.Moments.Model
			m.Input = views.MomentsInput{}
			a.ui.Moments.touch(mo.ID, false)
			if indexOfMoment(m.Moments, mo.ID) < 0 {
				m.Moments = append([]protocol.Moment{mo}, m.Moments...)
			}
			m.Selected[views.TabTimeline] = 1
			m.CommentSel = -1
			a.clampMoments()
		})
	case views.InputComment, views.InputReply:
		i := TimelineSelected(m)
		if i < 0 {
			*in = views.MomentsInput{}
			return
		}
		mo := m.Moments[i]
		req := protocol.MomentCommentReq{MomentID: mo.ID, Body: body}
		if in.Kind == views.InputReply {
			if cm := selectedComment(m, i); cm != nil {
				req.ReplyTo = cm.ID
			}
		}
		in.Busy = true
		socialDo(a, protocol.OpMomentComment, req, func(pm protocol.Moment) {
			a.ui.Moments.Model.Input = views.MomentsInput{}
			a.replaceMoment(pm)
		})
	}
}

// ---------- the profile page ----------

// openProfile shows username's profile and hands it the keyboard.
func (a *App) openProfile(username string) {
	ms := &a.ui.Moments
	if a.page() != client.PageProfile {
		ms.ProfileBack = a.page()
	}
	ms.Profile = views.ProfileModel{Loading: true}
	a.ui.Page = client.PageProfile
	entry := client.EntryMoments
	if ms.ProfileBack == client.PageFriends {
		entry = client.EntryFriends
	}
	if i := client.IndexOfEntry(a.st.SidebarRows(), entry); i >= 0 {
		a.ui.Selected = i
	}
	if a.st.Focus != client.FocusInput {
		a.st.Focus = client.FocusInput
		a.startFocusTransition(client.FocusInput)
	}
	a.syncFold()
	a.syncMarker()
	socialDo(a, protocol.OpProfileGet, protocol.ProfileGetReq{Username: username}, func(p protocol.Profile) {
		a.ui.Moments.Profile = views.ProfileModel{Profile: &p}
		if a.ui.Moments.editOnLoad {
			a.ui.Moments.editOnLoad = false
			a.startOwnEdit()
		}
	})
}

// leaveProfile goes back to where the profile was opened from.
func (a *App) leaveProfile() {
	back := a.ui.Moments.ProfileBack
	if back == client.PageProfile || back == client.PageWelcome {
		back = client.PageMoments
	}
	a.ui.Page = back
	if back != client.PageMoments && back != client.PageFriends && back != client.PageMe {
		a.moveFocus(client.FocusList)
	}
	a.syncMarker()
}

func (a *App) handleProfileKey(ctx context.Context, ev *tcell.EventKey) {
	pm := &a.ui.Moments.Profile
	if pm.Edit.Active {
		a.handleProfileEditKey(ev)
		return
	}
	// A block asked from here waits for y; anything else cancels it.
	if a.ui.Moments.Model.Confirm != "" {
		run := a.ui.Moments.confirm
		a.ui.Moments.Model.Confirm, a.ui.Moments.confirm = "", nil
		pm.Notice = ""
		if ev.Key() == tcell.KeyRune && (ev.Rune() == 'y' || ev.Rune() == 'Y') && run != nil {
			run()
			a.leaveProfile()
		}
		return
	}
	pm.Notice = ""
	p := pm.Profile
	btns := views.ProfileButtons(p)
	switch ev.Key() {
	case tcell.KeyEscape:
		a.leaveProfile()
		return
	case tcell.KeyLeft, tcell.KeyRight:
		// ← and → move between the action buttons; with none, ← goes back.
		if len(btns) == 0 {
			if ev.Key() == tcell.KeyLeft {
				a.leaveProfile()
			}
			return
		}
		d := 1
		if ev.Key() == tcell.KeyLeft {
			d = len(btns) - 1
		}
		pm.Button = (pm.Button + d) % len(btns)
		return
	case tcell.KeyEnter:
		if pm.Button < len(btns) {
			a.pressProfileButton(ctx, btns[pm.Button])
		}
		return
	case tcell.KeyUp:
		pm.Scroll = maxInt(0, pm.Scroll-1)
		return
	case tcell.KeyDown:
		pm.Scroll++
		a.clampProfileScroll()
		return
	case tcell.KeyPgUp:
		pm.Scroll = maxInt(0, pm.Scroll-10)
		return
	case tcell.KeyPgDn:
		pm.Scroll += 10
		a.clampProfileScroll()
		return
	case tcell.KeyRune:
	default:
		return
	}
	if p == nil {
		return
	}
	switch ev.Rune() {
	case 'm':
		if p.IsFriend {
			a.pressProfileButton(ctx, views.BtnMessage)
		}
	case 'a':
		if !p.IsSelf && !p.IsFriend {
			a.pressProfileButton(ctx, views.BtnAdd)
		}
	case 'b':
		if !p.IsSelf {
			a.pressProfileButton(ctx, views.BtnBlock)
		}
	case 'e':
		if p.IsSelf {
			a.startOwnEdit()
		}
	}
}

// pressProfileButton does what a profile button says.
func (a *App) pressProfileButton(ctx context.Context, b views.ProfileButton) {
	pm := &a.ui.Moments.Profile
	p := pm.Profile
	if p == nil {
		return
	}
	switch b {
	case views.BtnMessage:
		a.messageUser(ctx, p.User.Username)
	case views.BtnAdd:
		if !p.RequestSent {
			a.sendFriendRequest(p.User.Username, "")
		}
	case views.BtnBlock:
		// The question lives on the moments page's model; show it here too.
		a.askBlock(p.User)
		pm.Notice, pm.NoticeErr = a.ui.Moments.Model.Confirm, true
	}
}

func (a *App) clampProfileScroll() {
	pm := &a.ui.Moments.Profile
	w, h := a.scr.Size()
	main := views.MainRectAt(views.ContentRect(w, h), 1)
	n := views.ProfileLineCount(a.tokens, pm.Profile, views.MomentsContentWidth(main.W), nowMillis(), a.lang)
	pm.Scroll = clamp(pm.Scroll, 0, maxInt(0, n-views.ProfileMomentRows(main.H)))
}

func (a *App) handleProfileEditKey(ev *tcell.EventKey) {
	e := &a.ui.Moments.Profile.Edit
	if e.Busy {
		return
	}
	var text *[]rune
	switch e.Field {
	case views.EditNickname:
		text = &e.Nickname
	case views.EditBio:
		text = &e.Bio
	case views.EditAvatar:
		text = &e.Avatar
	case views.EditAge:
		text = &e.Age
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		*e = views.ProfileEdit{}
	case tcell.KeyUp, tcell.KeyBacktab:
		e.Field = e.Field.Prev()
	case tcell.KeyDown, tcell.KeyTab:
		e.Field = e.Field.Next()
	case tcell.KeyLeft, tcell.KeyRight:
		switch e.Field {
		case views.EditColor:
			e.Color = cycleColor(e.Color, ev.Key() == tcell.KeyRight)
		case views.EditGender:
			e.Gender = cycleIn(protocol.Genders, e.Gender, ev.Key() == tcell.KeyRight)
		}
	case tcell.KeyEnter:
		a.saveProfile()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if text != nil && len(*text) > 0 {
			*text = (*text)[:len(*text)-1]
		}
	case tcell.KeyRune:
		if text == nil {
			return
		}
		if e.Field == views.EditAvatar {
			// One character: typing replaces it.
			*text = []rune{ev.Rune()}
			return
		}
		if e.Field == views.EditAge && (ev.Rune() < '0' || ev.Rune() > '9' || len(*text) >= 3) {
			return
		}
		*text = append(*text, ev.Rune())
	}
}

// cycleIn steps to the next or previous value of list after cur, wrapping.
func cycleIn(list []string, cur string, forward bool) string {
	i := 0
	for k, x := range list {
		if x == cur {
			i = k
		}
	}
	if forward {
		return list[(i+1)%len(list)]
	}
	return list[(i+len(list)-1)%len(list)]
}

func cycleColor(c string, forward bool) string {
	cs := protocol.AvatarColors
	i := 0
	for k, x := range cs {
		if x == c {
			i = k
		}
	}
	if forward {
		return cs[(i+1)%len(cs)]
	}
	return cs[(i+len(cs)-1)%len(cs)]
}

func (a *App) saveProfile() {
	e := &a.ui.Moments.Profile.Edit
	nick, bio, avatar, color := strings.TrimSpace(string(e.Nickname)), strings.TrimSpace(string(e.Bio)), strings.TrimSpace(string(e.Avatar)), e.Color
	gender, age := e.Gender, 0
	fmt.Sscan(string(e.Age), &age)
	e.Busy = true
	socialDo(a, protocol.OpProfileUpdate, protocol.ProfileUpdateReq{Nickname: &nick, Bio: &bio, Avatar: &avatar, Color: &color, Gender: &gender, Age: &age}, func(ev protocol.ProfileUpdatedEvent) {
		a.ui.Moments.Profile.Edit = views.ProfileEdit{}
		a.applyProfile(ev)
		if ev.User.ID == a.st.UserID {
			u := ev.User
			u.Bio = ev.Bio
			a.ui.Me.Self = u
		}
		a.momentsNotice(i18n.ProfileSaved.Get(a.lang), false)
	})
}

// ---------- render ----------

func (a *App) momentsModel(focused, nameInFlight bool) views.MomentsModel {
	m := a.ui.Moments.Model
	m.SelfID = a.st.UserID
	m.Focused, m.NameInFlight = focused, nameInFlight
	m.Now = nowMillis()
	if !focused {
		m.CommentSel = -1
	}
	return m
}

func (a *App) profileModel(focused, nameInFlight bool) views.ProfileModel {
	p := a.ui.Moments.Profile
	p.Focused, p.NameInFlight = focused, nameInFlight
	p.Now = nowMillis()
	return p
}

// avatarIndex maps lower-cased usernames to known users, for DM rows.
func (a *App) avatarIndex() map[string]protocol.User {
	out := make(map[string]protocol.User, len(a.st.Users))
	for _, u := range a.st.Users {
		out[strings.ToLower(u.Username)] = u
	}
	return out
}
