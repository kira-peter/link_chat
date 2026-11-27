package server

import (
	"errors"
	"strings"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Moments (spec moments).

// visibleMoment loads a moment uid may see. A missing one and one uid may not
// see are the same error.
func (s *Server) visibleMoment(c *Conn, id, uid, momentID string) (storage.Moment, viewer, bool) {
	m, err := s.st.MomentByID(momentID)
	if errors.Is(err, storage.ErrNotFound) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeMomentNotFound))
		return storage.Moment{}, viewer{}, false
	}
	if err != nil {
		s.failStorage(c, id, err)
		return storage.Moment{}, viewer{}, false
	}
	v, err := s.newViewer(uid)
	if err != nil {
		s.failStorage(c, id, err)
		return storage.Moment{}, viewer{}, false
	}
	if !canSeeMoment(v, m) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeMomentNotFound))
		return storage.Moment{}, viewer{}, false
	}
	return m, v, true
}

// broadcastMoment sends each member of m's audience the moment as they see
// it. Their views differ, since comments are filtered per viewer
// (design.md decision 3).
func (s *Server) broadcastMoment(name string, m storage.Moment) {
	online := s.hub.onlineUsers()
	for _, uid := range s.momentAudience(m) {
		if !online[uid] {
			continue
		}
		pm, ok, err := s.renderMomentFor(uid, m)
		if err != nil || !ok {
			continue
		}
		s.pushTo([]string{uid}, name, pm)
	}
}

func (s *Server) opMomentPost(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.MomentPostReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if len([]rune(body)) > protocol.MaxMomentLen {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeMomentTooLong))
		return
	}
	vis := req.Visibility
	switch vis {
	case "":
		vis = protocol.VisibilityFriends
	case protocol.VisibilityFriends, protocol.VisibilityPrivate:
	default:
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if s.failIfMuted(c, id, uid) {
		return
	}
	if ok, wait := s.postLimit.Allow(uid); !ok {
		s.failRateLimited(c, id, wait)
		return
	}
	m := storage.Moment{ID: newID("mo_"), AuthorID: uid, Body: body, Visibility: vis, CreatedAt: storage.Now()}
	if err := s.st.AddMoment(m); err != nil {
		s.failStorage(c, id, err)
		return
	}
	pm, _, err := s.renderMomentFor(uid, m)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, pm)
	s.log.Info("moment posted", "user", s.userName(uid), "visibility", vis)
	s.activity("moment.posted", ActivitySocial, uid, "", storage.Conversation{}, "")
	s.broadcastMoment(protocol.EvMomentNew, m)
}

func (s *Server) opMomentTimeline(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.MomentTimelineReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	v, err := s.newViewer(uid)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	authors := []string{uid}
	for f := range v.friends {
		authors = append(authors, f)
	}
	var from storage.MomentCursor
	if req.Cursor != nil {
		from = storage.MomentCursor{At: req.Cursor.At, ID: req.Cursor.ID}
	}
	page, err := s.st.MomentsBy(authors, uid, from, protocol.TimelinePageSize)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	ms, err := s.renderMoments(v, page.Moments)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	// Reading the first page is reading what is new (spec moments / 朋友圈未读).
	if req.Cursor == nil {
		if err := s.st.SetMomentsSeenAt(uid, storage.Now()); err != nil {
			s.log.Warn("moments seen", "err", err)
		}
	}
	s.reply(c, id, protocol.MomentTimelineRes{Moments: ms, Next: toProtoCursor(page.Next)})
}

func (s *Server) opMomentDelete(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.MomentRef
	if err := protocol.DecodePayload(p.Data, &req); err != nil || req.MomentID == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	m, _, ok := s.visibleMoment(c, id, uid, req.MomentID)
	if !ok {
		return
	}
	if m.AuthorID != uid {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeForbidden))
		return
	}
	if err := s.removeMoment(m); err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.activity("moment.deleted", ActivitySocial, uid, "", storage.Conversation{}, "")
	s.reply(c, id, struct{}{})
}

// removeMoment deletes m and tells everyone who could see it.
func (s *Server) removeMoment(m storage.Moment) error {
	audience := s.momentAudience(m)
	if err := s.st.DeleteMoment(m.ID); err != nil {
		return err
	}
	s.pushTo(audience, protocol.EvMomentDeleted, protocol.MomentDeletedEvent{MomentID: m.ID})
	return nil
}

func (s *Server) opMomentLike(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.MomentLikeReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || req.MomentID == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	m, _, ok := s.visibleMoment(c, id, uid, req.MomentID)
	if !ok {
		return
	}
	if ok, wait := s.interactLimit.Allow(uid); !ok {
		s.failRateLimited(c, id, wait)
		return
	}
	likes, err := s.st.LikesOf([]string{m.ID})
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	had := false
	for _, l := range likes[m.ID] {
		had = had || l.UserID == uid
	}
	now := storage.Now()
	if err := s.st.SetLike(storage.Like{MomentID: m.ID, UserID: uid, CreatedAt: now}, req.Like); err != nil {
		s.failStorage(c, id, err)
		return
	}
	pm, _, err := s.renderMomentFor(uid, m)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, pm)
	if req.Like == had {
		return
	}
	if req.Like {
		s.activity("moment.liked", ActivitySocial, uid, m.AuthorID, storage.Conversation{}, "")
		s.notify(storage.Notification{UserID: m.AuthorID, Kind: protocol.NotifyLike, ActorID: uid, MomentID: m.ID, CreatedAt: now})
	}
	s.broadcastMoment(protocol.EvMomentUpdated, m)
}

