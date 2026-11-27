package storage

import (
	"database/sql"
	"strings"
)

// SQLite implementation of Social.

// inList returns "?, ?, ?" for n placeholders and the args for ids.
func inList(ids []string) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i], args[i] = "?", id
	}
	return strings.Join(ph, ", "), args
}

func pair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

func collectStrings(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, s)
	}
	return out, mapErr(rows.Err())
}

// ---------- profiles ----------

func (s *sqlStore) ProfileOf(userID string) (Profile, error) {
	p := Profile{UserID: userID}
	err := s.db.QueryRow(`SELECT nickname, bio, avatar, color, gender, age, updated_at FROM user_profiles WHERE user_id = ?`, userID).
		Scan(&p.Nickname, &p.Bio, &p.Avatar, &p.Color, &p.Gender, &p.Age, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return p, nil
	}
	return p, mapErr(err)
}

func (s *sqlStore) ProfilesOf(userIDs []string) (map[string]Profile, error) {
	out := make(map[string]Profile, len(userIDs))
	for _, id := range userIDs {
		out[id] = Profile{UserID: id}
	}
	if len(userIDs) == 0 {
		return out, nil
	}
	ph, args := inList(userIDs)
	rows, err := s.db.Query(`SELECT user_id, nickname, bio, avatar, color, gender, age, updated_at FROM user_profiles WHERE user_id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p Profile
		if err := rows.Scan(&p.UserID, &p.Nickname, &p.Bio, &p.Avatar, &p.Color, &p.Gender, &p.Age, &p.UpdatedAt); err != nil {
			return nil, mapErr(err)
		}
		out[p.UserID] = p
	}
	return out, mapErr(rows.Err())
}

func (s *sqlStore) SaveProfile(p Profile) error {
	_, err := s.db.Exec(`INSERT INTO user_profiles (user_id, nickname, bio, avatar, color, gender, age, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET nickname = excluded.nickname, bio = excluded.bio,
			avatar = excluded.avatar, color = excluded.color, gender = excluded.gender, age = excluded.age,
			updated_at = excluded.updated_at`,
		p.UserID, p.Nickname, p.Bio, p.Avatar, p.Color, p.Gender, p.Age, p.UpdatedAt)
	return mapErr(err)
}

// ---------- friendships ----------

// AddFriendship makes a and b friends and clears any request between them.
func (s *sqlStore) AddFriendship(a, b string, at int64) error {
	x, y := pair(a, b)
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO friendships (user_a, user_b, created_at) VALUES (?, ?, ?)`, x, y, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM friend_removals WHERE (user_id = ? AND by_id = ?) OR (user_id = ? AND by_id = ?)`, a, b, b, a); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM friend_requests WHERE (from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?)`, a, b, b, a)
		return err
	})
}

func (s *sqlStore) AddRemoval(userID, byID string, at int64) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO friend_removals (user_id, by_id, created_at) VALUES (?, ?, ?)`, userID, byID, at)
	return mapErr(err)
}

func (s *sqlStore) DeleteRemoval(userID, byID string) error {
	_, err := s.db.Exec(`DELETE FROM friend_removals WHERE user_id = ? AND by_id = ?`, userID, byID)
	return mapErr(err)
}

func (s *sqlStore) RemovedByIDs(userID string) ([]string, error) {
	return collectStrings(s.db.Query(`SELECT by_id FROM friend_removals WHERE user_id = ? ORDER BY created_at DESC`, userID))
}

func (s *sqlStore) PlazaUserIDs() ([]string, error) {
	return collectStrings(s.db.Query(`SELECT user_id FROM user_settings WHERE key = 'plaza' AND value = 'on'`))
}

func (s *sqlStore) RemoveFriendship(a, b string) error {
	x, y := pair(a, b)
	_, err := s.db.Exec(`DELETE FROM friendships WHERE user_a = ? AND user_b = ?`, x, y)
	return mapErr(err)
}

func (s *sqlStore) AreFriends(a, b string) (bool, error) {
	x, y := pair(a, b)
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM friendships WHERE user_a = ? AND user_b = ?`, x, y).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, mapErr(err)
}

func (s *sqlStore) FriendIDs(userID string) ([]string, error) {
	return collectStrings(s.db.Query(`SELECT user_b FROM friendships WHERE user_a = ?
		UNION SELECT user_a FROM friendships WHERE user_b = ?`, userID, userID))
}

