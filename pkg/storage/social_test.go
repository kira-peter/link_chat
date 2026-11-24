package storage

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestFriendshipIsOneRowPerPair(t *testing.T) {
	st, _ := newTestStore(t)
	if err := st.AddFriendship("u2", "u1", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddFriendship("u1", "u2", 2); err != nil {
		t.Fatal(err)
	}
	var n int
	sqlStoreOf(t, st).db.QueryRow(`SELECT count(*) FROM friendships`).Scan(&n)
	if n != 1 {
		t.Fatalf("rows: %d", n)
	}
	for _, p := range [][2]string{{"u1", "u2"}, {"u2", "u1"}} {
		if ok, _ := st.AreFriends(p[0], p[1]); !ok {
			t.Fatalf("not friends as %v", p)
		}
	}
	st.AddFriendship("u1", "u3", 3)
	ids, _ := st.FriendIDs("u1")
	sort.Strings(ids)
	if strings.Join(ids, ",") != "u2,u3" {
		t.Fatalf("friends of u1: %v", ids)
	}
	if err := st.RemoveFriendship("u2", "u1"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.AreFriends("u1", "u2"); ok {
		t.Fatal("still friends")
	}
}

func TestFriendRequestsMergeAndClear(t *testing.T) {
	st, _ := newTestStore(t)
	first, err := st.SaveFriendRequest(FriendRequest{FromID: "u1", ToID: "u2", Note: "hi", CreatedAt: 1})
	if err != nil || first.ID == "" {
		t.Fatalf("%+v %v", first, err)
	}
	again, _ := st.SaveFriendRequest(FriendRequest{FromID: "u1", ToID: "u2", Note: "again", CreatedAt: 2})
	if again.ID != first.ID {
		t.Fatalf("second request got a new id: %s vs %s", again.ID, first.ID)
	}
	in, _ := st.IncomingFriendRequests("u2")
	if len(in) != 1 || in[0].Note != "again" {
		t.Fatalf("incoming: %+v", in)
	}
	if _, err := st.FriendRequestBetween("u2", "u1"); err != ErrNotFound {
		t.Fatalf("reverse request: %v", err)
	}
	// Becoming friends clears the request.
	st.AddFriendship("u1", "u2", 3)
	if in, _ := st.IncomingFriendRequests("u2"); len(in) != 0 {
		t.Fatalf("request survived friendship: %+v", in)
	}
}

func TestBlockEndsFriendshipAndRequests(t *testing.T) {
	st, _ := newTestStore(t)
	st.AddFriendship("u1", "u2", 1)
	st.SaveFriendRequest(FriendRequest{FromID: "u2", ToID: "u1", CreatedAt: 1})
	if err := st.Block("u1", "u2", 2); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.AreFriends("u1", "u2"); ok {
		t.Fatal("still friends after block")
	}
	if in, _ := st.IncomingFriendRequests("u1"); len(in) != 0 {
		t.Fatal("request survived block")
	}
	for _, p := range [][2]string{{"u1", "u2"}, {"u2", "u1"}} {
		if ok, _ := st.IsBlocked(p[0], p[1]); !ok {
			t.Fatalf("IsBlocked%v false", p)
		}
	}
	if ids, _ := st.BlockedIDs("u1"); len(ids) != 1 || ids[0] != "u2" {
		t.Fatalf("blocked: %v", ids)
	}
	if ids, _ := st.BlockedIDs("u2"); len(ids) != 0 {
		t.Fatalf("block is directed: %v", ids)
	}
	st.Unblock("u1", "u2")
	if ok, _ := st.IsBlocked("u1", "u2"); ok {
		t.Fatal("unblock failed")
	}
}

func TestProfiles(t *testing.T) {
	st, _ := newTestStore(t)
	p, err := st.ProfileOf("u1")
	if err != nil || p.UserID != "u1" || p.Nickname != "" {
		t.Fatalf("missing profile: %+v %v", p, err)
	}
	st.SaveProfile(Profile{UserID: "u1", Nickname: "小爱", Bio: "hi", Avatar: "🐱", Color: "rose", UpdatedAt: 5})
	st.SaveProfile(Profile{UserID: "u1", Nickname: "小爱2", Bio: "hi", Avatar: "🐱", Color: "rose", UpdatedAt: 6})
	all, _ := st.ProfilesOf([]string{"u1", "u2"})
	if all["u1"].Nickname != "小爱2" || all["u2"].UserID != "u2" || all["u2"].Nickname != "" {
		t.Fatalf("profiles: %+v", all)
	}
}

func TestMomentsPagingAndVisibility(t *testing.T) {
	st, _ := newTestStore(t)
	// 45 moments by u2 (friends), one private by u2, five by u1 including a
	// private one; several share a timestamp.
	for i := 0; i < 45; i++ {
		st.AddMoment(Moment{ID: fmt.Sprintf("b%02d", i), AuthorID: "u2", Body: "x", Visibility: VisibilityFriends, CreatedAt: int64(100 + i/3)})
	}
	st.AddMoment(Moment{ID: "bpriv", AuthorID: "u2", Body: "x", Visibility: VisibilityPrivate, CreatedAt: 500})
	for i := 0; i < 5; i++ {
		vis := VisibilityFriends
		if i == 0 {
			vis = VisibilityPrivate
		}
		st.AddMoment(Moment{ID: fmt.Sprintf("a%d", i), AuthorID: "u1", Body: "y", Visibility: vis, CreatedAt: int64(110 + i)})
	}

	seen := map[string]bool{}
	cur := MomentCursor{}
	last := int64(1 << 62)
	for pages := 0; ; pages++ {
		p, err := st.MomentsBy([]string{"u1", "u2"}, "u1", cur, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range p.Moments {
			if seen[m.ID] {
				t.Fatalf("repeated %s", m.ID)
			}
			if m.CreatedAt > last {
				t.Fatalf("out of order at %s", m.ID)
			}
			seen[m.ID], last = true, m.CreatedAt
		}
		if p.Next == nil {
			break
		}
		cur = *p.Next
	}
	if len(seen) != 50 || seen["bpriv"] || !seen["a0"] {
		t.Fatalf("u1 timeline: %d moments, bpriv=%v a0=%v", len(seen), seen["bpriv"], seen["a0"])
	}
	// Timestamps are 100+i/3, so after 110 means i >= 33: twelve moments.
	if n, _ := st.CountMomentsSince([]string{"u2"}, 110); n != 12 {
		t.Fatalf("since: %d", n)
	}
	all, _ := st.AllMoments("", MomentCursor{}, 100)
	if len(all.Moments) != 51 {
		t.Fatalf("admin sees %d", len(all.Moments))
	}
}

func TestLikesCommentsNotificationsAndDelete(t *testing.T) {
	st, _ := newTestStore(t)
	st.AddMoment(Moment{ID: "m1", AuthorID: "u1", Body: "x", Visibility: VisibilityFriends, CreatedAt: 1})
	st.SetLike(Like{MomentID: "m1", UserID: "u2", CreatedAt: 2}, true)
	st.SetLike(Like{MomentID: "m1", UserID: "u2", CreatedAt: 3}, true)
	st.SetLike(Like{MomentID: "m1", UserID: "u3", CreatedAt: 3}, true)
	st.SetLike(Like{MomentID: "m1", UserID: "u3"}, false)
	likes, _ := st.LikesOf([]string{"m1"})
	if len(likes["m1"]) != 1 || likes["m1"][0].UserID != "u2" {
		t.Fatalf("likes: %+v", likes)
	}

	st.AddComment(Comment{ID: "c1", MomentID: "m1", AuthorID: "u2", Body: "nice", CreatedAt: 4})
	st.AddComment(Comment{ID: "c2", MomentID: "m1", AuthorID: "u1", ReplyTo: "c1", ReplyToUser: "u2", Body: "thanks", CreatedAt: 5})
	n1, _ := st.AddNotification(Notification{UserID: "u1", Kind: "comment", ActorID: "u2", MomentID: "m1", CommentID: "c1", Body: "nice", CreatedAt: 4})
	st.AddNotification(Notification{UserID: "u2", Kind: "reply", ActorID: "u1", MomentID: "m1", CommentID: "c2", Body: "thanks", CreatedAt: 5})
	if n1.ID == 0 {
		t.Fatal("notification id not assigned")
	}
	if n, _ := st.UnreadNotifications("u1"); n != 1 {
		t.Fatalf("unread u1: %d", n)
	}
	st.MarkNotificationsRead("u1")
	if n, _ := st.UnreadNotifications("u1"); n != 0 {
		t.Fatalf("unread after read: %d", n)
	}
	if list, _ := st.Notifications("u1", 10); len(list) != 1 || !list[0].Read {
		t.Fatalf("notifications: %+v", list)
	}

	if err := st.DeleteComment("c2"); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.Notifications("u2", 10); len(list) != 0 {
		t.Fatalf("reply notification survived its comment: %+v", list)
	}
	if err := st.DeleteComment("c2"); err != ErrNotFound {
		t.Fatalf("double delete: %v", err)
	}

	if err := st.DeleteMoment("m1"); err != nil {
		t.Fatal(err)
	}
	likes, _ = st.LikesOf([]string{"m1"})
	comments, _ := st.CommentsOf([]string{"m1"})
	notes, _ := st.Notifications("u1", 10)
	if len(likes["m1"]) != 0 || len(comments["m1"]) != 0 || len(notes) != 0 {
		t.Fatalf("leftovers: %v %v %v", likes, comments, notes)
	}
	if _, err := st.MomentByID("m1"); err != ErrNotFound {
		t.Fatalf("moment still there: %v", err)
	}
}

func TestMomentsSeenAt(t *testing.T) {
	st, _ := newTestStore(t)
	if at, _ := st.MomentsSeenAt("u1"); at != 0 {
		t.Fatalf("never seen: %d", at)
	}
	st.SetMomentsSeenAt("u1", 10)
	st.SetMomentsSeenAt("u1", 20)
	if at, _ := st.MomentsSeenAt("u1"); at != 20 {
		t.Fatalf("seen: %d", at)
	}
}
