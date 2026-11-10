package client

import (
	"sort"
	"strings"
	"time"

	"link-chat/pkg/protocol"
)

// MessageCacheLimit is how many messages are kept in memory per conversation.
// Older ones are re-fetched on demand, which keeps a long-lived client's memory
// flat (design.md, Risks).
const MessageCacheLimit = 400

// Conversation is a client-side view of a channel or DM, including UI-only state
// (unread count, draft) that the server knows nothing about.
type Conversation struct {
	protocol.Conversation

	Messages []protocol.Message
	Unread   int
	Draft    []rune

	// HasMore is true while earlier history remains unfetched.
	HasMore bool
	// LoadingOlder guards against duplicate page requests.
	LoadingOlder bool
	// LastKnownSeq is the highest seq the client has seen, used as the resume
	// anchor after a reconnect.
	LastKnownSeq int64

	// lastPreview is the server-sent one-line preview, shown in the list until
	// real messages are loaded.
	lastPreview string
}

// Preview is the one-line text shown next to a conversation in the list.
func (c *Conversation) Preview() string {
	if len(c.Messages) > 0 {
		m := c.Messages[len(c.Messages)-1]
		if m.Deleted {
			return ""
		}
		return m.Body
	}
	return c.lastPreview
}

// AppState is everything a view needs to render. All mutation happens through
// the reducer, so "given this event, what does the screen show" is a pure
// function that can be tested without a terminal (design.md decision 8).
type AppState struct {
	// Identity.
	UserID    string
	Username  string
	ServerURL string
	Token     string
	State     ConnState
	LastError string
	RetryIn   time.Duration

	// Conversations, in display order, plus a lookup.
	Order         []string
	Conversations map[string]*Conversation

	// ActiveID is the conversation currently open, or "" for the picker.
	ActiveID string

	// Focus tells the layout which pane owns the keyboard.
	Focus Focus

	// Chrome.
	ShowHelp     bool
	ShowSettings bool
	ShowLogin    bool
	StatusMsg    string
	StatusErr    bool
	// PendingNewMessage is set when a message arrives while the user is reading
	// history, so the UI can offer a jump instead of yanking the viewport
	// (spec tui-chat-client / 新消息不打断阅读).
	PendingNewMessage bool

	// Input.
	Input       []rune
	InputCursor int
	InputErr    string

	// Filter for the conversation list.
	Filter []rune

	// Users for the DM picker / presence display.
	Users map[string]protocol.User

	// Theme and language names, kept here so the settings view has one source.
	ThemeName string
	Lang      string
	MotionOff bool
}

// Focus identifies which region has the keyboard.
//
// The two regions sit side by side — the conversation list on the left, the
// composer on the right — and the type models exactly that. Direction is a
// property of position, so Left and Right are fixed points rather than a toggle:
// asking the leftmost region to move left stays where it is, which a symmetric
// "flip" cannot express. The old Next/Prev flip was the reason ← took you to the
// composer and → took you to the list (design.md decision 1).
type Focus int

const (
	// FocusInput is the default: a chat client is for typing, so the composer
	// owns the keyboard from the first keystroke. Enter sends immediately
	// without a preliminary click.
	FocusInput Focus = iota
	FocusList
)

// Left is where the focus goes when the user presses ←. The list is the
// leftmost region, so it is also its own left neighbour.
func (f Focus) Left() Focus { return FocusList }

// Right is where the focus goes when the user presses →. The composer is the
// rightmost region, so it is also its own right neighbour.
func (f Focus) Right() Focus { return FocusInput }

// Cycle advances to the other region. It is for Tab, which means "next", not a
// direction, and therefore is allowed to wrap.
func (f Focus) Cycle() Focus {
	if f == FocusInput {
		return FocusList
	}
	return FocusInput
}

// CycleBack is Shift+Tab. With two regions it lands in the same place as Cycle,
// but it is named separately so the call sites read as what the user did.
func (f Focus) CycleBack() Focus { return f.Cycle() }

// IsLeft and IsRight describe where a region sits on screen.
func (f Focus) IsLeft() bool  { return f == FocusList }
func (f Focus) IsRight() bool { return f == FocusInput }

// NewAppState returns an empty state with initialised maps.
func NewAppState() *AppState {
	return &AppState{
		Conversations: map[string]*Conversation{},
		Users:         map[string]protocol.User{},
		ThemeName:     "ink",
		Lang:          "zh",
		Focus:         FocusInput,
	}
}

// EnsureConversation returns the conversation, creating a blank one if needed.
func (s *AppState) EnsureConversation(id string) *Conversation {
	if cv, ok := s.Conversations[id]; ok {
		return cv
	}
	cv := &Conversation{Conversation: protocol.Conversation{ID: id}}
	s.Conversations[id] = cv
	s.Order = append(s.Order, id)
	return cv
}

