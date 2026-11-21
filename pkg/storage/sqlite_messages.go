package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ---------- messages ----------

const msgCols = `id, conversation_id, seq, author_id, body, client_msg_id, created_at, edited_at, deleted`

func scanMessage(r rowScanner) (Message, error) {
	var m Message
	var deleted int
	err := r.Scan(&m.ID, &m.ConversationID, &m.Seq, &m.AuthorID, &m.Body, &m.ClientMsgID,
		&m.CreatedAt, &m.EditedAt, &deleted)
	if err != nil {
		return Message{}, mapErr(err)
	}
	m.Deleted = deleted != 0
	return m, nil
}

func collectMessages(rows *sql.Rows, err error) ([]Message, error) {
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, mapErr(rows.Err())
}

// AppendMessage assigns the next seq and stores the row in one transaction, so
// a crash can never consume a seq without its message and two concurrent sends
// can never share one (spec message-delivery / 消息顺序保证).
func (s *sqlStore) AppendMessage(msg Message) (Message, error) {
	if msg.ID == "" {
		return Message{}, errors.New("storage: message requires an id")
	}
	if msg.CreatedAt == 0 {
		msg.CreatedAt = Now()
	}
	var out Message
	err := s.withTx(func(tx *sql.Tx) error {
		// Retry safety: a resend with the same client ID returns the stored row
		// (spec message-delivery / 消息标识与幂等).
		if msg.ClientMsgID != "" {
			existing, err := scanMessage(tx.QueryRow(`SELECT `+msgCols+` FROM messages
				WHERE conversation_id = ? AND author_id = ? AND client_msg_id = ?`,
				msg.ConversationID, msg.AuthorID, msg.ClientMsgID))
			if err == nil {
				out = existing
				return nil
			}
			if err != ErrNotFound {
				return err
			}
		}
		if err := tx.QueryRow(`INSERT INTO conversation_seq (conversation_id, next_seq) VALUES (?, 2)
			ON CONFLICT(conversation_id) DO UPDATE SET next_seq = next_seq + 1
			RETURNING next_seq - 1`, msg.ConversationID).Scan(&msg.Seq); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO messages (`+msgCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			msg.ID, msg.ConversationID, msg.Seq, msg.AuthorID, msg.Body, msg.ClientMsgID,
			msg.CreatedAt, msg.EditedAt, boolInt(msg.Deleted)); err != nil {
			return err
		}
		out = msg
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	return out, nil
}

func (s *sqlStore) MessageByID(id string) (Message, error) {
	return scanMessage(s.db.QueryRow(`SELECT `+msgCols+` FROM messages WHERE id = ?`, id))
}

// MessagesBefore returns a page ending just before beforeSeq, in ascending seq
// order so the caller can append it directly. The bool reports whether older
// messages remain (spec message-delivery / 历史补发与分页).
func (s *sqlStore) MessagesBefore(conversationID string, beforeSeq int64, limit int) ([]Message, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	if beforeSeq <= 0 {
		beforeSeq = 1 << 62
	}
	// One extra row tells whether an older page exists.
	list, err := collectMessages(s.db.Query(`SELECT `+msgCols+` FROM messages
		WHERE conversation_id = ? AND seq < ? ORDER BY seq DESC LIMIT ?`,
		conversationID, beforeSeq, limit+1))
	if err != nil {
		return nil, false, err
	}
	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	out := make([]Message, len(list))
	for i, m := range list {
		out[len(list)-1-i] = m
	}
	return out, hasMore, nil
}

// UpdateMessage stores an edit or a soft delete. Seq, author and creation time
// are fixed once the message exists.
func (s *sqlStore) UpdateMessage(msg Message) error {
	return s.execOne(`UPDATE messages SET body = ?, edited_at = ?, deleted = ? WHERE id = ? AND conversation_id = ?`,
		msg.Body, msg.EditedAt, boolInt(msg.Deleted), msg.ID, msg.ConversationID)
}

// ---------- settings ----------

// UserSettings returns a copy of the user's settings and whether any were ever
// saved. A merge of an empty patch still counts as saved, which settings_owner
// records independently of the key rows.
func (s *sqlStore) UserSettings(userID string) (map[string]string, bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM settings_owner WHERE user_id = ?`, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]string{}, false, nil
	}
	if err != nil {
		return nil, false, mapErr(err)
	}
	got, err := readSettings(s.db, userID)
	return got, err == nil, err
}

func readSettings(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, userID string) (map[string]string, error) {
	rows, err := q.Query(`SELECT key, value FROM user_settings WHERE user_id = ?`, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, mapErr(err)
		}
		out[k] = v
	}
	return out, mapErr(rows.Err())
}