// ---------- requests ----------

const requestCols = `id, from_id, to_id, note, created_at`

func scanRequest(r rowScanner) (FriendRequest, error) {
	var fr FriendRequest
	err := r.Scan(&fr.ID, &fr.FromID, &fr.ToID, &fr.Note, &fr.CreatedAt)
	return fr, mapErr(err)
}

func (s *sqlStore) SaveFriendRequest(r FriendRequest) (FriendRequest, error) {
	if r.ID == "" {
		r.ID = "fr_" + newID()
	}
	err := s.db.QueryRow(`INSERT INTO friend_requests (`+requestCols+`) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(from_id, to_id) DO UPDATE SET note = excluded.note, created_at = excluded.created_at
		RETURNING id`, r.ID, r.FromID, r.ToID, r.Note, r.CreatedAt).Scan(&r.ID)
	if err != nil {
		return FriendRequest{}, mapErr(err)
	}
	return r, nil
}

func (s *sqlStore) FriendRequestByID(id string) (FriendRequest, error) {
	return scanRequest(s.db.QueryRow(`SELECT `+requestCols+` FROM friend_requests WHERE id = ?`, id))
}

func (s *sqlStore) FriendRequestBetween(from, to string) (FriendRequest, error) {
	return scanRequest(s.db.QueryRow(`SELECT `+requestCols+` FROM friend_requests WHERE from_id = ? AND to_id = ?`, from, to))
}

func (s *sqlStore) IncomingFriendRequests(userID string) ([]FriendRequest, error) {
	rows, err := s.db.Query(`SELECT `+requestCols+` FROM friend_requests WHERE to_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []FriendRequest{}
	for rows.Next() {
		fr, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fr)
	}
	return out, mapErr(rows.Err())
}

func (s *sqlStore) DeleteFriendRequests(a, b string) error {
	_, err := s.db.Exec(`DELETE FROM friend_requests WHERE (from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?)`, a, b, b, a)
	return mapErr(err)
}

// ---------- blocks ----------

// Block also ends any friendship and pending request between the two
// (spec friends / 拉黑).
func (s *sqlStore) Block(blocker, blocked string, at int64) error {
	x, y := pair(blocker, blocked)
	return s.withTx(func(tx *sql.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT OR IGNORE INTO blocks (blocker_id, blocked_id, created_at) VALUES (?, ?, ?)`, []any{blocker, blocked, at}},
			{`DELETE FROM friendships WHERE user_a = ? AND user_b = ?`, []any{x, y}},
			{`DELETE FROM friend_requests WHERE (from_id = ? AND to_id = ?) OR (from_id = ? AND to_id = ?)`, []any{blocker, blocked, blocked, blocker}},
			{`DELETE FROM friend_removals WHERE (user_id = ? AND by_id = ?) OR (user_id = ? AND by_id = ?)`, []any{blocker, blocked, blocked, blocker}},
		} {
			if _, err := tx.Exec(q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *sqlStore) Unblock(blocker, blocked string) error {
	_, err := s.db.Exec(`DELETE FROM blocks WHERE blocker_id = ? AND blocked_id = ?`, blocker, blocked)
	return mapErr(err)
}

func (s *sqlStore) IsBlocked(a, b string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM blocks WHERE (blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?) LIMIT 1`, a, b, b, a).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, mapErr(err)
}

func (s *sqlStore) BlockedIDs(blocker string) ([]string, error) {
	return collectStrings(s.db.Query(`SELECT blocked_id FROM blocks WHERE blocker_id = ? ORDER BY created_at DESC`, blocker))
}

// ---------- moments ----------

const momentCols = `id, author_id, body, visibility, created_at`

func scanMoment(r rowScanner) (Moment, error) {
	var m Moment
	err := r.Scan(&m.ID, &m.AuthorID, &m.Body, &m.Visibility, &m.CreatedAt)
	return m, mapErr(err)
}

func (s *sqlStore) AddMoment(m Moment) error {
	_, err := s.db.Exec(`INSERT INTO moments (`+momentCols+`) VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.AuthorID, m.Body, m.Visibility, m.CreatedAt)
	return mapErr(err)
}

