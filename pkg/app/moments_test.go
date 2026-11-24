package app

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// fakeSocial stands in for the server: it records calls and answers them.
type fakeSocial struct {
	mu     sync.Mutex
	calls  []string
	reqs   map[string][]any
	answer map[string]any
	fail   map[string]string
}

func newFakeSocial() *fakeSocial {
	return &fakeSocial{reqs: map[string][]any{}, answer: map[string]any{}, fail: map[string]string{}}
}

func (f *fakeSocial) call(op string, data any) (protocol.Frame, error) {
	f.mu.Lock()
	f.calls = append(f.calls, op)
	f.reqs[op] = append(f.reqs[op], data)
	code, failing := f.fail[op]
	ans, ok := f.answer[op]
	f.mu.Unlock()
	if failing {
		return protocol.NewError("r", protocol.NewErrorCode(code))
	}
	if !ok {
		ans = struct{}{}
	}
	return protocol.NewRes("r", ans)
}

func (f *fakeSocial) called(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs[op])
}

func (f *fakeSocial) last(op string) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.reqs[op]
	if len(r) == 0 {
		return nil
	}
	return r[len(r)-1]
}

var (
	sAlice = protocol.User{ID: "u_me", Username: "me"}
	sBob   = protocol.User{ID: "u_bob", Username: "bob", Avatar: "B", Color: "blue"}
	sCarol = protocol.User{ID: "u_carol", Username: "carol", Avatar: "C", Color: "green"}
)

func socialMoments() []protocol.Moment {
	return []protocol.Moment{
		{ID: "m1", Author: sBob, Body: "bob says", Visibility: protocol.VisibilityFriends, CreatedAt: 200,
			Comments: []protocol.Comment{{ID: "c1", MomentID: "m1", Author: sCarol, Body: "nice"}}},
		{ID: "m2", Author: sAlice, Body: "mine", Visibility: protocol.VisibilityFriends, CreatedAt: 100},
	}
}

// momentsApp is an app on the moments page with the keyboard, the timeline
// loaded from a fake server.
func momentsApp(t *testing.T) (*App, *fakeSocial) {
	t.Helper()
	a, _ := newKeyTestApp(t)
	a.st.UserID, a.st.Username = sAlice.ID, sAlice.Username
	f := newFakeSocial()
	f.answer[protocol.OpMomentTimeline] = protocol.MomentTimelineRes{Moments: socialMoments()}
	f.answer[protocol.OpFriendList] = protocol.FriendListRes{
		Friends:  []protocol.User{sBob},
		Incoming: []protocol.FriendRequest{{ID: "r1", From: sCarol, Note: "hi"}},
		Blocked:  []protocol.User{},
	}
	f.answer[protocol.OpMomentNotifications] = protocol.NotificationsRes{Notifications: []protocol.Notification{
		{ID: 1, Kind: protocol.NotifyLike, Actor: sBob, MomentID: "m2"},
	}}
	a.socialCallFn = f.call
	a.mu.Lock()
	a.openMoments()
	a.mu.Unlock()
	settle(t, a, func() bool { return a.ui.Moments.Model.Loaded && len(a.ui.Moments.Model.Friends) == 1 })
	return a, f
}

// settle waits for background calls to land.
func settle(t *testing.T, a *App, done func() bool) {
	t.Helper()
	waitFor(t, 2*time.Second, "social calls", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return done()
	})
}

func pressKey(a *App, k tcell.Key) {
	a.mu.Lock()
	a.handleKey(nil, specialKey(k))
	a.mu.Unlock()
}

