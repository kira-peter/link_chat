package server

import (
	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Who may see which moment, like and comment (spec moments / 动态的可见性,
// 互动只在共同好友之间可见). Every read and every push goes through here.

// viewer is one user with their friends, loaded once per request.
type viewer struct {
	id      string
	friends map[string]bool
}

func (s *Server) newViewer(uid string) (viewer, error) {
	ids, err := s.st.FriendIDs(uid)
	if err != nil {
		return viewer{}, err
	}
	v := viewer{id: uid, friends: make(map[string]bool, len(ids))}
	for _, id := range ids {
		v.friends[id] = true
	}
	return v, nil
}

// sees reports whether the viewer is this user or one of their friends.
func (v viewer) sees(uid string) bool { return uid == v.id || v.friends[uid] }

// canSeeMoment: the author always; their friends when it is friends-visible.
// A block ends the friendship, so friendship alone is enough here.
func canSeeMoment(v viewer, m storage.Moment) bool {
	if m.AuthorID == v.id {
		return true
	}
	return m.Visibility == storage.VisibilityFriends && v.friends[m.AuthorID]
}

// filterInteractions keeps the likes and comments v may see: the author sees
// all; anyone else sees their own and their friends'. A reply's target that v
// may not see comes back with ReplyToUser cleared.
func filterInteractions(v viewer, m storage.Moment, likes []storage.Like, comments []storage.Comment) ([]storage.Like, []storage.Comment) {
	if m.AuthorID == v.id {
		return likes, comments
	}
	var ls []storage.Like
	for _, l := range likes {
		if v.sees(l.UserID) {
			ls = append(ls, l)
		}
	}
	var cs []storage.Comment
	for _, c := range comments {
		if c.AuthorID != "" && !v.sees(c.AuthorID) {
			continue
		}
		if c.ReplyToUser != "" && c.ReplyToUser != m.AuthorID && !v.sees(c.ReplyToUser) {
			c.ReplyToUser = ""
		}
		cs = append(cs, c)
	}
	return ls, cs
}

const (
	momentLikesPreview    = 8
	momentCommentsPreview = 12
	momentCommentsPage    = 24
)

// renderMoments turns moments into what v sees, with likes and comments
// filtered. Moments v may not see are dropped.
func (s *Server) renderMoments(v viewer, ms []storage.Moment) ([]protocol.Moment, error) {
	out := []protocol.Moment{}
	if len(ms) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	likes, err := s.st.LikePreviews(v.id, ids, momentLikesPreview)
	if err != nil {
		return nil, err
	}
	comments, err := s.st.CommentPreviews(v.id, ids, momentCommentsPreview)
	if err != nil {
		return nil, err
	}
	users := s.newUserCache()
	for _, m := range ms {
		if !canSeeMoment(v, m) {
			continue
		}
		likePreview, commentPreview := likes[m.ID], comments[m.ID]
		ls, cs := filterInteractions(v, m, likePreview.Likes, commentPreview.Comments)
		result := renderMoment(users, v.id, m, ls, cs)
		result.Liked = likePreview.Liked
		result.MoreLikes = max(0, likePreview.Total-len(result.Likes))
		result.CommentsBefore = toProtoCursor(commentPreview.Next)
		out = append(out, result)
	}
	return out, nil
}

// renderMoment builds the wire moment from already-filtered interactions.
// viewerID decides Liked; empty means "nobody", as the admin console sees it.
func renderMoment(users *userCache, viewerID string, m storage.Moment, likes []storage.Like, comments []storage.Comment) protocol.Moment {
	need := []string{m.AuthorID}
	for _, l := range likes {
		need = append(need, l.UserID)
	}
	for _, c := range comments {
		need = append(need, c.AuthorID, c.ReplyToUser)
	}
	users.load(need)
	pm := protocol.Moment{
		ID: m.ID, Author: users.get(m.AuthorID), Body: m.Body,
		Visibility: m.Visibility, CreatedAt: m.CreatedAt,
		Likes: []protocol.User{}, Comments: []protocol.Comment{},
	}
	for _, l := range likes {
		pm.Likes = append(pm.Likes, users.get(l.UserID))
		if l.UserID == viewerID {
			pm.Liked = true
		}
	}
	pm.Comments = renderComments(users, comments)
	return pm
}

func renderComments(users *userCache, comments []storage.Comment) []protocol.Comment {
	var ids []string
	for _, c := range comments {
		ids = append(ids, c.AuthorID, c.ReplyToUser)
	}
	users.load(ids)
	out := make([]protocol.Comment, 0, len(comments))
	for _, c := range comments {
		pc := protocol.Comment{ID: c.ID, MomentID: c.MomentID, Author: users.get(c.AuthorID), Body: c.Body, CreatedAt: c.CreatedAt}
		if c.AuthorID == "" {
			pc.Author.Nickname = "已删除账号"
		}
		if c.ReplyToUser != "" {
			u := users.get(c.ReplyToUser)
			pc.ReplyTo = &u
		}
		out = append(out, pc)
	}
	return out
}

// renderMomentFor is one moment as uid sees it; ok is false when uid may not
// see it at all.
func (s *Server) renderMomentFor(uid string, m storage.Moment) (protocol.Moment, bool, error) {
	v, err := s.newViewer(uid)
	if err != nil {
		return protocol.Moment{}, false, err
	}
	if !canSeeMoment(v, m) {
		return protocol.Moment{}, false, nil
	}
	ms, err := s.renderMoments(v, []storage.Moment{m})
	if err != nil || len(ms) == 0 {
		return protocol.Moment{}, false, err
	}
	return ms[0], true, nil
}

// momentAudience is who may see m right now: its author, and for a
// friends-visible moment the author's friends.
func (s *Server) momentAudience(m storage.Moment) []string {
	out := []string{m.AuthorID}
	if m.Visibility != storage.VisibilityFriends {
		return out
	}
	friends, err := s.st.FriendIDs(m.AuthorID)
	if err != nil {
		return out
	}
	return append(out, friends...)
}

func toProtoCursor(c *storage.MomentCursor) *protocol.MomentCursor {
	if c == nil {
		return nil
	}
	return &protocol.MomentCursor{At: c.At, ID: c.ID}
}
