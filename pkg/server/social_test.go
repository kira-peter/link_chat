package server

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// social is a server with signed-in clients by name.
type social struct {
	t   *testing.T
	srv *Server
	c   map[string]*client
	id  map[string]string
}

func newSocial(t *testing.T, names ...string) *social {
	t.Helper()
	hs, srv := testServer(t)
	s := &social{t: t, srv: srv, c: map[string]*client{}, id: map[string]string{}}
	for _, n := range names {
		c := dial(t, hs)
		s.id[n] = c.register(n, pw).User.ID
		s.c[n] = c
	}
	return s
}

// befriend makes a and b friends through the protocol.
func (s *social) befriend(a, b string) {
	s.t.Helper()
	s.c[a].callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: b}, nil)
	var res protocol.FriendRequestRes
	s.c[b].callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: a}, &res)
	if res.Result != protocol.FriendRequestAdded {
		s.t.Fatalf("befriend %s %s: %+v", a, b, res)
	}
}

func (s *social) post(who, body, vis string) protocol.Moment {
	s.t.Helper()
	var m protocol.Moment
	s.c[who].callOK(protocol.OpMomentPost, protocol.MomentPostReq{Body: body, Visibility: vis}, &m)
	return m
}

func (s *social) timeline(who string) []protocol.Moment {
	s.t.Helper()
	var res protocol.MomentTimelineRes
	s.c[who].callOK(protocol.OpMomentTimeline, protocol.MomentTimelineReq{}, &res)
	return res.Moments
}

func (s *social) list(who string) protocol.FriendListRes {
	s.t.Helper()
	var res protocol.FriendListRes
	s.c[who].callOK(protocol.OpFriendList, nil, &res)
	return res
}

func names(us []protocol.User) string {
	var out []string
	for _, u := range us {
		out = append(out, u.Username)
	}
	return strings.Join(out, ",")
}

func bodies(ms []protocol.Moment) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Body)
	}
	return strings.Join(out, ",")
}

func commentBodies(m protocol.Moment) string {
	var out []string
	for _, c := range m.Comments {
		out = append(out, c.Author.Username+":"+c.Body)
	}
	return strings.Join(out, ",")
}

func eventData(t *testing.T, ep protocol.EventPayload, out any) {
	t.Helper()
	if err := json.Unmarshal(ep.Data, out); err != nil {
		t.Fatalf("decode %s: %v", ep.Name, err)
	}
}

// ---------- friends ----------

func TestFriendRequestAcceptAndList(t *testing.T) {
	s := newSocial(t, "alice", "bob", "carol")
	var sent protocol.FriendRequestRes
	s.c["alice"].callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob", Note: "我是 alice"}, &sent)
	if sent.Result != protocol.FriendRequestSent {
		t.Fatalf("result: %+v", sent)
	}
	ep := s.c["bob"].waitEvent(protocol.EvFriendRequested, 2*time.Second)
	var fr protocol.FriendRequest
	eventData(t, ep, &fr)
	if fr.From.Username != "alice" || fr.Note != "我是 alice" || fr.From.Avatar != "A" {
		t.Fatalf("requested event: %+v", fr)
	}
	in := s.list("bob").Incoming
	if len(in) != 1 || in[0].ID != fr.ID {
		t.Fatalf("incoming: %+v", in)
	}

	// Someone else cannot answer it.
	s.c["carol"].callErr(protocol.OpFriendRespond, protocol.FriendRespondReq{RequestID: fr.ID, Accept: true}, protocol.CodeNotFound)

	s.c["bob"].callOK(protocol.OpFriendRespond, protocol.FriendRespondReq{RequestID: fr.ID, Accept: true}, nil)
	ep = s.c["alice"].waitEvent(protocol.EvFriendAdded, 2*time.Second)
	var added protocol.User
	eventData(t, ep, &added)
	if added.Username != "bob" {
		t.Fatalf("added: %+v", added)
	}
	if got := names(s.list("alice").Friends); got != "bob" {
		t.Fatalf("alice's friends: %s", got)
	}
	if l := s.list("bob"); names(l.Friends) != "alice" || len(l.Incoming) != 0 {
		t.Fatalf("bob: %+v", l)
	}
	s.c["alice"].callErr(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob"}, protocol.CodeAlreadyFriends)
}

func TestFriendRequestRules(t *testing.T) {
	s := newSocial(t, "alice", "bob")
	s.c["alice"].callErr(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "alice"}, protocol.CodeInvalidArgument)
	s.c["alice"].callErr(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "nobody"}, protocol.CodeUserNotFound)

	// Repeating merges into one request with the newest note.
	s.c["alice"].callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob", Note: "1"}, nil)
	s.c["alice"].callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob", Note: "再试一次"}, nil)
	in := s.list("bob").Incoming
	if len(in) != 1 || in[0].Note != "再试一次" {
		t.Fatalf("merged: %+v", in)
	}

	// Declining lets them ask again.
	s.c["bob"].callOK(protocol.OpFriendRespond, protocol.FriendRespondReq{RequestID: in[0].ID}, nil)
	if l := s.list("bob"); len(l.Incoming) != 0 || len(l.Friends) != 0 {
		t.Fatalf("after decline: %+v", l)
	}
	s.c["alice"].callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob"}, nil)
}