func typeRune(a *App, r rune) {
	a.mu.Lock()
	a.handleKey(nil, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	a.mu.Unlock()
}

func typeText(a *App, s string) {
	for _, r := range s {
		typeRune(a, r)
	}
}

func TestAltPOpensMomentsAndLoads(t *testing.T) {
	a, f := momentsApp(t)
	if a.page() != client.PageMoments || a.st.Focus != client.FocusInput {
		t.Fatalf("page %v focus %v", a.page(), a.st.Focus)
	}
	if f.called(protocol.OpMomentTimeline) != 1 {
		t.Fatalf("timeline loads: %d", f.called(protocol.OpMomentTimeline))
	}
	a.render()
	text := screenText(a.scr.(tcell.SimulationScreen))
	if !strings.Contains(text, "bob says") || !strings.Contains(text, "carol：nice") {
		t.Fatalf("timeline not drawn:\n%s", text)
	}
}

func TestMomentsLikeCommentReplyDelete(t *testing.T) {
	a, f := momentsApp(t)
	pressKey(a, tcell.KeyDown) // onto bob's moment
	liked := socialMoments()[0]
	liked.Liked, liked.Likes = true, []protocol.User{sAlice}
	f.answer[protocol.OpMomentLike] = liked
	typeRune(a, 'l')
	settle(t, a, func() bool { return a.ui.Moments.Model.Moments[0].Liked })
	if req := f.last(protocol.OpMomentLike).(protocol.MomentLikeReq); req.MomentID != "m1" || !req.Like {
		t.Fatalf("like: %+v", req)
	}

	// c opens the comment strip; Enter sends.
	commented := liked
	commented.Comments = append(commented.Comments, protocol.Comment{ID: "c2", Author: sAlice, Body: "ok"})
	f.answer[protocol.OpMomentComment] = commented
	typeRune(a, 'c')
	if a.ui.Moments.Model.Input.Kind != views.InputComment {
		t.Fatal("c did not open the comment input")
	}
	typeText(a, "ok")
	pressKey(a, tcell.KeyEnter)
	settle(t, a, func() bool {
		return len(a.ui.Moments.Model.Moments[0].Comments) == 2 && !a.ui.Moments.Model.Input.Active()
	})
	if req := f.last(protocol.OpMomentComment).(protocol.MomentCommentReq); req.Body != "ok" || req.ReplyTo != "" {
		t.Fatalf("comment: %+v", req)
	}

	// ↓ steps onto the first comment; r replies to it.
	pressKey(a, tcell.KeyDown)
	if a.ui.Moments.Model.CommentSel != 0 {
		t.Fatalf("comment selection: %d", a.ui.Moments.Model.CommentSel)
	}
	typeRune(a, 'r')
	if in := a.ui.Moments.Model.Input; in.Kind != views.InputReply || in.ReplyName != "carol" {
		t.Fatalf("reply input: %+v", in)
	}
	typeText(a, "thanks")
	pressKey(a, tcell.KeyEnter)
	settle(t, a, func() bool { return f.called(protocol.OpMomentComment) == 2 })
	if req := f.last(protocol.OpMomentComment).(protocol.MomentCommentReq); req.ReplyTo != "c1" {
		t.Fatalf("reply: %+v", req)
	}

	// d on someone else's moment does nothing; on mine it asks, and y deletes.
	settle(t, a, func() bool { return !a.ui.Moments.Model.Input.Active() })
	// On bob's moment d does nothing.
	typeRune(a, 'd')
	if a.ui.Moments.Model.Confirm != "" {
		t.Fatal("offered to delete someone else's comment")
	}
	// From the first comment: the second comment, then my moment.
	pressKey(a, tcell.KeyDown)
	pressKey(a, tcell.KeyDown)
	if i := TimelineSelected(&a.ui.Moments.Model); i != 1 {
		t.Fatalf("selected moment %d", i)
	}
	typeRune(a, 'd')
	if a.ui.Moments.Model.Confirm == "" {
		t.Fatal("d did not ask")
	}
	typeRune(a, 'y')
	settle(t, a, func() bool { return len(a.ui.Moments.Model.Moments) == 1 })
	if req := f.last(protocol.OpMomentDelete).(protocol.MomentRef); req.MomentID != "m2" {
		t.Fatalf("delete: %+v", req)
	}
}

func TestMomentsComposePost(t *testing.T) {
	a, f := momentsApp(t)
	f.answer[protocol.OpMomentPost] = protocol.Moment{ID: "m9", Author: sAlice, Body: "first\nsecond", Visibility: protocol.VisibilityPrivate}
	pressKey(a, tcell.KeyEnter) // the compose row
	if a.ui.Moments.Model.Input.Kind != views.InputPost {
		t.Fatal("Enter on the compose row did not open it")
	}
	typeText(a, "first")
	a.mu.Lock()
	a.handleKey(nil, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift))
	a.mu.Unlock()
	typeText(a, "second")
	pressKey(a, tcell.KeyTab)
	pressKey(a, tcell.KeyEnter)
	settle(t, a, func() bool { return len(a.ui.Moments.Model.Moments) == 3 })
	req := f.last(protocol.OpMomentPost).(protocol.MomentPostReq)
	if req.Body != "first\nsecond" || req.Visibility != protocol.VisibilityPrivate {
		t.Fatalf("post: %+v", req)
	}
	if a.ui.Moments.Model.Moments[0].ID != "m9" || a.ui.Moments.Model.Selected[views.TabTimeline] != 1 {
		t.Fatal("new moment not on top and selected")
	}
}