func (s *sqlStore) MomentByID(id string) (Moment, error) {
	return scanMoment(s.db.QueryRow(`SELECT `+momentCols+` FROM moments WHERE id = ?`, id))
}

func (s *sqlStore) DeleteMoment(id string) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM moments WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		for _, q := range []string{
			`DELETE FROM moment_likes WHERE moment_id = ?`,
			`DELETE FROM moment_comments WHERE moment_id = ?`,
			`DELETE FROM moment_notifications WHERE moment_id = ?`,
		} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// pageMoments runs a newest-first keyset query. where and args already hold
// the filter; the cursor and limit are appended here.
func (s *sqlStore) pageMoments(where string, args []any, from MomentCursor, limit int) (MomentPage, error) {
	if limit <= 0 {
		limit = 20
	}
	if from != (MomentCursor{}) {
		where += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, from.At, from.At, from.ID)
	}
	args = append(args, limit+1)
	rows, err := s.db.Query(`SELECT `+momentCols+` FROM moments WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return MomentPage{}, mapErr(err)
	}
	defer rows.Close()
	var page MomentPage
	for rows.Next() {
		m, err := scanMoment(rows)
		if err != nil {
			return MomentPage{}, err
		}
		page.Moments = append(page.Moments, m)
	}
	if err := rows.Err(); err != nil {
		return MomentPage{}, mapErr(err)
	}
	if len(page.Moments) > limit {
		page.Moments = page.Moments[:limit]
		last := page.Moments[limit-1]
		page.Next = &MomentCursor{At: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (s *sqlStore) MomentsBy(authorIDs []string, selfID string, from MomentCursor, limit int) (MomentPage, error) {
	if len(authorIDs) == 0 {
		return MomentPage{}, nil
	}
	ph, args := inList(authorIDs)
	where := `author_id IN (` + ph + `) AND (visibility = 'friends' OR author_id = ?)`
	return s.pageMoments(where, append(args, selfID), from, limit)
}

func (s *sqlStore) AllMoments(authorID string, from MomentCursor, limit int) (MomentPage, error) {
	if authorID == "" {
		return s.pageMoments(`1 = 1`, nil, from, limit)
	}
	return s.pageMoments(`author_id = ?`, []any{authorID}, from, limit)
}

func (s *sqlStore) CountMomentsSince(authorIDs []string, since int64) (int, error) {
	if len(authorIDs) == 0 {
		return 0, nil
	}
	ph, args := inList(authorIDs)
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM moments WHERE author_id IN (`+ph+`) AND visibility = 'friends' AND created_at > ?`,
		append(args, since)...).Scan(&n)
	return n, mapErr(err)
}

// ---------- likes ----------

func (s *sqlStore) SetLike(l Like, on bool) error {
	var err error
	if on {
		_, err = s.db.Exec(`INSERT OR IGNORE INTO moment_likes (moment_id, user_id, created_at) VALUES (?, ?, ?)`, l.MomentID, l.UserID, l.CreatedAt)
	} else {
		_, err = s.db.Exec(`DELETE FROM moment_likes WHERE moment_id = ? AND user_id = ?`, l.MomentID, l.UserID)
	}
	return mapErr(err)
}

func (s *sqlStore) LikesOf(momentIDs []string) (map[string][]Like, error) {
	out := map[string][]Like{}
	if len(momentIDs) == 0 {
		return out, nil
	}
	ph, args := inList(momentIDs)
	rows, err := s.db.Query(`SELECT moment_id, user_id, created_at FROM moment_likes WHERE moment_id IN (`+ph+`) ORDER BY created_at, user_id`, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var l Like
		if err := rows.Scan(&l.MomentID, &l.UserID, &l.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out[l.MomentID] = append(out[l.MomentID], l)
	}
	return out, mapErr(rows.Err())
}

// ---------- comments ----------

const commentCols = `id, moment_id, author_id, reply_to, reply_to_user, body, created_at`

func scanComment(r rowScanner) (Comment, error) {
	var c Comment
	err := r.Scan(&c.ID, &c.MomentID, &c.AuthorID, &c.ReplyTo, &c.ReplyToUser, &c.Body, &c.CreatedAt)
	return c, mapErr(err)
}

func (s *sqlStore) AddComment(c Comment) error {
	_, err := s.db.Exec(`INSERT INTO moment_comments (`+commentCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.MomentID, c.AuthorID, c.ReplyTo, c.ReplyToUser, c.Body, c.CreatedAt)
	return mapErr(err)
}

func (s *sqlStore) CommentByID(id string) (Comment, error) {
	return scanComment(s.db.QueryRow(`SELECT `+commentCols+` FROM moment_comments WHERE id = ?`, id))
}

// DeleteComment removes the comment and the notifications it caused.
func (s *sqlStore) DeleteComment(id string) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM moment_comments WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(`DELETE FROM moment_notifications WHERE comment_id = ?`, id)
		return err
	})
}

