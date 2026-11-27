package storage

import (
	"database/sql"
	"sort"
)

// ---------- users ----------

const userCols = `id, username, username_key, password_hash, created_at,
	security_question, security_answer_hash, perms, super`

func scanUser(r rowScanner) (User, error) {
	var u User
	var perms string
	var super int
	err := r.Scan(&u.ID, &u.Username, &u.UsernameKey, &u.PasswordHash, &u.CreatedAt,
		&u.SecurityQuestion, &u.SecurityAnswerHash, &perms, &super)
	if err != nil {
		return User{}, mapErr(err)
	}
	u.Perms = decodeList(perms)
	u.Super = super != 0
	return u, nil
}

func (s *sqlStore) CreateUser(u User) error {
	u.UsernameKey = nameKey(u.Username)
	_, err := s.db.Exec(`INSERT INTO users (`+userCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.UsernameKey, u.PasswordHash, u.CreatedAt,
		u.SecurityQuestion, u.SecurityAnswerHash, encodeList(u.Perms), boolInt(u.Super))
	return mapErr(err)
}

func (s *sqlStore) UserByUsernameKey(key string) (User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username_key = ?`, nameKey(key)))
}

func (s *sqlStore) UserByID(id string) (User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *sqlStore) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY username`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	list := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, mapErr(rows.Err())
}