func TestMomentsTabsAndNotices(t *testing.T) {
	a, f := momentsApp(t)
	a.ui.Moments.Unread.Notifications = 4
	pressKey(a, tcell.KeyRight)
	if a.ui.Moments.Model.Tab != views.TabNotices {
		t.Fatal("→ did not reach notices")
	}
	settle(t, a, func() bool { return len(a.ui.Moments.Model.Notifications) == 1 })
	if a.ui.Moments.Unread.Notifications != 0 || f.called(protocol.OpMomentNotifRead) != 1 {
		t.Fatalf("notices not marked read: %d %d", a.ui.Moments.Unread.Notifications, f.called(protocol.OpMomentNotifRead))
	}
	pressKey(a, tcell.KeyRight) // wraps round to the first tab
	if a.ui.Moments.Model.Tab != views.TabTimeline {
		t.Fatalf("tab %v", a.ui.Moments.Model.Tab)
	}
	pressKey(a, tcell.KeyEscape)
	if a.st.Focus != client.FocusList {
		t.Fatal("Esc did not return to the list")
	}
}

func TestFriendsPage(t *testing.T) {
	a, f := momentsApp(t)
	a.mu.Lock()
	a.showEntryPage(client.EntryFriends)
	a.mu.Unlock()

	// The requests tab: a accepts the request under the cursor.
	pressKey(a, tcell.KeyRight)
	if a.ui.Friends.Tab != views.TabRequests {
		t.Fatalf("tab %v", a.ui.Friends.Tab)
	}
	f.answer[protocol.OpFriendRespond] = protocol.FriendRequestRes{Result: protocol.FriendRequestAdded, Friend: &sCarol}
	typeRune(a, 'a')
	settle(t, a, func() bool { return f.called(protocol.OpFriendRespond) == 1 })
	if req := f.last(protocol.OpFriendRespond).(protocol.FriendRespondReq); req.RequestID != "r1" || !req.Accept {
		t.Fatalf("respond: %+v", req)
	}

	// The plaza tab: a typed name and a note.
	pressKey(a, tcell.KeyRight)
	f.answer[protocol.OpFriendRequest] = protocol.FriendRequestRes{Result: protocol.FriendRequestSent}
	typeText(a, "dave")
	pressKey(a, tcell.KeyEnter)
	typeText(a, "hello")
	pressKey(a, tcell.KeyEnter)
	settle(t, a, func() bool { return a.ui.Moments.Model.Notice == i18n.MomentsRequestSent.Get(a.lang) })
	if req := f.last(protocol.OpFriendRequest).(protocol.FriendRequestReq); req.Username != "dave" || req.Note != "hello" {
		t.Fatalf("request: %+v", req)
	}

	// Contacts (wrapping round): on bob, d asks and removes; b asks and blocks.
	pressKey(a, tcell.KeyRight)
	a.mu.Lock()
	a.ui.Friends.Selected[views.TabContacts] = 0
	a.ui.Moments.Model.Friends = []protocol.User{sBob}
	a.mu.Unlock()
	typeRune(a, 'd')
	typeRune(a, 'n') // anything but y cancels
	if f.called(protocol.OpFriendRemove) != 0 || a.ui.Friends.Confirm != "" {
		t.Fatal("cancel did not cancel")
	}
	typeRune(a, 'b')
	typeRune(a, 'y')
	settle(t, a, func() bool { return f.called(protocol.OpFriendBlock) == 1 })
	if req := f.last(protocol.OpFriendBlock).(protocol.FriendUserReq); req.Username != "bob" {
		t.Fatalf("block: %+v", req)
	}
}

