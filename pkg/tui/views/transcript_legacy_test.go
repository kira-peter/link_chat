package views

import (
	"strings"
	"testing"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/widget"
)

// These three tests used to check the old single-column layout (a name column, then
// a body column). They keep their intent — grouping, alignment, the deleted
// placeholder — but assert it against bubbles, and add what the new layout newly
// promises. The old assertions were not just left in place because they still
// passed: a test that passes for the wrong reason is worse than none.

func transcriptScreen(t *testing.T, w, h int, cv *client.Conversation) []string {
	t.Helper()
	s := newScreen(t, w, h)
	Transcript{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: w, H: h}, TranscriptModel{
		Conversation: cv, UserID: "me", Now: 10000,
	}, i18n.ZH)
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		rows[y] = rowText(s, y, w)
	}
	return rows
}

// Consecutive messages from one author collapse to a single header and sit as
// separate, adjacent bubbles (spec tui-chat-client / 连续消息合并).
func TestTranscriptMergesConsecutiveMessages(t *testing.T) {
	cv := &client.Conversation{}
	cv.ID, cv.Name = "c1", "general"
	cv.Messages = []protocol.Message{
		{ID: "m1", Seq: 1, AuthorID: "u1", AuthorName: "alice", Body: "first", CreatedAt: 1000},
		{ID: "m2", Seq: 2, AuthorID: "u1", AuthorName: "alice", Body: "second", CreatedAt: 2000},
		{ID: "m3", Seq: 3, AuthorID: "u1", AuthorName: "alice", Body: "third", CreatedAt: 3000},
	}
	whole := strings.Join(transcriptScreen(t, 70, 20, cv), "\n")

	if n := strings.Count(whole, "alice"); n != 1 {
		t.Fatalf("author should appear once, found %d times:\n%s", n, whole)
	}
	for _, body := range []string{"first", "second", "third"} {
		if !strings.Contains(whole, body) {
			t.Fatalf("body %q missing:\n%s", body, whole)
		}
	}
	// Three separate bubbles: three top borders and three bottom borders.
	if tops := strings.Count(whole, "┌"); tops != 3 {
		t.Fatalf("want 3 bubbles (3 top borders), found %d:\n%s", tops, whole)
	}
	// All on the same (left) side: every bubble's top-left corner is in column 0.
	for _, row := range strings.Split(whole, "\n") {
		if strings.Contains(row, "┌") && !strings.HasPrefix(row, "┌") {
			t.Fatalf("a bubble of the group is not on the left edge: %q", row)
		}
	}
}

// Your own messages never carry your name, and a message from someone else does.
func TestOnlyTheOtherSideShowsAName(t *testing.T) {
	cv := &client.Conversation{}
	cv.ID, cv.Name = "c1", "general"
	cv.Messages = []protocol.Message{
		{ID: "m1", Seq: 1, AuthorID: "u1", AuthorName: "alice", Body: "from alice", CreatedAt: 1000},
		{ID: "m2", Seq: 2, AuthorID: "me", AuthorName: "myname", Body: "from me", CreatedAt: 2000},
	}
	whole := strings.Join(transcriptScreen(t, 70, 20, cv), "\n")

	if !strings.Contains(whole, "alice") {
		t.Fatalf("the other side's name is missing:\n%s", whole)
	}
	if strings.Contains(whole, "myname") {
		t.Fatalf("my own name is shown above my own message:\n%s", whole)
	}
}

// Wrapped continuation lines stay aligned with the first line's text, now inside
// the bubble: every line of the message starts at the same column, and none of
// them touches the border (spec tui-chat-client / 长消息折行).
func TestTranscriptAlignsContinuationLines(t *testing.T) {
	cv := &client.Conversation{}
	cv.ID, cv.Name = "c1", "general"
	cv.Messages = []protocol.Message{
		{ID: "m1", Seq: 1, AuthorID: "u1", AuthorName: "alice", Body: strings.Repeat("word ", 40), CreatedAt: 1000},
	}
	rows := transcriptScreen(t, 60, 24, cv)

	bodyCol, continuations := -1, 0
	for y, row := range rows {
		idx := strings.Index(row, "word")
		if idx < 0 {
			continue
		}
		if bodyCol == -1 {
			bodyCol = idx
			continue
		}
		continuations++
		if idx != bodyCol {
			t.Fatalf("continuation on row %d starts at %d, want %d", y, idx, bodyCol)
		}
	}
	if continuations == 0 {
		t.Fatal("message did not wrap")
	}
	// The text sits inside the border: a border glyph immediately left of the text
	// column, with a padding cell between them.
	for y, row := range rows {
		if strings.Contains(row, "word") && !strings.HasPrefix(row, "│ ") {
			t.Fatalf("row %d: text is not inside the bubble's border: %q", y, row)
		}
	}
}

// Deleted messages keep a placeholder, never show their old body, and are not
// boxed: a box around "message deleted" would make it look like a message.
func TestTranscriptShowsDeletedPlaceholder(t *testing.T) {
	cv := &client.Conversation{}
	cv.ID, cv.Name = "c1", "general"
	cv.Messages = []protocol.Message{
		{ID: "m1", Seq: 1, AuthorID: "u1", AuthorName: "alice", Body: "SECRET", Deleted: true, CreatedAt: 1000},
	}
	whole := strings.Join(transcriptScreen(t, 60, 16, cv), "\n")

	if !strings.Contains(whole, i18n.MessageDeleted.Get(i18n.ZH)) {
		t.Fatalf("deleted placeholder missing:\n%s", whole)
	}
	if strings.Contains(whole, "SECRET") {
		t.Fatalf("a deleted message leaked its body:\n%s", whole)
	}
	if strings.ContainsAny(whole, "┌┐└┘") {
		t.Fatalf("the placeholder is boxed:\n%s", whole)
	}
}