func (s *sqlStore) MergeUserSettings(userID string, patch map[string]string) (map[string]string, error) {
	var merged map[string]string
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO settings_owner (user_id) VALUES (?)`, userID); err != nil {
			return err
		}
		for k, v := range patch {
			if _, err := tx.Exec(`INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?)
				ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`, userID, k, v); err != nil {
				return err
			}
		}
		var err error
		merged, err = readSettings(tx, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// ---------- overview and search ----------

func (s *sqlStore) Counts() (Counts, error) {
	var c Counts
	err := s.db.QueryRow(`SELECT
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM conversations WHERE kind <> 'dm'),
		(SELECT count(*) FROM conversations WHERE kind = 'dm'),
		(SELECT count(*) FROM messages)`).Scan(&c.Users, &c.Channels, &c.DMs, &c.Messages)
	return c, mapErr(err)
}

// likePattern builds a substring LIKE pattern for kw and reports whether it
// needs an ESCAPE clause. Plain keywords skip ESCAPE so the trigram index can
// serve the LIKE.
func likePattern(kw string) (string, bool) {
	if !strings.ContainsAny(kw, `%_\`) {
		return "%" + kw + "%", false
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(kw) + "%", true
}

// SearchMessages returns matches newest first across all conversations, paged
// by (created_at, id) so a page never repeats or skips a row
// (spec admin-console / 跨会话搜索). Reads do not block sends, so a call no
// longer needs a scan cap.
func (s *sqlStore) SearchMessages(f SearchFilter, from SearchCursor) (SearchResult, error) {
	limit := f.Limit
	if limit <= 0 || limit > SearchMaxResults {
		limit = SearchMaxResults
	}
	where := []string{"deleted = 0"}
	var args []any
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		pat, esc := likePattern(kw)
		clause := `pk IN (SELECT rowid FROM messages_fts WHERE body LIKE ?`
		if esc {
			clause += ` ESCAPE '\'`
		}
		where = append(where, clause+`)`)
		args = append(args, pat)
	}
	if f.AuthorID != "" {
		where, args = append(where, "author_id = ?"), append(args, f.AuthorID)
	}
	if f.ConversationID != "" {
		where, args = append(where, "conversation_id = ?"), append(args, f.ConversationID)
	}
	if f.From > 0 {
		where, args = append(where, "created_at >= ?"), append(args, f.From)
	}
	if f.To > 0 {
		where, args = append(where, "created_at <= ?"), append(args, f.To)
	}
	if from != (SearchCursor{}) {
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, from.At, from.At, from.ID)
	}
	args = append(args, limit+1)
	list, err := collectMessages(s.db.Query(`SELECT `+msgCols+` FROM messages WHERE `+
		strings.Join(where, " AND ")+` ORDER BY created_at DESC, id DESC LIMIT ?`, args...))
	if err != nil {
		return SearchResult{}, err
	}
	var res SearchResult
	if len(list) > limit {
		list = list[:limit]
		last := list[limit-1]
		res.Next = &SearchCursor{At: last.CreatedAt, ID: last.ID}
	}
	res.Messages = list
	return res, nil
}

// ---------- sanctions ----------

const sanctionCols = `id, user_id, kind, reason, created_by, created_at, expires_at, revoked_by, revoked_at, superseded_by`

// Newest first; rowid breaks ties within one millisecond by insertion order.
const sanctionOrder = ` ORDER BY created_at DESC, rowid DESC`

func scanSanction(r rowScanner) (Sanction, error) {
	var sc Sanction
	err := r.Scan(&sc.ID, &sc.UserID, &sc.Kind, &sc.Reason, &sc.CreatedBy, &sc.CreatedAt,
		&sc.ExpiresAt, &sc.RevokedBy, &sc.RevokedAt, &sc.SupersededBy)
	return sc, mapErr(err)
}