func TestFriendRemoveIsSilentButTold(t *testing.T) {
	s := newSocial(t, "alice", "bob")
	s.befriend("alice", "bob")
	s.c["bob"].drainEvents()
	s.c["alice"].callOK(protocol.OpFriendRemove, protocol.FriendUserReq{Username: "bob"}, nil)
	ep := s.c["bob"].waitEvent(protocol.EvFriendRemoved, 2*time.Second)
	var ev protocol.FriendRemovedEvent
	eventData(t, ep, &ev)
	if ev.UserID != s.id["alice"] {
		t.Fatalf("removed: %+v", ev)
	}
	if len(s.list("bob").Friends) != 0 || len(s.list("alice").Friends) != 0 {
		t.Fatal("still friends")
	}
}

func TestBlock(t *testing.T) {
	s := newSocial(t, "alice", "bob", "dave")
	s.befriend("alice", "bob")
	s.c["alice"].callOK(protocol.OpFriendBlock, protocol.FriendUserReq{Username: "bob"}, nil)
	s.c["alice"].callOK(protocol.OpFriendBlock, protocol.FriendUserReq{Username: "dave"}, nil)
	if l := s.list("alice"); len(l.Friends) != 0 || names(l.Blocked) != "dave,bob" && names(l.Blocked) != "bob,dave" {
		t.Fatalf("alice after block: %+v", l)
	}
	// The blocked side sees neither the profile nor a way to ask.
	s.c["bob"].callErr(protocol.OpProfileGet, protocol.ProfileGetReq{Username: "alice"}, protocol.CodeUserNotFound)
	s.c["bob"].callErr(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "alice"}, protocol.CodeUserNotFound)
	if len(s.list("alice").Incoming) != 0 {
		t.Fatal("a request got through the block")
	}
	var ul protocol.UserListRes
	s.c["bob"].callOK(protocol.OpUserList, nil, &ul)
	if strings.Contains(names(ul.Users), "alice") {
		t.Fatalf("blocker still listed: %s", names(ul.Users))
	}

	s.c["alice"].callOK(protocol.OpFriendUnblock, protocol.FriendUserReq{Username: "bob"}, nil)
	if l := s.list("alice"); len(l.Friends) != 0 || names(l.Blocked) != "dave" {
		t.Fatalf("after unblock: %+v", l)
	}
	s.c["bob"].callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "alice"}, nil)
}