func TestProfileOpenEditSave(t *testing.T) {
	a, f := momentsApp(t)
	f.answer[protocol.OpProfileGet] = protocol.Profile{User: protocol.User{ID: sAlice.ID, Username: "me", Avatar: "M", Color: "cyan"}, IsSelf: true}
	typeRune(a, 'p')
	if a.page() != client.PageProfile {
		t.Fatalf("p opened %v", a.page())
	}
	settle(t, a, func() bool { return a.ui.Moments.Profile.Profile != nil })
	if req := f.last(protocol.OpProfileGet).(protocol.ProfileGetReq); req.Username != "me" {
		t.Fatalf("get: %+v", req)
	}
	typeRune(a, 'e')
	e := &a.ui.Moments.Profile.Edit
	if !e.Active || e.Field != views.EditNickname {
		t.Fatal("e did not start editing")
	}
	typeText(a, "小爱")
	pressKey(a, tcell.KeyDown)
	typeText(a, "hi")
	pressKey(a, tcell.KeyDown)
	typeRune(a, '★')
	pressKey(a, tcell.KeyDown)
	pressKey(a, tcell.KeyRight) // cyan → blue
	f.answer[protocol.OpProfileUpdate] = protocol.ProfileUpdatedEvent{User: protocol.User{ID: sAlice.ID, Username: "me", Nickname: "小爱", Avatar: "★", Color: "blue"}, Bio: "hi"}
	pressKey(a, tcell.KeyEnter)
	settle(t, a, func() bool { return !a.ui.Moments.Profile.Edit.Active })
	req := f.last(protocol.OpProfileUpdate).(protocol.ProfileUpdateReq)
	if *req.Nickname != "小爱" || *req.Bio != "hi" || *req.Avatar != "★" || *req.Color != "blue" {
		t.Fatalf("update: %+v %+v %+v %+v", *req.Nickname, *req.Bio, *req.Avatar, *req.Color)
	}
	if p := a.ui.Moments.Profile.Profile; p.User.Nickname != "小爱" || p.Bio != "hi" {
		t.Fatalf("profile after save: %+v", p)
	}
	pressKey(a, tcell.KeyEscape)
	if a.page() != client.PageMoments {
		t.Fatalf("Esc went to %v", a.page())
	}
}

func TestProfileOfAnotherFromTimeline(t *testing.T) {
	a, f := momentsApp(t)
	f.answer[protocol.OpProfileGet] = protocol.Profile{User: sBob, IsFriend: true}
	pressKey(a, tcell.KeyDown)
	pressKey(a, tcell.KeyEnter)
	settle(t, a, func() bool { return a.ui.Moments.Profile.Profile != nil })
	if req := f.last(protocol.OpProfileGet).(protocol.ProfileGetReq); req.Username != "bob" {
		t.Fatalf("opened %+v", req)
	}
	a.render()
	if text := screenText(a.scr.(tcell.SimulationScreen)); !strings.Contains(text, "@bob") || !strings.Contains(text, "发消息") || !strings.Contains(text, "拉黑") {
		t.Fatalf("profile:\n%s", text)
	}
}

// The "edit profile" row of "me" lands straight in the edit form, and Esc from
// settings goes back to "me".
func TestMePage(t *testing.T) {
	a, f := momentsApp(t)
	f.answer[protocol.OpProfileGet] = protocol.Profile{User: protocol.User{ID: sAlice.ID, Username: "me"}, IsSelf: true}
	a.mu.Lock()
	a.showEntryPage(client.EntryMe)
	a.mu.Unlock()
	if a.page() != client.PageMe {
		t.Fatalf("page %v", a.page())
	}
	pressKey(a, tcell.KeyEnter) // 编辑资料
	settle(t, a, func() bool { return a.ui.Moments.Profile.Edit.Active })
	pressKey(a, tcell.KeyEscape) // leave the form
	pressKey(a, tcell.KeyEscape) // leave the profile
	if a.page() != client.PageMe {
		t.Fatalf("Esc from the profile went to %v", a.page())
	}
	pressKey(a, tcell.KeyDown)
	pressKey(a, tcell.KeyDown)
	pressKey(a, tcell.KeyEnter) // 设置
	if a.page() != client.PageSettings {
		t.Fatalf("menu opened %v", a.page())
	}
	pressKey(a, tcell.KeyEscape)
	if a.page() != client.PageMe || a.st.Focus != client.FocusInput {
		t.Fatalf("Esc from settings: page %v focus %v", a.page(), a.st.Focus)
	}
}