func (s *Server) opMomentComment(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.MomentCommentReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || req.MomentID == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" || !protocol.ValidProfileText(body, protocol.MaxCommentLen) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	m, _, ok := s.visibleMoment(c, id, uid, req.MomentID)
	if !ok {
		return
	}
	var target storage.Comment
	if req.ReplyTo != "" {
		t, err := s.st.CommentByID(req.ReplyTo)
		if err != nil || t.MomentID != m.ID {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
			return
		}
		target = t
	}
	if s.failIfMuted(c, id, uid) {
		return
	}
	if ok, wait := s.interactLimit.Allow(uid); !ok {
		s.failRateLimited(c, id, wait)
		return
	}
	cm := storage.Comment{ID: newID("cm_"), MomentID: m.ID, AuthorID: uid, ReplyTo: target.ID, ReplyToUser: target.AuthorID, Body: body, CreatedAt: storage.Now()}
	if err := s.st.AddComment(cm); err != nil {
		s.failStorage(c, id, err)
		return
	}
	pm, _, err := s.renderMomentFor(uid, m)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, pm)

	// The author hears of any comment; the person replied to hears of the
	// reply (spec moments / 互动通知).
	s.activity("moment.commented", ActivitySocial, uid, m.AuthorID, storage.Conversation{}, "")
	s.notify(storage.Notification{UserID: m.AuthorID, Kind: protocol.NotifyComment, ActorID: uid, MomentID: m.ID, CommentID: cm.ID, Body: body, CreatedAt: cm.CreatedAt})
	if target.AuthorID != "" && target.AuthorID != m.AuthorID {
		s.notify(storage.Notification{UserID: target.AuthorID, Kind: protocol.NotifyReply, ActorID: uid, MomentID: m.ID, CommentID: cm.ID, Body: body, CreatedAt: cm.CreatedAt})
	}
	s.broadcastMoment(protocol.EvMomentUpdated, m)
}

func (s *Server) opMomentCommentDelete(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.CommentRef
	if err := protocol.DecodePayload(p.Data, &req); err != nil || req.CommentID == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	cm, err := s.st.CommentByID(req.CommentID)
	if errors.Is(err, storage.ErrNotFound) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeNotFound))
		return
	}
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	m, _, ok := s.visibleMoment(c, id, uid, cm.MomentID)
	if !ok {
		return
	}
	if cm.AuthorID != uid && m.AuthorID != uid {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeForbidden))
		return
	}
	if err := s.st.DeleteComment(cm.ID); err != nil {
		s.failStorage(c, id, err)
		return
	}
	pm, _, err := s.renderMomentFor(uid, m)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, pm)
	s.broadcastMoment(protocol.EvMomentUpdated, m)
}

// notify stores a notification and pushes it to its recipient. Nobody is
// told about their own actions.
func (s *Server) notify(n storage.Notification) {
	if n.UserID == n.ActorID {
		return
	}
	stored, err := s.st.AddNotification(n)
	if err != nil {
		s.log.Warn("add notification", "err", err)
		return
	}
	visible, err := s.st.NotificationFor(n.UserID, stored.ID)
	if errors.Is(err, storage.ErrNotFound) {
		return
	}
	if err != nil {
		s.log.Warn("read notification visibility", "err", err)
		return
	}
	s.pushTo([]string{n.UserID}, protocol.EvMomentNotify, renderNotification(s.newUserCache(), visible))
}

// excerptLen is how much of a moment a notification quotes.
const excerptLen = 40

func renderNotification(users *userCache, n storage.Notification) protocol.Notification {
	excerpt := []rune(strings.ReplaceAll(n.MomentBody, "\n", " "))
	if len(excerpt) > excerptLen {
		excerpt = append(excerpt[:excerptLen], '…')
	}
	return protocol.Notification{
		ID: n.ID, Kind: n.Kind, Actor: users.get(n.ActorID), MomentID: n.MomentID,
		Body: n.Body, Excerpt: string(excerpt), CreatedAt: n.CreatedAt, Read: n.Read,
	}
}

func (s *Server) opMomentNotifications(c *Conn, id string) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	list, err := s.st.Notifications(uid, 100)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	users := s.newUserCache()
	actorIDs := make([]string, 0, len(list))
	for _, n := range list {
		actorIDs = append(actorIDs, n.ActorID)
	}
	users.load(actorIDs)
	res := protocol.NotificationsRes{Notifications: make([]protocol.Notification, 0, len(list))}
	for _, n := range list {
		res.Notifications = append(res.Notifications, renderNotification(users, n))
	}
	s.reply(c, id, res)
}

func (s *Server) opMomentNotifRead(c *Conn, id string) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	if err := s.st.MarkNotificationsRead(uid); err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, struct{}{})
}

func (s *Server) opMomentUnread(c *Conn, id string) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	res, err := s.unreadFor(uid)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, res)
}

func (s *Server) unreadFor(uid string) (protocol.UnreadRes, error) {
	var res protocol.UnreadRes
	friends, err := s.st.FriendIDs(uid)
	if err != nil {
		return res, err
	}
	seen, err := s.st.MomentsSeenAt(uid)
	if err != nil {
		return res, err
	}
	if res.Moments, err = s.st.CountMomentsSince(friends, seen); err != nil {
		return res, err
	}
	if res.Notifications, err = s.st.UnreadNotifications(uid); err != nil {
		return res, err
	}
	reqs, err := s.st.IncomingFriendRequests(uid)
	if err != nil {
		return res, err
	}
	res.Requests = len(reqs)
	return res, nil
}