func collectSanctions(rows *sql.Rows, err error) ([]Sanction, error) {
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []Sanction{}
	for rows.Next() {
		sc, err := scanSanction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, mapErr(rows.Err())
}

// activeAtSQL mirrors Sanction.ActiveAt for a time bound to the next argument.
const activeAtSQL = `revoked_at = 0 AND superseded_by = '' AND (expires_at = 0 OR expires_at > ?)`

// AddSanction stores sc and, in the same transaction, supersedes whatever of
// this kind is still in force, so two sanctions of one kind are never active
// together.
func (s *sqlStore) AddSanction(sc Sanction) (Sanction, error) {
	if sc.ID == "" {
		sc.ID = "s_" + newID()
	}
	if sc.CreatedAt == 0 {
		sc.CreatedAt = Now()
	}
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE sanctions SET superseded_by = ? WHERE user_id = ? AND kind = ? AND `+activeAtSQL,
			sc.ID, sc.UserID, sc.Kind, sc.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO sanctions (`+sanctionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sc.ID, sc.UserID, sc.Kind, sc.Reason, sc.CreatedBy, sc.CreatedAt,
			sc.ExpiresAt, sc.RevokedBy, sc.RevokedAt, sc.SupersededBy)
		return err
	})
	if err != nil {
		return Sanction{}, err
	}
	return sc, nil
}

// RevokeSanction ends a sanction early. Revoking one no longer in force is
// ErrConflict.
func (s *sqlStore) RevokeSanction(id, by string, at int64) (Sanction, error) {
	var out Sanction
	err := s.withTx(func(tx *sql.Tx) error {
		sc, err := scanSanction(tx.QueryRow(`SELECT `+sanctionCols+` FROM sanctions WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if !sc.ActiveAt(at) {
			return ErrConflict
		}
		sc.RevokedBy, sc.RevokedAt = by, at
		if _, err := tx.Exec(`UPDATE sanctions SET revoked_by = ?, revoked_at = ? WHERE id = ?`, by, at, id); err != nil {
			return err
		}
		out = sc
		return nil
	})
	return out, err
}

func (s *sqlStore) SanctionsForUser(userID string) ([]Sanction, error) {
	return collectSanctions(s.db.Query(`SELECT `+sanctionCols+` FROM sanctions WHERE user_id = ?`+sanctionOrder, userID))
}

func (s *sqlStore) ActiveSanction(userID, kind string, now int64) (Sanction, bool, error) {
	sc, err := scanSanction(s.db.QueryRow(`SELECT `+sanctionCols+` FROM sanctions
		WHERE user_id = ? AND kind = ? AND `+activeAtSQL+sanctionOrder+` LIMIT 1`, userID, kind, now))
	if err == ErrNotFound {
		return Sanction{}, false, nil
	}
	if err != nil {
		return Sanction{}, false, err
	}
	return sc, true, nil
}

// ---------- audit ----------

// AppendAudit adds one entry. The interface offers no way to change or remove
// one (spec account-moderation / 审计日志).
func (s *sqlStore) AppendAudit(e AuditEntry) error {
	if e.At == 0 {
		e.At = Now()
	}
	detail := ""
	if len(e.Detail) > 0 {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		detail = string(b)
	}
	_, err := s.db.Exec(`INSERT INTO audit (at, actor_id, actor_name, action, target, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		e.At, e.ActorID, e.ActorName, e.Action, e.Target, detail)
	return mapErr(err)
}

// ListAudit returns matching entries, newest first.
func (s *sqlStore) ListAudit(f AuditFilter) ([]AuditEntry, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where := []string{"1 = 1"}
	var args []any
	if f.Before > 0 {
		where, args = append(where, "at < ?"), append(args, f.Before)
	}
	if f.ActorID != "" {
		where, args = append(where, "actor_id = ?"), append(args, f.ActorID)
	}
	if f.Action != "" {
		where, args = append(where, "action = ?"), append(args, f.Action)
	}
	if f.From > 0 {
		where, args = append(where, "at >= ?"), append(args, f.From)
	}
	if f.To > 0 {
		where, args = append(where, "at <= ?"), append(args, f.To)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT at, actor_id, actor_name, action, target, detail FROM audit WHERE `+
		strings.Join(where, " AND ")+` ORDER BY at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var detail string
		if err := rows.Scan(&e.At, &e.ActorID, &e.ActorName, &e.Action, &e.Target, &detail); err != nil {
			return nil, mapErr(err)
		}
		if detail != "" {
			_ = json.Unmarshal([]byte(detail), &e.Detail)
		}
		out = append(out, e)
	}
	return out, mapErr(rows.Err())
}
