package storage

import (
	"errors"
	"testing"
)

func TestDeleteUserRemovesContentAndMayAnonymizeComments(t *testing.T) {
	for _, keep := range []bool{false, true} {
		name := "delete comments"
		if keep {
			name = "anonymize comments"
		}
		t.Run(name, func(t *testing.T) {
			st, _ := newTestStore(t)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := st.EnsureSimulationUsers([]User{{ID: "victim", Username: "test_1"}}, "run")
			must(err)
			must(st.CreateUser(User{ID: "peer", Username: "peer"}))
			must(st.AddFriendship("victim", "peer", 1))
			must(st.SaveToken(Token{Hash: "victim-token", UserID: "victim"}))
			must(st.CreateConversation(Conversation{ID: "shared", Kind: "channel", Name: "shared", CreatedBy: "victim"}))
			must(st.CreateConversation(Conversation{ID: "empty", Kind: "channel", Name: "empty", CreatedBy: "victim"}))
			must(st.AddMember(Member{ConversationID: "shared", UserID: "peer", JoinedAt: 2}))
			dm, err := st.DMBetween("victim", "peer")
			must(err)
			_, err = st.AppendMessage(Message{ID: "victim-message", ConversationID: "shared", AuthorID: "victim", Body: "delete me"})
			must(err)
			_, err = st.AppendMessage(Message{ID: "peer-message", ConversationID: "shared", AuthorID: "peer", Body: "keep me"})
			must(err)
			must(st.AddMoment(Moment{ID: "own", AuthorID: "victim", Visibility: VisibilityFriends}))
			must(st.AddMoment(Moment{ID: "other", AuthorID: "peer", Visibility: VisibilityFriends}))
			must(st.AddComment(Comment{ID: "mine", MomentID: "other", AuthorID: "victim", Body: "on peer's moment"}))
			must(st.AddComment(Comment{ID: "reply", MomentID: "other", AuthorID: "peer", ReplyTo: "mine", ReplyToUser: "victim"}))
			must(st.AddComment(Comment{ID: "other-comment", MomentID: "own", AuthorID: "peer"}))
			must(st.SetLike(Like{MomentID: "other", UserID: "victim"}, true))
			_, err = st.AddNotification(Notification{UserID: "peer", ActorID: "victim", MomentID: "other"})
			must(err)

			deleted, err := st.DeleteUser("victim", keep)
			must(err)
			if len(deleted.Conversations) != 2 {
				t.Fatalf("deleted conversations: %+v", deleted.Conversations)
			}
			if _, err := st.UserByID("victim"); !errors.Is(err, ErrNotFound) {
				t.Fatal("victim still exists", err)
			}
			if _, err := st.TokenByHash("victim-token"); !errors.Is(err, ErrNotFound) {
				t.Fatal("session still exists", err)
			}
			if _, err := st.ConversationByID(dm.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("DM still exists", err)
			}
			channel, err := st.ConversationByID("shared")
			must(err)
			if channel.CreatedBy != "peer" || st.IsMember("shared", "victim") {
				t.Fatalf("channel ownership or membership: %+v", channel)
			}
			if _, err := st.MessageByID("victim-message"); !errors.Is(err, ErrNotFound) {
				t.Fatal("victim's channel message remains", err)
			}
			_, err = st.MessageByID("peer-message")
			must(err)
			if _, err := st.MomentByID("own"); !errors.Is(err, ErrNotFound) {
				t.Fatal("victim's moment remains", err)
			}
			comment, err := st.CommentByID("mine")
			if keep && (err != nil || comment.AuthorID != "" || comment.Body == "") {
				t.Fatalf("not anonymized: %+v %v", comment, err)
			}
			if !keep && !errors.Is(err, ErrNotFound) {
				t.Fatalf("comment still exists: %+v %v", comment, err)
			}
			reply, err := st.CommentByID("reply")
			must(err)
			if reply.ReplyTo != "" || reply.ReplyToUser != "" {
				t.Fatalf("reply still points at deleted user: %+v", reply)
			}
			former, err := st.FriendIDs("peer")
			must(err)
			likes, err := st.LikesOf([]string{"other"})
			must(err)
			notes, err := st.Notifications("peer", 10)
			must(err)
			accounts, err := st.SimulationUsers()
			must(err)
			if len(former)+len(likes["other"])+len(notes)+len(accounts) != 0 {
				t.Fatal("relationship, like, notification, or simulation account remains")
			}
		})
	}
}

func TestSimulationBatchSkipsConflictsAndReusesSimulationUser(t *testing.T) {
	st, _ := newTestStore(t)
	if err := st.CreateUser(User{ID: "existing", Username: "test_2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureSimulationUsers([]User{{ID: "original", Username: "TEST_1"}}, "original-batch"); err != nil {
		t.Fatal(err)
	}
	batch, err := st.EnsureSimulationUsers([]User{
		{ID: "one", Username: "test_1"}, {ID: "two", Username: "test_2"}, {ID: "three", Username: "test_3"},
	}, "batch")
	if err != nil || len(batch.Users) != 2 || len(batch.Created) != 1 || batch.Users[0].ID != "original" || batch.Created[0].ID != "three" {
		t.Fatalf("batch after conflicts: %+v, %v", batch, err)
	}
	users, err := st.SimulationUsers()
	if err != nil || len(users) != 2 || users[0].RunID != "original-batch" {
		t.Fatalf("simulation rows after conflict: %+v, %v", users, err)
	}
}
