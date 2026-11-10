package storage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) (Storage, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenJSON(dir)
	if err != nil {
		t.Fatalf("OpenJSON: %v", err)
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
	st, err := OpenJSON(dir)
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

	again, err := OpenJSON(dir)
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

// A torn trailing line (interrupted append) must not make the log unreadable.
func TestTornTrailingLineIsIgnored(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(Message{ID: "m1", ConversationID: "c1", AuthorID: "u1", Body: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	path := messagePath(dir, "c1")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"m2","conversation_id":"c1","s`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	again, err := OpenJSON(dir)
	if err != nil {
		t.Fatalf("reopen with torn line: %v", err)
	}
	defer again.Close()
	msgs, _, err := again.MessagesBefore("c1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want the intact message only, got %d", len(msgs))
	}
}

// The atomic writer must leave either the old or the new file, never a mix.
func TestWriteFileAtomicReplacesWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")

	if err := writeFileAtomic(path, []byte("old-content")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" {
		t.Fatalf("want new, got %q", b)
	}

	// No temp files may be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("stray temp files: %v", entries)
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
