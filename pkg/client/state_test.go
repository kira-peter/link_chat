package client

import (
	"testing"

	"link-chat/pkg/protocol"
)

func msg(id string, seq int64, author, body string) protocol.Message {
	return protocol.Message{
		ID: id, ConversationID: "c1", Seq: seq,
		AuthorID: author, AuthorName: author, Body: body, CreatedAt: int64(seq) * 1000,
	}
}

// Messages arriving out of order must still be stored in seq order.
func TestInsertMessageKeepsOrder(t *testing.T) {
	s := NewAppState()
	s.UserID = "me"
	s.InsertMessage(msg("m2", 2, "u1", "second"), false)
	s.InsertMessage(msg("m1", 1, "u1", "first"), false)
	s.InsertMessage(msg("m3", 3, "u1", "third"), false)

	cv := s.Conversations["c1"]
	if len(cv.Messages) != 3 {
		t.Fatalf("want 3, got %d", len(cv.Messages))
	}
	for i, want := range []string{"first", "second", "third"} {
		if cv.Messages[i].Body != want {
			t.Fatalf("index %d: want %q, got %q", i, want, cv.Messages[i].Body)
		}
	}
}

// The same message delivered twice (event plus ack) must not duplicate.
func TestInsertMessageIsIdempotent(t *testing.T) {
	s := NewAppState()
	m := msg("m1", 1, "u1", "hi")
	s.InsertMessage(m, true)
	s.InsertMessage(m, true)
	if len(s.Conversations["c1"].Messages) != 1 {
		t.Fatalf("duplicated: %d", len(s.Conversations["c1"].Messages))
	}
}

// An optimistic local copy is replaced by the server's version, not appended.
func TestInsertMessageReplacesOptimisticCopy(t *testing.T) {
	s := NewAppState()
	s.InsertMessage(protocol.Message{
		ID: "local-1", ClientMsgID: "local-1", ConversationID: "c1",
		Seq: 1, AuthorID: "me", Body: "hi",
	}, true)
	s.InsertMessage(protocol.Message{
		ID: "server-1", ClientMsgID: "local-1", ConversationID: "c1",
		Seq: 1, AuthorID: "me", Body: "hi",
	}, true)

	cv := s.Conversations["c1"]
	if len(cv.Messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(cv.Messages))
	}
	if cv.Messages[0].ID != "server-1" {
		t.Fatalf("local copy not replaced: %s", cv.Messages[0].ID)
	}
}

// Only other people's messages raise the unread badge, and only when the
// conversation is not open.
func TestUnreadCountRules(t *testing.T) {
	s := NewAppState()
	s.UserID = "me"

	s.InsertMessage(msg("m1", 1, "other", "hello"), false)
	if got := s.Conversations["c1"].Unread; got != 1 {
		t.Fatalf("want 1 unread, got %d", got)
	}

	// My own message never counts as unread.
	s.InsertMessage(msg("m2", 2, "me", "mine"), false)
	if got := s.Conversations["c1"].Unread; got != 1 {
		t.Fatalf("own message counted: %d", got)
	}

	// The active conversation suppresses unread.
	s.InsertMessage(msg("m3", 3, "other", "again"), true)
	if got := s.Conversations["c1"].Unread; got != 1 {
		t.Fatalf("active conversation counted unread: %d", got)
	}

	s.MarkRead("c1")
	if got := s.Conversations["c1"].Unread; got != 0 {
		t.Fatalf("MarkRead did not clear: %d", got)
	}
}

// The cache must stay bounded so a long-lived session does not grow forever.
func TestCacheIsBounded(t *testing.T) {
	s := NewAppState()
	for i := int64(1); i <= MessageCacheLimit+50; i++ {
		s.InsertMessage(msg("m"+itoa64(i), i, "u1", "x"), true)
	}
	cv := s.Conversations["c1"]
	if len(cv.Messages) > MessageCacheLimit {
		t.Fatalf("cache grew past limit: %d", len(cv.Messages))
	}
	// The newest message must survive the trim.
	last := cv.Messages[len(cv.Messages)-1]
	if last.Seq != MessageCacheLimit+50 {
		t.Fatalf("newest message dropped: seq %d", last.Seq)
	}
	if !cv.HasMore {
		t.Fatal("trimming should mark that older history remains")
	}
}