func TestFriendRequestRateLimit(t *testing.T) {
	hs, srv := testServerWith(t, func(c *Config) { c.FriendRequestsPerHour = 2 }, io.Discard)
	_ = srv
	a := dial(t, hs)
	a.register("alice", pw)
	for _, n := range []string{"bob1", "bob2", "bob3"} {
		dial(t, hs).register(n, pw)
	}
	a.callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob1"}, nil)
	a.callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob2"}, nil)
	f := a.call(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob3"})
	var p protocol.ResPayload
	json.Unmarshal(f.Payload, &p)
	if p.Code != protocol.CodeRateLimited || p.RetryAfterMS <= 0 {
		t.Fatalf("third request: %+v", p)
	}
}

// ---------- profiles ----------

func TestProfileDefaultsAndUpdate(t *testing.T) {
	s := newSocial(t, "alice", "bob")
	s.befriend("alice", "bob")
	var p protocol.Profile
	s.c["bob"].callOK(protocol.OpProfileGet, protocol.ProfileGetReq{Username: "alice"}, &p)
	wantA, wantC := protocol.DefaultAvatar("alice")
	if p.User.DisplayName() != "alice" || p.User.Avatar != wantA || p.User.Color != wantC || !p.IsFriend || p.IsSelf {
		t.Fatalf("default profile: %+v", p)
	}

	s.c["bob"].drainEvents()
	nick, bio, av, col := "小爱", "hello", "🐱", "rose"
	s.c["alice"].callOK(protocol.OpProfileUpdate, protocol.ProfileUpdateReq{Nickname: &nick, Bio: &bio, Avatar: &av, Color: &col}, nil)
	ep := s.c["bob"].waitEvent(protocol.EvProfileUpdated, 2*time.Second)
	var ev protocol.ProfileUpdatedEvent
	eventData(t, ep, &ev)
	if ev.User.Nickname != "小爱" || ev.User.Avatar != "🐱" || ev.Bio != "hello" {
		t.Fatalf("pushed: %+v", ev)
	}
	if got := s.list("bob").Friends[0]; got.Nickname != "小爱" || got.Color != "rose" {
		t.Fatalf("friend list: %+v", got)
	}

	// Only the bio changes when only the bio is sent.
	bio2 := "updated"
	s.c["alice"].callOK(protocol.OpProfileUpdate, protocol.ProfileUpdateReq{Bio: &bio2}, nil)
	s.c["alice"].callOK(protocol.OpProfileGet, protocol.ProfileGetReq{Username: "alice"}, &p)
	if p.Bio != "updated" || p.User.Nickname != "小爱" || !p.IsSelf {
		t.Fatalf("partial update: %+v", p)
	}

	for _, bad := range []protocol.ProfileUpdateReq{
		{Bio: strp(strings.Repeat("长", protocol.MaxBioLen+1))},
		{Avatar: strp("我爱你")},
		{Color: strp("ultraviolet")},
		{Nickname: strp("a\nb")},
	} {
		s.c["alice"].callErr(protocol.OpProfileUpdate, bad, protocol.CodeInvalidProfile)
	}
	s.c["alice"].callOK(protocol.OpProfileGet, protocol.ProfileGetReq{Username: "alice"}, &p)
	if p.Bio != "updated" {
		t.Fatalf("a refused update changed the bio: %q", p.Bio)
	}
}

func strp(s string) *string { return &s }

func TestProfileMomentsForStrangers(t *testing.T) {
	s := newSocial(t, "alice", "carol")
	s.post("alice", "friends only", "")
	time.Sleep(3 * time.Millisecond)
	s.post("alice", "just me", protocol.VisibilityPrivate)
	var p protocol.Profile
	s.c["carol"].callOK(protocol.OpProfileGet, protocol.ProfileGetReq{Username: "alice"}, &p)
	if p.IsFriend || len(p.Moments) != 0 || p.User.Username != "alice" {
		t.Fatalf("stranger: %+v", p)
	}
	s.c["alice"].callOK(protocol.OpProfileGet, protocol.ProfileGetReq{Username: "alice"}, &p)
	if bodies(p.Moments) != "just me,friends only" {
		t.Fatalf("own profile: %s", bodies(p.Moments))
	}
}

func TestMessagesCarryAuthorAvatar(t *testing.T) {
	s := newSocial(t, "alice", "bob")
	av, col := "★", "amber"
	s.c["alice"].callOK(protocol.OpProfileUpdate, protocol.ProfileUpdateReq{Avatar: &av, Color: &col}, nil)
	s.befriend("alice", "bob")
	var dm protocol.Conversation
	s.c["alice"].callOK(protocol.OpDMOpen, protocol.DMOpenReq{Username: "bob"}, &dm)
	var sent protocol.MessageEvent
	s.c["alice"].callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: dm.ID, Body: "hi"}, &sent)
	msg := sent.Message
	if msg.AuthorAvatar != "★" || msg.AuthorColor != "amber" {
		t.Fatalf("message: %+v", msg)
	}
	var ul protocol.UserListRes
	s.c["bob"].callOK(protocol.OpUserList, nil, &ul)
	if len(ul.Users) != 1 || ul.Users[0].Avatar != "★" {
		t.Fatalf("user list: %+v", ul.Users)
	}
}