func (s *sqlStore) CommentsOf(momentIDs []string) (map[string][]Comment, error) {
	out := map[string][]Comment{}
	if len(momentIDs) == 0 {
		return out, nil
	}
	ph, args := inList(momentIDs)
	rows, err := s.db.Query(`SELECT `+commentCols+` FROM moment_comments WHERE moment_id IN (`+ph+`) ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out[c.MomentID] = append(out[c.MomentID], c)
	}
	return out, mapErr(rows.Err())
}

// ---------- notifications ----------

func (s *sqlStore) AddNotification(n Notification) (Notification, error) {
	err := s.db.QueryRow(`INSERT INTO moment_notifications (user_id, kind, actor_id, moment_id, comment_id, body, created_at, read)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0) RETURNING id`,
		n.UserID, n.Kind, n.ActorID, n.MomentID, n.CommentID, n.Body, n.CreatedAt).Scan(&n.ID)
	if err != nil {
		return Notification{}, mapErr(err)
	}
	n.Read = false
	return n, nil
}

const notificationCols = `n.id, n.user_id, n.kind, n.actor_id, n.moment_id,
	n.comment_id, n.body, n.created_at, n.read, m.body`

// A recipient can see all interactions on their own moment. Other recipients
// must still see the moment and be friends with the actor who interacted.
const visibleNotifications = ` FROM moment_notifications n JOIN moments m ON m.id = n.moment_id
	WHERE n.user_id = ? AND (
		m.author_id = n.user_id OR (
			m.visibility = '` + VisibilityFriends + `'
			AND EXISTS (SELECT 1 FROM friendships f
				WHERE f.user_a = min(n.user_id, m.author_id) AND f.user_b = max(n.user_id, m.author_id))
			AND (n.actor_id = n.user_id OR EXISTS (SELECT 1 FROM friendships f
				WHERE f.user_a = min(n.user_id, n.actor_id) AND f.user_b = max(n.user_id, n.actor_id)))
		)
	)`

func scanNotification(row rowScanner) (Notification, error) {
	var n Notification
	var read int
	err := row.Scan(&n.ID, &n.UserID, &n.Kind, &n.ActorID, &n.MomentID, &n.CommentID,
		&n.Body, &n.CreatedAt, &read, &n.MomentBody)
	n.Read = read != 0
	return n, mapErr(err)
}

func (s *sqlStore) NotificationFor(userID string, id int64) (Notification, error) {
	return scanNotification(s.db.QueryRow(`SELECT `+notificationCols+visibleNotifications+` AND n.id = ?`, userID, id))
}

func (s *sqlStore) Notifications(userID string, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+notificationCols+visibleNotifications+` ORDER BY n.id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, mapErr(rows.Err())
}

func (s *sqlStore) MarkNotificationsRead(userID string) error {
	_, err := s.db.Exec(`UPDATE moment_notifications SET read = 1 WHERE user_id = ? AND read = 0`, userID)
	return mapErr(err)
}

func (s *sqlStore) UnreadNotifications(userID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*)`+visibleNotifications+` AND n.read = 0`, userID).Scan(&n)
	return n, mapErr(err)
}

// ---------- seen ----------

func (s *sqlStore) MomentsSeenAt(userID string) (int64, error) {
	var at int64
	err := s.db.QueryRow(`SELECT seen_at FROM moment_seen WHERE user_id = ?`, userID).Scan(&at)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return at, mapErr(err)
}

func (s *sqlStore) SetMomentsSeenAt(userID string, at int64) error {
	_, err := s.db.Exec(`INSERT INTO moment_seen (user_id, seen_at) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET seen_at = excluded.seen_at`, userID, at)
	return mapErr(err)
}
