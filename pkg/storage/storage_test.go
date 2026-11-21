package storage

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) (Storage, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, dir
}

func TestCreateUserRejectsDuplicateUsernameCaseInsensitively(t *testing.T) {
	st, _ := newTestStore(t)

	if err := st.CreateUser(User{ID: "u1", Username: "alice", UsernameKey: "alice"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := st.CreateUser(User{ID: "u2", Username: "Alice", UsernameKey: "alice"})
	if err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestUserLookupIsCaseInsensitive(t *testing.T) {
	st, _ := newTestStore(t)
	if err := st.CreateUser(User{ID: "u1", Username: "Alice", UsernameKey: "alice"}); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByUsernameKey("ALICE")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if u.ID != "u1" {
		t.Fatalf("want u1, got %s", u.ID)
	}
}

// Two concurrent sends must never share a sequence number, and no seq may be
// skipped (spec message-delivery / 消息顺序保证).
func TestAppendMessageAssignsGapfreeUniqueSeq(t *testing.T) {
	st, _ := newTestStore(t)
	if err := st.CreateConversation(Conversation{ID: "c1", Kind: "channel", Name: "general"}); err != nil {
		t.Fatal(err)
	}

	const n = 50
	var wg sync.WaitGroup
	seqs := make([]int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m, err := st.AppendMessage(Message{
				ID:             newID(),
				ConversationID: "c1",
				AuthorID:       "u1",
				Body:           "hi",
			})
			if err != nil {
				t.Errorf("append: %v", err)
				return
			}
			seqs[i] = m.Seq
		}(i)
	}
	wg.Wait()

	seen := map[int64]bool{}
	for _, s := range seqs {
		if seen[s] {
			t.Fatalf("duplicate seq %d", s)
		}
		seen[s] = true
	}
	for i := int64(1); i <= n; i++ {
		if !seen[i] {
			t.Fatalf("missing seq %d", i)
		}
	}
}

// A resend with the same client message id must not create a second row
// (spec message-delivery / 消息标识与幂等).
func TestAppendMessageIsIdempotentByClientMsgID(t *testing.T) {
	st, _ := newTestStore(t)
	first, err := st.AppendMessage(Message{
		ID: "m1", ConversationID: "c1", AuthorID: "u1", Body: "hi", ClientMsgID: "k1",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.AppendMessage(Message{
		ID: "m2", ConversationID: "c1", AuthorID: "u1", Body: "hi", ClientMsgID: "k1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Seq != first.Seq {
		t.Fatalf("resend duplicated: %+v vs %+v", first, second)
	}
	msgs, _, err := st.MessagesBefore("c1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
}

// Paging must walk backwards without repeating or skipping rows
// (spec message-delivery / 历史补发与分页).
func TestMessagesBeforePagesBackwards(t *testing.T) {
	st, _ := newTestStore(t)
	for i := 0; i < 25; i++ {
		if _, err := st.AppendMessage(Message{
			ID: newID(), ConversationID: "c1", AuthorID: "u1", Body: "m",
		}); err != nil {
			t.Fatal(err)
		}
	}

	var collected []int64
	before := int64(0)
	for pages := 0; pages < 10; pages++ {
		msgs, hasMore, err := st.MessagesBefore("c1", before, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) == 0 {
			break
		}
		for _, m := range msgs {
			collected = append(collected, m.Seq)
		}
		before = msgs[0].Seq
		if !hasMore {
			break
		}
	}

	if len(collected) != 25 {
		t.Fatalf("want 25 messages across pages, got %d", len(collected))
	}
	seen := map[int64]bool{}
	for _, s := range collected {
		if seen[s] {
			t.Fatalf("duplicate seq %d in paging", s)
		}
		seen[s] = true
	}
}

// Data survives a close/reopen cycle (spec server-runtime / 重启后数据完整).
func TestStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(User{ID: "u1", Username: "alice", UsernameKey: "alice", PasswordHash: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(Message{ID: "m1", ConversationID: "c1", AuthorID: "u1", Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()

	if _, err := again.UserByUsernameKey("alice"); err != nil {
		t.Fatalf("user lost: %v", err)
	}
	msgs, _, err := again.MessagesBefore("c1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Body != "hello" {
		t.Fatalf("messages lost: %+v", msgs)
	}
}

// The DM conversation ID must be identical regardless of argument order, so two
// peers converge on one conversation (spec chat-rooms / 双方得到相同会话标识).
func TestDMBetweenIsOrderIndependent(t *testing.T) {
	st, _ := newTestStore(t)
	a, err := st.DMBetween("u_a", "u_b")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.DMBetween("u_b", "u_a")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("dm ids differ: %s vs %s", a.ID, b.ID)
	}
}

func TestDeleteConversationRemovesMessages(t *testing.T) {
	st, _ := newTestStore(t)
	if err := st.CreateConversation(Conversation{ID: "c1", Kind: "channel", Name: "general"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(Message{ID: "m1", ConversationID: "c1", AuthorID: "u1", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteConversation("c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConversationByID("c1"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	msgs, _, err := st.MessagesBefore("c1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages survived deletion: %d", len(msgs))
	}
}

// Heavier contention than the test above, across two conversations: each gets
// its own gap-free sequence.
func TestAppendMessageSeqUnderContention(t *testing.T) {
	st, _ := newTestStore(t)
	const n = 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	seqs := map[string]map[int64]bool{"c1": {}, "c2": {}}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conv := []string{"c1", "c2"}[i%2]
			m, err := st.AppendMessage(Message{ID: newID(), ConversationID: conv, AuthorID: "u1", Body: "hi"})
			if err != nil {
				t.Errorf("append: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seqs[conv][m.Seq] {
				t.Errorf("%s: duplicate seq %d", conv, m.Seq)
			}
			seqs[conv][m.Seq] = true
		}(i)
	}
	wg.Wait()
	for conv, got := range seqs {
		for i := int64(1); i <= n/2; i++ {
			if !got[i] {
				t.Fatalf("%s: missing seq %d", conv, i)
			}
		}
	}
}

// A write transaction that never commits leaves nothing behind, and the store
// reopens with the committed rows intact (spec server-runtime / 原子写入).
func TestUncommittedWriteLeavesNoTrace(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(Message{ID: "m1", ConversationID: "c1", AuthorID: "u1", Body: "kept"}); err != nil {
		t.Fatal(err)
	}
	tx, err := sqlStoreOf(t, st).db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO messages (` + msgCols + `) VALUES ('m2', 'c1', 2, 'u1', 'torn', '', 1, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	st.Close()

	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	msgs, _, _ := again.MessagesBefore("c1", 0, 10)
	if len(msgs) != 1 || msgs[0].Body != "kept" {
		t.Fatalf("after reopen: %+v", msgs)
	}
	next, err := again.AppendMessage(Message{ID: "m3", ConversationID: "c1", AuthorID: "u1", Body: "next"})
	if err != nil || next.Seq != 2 {
		t.Fatalf("seq after rollback: %+v %v", next, err)
	}
}

// MessageByID finds a row in any conversation; an unknown ID is ErrNotFound.
func TestMessageByID(t *testing.T) {
	st, _ := newTestStore(t)
	for i := 0; i < 3; i++ {
		st.AppendMessage(Message{ID: fmt.Sprint("x", i), ConversationID: fmt.Sprint("c", i), AuthorID: "u", Body: "b"})
	}
	m, err := st.MessageByID("x2")
	if err != nil || m.ConversationID != "c2" {
		t.Fatalf("lookup: %+v %v", m, err)
	}
	if _, err := st.MessageByID("nope"); err != ErrNotFound {
		t.Fatalf("unknown id: %v", err)
	}
}

// Database errors reach callers only as the three sentinels.
func TestMapErr(t *testing.T) {
	st, _ := newTestStore(t)
	if err := st.CreateUser(User{ID: "u1", Username: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(User{ID: "u1", Username: "b"}); err != ErrConflict {
		t.Fatalf("primary key clash: %v", err)
	}
	if _, err := st.UserByID("missing"); err != ErrNotFound {
		t.Fatalf("missing row: %v", err)
	}
	st.Close()
	err := st.CreateUser(User{ID: "u2", Username: "c"})
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "linkchat.db") {
		t.Fatalf("closed store: %v", err)
	}
}