// ---------- visibility ----------

func TestFilterInteractions(t *testing.T) {
	m := storage.Moment{ID: "m", AuthorID: "alice", Visibility: storage.VisibilityFriends}
	likes := []storage.Like{{UserID: "bob"}, {UserID: "carol"}, {UserID: "dave"}}
	comments := []storage.Comment{
		{ID: "1", AuthorID: "carol", Body: "c"},
		{ID: "2", AuthorID: "dave", Body: "d"},
		{ID: "3", AuthorID: "bob", ReplyTo: "2", ReplyToUser: "dave", Body: "b->d"},
		{ID: "4", AuthorID: "carol", ReplyTo: "x", ReplyToUser: "alice", Body: "c->a"},
	}
	// bob and carol are friends; dave knows only alice.
	bob := viewer{id: "bob", friends: map[string]bool{"alice": true, "carol": true}}
	alice := viewer{id: "alice", friends: map[string]bool{"bob": true, "carol": true, "dave": true}}
	stranger := viewer{id: "eve", friends: map[string]bool{}}

	ls, cs := filterInteractions(alice, m, likes, comments)
	if len(ls) != 3 || len(cs) != 4 {
		t.Fatalf("author sees %d likes %d comments", len(ls), len(cs))
	}
	ls, cs = filterInteractions(bob, m, likes, comments)
	var ids []string
	for _, c := range cs {
		ids = append(ids, c.ID+"@"+c.ReplyToUser)
	}
	if len(ls) != 2 || strings.Join(ids, ",") != "1@,3@,4@alice" {
		t.Fatalf("bob sees likes %v, comments %v", ls, ids)
	}
	if !canSeeMoment(bob, m) || canSeeMoment(stranger, m) {
		t.Fatal("moment visibility")
	}
	priv := m
	priv.Visibility = storage.VisibilityPrivate
	if canSeeMoment(bob, priv) || !canSeeMoment(alice, priv) {
		t.Fatal("private visibility")
	}
}

// ---------- moments ----------

func TestMomentTimelineVisibility(t *testing.T) {
	s := newSocial(t, "alice", "bob", "carol")
	s.befriend("alice", "bob")
	// Moments within one millisecond order by ID, so space them out.
	for _, p := range [][3]string{{"bob", "bob first", ""}, {"alice", "alice later", ""}, {"alice", "alice private", protocol.VisibilityPrivate}, {"carol", "carol stranger", ""}} {
		s.post(p[0], p[1], p[2])
		time.Sleep(3 * time.Millisecond)
	}

	if got := bodies(s.timeline("alice")); got != "alice private,alice later,bob first" {
		t.Fatalf("alice: %s", got)
	}
	if got := bodies(s.timeline("bob")); got != "alice later,bob first" {
		t.Fatalf("bob: %s", got)
	}
	// carol may not touch alice's moment.
	am := s.timeline("bob")[0]
	s.c["carol"].callErr(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: am.ID, Like: true}, protocol.CodeMomentNotFound)

	s.c["alice"].callOK(protocol.OpFriendRemove, protocol.FriendUserReq{Username: "bob"}, nil)
	if got := bodies(s.timeline("bob")); got != "bob first" {
		t.Fatalf("bob after unfriending: %s", got)
	}
}