// Active returns the open conversation, or nil.
func (s *AppState) Active() *Conversation {
	if s.ActiveID == "" {
		return nil
	}
	return s.Conversations[s.ActiveID]
}

// VisibleConversations returns the list filtered by the current filter text and
// ordered by most recent activity.
func (s *AppState) VisibleConversations() []*Conversation {
	filter := strings.ToLower(string(s.Filter))
	out := make([]*Conversation, 0, len(s.Conversations))
	for _, id := range s.Order {
		cv := s.Conversations[id]
		if cv == nil {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(cv.Name), filter) {
			continue
		}
		out = append(out, cv)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastMessageAt != out[j].LastMessageAt {
			return out[i].LastMessageAt > out[j].LastMessageAt
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ---------- reducer ----------

// SetConversations replaces the list from a server response, preserving cached
// messages, drafts and unread counts for conversations that still exist.
func (s *AppState) SetConversations(list []protocol.Conversation) {
	seen := map[string]bool{}
	for _, pc := range list {
		seen[pc.ID] = true
		cv := s.EnsureConversation(pc.ID)
		// Keep UI-only state; take everything else from the server.
		msgs, unread, draft, hasMore, lastSeq := cv.Messages, cv.Unread, cv.Draft, cv.HasMore, cv.LastKnownSeq
		cv.Conversation = pc
		cv.Messages, cv.Unread, cv.Draft, cv.HasMore, cv.LastKnownSeq = msgs, unread, draft, hasMore, lastSeq
		if pc.LastMessage != "" {
			// Nothing cached yet; the preview text is all we know.
			cv.lastPreview = pc.LastMessage
		}
	}
	// Drop conversations the server no longer reports (deleted channels).
	kept := s.Order[:0]
	for _, id := range s.Order {
		if seen[id] {
			kept = append(kept, id)
			continue
		}
		if id == s.ActiveID {
			s.ActiveID = ""
		}
		delete(s.Conversations, id)
	}
	s.Order = kept
}

// InsertMessage adds or replaces a message, keeping the cache window bounded.
//
// It returns true when the message was new, which is what tells the caller
// whether to bump an unread count or offer a jump-to-bottom hint.
func (s *AppState) InsertMessage(m protocol.Message, isActive bool) bool {
	cv := s.EnsureConversation(m.ConversationID)

	// Replace an optimistic local copy rather than appending a duplicate: the
	// server echoes client_msg_id for exactly this purpose.
	if m.ClientMsgID != "" {
		for i := range cv.Messages {
			if cv.Messages[i].ClientMsgID == m.ClientMsgID && cv.Messages[i].ID == m.ClientMsgID {
				cv.Messages[i] = m
				cv.LastKnownSeq = max64(cv.LastKnownSeq, m.Seq)
				return true
			}
		}
	}
	for i := range cv.Messages {
		if cv.Messages[i].ID == m.ID {
			cv.Messages[i] = m
			return false
		}
	}

	// Keep ordered by seq even if frames arrive out of order.
	idx := sort.Search(len(cv.Messages), func(i int) bool { return cv.Messages[i].Seq >= m.Seq })
	cv.Messages = append(cv.Messages, protocol.Message{})
	copy(cv.Messages[idx+1:], cv.Messages[idx:])
	cv.Messages[idx] = m

	if len(cv.Messages) > MessageCacheLimit {
		// Drop from the head (oldest); HasMore stays true so paging can refill.
		drop := len(cv.Messages) - MessageCacheLimit
		cv.Messages = cv.Messages[drop:]
		cv.HasMore = true
	}

	cv.LastKnownSeq = max64(cv.LastKnownSeq, m.Seq)
	cv.LastMessageAt = max64(cv.LastMessageAt, m.CreatedAt)

	if !m.Deleted {
		cv.LastMessage = m.Body
	}
	if !isActive && m.AuthorID != s.UserID {
		cv.Unread++
	}
	return true
}

// SetHistory installs a page of history.
//
// When older is true the page is prepended (paging up); otherwise it replaces the
// cache (opening a conversation). Duplicates are removed either way, which makes
// a resume-after-reconnect idempotent.
func (s *AppState) SetHistory(id string, msgs []protocol.Message, hasMore bool, older bool) {
	cv := s.EnsureConversation(id)
	if !older {
		cv.Messages = nil
	}
	existing := make(map[string]bool, len(cv.Messages))
	for _, m := range cv.Messages {
		existing[m.ID] = true
	}
	var merged []protocol.Message
	if older {
		for _, m := range msgs {
			if !existing[m.ID] {
				merged = append(merged, m)
			}
		}
		merged = append(merged, cv.Messages...)
	} else {
		merged = append(merged, msgs...)
		for _, m := range cv.Messages {
			if !existing[m.ID] {
				merged = append(merged, m)
			}
		}
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Seq < merged[j].Seq })
	cv.Messages = merged
	cv.HasMore = hasMore
	if len(merged) > 0 {
		cv.LastKnownSeq = max64(cv.LastKnownSeq, merged[len(merged)-1].Seq)
	}
}

// MarkRead clears the unread badge and remembers the conversation is open.
func (s *AppState) MarkRead(id string) {
	if cv := s.Conversations[id]; cv != nil {
		cv.Unread = 0
	}
	s.PendingNewMessage = false
}

// RemoveConversation drops a conversation the server says is gone.
func (s *AppState) RemoveConversation(id string) {
	delete(s.Conversations, id)
	kept := s.Order[:0]
	for _, o := range s.Order {
		if o != id {
			kept = append(kept, o)
		}
	}
	s.Order = kept
	if s.ActiveID == id {
		s.ActiveID = ""
	}
}

// SetPresence records a user's online state.
func (s *AppState) SetPresence(ev protocol.PresenceEvent) {
	u := s.Users[ev.UserID]
	u.ID = ev.UserID
	u.Username = ev.Username
	u.Online = ev.Online
	s.Users[ev.UserID] = u
}

// TotalUnread sums unread counts, shown in the window title-adjacent area.
func (s *AppState) TotalUnread() int {
	n := 0
	for _, cv := range s.Conversations {
		n += cv.Unread
	}
	return n
}

// ---------- input editing ----------

// InsertRunes inserts text at the cursor.
func (s *AppState) InsertRunes(rs []rune) {
	if len(rs) == 0 {
		return
	}
	tail := append([]rune{}, s.Input[s.InputCursor:]...)
	s.Input = append(s.Input[:s.InputCursor], append(rs, tail...)...)
	s.InputCursor += len(rs)
	s.InputErr = ""
}

// Backspace deletes the rune before the cursor.
func (s *AppState) Backspace() {
	if s.InputCursor == 0 {
		return
	}
	s.Input = append(s.Input[:s.InputCursor-1], s.Input[s.InputCursor:]...)
	s.InputCursor--
}

// DeleteForward deletes the rune under the cursor.
func (s *AppState) DeleteForward() {
	if s.InputCursor >= len(s.Input) {
		return
	}
	s.Input = append(s.Input[:s.InputCursor], s.Input[s.InputCursor+1:]...)
}

// MoveCursor shifts the caret by delta, clamped.
func (s *AppState) MoveCursor(delta int) {
	s.InputCursor += delta
	if s.InputCursor < 0 {
		s.InputCursor = 0
	}
	if s.InputCursor > len(s.Input) {
		s.InputCursor = len(s.Input)
	}
}

// CursorWordLeft moves to the start of the previous word.
func (s *AppState) CursorWordLeft() {
	i := s.InputCursor
	for i > 0 && isSpace(s.Input[i-1]) {
		i--
	}
	for i > 0 && !isSpace(s.Input[i-1]) {
		i--
	}
	s.InputCursor = i
}

// CursorWordRight moves past the end of the next word.
func (s *AppState) CursorWordRight() {
	i := s.InputCursor
	for i < len(s.Input) && !isSpace(s.Input[i]) {
		i++
	}
	for i < len(s.Input) && isSpace(s.Input[i]) {
		i++
	}
	s.InputCursor = i
}

// DeleteWordBefore removes the word before the cursor (Ctrl+W).
func (s *AppState) DeleteWordBefore() {
	start := s.InputCursor
	i := start
	for i > 0 && isSpace(s.Input[i-1]) {
		i--
	}
	for i > 0 && !isSpace(s.Input[i-1]) {
		i--
	}
	s.Input = append(s.Input[:i], s.Input[start:]...)
	s.InputCursor = i
}

// CursorHome and CursorEnd are line-wise jumps for the single-line composer.
func (s *AppState) CursorHome() { s.InputCursor = 0 }
func (s *AppState) CursorEnd()  { s.InputCursor = len(s.Input) }

// TakeInput returns the trimmed input and clears it, used when sending.
func (s *AppState) TakeInput() string {
	v := string(s.Input)
	s.Input = nil
	s.InputCursor = 0
	return v
}

// RestoreInput puts text back into the composer, used when a send fails so the
// user does not lose what they typed (spec message-delivery / 发送失败保留草稿).
func (s *AppState) RestoreInput(v string) {
	s.Input = []rune(v)
	s.InputCursor = len(s.Input)
}

// InputText returns the composer contents.
func (s *AppState) InputText() string { return string(s.Input) }

// StashDraft saves the composer into the active conversation when switching away
// (spec tui-chat-client / 草稿按会话保留).
func (s *AppState) StashDraft() {
	if cv := s.Active(); cv != nil {
		cv.Draft = append([]rune{}, s.Input...)
	}
}

// LoadDraft restores a conversation's draft into the composer.
func (s *AppState) LoadDraft(id string) {
	s.Input = nil
	s.InputCursor = 0
	if cv := s.Conversations[id]; cv != nil {
		s.Input = append([]rune{}, cv.Draft...)
		s.InputCursor = len(s.Input)
	}
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
