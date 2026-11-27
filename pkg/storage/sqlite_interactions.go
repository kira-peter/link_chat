package storage

import "slices"

// An author sees every interaction on their post. Other viewers see their
// own interactions and those of their friends.
const visibleInteractionMoment = `(m.author_id = v.id OR (m.visibility = '` + VisibilityFriends + `' AND EXISTS (
	SELECT 1 FROM friendships WHERE user_a = min(v.id, m.author_id) AND user_b = max(v.id, m.author_id)
)))`

const visibleLikeActors = `(m.author_id = v.id OR l.user_id = v.id OR EXISTS (
	SELECT 1 FROM friendships WHERE user_a = min(v.id, l.user_id) AND user_b = max(v.id, l.user_id)
))`

const visibleCommentActors = `(m.author_id = v.id OR cm.author_id = v.id OR EXISTS (
	SELECT 1 FROM friendships WHERE user_a = min(v.id, cm.author_id) AND user_b = max(v.id, cm.author_id)
))`

func (s *sqlStore) LikePreviews(viewerID string, momentIDs []string, limit int) (map[string]LikePreview, error) {
	out := make(map[string]LikePreview, len(momentIDs))
	if len(momentIDs) == 0 {
		return out, nil
	}
	ph, ids := inList(momentIDs)
	args := append([]any{viewerID}, ids...)
	rows, err := s.db.Query(`
		WITH viewer(id) AS (VALUES (?)), ranked AS (
			SELECT l.moment_id, l.user_id, l.created_at,
				ROW_NUMBER() OVER (PARTITION BY l.moment_id ORDER BY l.created_at, l.user_id) AS rank,
				COUNT(*) OVER (PARTITION BY l.moment_id) AS total,
				MAX(l.user_id = v.id) OVER (PARTITION BY l.moment_id) AS liked
			FROM moment_likes l JOIN moments m ON m.id = l.moment_id CROSS JOIN viewer v
			WHERE l.moment_id IN (`+ph+`) AND `+visibleInteractionMoment+` AND `+visibleLikeActors+`
		)
		SELECT moment_id, user_id, created_at, total, liked FROM ranked
		WHERE rank <= ? ORDER BY moment_id, rank`, append(args, limit)...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var l Like
		var total int
		var liked bool
		if err := rows.Scan(&l.MomentID, &l.UserID, &l.CreatedAt, &total, &liked); err != nil {
			return nil, mapErr(err)
		}
		page := out[l.MomentID]
		page.Likes = append(page.Likes, l)
		page.Total, page.Liked = total, liked
		out[l.MomentID] = page
	}
	return out, mapErr(rows.Err())
}

const qualifiedCommentCols = `cm.id, cm.moment_id, cm.author_id, cm.reply_to, cm.reply_to_user, cm.body, cm.created_at`

// CommentPreviews fetches the most recent visible comments for every moment in
// one query, plus one record per moment to detect any older page.
func (s *sqlStore) CommentPreviews(viewerID string, momentIDs []string, limit int) (map[string]CommentPage, error) {
	if limit <= 0 {
		limit = 12
	}
	out := make(map[string]CommentPage, len(momentIDs))
	if len(momentIDs) == 0 {
		return out, nil
	}
	ph, ids := inList(momentIDs)
	args := append([]any{viewerID, limit + 1}, ids...)
	rows, err := s.db.Query(`
		WITH viewer(id) AS (VALUES (?))
		SELECT page.id, page.moment_id, page.author_id, page.reply_to,
			page.reply_to_user, page.body, page.created_at
		FROM moments m CROSS JOIN viewer v JOIN moment_comments page ON page.rowid IN (
			SELECT cm.rowid FROM moment_comments cm
			WHERE cm.moment_id = m.id AND `+visibleCommentActors+`
			ORDER BY cm.created_at DESC, cm.id DESC LIMIT ?
		)
		WHERE m.id IN (`+ph+`) AND `+visibleInteractionMoment+`
		ORDER BY page.moment_id, page.created_at DESC, page.id DESC`, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	byMoment := make(map[string][]Comment, len(momentIDs))
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		byMoment[c.MomentID] = append(byMoment[c.MomentID], c)
	}
	for id, comments := range byMoment {
		out[id] = commentPage(comments, limit)
	}
	return out, mapErr(rows.Err())
}

func (s *sqlStore) MomentComments(viewerID, momentID string, before MomentCursor, limit int) (CommentPage, error) {
	if limit <= 0 {
		limit = 24
	}
	where := ""
	args := []any{viewerID, momentID}
	if before.ID != "" {
		where = ` AND (cm.created_at, cm.id) < (?, ?)`
		args = append(args, before.At, before.ID)
	}
	rows, err := s.db.Query(`
		WITH viewer(id) AS (VALUES (?))
		SELECT `+qualifiedCommentCols+`
		FROM moment_comments cm JOIN moments m ON m.id = cm.moment_id CROSS JOIN viewer v
		WHERE cm.moment_id = ? AND `+visibleInteractionMoment+` AND `+visibleCommentActors+where+
		` ORDER BY cm.created_at DESC, cm.id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return CommentPage{}, mapErr(err)
	}
	defer rows.Close()
	var comments []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return CommentPage{}, err
		}
		comments = append(comments, c)
	}
	return commentPage(comments, limit), mapErr(rows.Err())
}

func commentPage(comments []Comment, limit int) CommentPage {
	var page CommentPage
	if len(comments) > limit {
		comments = comments[:limit]
		last := comments[len(comments)-1]
		page.Next = &MomentCursor{At: last.CreatedAt, ID: last.ID}
	}
	slices.Reverse(comments)
	page.Comments = comments
	return page
}