func TestMomentPostValidation(t *testing.T) {
	s := newSocial(t, "alice")
	s.c["alice"].callErr(protocol.OpMomentPost, protocol.MomentPostReq{Body: "   "}, protocol.CodeInvalidArgument)
	s.c["alice"].callErr(protocol.OpMomentPost, protocol.MomentPostReq{Body: strings.Repeat("字", protocol.MaxMomentLen+1)}, protocol.CodeMomentTooLong)
	s.c["alice"].callErr(protocol.OpMomentPost, protocol.MomentPostReq{Body: "x", Visibility: "public"}, protocol.CodeInvalidArgument)
	m := s.post("alice", "  多行\n正文  ", "")
	if m.Body != "多行\n正文" || m.Visibility != protocol.VisibilityFriends || m.Author.Username != "alice" {
		t.Fatalf("posted: %+v", m)
	}
}

func TestMomentTimelinePaging(t *testing.T) {
	s := newSocial(t, "alice")
	for i := 0; i < 45; i++ {
		s.srv.st.AddMoment(storage.Moment{ID: fmt.Sprintf("m%02d", i), AuthorID: s.id["alice"], Body: "x", Visibility: storage.VisibilityFriends, CreatedAt: int64(1000 + i/2)})
	}
	seen := map[string]bool{}
	var cur *protocol.MomentCursor
	for i := 0; i < 10; i++ {
		var res protocol.MomentTimelineRes
		s.c["alice"].callOK(protocol.OpMomentTimeline, protocol.MomentTimelineReq{Cursor: cur}, &res)
		for _, m := range res.Moments {
			if seen[m.ID] {
				t.Fatalf("repeated %s", m.ID)
			}
			seen[m.ID] = true
		}
		if res.Next == nil {
			break
		}
		cur = res.Next
	}
	if len(seen) != 45 {
		t.Fatalf("paged %d", len(seen))
	}
}

func TestLikesCommentsAndMutualVisibility(t *testing.T) {
	s := newSocial(t, "alice", "bob", "carol", "dave")
	s.befriend("alice", "bob")
	s.befriend("alice", "carol")
	s.befriend("alice", "dave")
	s.befriend("bob", "carol")
	m := s.post("alice", "hello", "")

	var got protocol.Moment
	s.c["bob"].callOK(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: m.ID, Like: true}, &got)
	s.c["bob"].callOK(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: m.ID, Like: true}, &got)
	if names(got.Likes) != "bob" || !got.Liked {
		t.Fatalf("liked: %+v", got)
	}
	s.c["dave"].callOK(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: m.ID, Like: true}, nil)
	s.c["carol"].callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "好看"}, &got)
	s.c["dave"].callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "dave here"}, nil)

	byAuthor := s.timeline("alice")[0]
	if len(byAuthor.Likes) != 2 || len(byAuthor.Comments) != 2 {
		t.Fatalf("author sees: %+v", byAuthor)
	}
	byBob := s.timeline("bob")[0]
	if names(byBob.Likes) != "bob" || commentBodies(byBob) != "carol:好看" {
		t.Fatalf("bob sees likes %s comments %s", names(byBob.Likes), commentBodies(byBob))
	}

	// Replying.
	carolComment := byAuthor.Comments[0]
	s.c["alice"].callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "谢谢", ReplyTo: carolComment.ID}, &got)
	last := got.Comments[len(got.Comments)-1]
	if last.ReplyTo == nil || last.ReplyTo.Username != "carol" || last.Body != "谢谢" {
		t.Fatalf("reply: %+v", last)
	}
	s.c["bob"].callErr(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "x", ReplyTo: "cm_nope"}, protocol.CodeInvalidArgument)

	// Unlike.
	s.c["bob"].callOK(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: m.ID, Like: false}, &got)
	if got.Liked || strings.Contains(names(got.Likes), "bob") {
		t.Fatalf("unliked: %+v", got)
	}
}

