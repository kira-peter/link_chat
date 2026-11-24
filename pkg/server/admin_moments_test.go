package server

import (
	"net/http"
	"testing"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// The console lists every moment, including private ones; deleting needs
// users.moderate on top of chat.view and is audited with the body
// (spec admin-console / 动态浏览与删除, account-moderation / 审计日志).
func TestAdminMoments(t *testing.T) {
	hs, srv, root, rootID := adminFixture(t)
	alice := dial(t, hs)
	alice.register("alice", pw)
	bob := dial(t, hs)
	bob.register("bob", pw)
	alice.callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob"}, nil)
	bob.callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "alice"}, nil)

	var pub, priv protocol.Moment
	alice.callOK(protocol.OpMomentPost, protocol.MomentPostReq{Body: "违规内容"}, &pub)
	time.Sleep(3 * time.Millisecond)
	alice.callOK(protocol.OpMomentPost, protocol.MomentPostReq{Body: "secret", Visibility: protocol.VisibilityPrivate}, &priv)
	var withComment protocol.Moment
	bob.callOK(protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: pub.ID, Body: "bad comment"}, &withComment)

	st, list := root.get("/admin/api/moments")
	ms := list["moments"].([]any)
	if st != http.StatusOK || len(ms) != 2 {
		t.Fatalf("list: %d %v", st, list)
	}
	first := ms[0].(map[string]any)
	if first["visibility"] != protocol.VisibilityPrivate || first["body"] != "secret" {
		t.Fatalf("newest first, private included: %v", first)
	}
	if c := ms[1].(map[string]any)["comments"].([]any); len(c) != 1 {
		t.Fatalf("comments: %v", c)
	}
	if _, l := root.get("/admin/api/moments?author=bob"); len(l["moments"].([]any)) != 0 {
		t.Fatalf("author filter: %v", l)
	}

	// chat.view alone may look but not delete.
	viewerID := makeUser(t, hs, "viewer")
	srv.st.SetUserPerms(viewerID, []string{PermChatView})
	v := newAdminClient(t, hs)
	v.login("viewer")
	if st, _ := v.get("/admin/api/moments"); st != http.StatusOK {
		t.Fatalf("viewer list: %d", st)
	}
	if st, _ := v.post("/admin/api/moments/"+pub.ID+"/delete", struct{}{}); st != http.StatusForbidden {
		t.Fatalf("viewer delete: %d", st)
	}
	// users.moderate alone may not delete either: it cannot see.
	modID := makeUser(t, hs, "moder")
	srv.st.SetUserPerms(modID, []string{PermUsersModerate})
	m := newAdminClient(t, hs)
	m.login("moder")
	if st, _ := m.post("/admin/api/moments/"+pub.ID+"/delete", struct{}{}); st != http.StatusForbidden {
		t.Fatalf("moderator without chat.view: %d", st)
	}

	if st, _ := root.post("/admin/api/comments/"+withComment.Comments[0].ID+"/delete", struct{}{}); st != http.StatusOK {
		t.Fatalf("delete comment: %d", st)
	}
	bob.drainEvents()
	if st, _ := root.post("/admin/api/moments/"+pub.ID+"/delete", struct{}{}); st != http.StatusOK {
		t.Fatalf("delete moment: %d", st)
	}
	bob.waitEvent(protocol.EvMomentDeleted, 2*time.Second)
	if st, _ := root.post("/admin/api/moments/"+pub.ID+"/delete", struct{}{}); st != http.StatusNotFound {
		t.Fatalf("second delete: %d", st)
	}

	entries, _ := srv.st.ListAudit(storage.AuditFilter{ActorID: rootID, Limit: 50})
	found := map[string]string{}
	for _, e := range entries {
		found[e.Action] = e.Detail["body"] + "/" + e.Detail["username"]
	}
	if found["delete_moment"] != "违规内容/alice" || found["delete_comment"] != "bad comment/bob" {
		t.Fatalf("audit: %v", found)
	}
}
