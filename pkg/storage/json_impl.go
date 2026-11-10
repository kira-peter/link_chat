package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

// ---------- users ----------

func (s *jsonStore) CreateUser(u User) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		key := nameKey(u.Username)
		if _, exists := m.usernameIndex[key]; exists {
			return struct{}{}, ErrConflict
		}
		if _, exists := m.users[u.ID]; exists {
			return struct{}{}, ErrConflict
		}
		u.UsernameKey = key
		m.users[u.ID] = u
		m.usernameIndex[key] = u.ID
		if err := s.persistUsers(m); err != nil {
			// Roll the in-memory state back so memory and disk agree.
			delete(m.users, u.ID)
			delete(m.usernameIndex, key)
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *jsonStore) UserByUsernameKey(key string) (User, error) {
	return run(s, s.model, func(m *model) (User, error) {
		id, ok := m.usernameIndex[nameKey(key)]
		if !ok {
			return User{}, ErrNotFound
		}
		return m.users[id], nil
	})
}

func (s *jsonStore) UserByID(id string) (User, error) {
	return run(s, s.model, func(m *model) (User, error) {
		u, ok := m.users[id]
		if !ok {
			return User{}, ErrNotFound
		}
		return u, nil
	})
}

func (s *jsonStore) ListUsers() ([]User, error) {
	return run(s, s.model, func(m *model) ([]User, error) {
		list := make([]User, 0, len(m.users))
		for _, u := range m.users {
			list = append(list, u)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Username < list[j].Username })
		return list, nil
	})
}

// ListTokensForUser returns a user's tokens, newest first. The server uses it to
// describe the session being displaced by a newer login.
func (s *jsonStore) ListTokensForUser(userID string) ([]Token, error) {
	return run(s, s.model, func(m *model) ([]Token, error) {
		var out []Token
		for _, t := range m.tokens {
			if t.UserID == userID {
				out = append(out, t)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].CreatedAt > out[j].CreatedAt
		})
		return out, nil
	})
}