func TestMomentDeletion(t *testing.T) {
	s := newSocial(t, "alice", "bob")
	s.befriend("alice", "bob")
	m := s.post("alice", "hello", "")
	var got protocol.Moment
	s.c["bob"].callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "bob says"}, &got)
	bobComment := got.Comments[0].ID

	s.c["bob"].callErr(protocol.OpMomentDelete, protocol.MomentRef{MomentID: m.ID}, protocol.CodeForbidden)
	// The author may delete any comment on their moment.
	s.c["alice"].callOK(protocol.OpMomentCommentDelete, protocol.CommentRef{CommentID: bobComment}, &got)
	if len(got.Comments) != 0 {
		t.Fatalf("comment survived: %+v", got.Comments)
	}
	mine := s.post("bob", "bob post", "")
	s.c["alice"].callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: mine.ID, Body: "alice says"}, &got)
	s.c["bob"].callErr(protocol.OpMomentCommentDelete, protocol.CommentRef{CommentID: "cm_nope"}, protocol.CodeNotFound)

	s.c["bob"].drainEvents()
	s.c["alice"].callOK(protocol.OpMomentDelete, protocol.MomentRef{MomentID: m.ID}, nil)
	ep := s.c["bob"].waitEvent(protocol.EvMomentDeleted, 2*time.Second)
	var ev protocol.MomentDeletedEvent
	eventData(t, ep, &ev)
	if ev.MomentID != m.ID || bodies(s.timeline("bob")) != "bob post" {
		t.Fatalf("after delete: %+v %s", ev, bodies(s.timeline("bob")))
	}
}

func TestMomentPushRespectsVisibility(t *testing.T) {
	s := newSocial(t, "alice", "bob", "dave")
	s.befriend("alice", "bob")
	s.befriend("alice", "dave")
	s.c["bob"].drainEvents()
	s.post("alice", "just me", protocol.VisibilityPrivate)
	for _, f := range s.c["bob"].drainEvents() {
		if strings.Contains(string(f.Payload), protocol.EvMomentNew) {
			t.Fatalf("a private moment was pushed: %s", f.Payload)
		}
	}
	m := s.post("alice", "for friends", "")
	var pm protocol.Moment
	eventData(t, s.c["bob"].waitEvent(protocol.EvMomentNew, 2*time.Second), &pm)
	if pm.ID != m.ID {
		t.Fatalf("new: %+v", pm)
	}

	// dave's comment reaches alice but not bob, who is not dave's friend.
	s.c["bob"].drainEvents()
	s.c["dave"].callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "dave"}, nil)
	eventData(t, s.c["bob"].waitEvent(protocol.EvMomentUpdated, 2*time.Second), &pm)
	if len(pm.Comments) != 0 {
		t.Fatalf("bob was sent dave's comment: %+v", pm.Comments)
	}
}