// Pages loaded while paging up must merge without duplicates.
func TestSetHistoryOlderMergesWithoutDuplicates(t *testing.T) {
	s := NewAppState()
	s.SetHistory("c1", []protocol.Message{
		msg("m3", 3, "u1", "c"), msg("m4", 4, "u1", "d"),
	}, true, false)

	s.SetHistory("c1", []protocol.Message{
		msg("m1", 1, "u1", "a"), msg("m2", 2, "u1", "b"), msg("m3", 3, "u1", "c"),
	}, false, true)

	cv := s.Conversations["c1"]
	if len(cv.Messages) != 4 {
		t.Fatalf("want 4 unique messages, got %d", len(cv.Messages))
	}
	for i, want := range []string{"a", "b", "c", "d"} {
		if cv.Messages[i].Body != want {
			t.Fatalf("index %d: want %q got %q", i, want, cv.Messages[i].Body)
		}
	}
	if cv.HasMore {
		t.Fatal("hasMore should be false at the start")
	}
}

// Switching conversations preserves each draft.
func TestDraftsArePerConversation(t *testing.T) {
	s := NewAppState()
	s.EnsureConversation("a")
	s.EnsureConversation("b")

	s.ActiveID = "a"
	s.InsertRunes([]rune("draft for a"))
	s.StashDraft()

	s.ActiveID = "b"
	s.LoadDraft("b")
	if s.InputText() != "" {
		t.Fatalf("new conversation picked up another draft: %q", s.InputText())
	}
	s.InsertRunes([]rune("draft for b"))
	s.StashDraft()

	s.ActiveID = "a"
	s.LoadDraft("a")
	if s.InputText() != "draft for a" {
		t.Fatalf("draft a lost: %q", s.InputText())
	}
}

// Word-wise editing behaviour expected of a real terminal editor.
func TestWordEditing(t *testing.T) {
	s := NewAppState()
	s.InsertRunes([]rune("hello brave world"))

	s.CursorWordLeft()
	if s.InputCursor != len([]rune("hello brave ")) {
		t.Fatalf("word left landed at %d", s.InputCursor)
	}
	s.DeleteWordBefore()
	if s.InputText() != "hello world" {
		t.Fatalf("delete word gave %q", s.InputText())
	}

	s.CursorHome()
	s.InsertRunes([]rune("oh "))
	if s.InputText() != "oh hello world" {
		t.Fatalf("insert at home gave %q", s.InputText())
	}
}

func TestBackspaceAtStartIsSafe(t *testing.T) {
	s := NewAppState()
	s.CursorHome()
	s.Backspace()
	s.DeleteForward()
	if s.InputText() != "" {
		t.Fatal("editing empty input produced text")
	}
}

// Filtering narrows by name and keeps the ordering rule.
func TestVisibleConversationsFilters(t *testing.T) {
	s := NewAppState()
	a := s.EnsureConversation("a")
	a.Name = "general"
	b := s.EnsureConversation("b")
	b.Name = "random"

	if n := len(s.VisibleConversations()); n != 2 {
		t.Fatalf("want 2, got %d", n)
	}
	s.Filter = []rune("gen")
	got := s.VisibleConversations()
	if len(got) != 1 || got[0].Name != "general" {
		t.Fatalf("filter failed: %v", got)
	}
}

// Removing a conversation the server deleted must clear the active selection.
func TestRemoveConversationClearsActive(t *testing.T) {
	s := NewAppState()
	s.EnsureConversation("a")
	s.EnsureConversation("b")
	s.ActiveID = "a"

	s.RemoveConversation("a")
	if s.ActiveID != "" {
		t.Fatalf("active not cleared: %q", s.ActiveID)
	}
	if len(s.Order) != 1 {
		t.Fatalf("order not cleaned: %v", s.Order)
	}
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Direction is a property of position: the list is leftmost and the composer is
// rightmost, so the leftmost region is its own left neighbour and vice versa.
// A symmetric "flip" cannot express that, which is why ← and → used to be wrong.
func TestFocusDirectionsFollowPosition(t *testing.T) {
	cases := []struct {
		from        Focus
		left, right Focus
	}{
		{FocusList, FocusList, FocusInput},
		{FocusInput, FocusList, FocusInput},
	}
	for _, c := range cases {
		if got := c.from.Left(); got != c.left {
			t.Errorf("%v.Left() = %v, want %v", c.from, got, c.left)
		}
		if got := c.from.Right(); got != c.right {
			t.Errorf("%v.Right() = %v, want %v", c.from, got, c.right)
		}
	}
}

func TestFocusPositionPredicates(t *testing.T) {
	if !FocusList.IsLeft() || FocusList.IsRight() {
		t.Fatal("the list is the left region")
	}
	if !FocusInput.IsRight() || FocusInput.IsLeft() {
		t.Fatal("the composer is the right region")
	}
}

// Cycle is "next", not a direction, so it wraps; it must be its own inverse with
// two regions.
func TestFocusCycleWraps(t *testing.T) {
	if FocusInput.Cycle() != FocusList || FocusList.Cycle() != FocusInput {
		t.Fatal("Cycle does not alternate")
	}
	if FocusList.CycleBack() != FocusInput {
		t.Fatal("CycleBack does not alternate")
	}
}