// DeleteSessionTokensForUser revokes every session token of a user, leaving any
// reset token they hold untouched. Used after a password reset so every existing
// chat session dies while an in-flight recovery is not disturbed
// (spec account-auth / 重置后的会话吊销).
func (s *jsonStore) DeleteSessionTokensForUser(userID string) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		removed := map[string]Token{}
		for h, t := range m.tokens {
			if t.UserID != userID || t.IsReset() {
				continue
			}
			removed[h] = t
			delete(m.tokens, h)
		}
		if len(removed) == 0 {
			return struct{}{}, nil
		}
		if err := s.persistTokens(m); err != nil {
			for h, t := range removed {
				m.tokens[h] = t
			}
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

// UpdateUserCredentials writes the password hash and the recovery material in
// one atomic step, so a reset cannot leave an account with a new password and a
// stale answer hash.
func (s *jsonStore) UpdateUserCredentials(userID, passwordHash, question, answerHash string) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		u, ok := m.users[userID]
		if !ok {
			return struct{}{}, ErrNotFound
		}
		old := u
		u.PasswordHash = passwordHash
		u.SecurityQuestion = question
		u.SecurityAnswerHash = answerHash
		m.users[userID] = u
		if err := s.persistUsers(m); err != nil {
			m.users[userID] = old
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *jsonStore) UpdateUserPasswordHash(userID, hash string) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		u, ok := m.users[userID]
		if !ok {
			return struct{}{}, ErrNotFound
		}
		old := u.PasswordHash
		u.PasswordHash = hash
		m.users[userID] = u
		if err := s.persistUsers(m); err != nil {
			u.PasswordHash = old
			m.users[userID] = u
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

// ---------- tokens ----------

func (s *jsonStore) SaveToken(t Token) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		m.tokens[t.Hash] = t
		if err := s.persistTokens(m); err != nil {
			delete(m.tokens, t.Hash)
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *jsonStore) TokenByHash(hash string) (Token, error) {
	return run(s, s.model, func(m *model) (Token, error) {
		t, ok := m.tokens[hash]
		if !ok {
			return Token{}, ErrNotFound
		}
		return t, nil
	})
}

func (s *jsonStore) DeleteToken(hash string) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		t, ok := m.tokens[hash]
		if !ok {
			return struct{}{}, nil
		}
		delete(m.tokens, hash)
		if err := s.persistTokens(m); err != nil {
			m.tokens[hash] = t
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

// DeleteTokensForUser revokes every token issued to a user. Logout only revokes
// the presented token, so this is reserved for account-level actions.
func (s *jsonStore) DeleteTokensForUser(userID string) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		removed := map[string]Token{}
		for h, t := range m.tokens {
			if t.UserID == userID {
				removed[h] = t
				delete(m.tokens, h)
			}
		}
		if len(removed) == 0 {
			return struct{}{}, nil
		}
		if err := s.persistTokens(m); err != nil {
			for h, t := range removed {
				m.tokens[h] = t
			}
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

// ---------- conversations ----------

func (s *jsonStore) CreateConversation(c Conversation) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		if _, exists := m.conversations[c.ID]; exists {
			return struct{}{}, ErrConflict
		}
		if c.Kind == "channel" {
			key := nameKey(c.Name)
			if _, exists := m.channelIndex[key]; exists {
				return struct{}{}, ErrConflict
			}
			m.channelIndex[key] = c.ID
		}
		m.conversations[c.ID] = c
		if err := s.persistConversations(m); err != nil {
			delete(m.conversations, c.ID)
			if c.Kind == "channel" {
				delete(m.channelIndex, nameKey(c.Name))
			}
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *jsonStore) ConversationByID(id string) (Conversation, error) {
	return run(s, s.model, func(m *model) (Conversation, error) {
		c, ok := m.conversations[id]
		if !ok {
			return Conversation{}, ErrNotFound
		}
		return c, nil
	})
}

func (s *jsonStore) ChannelByNameKey(key string) (Conversation, error) {
	return run(s, s.model, func(m *model) (Conversation, error) {
		id, ok := m.channelIndex[nameKey(key)]
		if !ok {
			return Conversation{}, ErrNotFound
		}
		return m.conversations[id], nil
	})
}

func (s *jsonStore) ListConversations() ([]Conversation, error) {
	return run(s, s.model, func(m *model) ([]Conversation, error) {
		list := make([]Conversation, 0, len(m.conversations))
		for _, c := range m.conversations {
			list = append(list, c)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		return list, nil
	})
}

// DeleteConversation removes the conversation, its membership, and its message
// log. The message rows cannot simply be dropped because paging would otherwise
// resurrect them; the file itself goes away.
func (s *jsonStore) DeleteConversation(id string) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		c, ok := m.conversations[id]
		if !ok {
			return struct{}{}, ErrNotFound
		}
		oldMembers := m.members[id]
		oldMessages := m.messages[id]
		oldSeq := m.nextSeq[id]

		delete(m.conversations, id)
		if c.Kind == "channel" {
			delete(m.channelIndex, nameKey(c.Name))
		}
		delete(m.members, id)
		delete(m.messages, id)
		delete(m.nextSeq, id)

		if err := s.persistConversations(m); err != nil {
			m.conversations[id] = c
			if c.Kind == "channel" {
				m.channelIndex[nameKey(c.Name)] = id
			}
			m.members[id] = oldMembers
			m.messages[id] = oldMessages
			m.nextSeq[id] = oldSeq
			return struct{}{}, err
		}
		if err := s.persistMembers(m); err != nil {
			return struct{}{}, err
		}
		if err := removeIfExists(messagePath(s.root, id)); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

// DMBetween returns the DM conversation for a pair of users, creating one if it
// does not exist yet. The ID is derived from the sorted pair so both
// participants (and a restart) always land on the same conversation
// (spec chat-rooms / 私聊会话).
func (s *jsonStore) DMBetween(a, b string) (Conversation, error) {
	return run(s, s.model, func(m *model) (Conversation, error) {
		id := dmID(a, b)
		if c, ok := m.conversations[id]; ok {
			return c, nil
		}
		c := Conversation{
			ID:        id,
			Kind:      "dm",
			CreatedAt: Now(),
			MemberIDs: []string{minStr(a, b), maxStr(a, b)},
		}
		m.conversations[id] = c
		if err := s.persistConversations(m); err != nil {
			delete(m.conversations, id)
			return Conversation{}, err
		}
		return c, nil
	})
}

// dmID builds a stable, collision-free conversation ID from two user IDs.
func dmID(a, b string) string {
	return "dm_" + minStr(a, b) + "_" + maxStr(a, b)
}

func minStr(a, b string) string {
	if a < b {
		return a
	}
	return b
}

func maxStr(a, b string) string {
	if a > b {
		return a
	}
	return b
}

func removeIfExists(path string) error {
	if err := removeFile(path); err != nil && !errors.Is(err, errNotExist) {
		return err
	}
	return nil
}

// ---------- members ----------

func (s *jsonStore) AddMember(mem Member) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		if m.members[mem.ConversationID] == nil {
			m.members[mem.ConversationID] = map[string]Member{}
		}
		if _, exists := m.members[mem.ConversationID][mem.UserID]; exists {
			return struct{}{}, nil // idempotent
		}
		m.members[mem.ConversationID][mem.UserID] = mem
		if err := s.persistMembers(m); err != nil {
			delete(m.members[mem.ConversationID], mem.UserID)
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *jsonStore) RemoveMember(conversationID, userID string) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		byUser := m.members[conversationID]
		if byUser == nil {
			return struct{}{}, nil
		}
		old, exists := byUser[userID]
		if !exists {
			return struct{}{}, nil
		}
		delete(byUser, userID)
		if err := s.persistMembers(m); err != nil {
			byUser[userID] = old
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func (s *jsonStore) IsMember(conversationID, userID string) bool {
	ok, _ := run(s, s.model, func(m *model) (bool, error) {
		if byUser, exists := m.members[conversationID]; exists {
			if _, isIn := byUser[userID]; isIn {
				return true, nil
			}
		}
		// A DM is membership-by-definition: its members are fixed at creation
		// and never listed in the members collection.
		if c, exists := m.conversations[conversationID]; exists && c.Kind == "dm" {
			for _, id := range c.MemberIDs {
				if id == userID {
					return true, nil
				}
			}
		}
		return false, nil
	})
	return ok
}

func (s *jsonStore) ListMembers(conversationID string) ([]Member, error) {
	return run(s, s.model, func(m *model) ([]Member, error) {
		var list []Member
		for _, mem := range m.members[conversationID] {
			list = append(list, mem)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].UserID < list[j].UserID })
		return list, nil
	})
}

func (s *jsonStore) MemberCount(conversationID string) int {
	n, _ := run(s, s.model, func(m *model) (int, error) {
		if c, ok := m.conversations[conversationID]; ok && c.Kind == "dm" {
			return len(c.MemberIDs), nil
		}
		return len(m.members[conversationID]), nil
	})
	return n
}

// ---------- messages ----------

// AppendMessage assigns the next seq for the conversation, writes the row to the
// append-only log, and returns it. Seq assignment and the append happen under
// the same actor turn, so two concurrent sends can never share a seq
// (spec message-delivery / 消息顺序保证).
func (s *jsonStore) AppendMessage(msg Message) (Message, error) {
	return run(s, s.model, func(m *model) (Message, error) {
		if msg.ID == "" {
			return Message{}, errors.New("storage: message requires an id")
		}
		// Retry safety: a resend with the same client ID must not duplicate.
		if msg.ClientMsgID != "" {
			for _, existing := range m.messages[msg.ConversationID] {
				if existing.ClientMsgID == msg.ClientMsgID && existing.AuthorID == msg.AuthorID {
					return existing, nil
				}
			}
		}
		next := m.nextSeq[msg.ConversationID]
		if next == 0 {
			next = 1
		}
		msg.Seq = next
		if msg.CreatedAt == 0 {
			msg.CreatedAt = Now()
		}

		line, err := json.Marshal(msg)
		if err != nil {
			return Message{}, err
		}
		if err := appendLine(messagePath(s.root, msg.ConversationID), line); err != nil {
			return Message{}, wrapErr(err)
		}
		m.messages[msg.ConversationID] = append(m.messages[msg.ConversationID], msg)
		m.nextSeq[msg.ConversationID] = next + 1
		return msg, nil
	})
}

func (s *jsonStore) MessageByID(id string) (Message, error) {
	return run(s, s.model, func(m *model) (Message, error) {
		for _, list := range m.messages {
			for _, msg := range list {
				if msg.ID == id {
					return msg, nil
				}
			}
		}
		return Message{}, ErrNotFound
	})
}

// MessagesBefore returns a page ending just before beforeSeq, in ascending seq
// order so the caller can append it directly. The bool reports whether older
// messages remain (spec message-delivery / 历史补发与分页).
func (s *jsonStore) MessagesBefore(conversationID string, beforeSeq int64, limit int) ([]Message, bool, error) {
	type page struct {
		msgs    []Message
		hasMore bool
	}
	p, err := run(s, s.model, func(m *model) (page, error) {
		all := m.messages[conversationID]
		if limit <= 0 {
			limit = 50
		}
		// all is sorted ascending; find the index just past the end of the page.
		end := len(all)
		if beforeSeq > 0 {
			end = sort.Search(len(all), func(i int) bool { return all[i].Seq >= beforeSeq })
		}
		start := end - limit
		if start < 0 {
			start = 0
		}
		out := make([]Message, end-start)
		copy(out, all[start:end])
		return page{msgs: out, hasMore: start > 0}, nil
	})
	if err != nil {
		return nil, false, err
	}
	return p.msgs, p.hasMore, nil
}

func (s *jsonStore) UpdateMessage(msg Message) error {
	_, err := run(s, s.model, func(m *model) (struct{}, error) {
		list := m.messages[msg.ConversationID]
		for i := range list {
			if list[i].ID != msg.ID {
				continue
			}
			old := list[i]
			list[i] = msg
			// Rewrite the whole conversation log: edits are rare, and an
			// in-place append would leave two rows claiming the same seq.
			if err := s.rewriteMessages(msg.ConversationID, list); err != nil {
				list[i] = old
				return struct{}{}, err
			}
			return struct{}{}, nil
		}
		return struct{}{}, ErrNotFound
	})
	return err
}

func (s *jsonStore) rewriteMessages(conversationID string, list []Message) error {
	var buf []byte
	for _, msg := range list {
		line, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	return wrapErr(writeFileAtomic(messagePath(s.root, conversationID), buf))
}

// newID returns a random, non-enumerable identifier. Random rather than
// sequential so IDs do not disclose ordering or volume
// (spec message-delivery / 消息标识与幂等).
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("storage: entropy source failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