func TestNotificationsAndUnread(t *testing.T) {
	s := newSocial(t, "alice", "bob", "carol")
	s.befriend("alice", "bob")
	s.befriend("alice", "carol")
	s.befriend("bob", "carol")
	m := s.post("alice", "hello world", "")

	// Liking your own moment says nothing.
	s.c["alice"].callOK(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: m.ID, Like: true}, nil)
	s.c["alice"].drainEvents()
	s.c["bob"].callOK(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: m.ID, Like: true}, nil)
	var n protocol.Notification
	eventData(t, s.c["alice"].waitEvent(protocol.EvMomentNotify, 2*time.Second), &n)
	if n.Kind != protocol.NotifyLike || n.Actor.Username != "bob" || n.Excerpt != "hello world" {
		t.Fatalf("like notification: %+v", n)
	}

	var got protocol.Moment
	s.c["bob"].callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "nice"}, &got)
	s.c["carol"].callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "agreed", ReplyTo: got.Comments[0].ID}, nil)

	var list protocol.NotificationsRes
	s.c["alice"].callOK(protocol.OpMomentNotifications, nil, &list)
	var kinds []string
	for _, x := range list.Notifications {
		kinds = append(kinds, x.Kind+":"+x.Actor.Username)
	}
	if strings.Join(kinds, ",") != "comment:carol,comment:bob,like:bob" {
		t.Fatalf("alice notifications: %v", kinds)
	}
	s.c["bob"].callOK(protocol.OpMomentNotifications, nil, &list)
	if len(list.Notifications) != 1 || list.Notifications[0].Kind != protocol.NotifyReply || list.Notifications[0].Body != "agreed" {
		t.Fatalf("bob notifications: %+v", list.Notifications)
	}

	var u protocol.UnreadRes
	s.c["alice"].callOK(protocol.OpMomentUnread, nil, &u)
	if u.Notifications != 3 {
		t.Fatalf("unread: %+v", u)
	}
	s.c["alice"].callOK(protocol.OpMomentNotifRead, nil, nil)
	s.c["alice"].callOK(protocol.OpMomentUnread, nil, &u)
	if u.Notifications != 0 {
		t.Fatalf("after read: %+v", u)
	}

	// New moments since the last look.
	s.timeline("carol")
	time.Sleep(5 * time.Millisecond)
	s.post("bob", "one", "")
	s.post("alice", "two", "")
	s.post("alice", "hidden", protocol.VisibilityPrivate)
	s.c["carol"].callOK(protocol.OpMomentUnread, nil, &u)
	if u.Moments != 2 {
		t.Fatalf("new moments for carol: %+v", u)
	}
	s.timeline("carol")
	s.c["carol"].callOK(protocol.OpMomentUnread, nil, &u)
	if u.Moments != 0 {
		t.Fatalf("after reading: %+v", u)
	}

	// Bob was able to read Carol's reply while all three were friends. Losing
	// access to the original moment removes that reply from his inbox and badge.
	s.c["bob"].callOK(protocol.OpFriendRemove, protocol.FriendUserReq{Username: "alice"}, nil)
	s.c["bob"].callOK(protocol.OpMomentNotifications, nil, &list)
	s.c["bob"].callOK(protocol.OpMomentUnread, nil, &u)
	if len(list.Notifications) != 0 || u.Notifications != 0 {
		t.Fatalf("hidden moment still has notifications: %+v, %+v", list, u)
	}
}

func TestUnreadCountsRequests(t *testing.T) {
	s := newSocial(t, "alice", "bob", "carol")
	s.c["bob"].callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "alice"}, nil)
	s.c["carol"].callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "alice"}, nil)
	var u protocol.UnreadRes
	s.c["alice"].callOK(protocol.OpMomentUnread, nil, &u)
	if u.Requests != 2 {
		t.Fatalf("requests: %+v", u)
	}
}

// Muted: no posts, comments or profile edits; likes and deletes still work
// (spec account-moderation / 禁言).
func TestMutedSocial(t *testing.T) {
	s := newSocial(t, "alice", "dave")
	s.befriend("alice", "dave")
	m := s.post("alice", "hello", "")
	own := s.post("dave", "before the mute", "")
	if _, err := s.srv.Sanction(Actor{ID: "admin", Name: "admin"}, s.id["dave"], storage.SanctionMute, "spam", time.Hour); err != nil {
		t.Fatal(err)
	}
	d := s.c["dave"]
	d.callErr(protocol.OpMomentPost, protocol.MomentPostReq{Body: "x"}, protocol.CodeMuted)
	d.callErr(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "x"}, protocol.CodeMuted)
	d.callErr(protocol.OpProfileUpdate, protocol.ProfileUpdateReq{Nickname: strp("new")}, protocol.CodeMuted)
	d.callOK(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: m.ID, Like: true}, nil)
	d.callOK(protocol.OpMomentDelete, protocol.MomentRef{MomentID: own.ID}, nil)
	var p protocol.Profile
	d.callOK(protocol.OpProfileGet, protocol.ProfileGetReq{Username: "dave"}, &p)
	if p.User.Nickname != "" {
		t.Fatalf("muted edit applied: %+v", p.User)
	}
}

