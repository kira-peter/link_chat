package server

import (
	"errors"
	"net/http"
	"strings"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Moments in the admin console (spec admin-console / 动态浏览与删除). The
// console sees every moment and every comment: no visibility filter applies.

func (s *Server) adminMomentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/moments", s.adminAuth(PermChatView, s.apiMoments))
	mux.HandleFunc("POST /admin/api/moments/{id}/delete", s.adminAuth(PermUsersModerate, s.apiDeleteMoment))
	mux.HandleFunc("POST /admin/api/comments/{id}/delete", s.adminAuth(PermUsersModerate, s.apiDeleteComment))
}

type adminMomentsRes struct {
	Moments []protocol.Moment      `json:"moments"`
	Next    *protocol.MomentCursor `json:"next"`
}

func (s *Server) apiMoments(w http.ResponseWriter, r *http.Request, a adminSession) {
	q := r.URL.Query()
	authorID := ""
	if name := strings.TrimSpace(q.Get("author")); name != "" {
		u, err := s.st.UserByUsernameKey(strings.ToLower(name))
		if err != nil {
			writeJSON(w, http.StatusOK, adminMomentsRes{Moments: []protocol.Moment{}})
			return
		}
		authorID = u.ID
	}
	from := storage.MomentCursor{At: queryInt(q.Get("cursor_at")), ID: q.Get("cursor_id")}
	page, err := s.st.AllMoments(authorID, from, 30)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	ids := make([]string, 0, len(page.Moments))
	for _, m := range page.Moments {
		ids = append(ids, m.ID)
	}
	likes, err := s.st.LikesOf(ids)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	comments, err := s.st.CommentsOf(ids)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	users := s.newUserCache()
	res := adminMomentsRes{Moments: []protocol.Moment{}, Next: toProtoCursor(page.Next)}
	for _, m := range page.Moments {
		res.Moments = append(res.Moments, renderMoment(users, "", m, likes[m.ID], comments[m.ID]))
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) apiDeleteMoment(w http.ResponseWriter, r *http.Request, a adminSession) {
	// Deleting needs seeing as well (spec admin-console / 区域按权限可见).
	if !a.perms[PermChatView] {
		writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
		return
	}
	m, err := s.st.MomentByID(r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, http.StatusNotFound, protocol.CodeMomentNotFound)
		return
	}
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	if err := s.removeMoment(m); err != nil {
		s.writeStorageErr(w, err)
		return
	}
	s.audit(a.actor(), "delete_moment", m.AuthorID, map[string]string{
		"username": s.userName(m.AuthorID), "moment": m.ID, "body": m.Body,
	})
	s.log.Info("moment deleted by admin", "by", a.user.Username, "author", s.userName(m.AuthorID))
	s.activity("moment.deleted", ActivitySocial, a.user.ID, m.AuthorID, storage.Conversation{}, "")
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) apiDeleteComment(w http.ResponseWriter, r *http.Request, a adminSession) {
	if !a.perms[PermChatView] {
		writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
		return
	}
	cm, err := s.st.CommentByID(r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeErr(w, http.StatusNotFound, protocol.CodeNotFound)
		return
	}
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	if err := s.st.DeleteComment(cm.ID); err != nil {
		s.writeStorageErr(w, err)
		return
	}
	if m, err := s.st.MomentByID(cm.MomentID); err == nil {
		s.broadcastMoment(protocol.EvMomentUpdated, m)
	}
	s.audit(a.actor(), "delete_comment", cm.AuthorID, map[string]string{
		"username": s.userName(cm.AuthorID), "moment": cm.MomentID, "body": cm.Body,
	})
	writeJSON(w, http.StatusOK, struct{}{})
}