func TestSocialEventsUpdateState(t *testing.T) {
	a, _ := momentsApp(t)
	send := func(name string, v any) {
		b, _ := json.Marshal(v)
		a.onEvent(name, b)
	}
	// Leave the page so counts accumulate.
	pressKey(a, tcell.KeyEscape)
	a.mu.Lock()
	a.ui.Page = client.PageWelcome
	a.mu.Unlock()

	send(protocol.EvMomentNew, protocol.Moment{ID: "m3", Author: sBob, Body: "new"})
	send(protocol.EvMomentNotify, protocol.Notification{ID: 9, Kind: protocol.NotifyComment, Actor: sBob})
	send(protocol.EvFriendRequested, protocol.FriendRequest{ID: "r2", From: protocol.User{ID: "u_d", Username: "dave"}})
	a.mu.Lock()
	u := a.ui.Moments.Unread
	got := len(a.ui.Moments.Model.Moments)
	a.mu.Unlock()
	if u.Moments != 1 || u.Notifications != 1 || u.Requests != 2 || got != 3 {
		t.Fatalf("after events: %+v, %d moments", u, got)
	}
	m := a.listModel(client.FocusList)
	if m.MomentsBadge != 1 || m.FriendsBadge != 2 || !m.MomentsDot {
		t.Fatalf("badge %d friends %d dot %v", m.MomentsBadge, m.FriendsBadge, m.MomentsDot)
	}

	updated := socialMoments()[0]
	updated.Body = "edited"
	send(protocol.EvMomentUpdated, updated)
	send(protocol.EvMomentDeleted, protocol.MomentDeletedEvent{MomentID: "m2"})
	send(protocol.EvProfileUpdated, protocol.ProfileUpdatedEvent{User: protocol.User{ID: sBob.ID, Username: "bob", Nickname: "Bobby", Avatar: "★", Color: "rose"}})
	a.mu.Lock()
	ms := a.ui.Moments.Model.Moments
	nick := a.st.Users[sBob.ID].Nickname
	a.mu.Unlock()
	if len(ms) != 2 || ms[1].Body != "edited" || ms[1].Author.Nickname != "Bobby" || nick != "Bobby" {
		t.Fatalf("moments after events: %+v", ms)
	}

	send(protocol.EvFriendRemoved, protocol.FriendRemovedEvent{UserID: sBob.ID})
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, mo := range a.ui.Moments.Model.Moments {
		if mo.Author.ID == sBob.ID {
			t.Fatalf("an ex-friend's moment stayed: %+v", mo)
		}
	}
	if len(a.ui.Moments.Model.Friends) != 0 {
		t.Fatal("ex-friend still listed")
	}
}

func TestMomentsUnsupportedServer(t *testing.T) {
	a, _ := newKeyTestApp(t)
	f := newFakeSocial()
	f.fail[protocol.OpMomentTimeline] = protocol.CodeInvalidArgument
	f.fail[protocol.OpFriendList] = protocol.CodeInvalidArgument
	a.socialCallFn = f.call
	a.mu.Lock()
	a.openMoments()
	a.mu.Unlock()
	settle(t, a, func() bool { return a.ui.Moments.Model.Unsupported })
	a.render()
	if text := screenText(a.scr.(tcell.SimulationScreen)); !strings.Contains(text, "不支持朋友圈") {
		t.Fatalf("no notice:\n%s", text)
	}
	pressKey(a, tcell.KeyEscape)
	if a.st.Focus != client.FocusList {
		t.Fatal("cannot leave an unsupported page")
	}
}

func TestAutoLoadsNextPage(t *testing.T) {
	a, f := momentsApp(t)
	a.mu.Lock()
	a.ui.Moments.Next = &protocol.MomentCursor{At: 100, ID: "m2"}
	a.ui.Moments.Model.MoreMoments = true
	a.mu.Unlock()
	f.answer[protocol.OpMomentTimeline] = protocol.MomentTimelineRes{Moments: []protocol.Moment{{ID: "m0", Author: sBob, Body: "older"}}}
	pressKey(a, tcell.KeyDown)
	settle(t, a, func() bool { return len(a.ui.Moments.Model.Moments) == 3 })
	if req := f.last(protocol.OpMomentTimeline).(protocol.MomentTimelineReq); req.Cursor == nil || req.Cursor.ID != "m2" {
		t.Fatalf("next page: %+v", req)
	}
}

// A timeline page fetched before a like must not undo the like when its reply
// arrives after it.
func TestStaleTimelinePageKeepsNewerChanges(t *testing.T) {
	a, _ := momentsApp(t)
	a.mu.Lock()
	defer a.mu.Unlock()
	ms := &a.ui.Moments
	since := ms.gen
	liked := socialMoments()[0]
	liked.Liked = true
	a.replaceMoment(liked)
	a.removeMoment("m2")
	posted := protocol.Moment{ID: "m9", Author: sAlice, Body: "just now"}
	ms.touch(posted.ID, false)
	ms.Model.Moments = append([]protocol.Moment{posted}, ms.Model.Moments...)

	got := ms.mergePage(socialMoments(), since)
	if len(got) != 2 || got[0].ID != "m9" || got[1].ID != "m1" || !got[1].Liked {
		t.Fatalf("merged: %+v", got)
	}
	// A page fetched after the changes is taken as it is.
	if fresh := ms.mergePage(socialMoments(), ms.gen); len(fresh) != 2 || fresh[0].Liked {
		t.Fatalf("fresh page: %+v", fresh)
	}
}