func TestMomentRateLimits(t *testing.T) {
	hs, _ := testServerWith(t, func(c *Config) { c.MomentsPerHour = 1; c.InteractionsPerMinute = 2 }, io.Discard)
	a := dial(t, hs)
	a.register("alice", pw)
	var m protocol.Moment
	a.callOK(protocol.OpMomentPost, protocol.MomentPostReq{Body: "one"}, &m)
	a.callErr(protocol.OpMomentPost, protocol.MomentPostReq{Body: "two"}, protocol.CodeRateLimited)
	a.callOK(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: m.ID, Like: true}, nil)
	a.callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "c"}, nil)
	a.callErr(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: "c"}, protocol.CodeRateLimited)
}

func TestSocialRequiresAuth(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)
	c.hello()
	for _, op := range []string{protocol.OpFriendRequest, protocol.OpFriendList, protocol.OpProfileGet, protocol.OpMomentPost, protocol.OpMomentTimeline, protocol.OpMomentUnread} {
		c.callErr(op, struct{}{}, protocol.CodeUnauthorized)
	}
}

func TestMomentCommentPreviewAndOlderPages(t *testing.T) {
	s := newSocial(t, "alice", "bob", "carol")
	s.befriend("alice", "bob")
	s.befriend("alice", "carol")
	post := s.post("alice", "a busy moment", "")
	for i := 0; i < 40; i++ {
		err := s.srv.st.AddComment(storage.Comment{
			ID: fmt.Sprintf("visible-%02d", i), MomentID: post.ID, AuthorID: s.id["bob"], CreatedAt: 1, Body: "visible",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.srv.st.AddComment(storage.Comment{
		ID: "hidden", MomentID: post.ID, AuthorID: s.id["carol"], CreatedAt: 2, Body: "hidden",
	}); err != nil {
		t.Fatal(err)
	}
	s.srv.broadcastMoment(protocol.EvMomentUpdated, storage.Moment{ID: post.ID, AuthorID: s.id["alice"], Visibility: storage.VisibilityFriends})
	var pushed protocol.Moment
	eventData(t, s.c["bob"].waitEvent(protocol.EvMomentUpdated, 2*time.Second), &pushed)
	if len(pushed.Comments) != momentCommentsPreview || pushed.CommentsBefore == nil {
		t.Fatalf("unexpected realtime preview size: %d, cursor %+v", len(pushed.Comments), pushed.CommentsBefore)
	}

	seen := map[string]bool{}
	record := func(cs []protocol.Comment) {
		t.Helper()
		for _, c := range cs {
			if c.Author.ID != s.id["bob"] || seen[c.ID] {
				t.Fatalf("hidden or repeated comment: %+v", c)
			}
			seen[c.ID] = true
		}
	}
	record(pushed.Comments)
	for cursor := pushed.CommentsBefore; cursor != nil; {
		var response protocol.MomentCommentsRes
		s.c["bob"].callOK(protocol.OpMomentComments, protocol.MomentCommentsReq{MomentID: post.ID, Before: cursor}, &response)
		if len(response.Comments) == 0 || len(response.Comments) > momentCommentsPage {
			t.Fatalf("older comment page size %d", len(response.Comments))
		}
		record(response.Comments)
		cursor = response.Next
	}
	if len(seen) != 40 {
		t.Fatalf("loaded %d of 40 visible comments", len(seen))
	}
}