// execOne runs a single-row update and reports ErrNotFound when no row matched.
func (s *sqlStore) execOne(query string, args ...any) error {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return mapErr(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return mapErr(err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *sqlStore) UpdateUserPasswordHash(userID, hash string) error {
	return s.execOne(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID)
}

// UpdateUserCredentials writes the password hash and the recovery material in
// one statement, so a reset cannot leave an account with a new password and a
// stale answer hash.
func (s *sqlStore) UpdateUserCredentials(userID, passwordHash, question, answerHash string) error {
	return s.execOne(`UPDATE users SET password_hash = ?, security_question = ?, security_answer_hash = ? WHERE id = ?`,
		passwordHash, question, answerHash, userID)
}

func (s *sqlStore) SetUserPerms(userID string, perms []string) error {
	sorted := append([]string(nil), perms...)
	sort.Strings(sorted)
	return s.execOne(`UPDATE users SET perms = ? WHERE id = ?`, encodeList(sorted), userID)
}

func (s *sqlStore) SetUserSuper(userID string, super bool) error {
	return s.execOne(`UPDATE users SET super = ? WHERE id = ?`, boolInt(super), userID)
}

// ---------- tokens ----------

const tokenCols = `hash, user_id, created_at, expires_at, purpose, remote, device, last_seen_at, client_version`

func scanToken(r rowScanner) (Token, error) {
	var t Token
	err := r.Scan(&t.Hash, &t.UserID, &t.CreatedAt, &t.ExpiresAt, &t.Purpose,
		&t.Remote, &t.Device, &t.LastSeenAt, &t.ClientVersion)
	return t, mapErr(err)
}

// SaveToken inserts or replaces the token with the same hash.
func (s *sqlStore) SaveToken(t Token) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO tokens (`+tokenCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Hash, t.UserID, t.CreatedAt, t.ExpiresAt, t.Purpose, t.Remote, t.Device, t.LastSeenAt, t.ClientVersion)
	return mapErr(err)
}

func (s *sqlStore) TokenByHash(hash string) (Token, error) {
	return scanToken(s.db.QueryRow(`SELECT `+tokenCols+` FROM tokens WHERE hash = ?`, hash))
}

func (s *sqlStore) DeleteToken(hash string) error {
	_, err := s.db.Exec(`DELETE FROM tokens WHERE hash = ?`, hash)
	return mapErr(err)
}

// DeleteTokensForUser revokes every token issued to a user.
func (s *sqlStore) DeleteTokensForUser(userID string) error {
	_, err := s.db.Exec(`DELETE FROM tokens WHERE user_id = ?`, userID)
	return mapErr(err)
}

// DeleteSessionTokensForUser revokes every session token of a user, leaving
// reset and admin tokens untouched (spec account-auth / 重置后的会话吊销). The
// legacy empty purpose counts as a session.
func (s *sqlStore) DeleteSessionTokensForUser(userID string) error {
	return s.DeleteTokensForUserPurpose(userID, PurposeSession)
}

func (s *sqlStore) DeleteTokensForUserPurpose(userID, purpose string) error {
	var err error
	if purpose == PurposeSession {
		_, err = s.db.Exec(`DELETE FROM tokens WHERE user_id = ? AND purpose IN ('', ?)`, userID, purpose)
	} else {
		_, err = s.db.Exec(`DELETE FROM tokens WHERE user_id = ? AND purpose = ?`, userID, purpose)
	}
	return mapErr(err)
}

// ListTokensForUser returns a user's tokens, newest first.
func (s *sqlStore) ListTokensForUser(userID string) ([]Token, error) {
	rows, err := s.db.Query(`SELECT `+tokenCols+` FROM tokens WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, mapErr(rows.Err())
}

// ---------- conversations ----------

const convCols = `id, kind, name, topic, visibility, created_by, created_at, member_ids`

func scanConversation(r rowScanner) (Conversation, error) {
	var c Conversation
	var members string
	if err := r.Scan(&c.ID, &c.Kind, &c.Name, &c.Topic, &c.Visibility, &c.CreatedBy, &c.CreatedAt, &members); err != nil {
		return Conversation{}, mapErr(err)
	}
	c.MemberIDs = decodeList(members)
	return c, nil
}

func insertConversation(e interface {
	Exec(string, ...any) (sql.Result, error)
}, c Conversation) error {
	_, err := e.Exec(`INSERT INTO conversations (id, kind, name, name_key, topic, visibility, created_by, created_at, member_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Kind, c.Name, nameKey(c.Name), c.Topic, c.Visibility, c.CreatedBy, c.CreatedAt, encodeList(c.MemberIDs))
	return mapErr(err)
}

func (s *sqlStore) CreateConversation(c Conversation) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := insertConversation(tx, c); err != nil {
			return err
		}
		if c.Kind != "channel" || c.CreatedBy == "" {
			return nil
		}
		_, err := tx.Exec(`INSERT INTO members (conversation_id, user_id, joined_at) VALUES (?, ?, ?)`,
			c.ID, c.CreatedBy, c.CreatedAt)
		return err
	})
}

func (s *sqlStore) ConversationByID(id string) (Conversation, error) {
	return scanConversation(s.db.QueryRow(`SELECT `+convCols+` FROM conversations WHERE id = ?`, id))
}

func (s *sqlStore) ChannelByNameKey(key string) (Conversation, error) {
	return scanConversation(s.db.QueryRow(`SELECT `+convCols+` FROM conversations WHERE kind = 'channel' AND name_key = ?`, nameKey(key)))
}

func (s *sqlStore) ListConversations() ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT ` + convCols + ` FROM conversations ORDER BY id`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	list := []Conversation{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, mapErr(rows.Err())
}

// DeleteConversation removes the conversation, its membership, its messages
// and its seq counter in one transaction.
func (s *sqlStore) DeleteConversation(id string) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM conversations WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		for _, q := range []string{
			`DELETE FROM members WHERE conversation_id = ?`,
			`DELETE FROM messages WHERE conversation_id = ?`,
			`DELETE FROM conversation_seq WHERE conversation_id = ?`,
		} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// DMBetween returns the DM conversation for a pair of users, creating one if it
// does not exist yet. The ID is derived from the sorted pair so both
// participants (and a restart) always land on the same conversation
// (spec chat-rooms / 私聊会话).
func (s *sqlStore) DMBetween(a, b string) (Conversation, error) {
	id := dmID(a, b)
	var out Conversation
	err := s.withTx(func(tx *sql.Tx) error {
		c, err := scanConversation(tx.QueryRow(`SELECT `+convCols+` FROM conversations WHERE id = ?`, id))
		if err == nil {
			out = c
			return nil
		}
		if err != ErrNotFound {
			return err
		}
		out = Conversation{
			ID:        id,
			Kind:      "dm",
			CreatedAt: Now(),
			MemberIDs: []string{minStr(a, b), maxStr(a, b)},
		}
		return insertConversation(tx, out)
	})
	return out, err
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

// ---------- members ----------

// AddMember is idempotent: adding an existing member keeps the original row.
func (s *sqlStore) AddMember(m Member) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO members (conversation_id, user_id, joined_at) VALUES (?, ?, ?)`,
		m.ConversationID, m.UserID, m.JoinedAt)
	return mapErr(err)
}

func (s *sqlStore) RemoveMember(conversationID, userID string) error {
	_, err := s.db.Exec(`DELETE FROM members WHERE conversation_id = ? AND user_id = ?`, conversationID, userID)
	return mapErr(err)
}

// IsMember reports channel membership. A DM is membership-by-definition: its
// members are fixed at creation and never listed in the members table.
func (s *sqlStore) IsMember(conversationID, userID string) bool {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM members WHERE conversation_id = ? AND user_id = ?`, conversationID, userID).Scan(&one)
	if err == nil {
		return true
	}
	c, err := s.ConversationByID(conversationID)
	if err != nil || c.Kind != "dm" {
		return false
	}
	for _, id := range c.MemberIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func (s *sqlStore) ListMembers(conversationID string) ([]Member, error) {
	rows, err := s.db.Query(`SELECT conversation_id, user_id, joined_at FROM members WHERE conversation_id = ? ORDER BY user_id`, conversationID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var list []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ConversationID, &m.UserID, &m.JoinedAt); err != nil {
			return nil, mapErr(err)
		}
		list = append(list, m)
	}
	return list, mapErr(rows.Err())
}

func (s *sqlStore) MemberCount(conversationID string) int {
	if c, err := s.ConversationByID(conversationID); err == nil && c.Kind == "dm" {
		return len(c.MemberIDs)
	}
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM members WHERE conversation_id = ?`, conversationID).Scan(&n)
	return n
}
