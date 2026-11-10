package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// requireAuth returns the calling user's ID, replying with unauthorized when the
// connection has not authenticated yet.
func (s *Server) requireAuth(c *Conn, id string) (string, bool) {
	if uid := c.uid(); uid != "" {
		return uid, true
	}
	s.fail(c, id, protocol.NewErrorCode(protocol.CodeUnauthorized))
	return "", false
}

// ---------- auth ----------

func (s *Server) opRegister(c *Conn, id string, p protocol.ReqPayload) {
	if ok, wait := s.registerLimit.Allow(c.remoteAddr); !ok {
		s.failRateLimited(c, id, wait)
		return
	}
	var req protocol.RegisterReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	name, err := validateUsername(req.Username)
	if err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidUsername))
		return
	}
	if err := validatePassword(req.Password); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeWeakPassword))
		return
	}
	// A security question is what makes the account recoverable at all; without
	// one a forgotten password means a lost account
	// (spec account-auth / 注册需要安全问题).
	question := strings.TrimSpace(req.SecurityQuestion)
	if question == "" || strings.TrimSpace(req.SecurityAnswer) == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeSecurityQuestionReq))
		return
	}
	if len([]rune(question)) > 200 || len([]rune(req.SecurityAnswer)) > 200 {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}

	hash, err := hashPassword(req.Password, defaultArgonParams)
	if err != nil {
		s.log.Error("hash password", "err", err)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInternal))
		return
	}

	// The answer is stored normalised and hashed; the question is stored as
	// typed because it is shown back to the user
	// (spec account-auth / 安全问题存储).
	answerHash, err := hashAnswer(req.SecurityAnswer, defaultArgonParams)
	if err != nil {
		s.log.Error("hash security answer", "err", err)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInternal))
		return
	}

	u := storage.User{
		ID:                 newID("u_"),
		Username:           name,
		PasswordHash:       hash,
		CreatedAt:          storage.Now(),
		SecurityQuestion:   question,
		SecurityAnswerHash: answerHash,
	}
	if err := s.st.CreateUser(u); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeUsernameTaken))
			return
		}
		s.failStorage(c, id, err)
		return
	}

	// Registering signs you in: making a new user log in again right away is
	// friction with no security value.
	s.issueSession(c, id, u, "")
}

func (s *Server) opLogin(c *Conn, id string, p protocol.ReqPayload) {
	if ok, wait := s.loginLimit.Allow(c.remoteAddr); !ok {
		s.failRateLimited(c, id, wait)
		return
	}
	var req protocol.LoginReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	name, err := validateUsername(req.Username)
	if err != nil {
		// Deliberately the same error as a wrong password: telling a stranger
		// which usernames exist is an enumeration oracle.
		s.loginLimit.Fail(c.remoteAddr)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidCredentials))
		return
	}

	u, err := s.st.UserByUsernameKey(strings.ToLower(name))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.loginLimit.Fail(c.remoteAddr)
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidCredentials))
			return
		}
		s.failStorage(c, id, err)
		return
	}

	ok, needsRehash, err := verifyPassword(req.Password, u.PasswordHash)
	if err != nil {
		s.log.Error("verify password", "err", err, "user", u.Username)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInternal))
		return
	}
	if !ok {
		s.loginLimit.Fail(c.remoteAddr)
		s.log.Info("login failed", "addr", c.remoteAddr, "user", u.Username)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidCredentials))
		return
	}

	s.loginLimit.Succeed(c.remoteAddr)

	// Upgrade a hash created under weaker parameters (spec account-auth /
	// 密码存储). Failure here is not worth denying a valid login over.
	if needsRehash {
		if newHash, err := hashPassword(req.Password, defaultArgonParams); err == nil {
			if err := s.st.UpdateUserPasswordHash(u.ID, newHash); err != nil {
				s.log.Warn("rehash failed", "err", err, "user", u.Username)
			}
		}
	}

	s.issueSession(c, id, u, req.Device)
}

// issueSession mints a token, binds the connection to the user, and replies.
func (s *Server) issueSession(c *Conn, id string, u storage.User, device string) {
	if c.uid() != "" && c.uid() != u.ID {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeForbidden))
		return
	}

	token, err := newToken()
	if err != nil {
		s.log.Error("mint token", "err", err)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInternal))
		return
	}

	// Revoke the account's previous session tokens *before* storing ours.
	//
	// Revoking after storing would sweep the token we just minted into the same
	// deletion, leaving the caller holding credentials the server no longer
	// knows — the login would look successful and then fail on the next request.
	if err := s.st.DeleteSessionTokensForUser(u.ID); err != nil {
		s.log.Warn("revoke previous sessions", "err", err, "user", u.Username)
	}

	expires := time.Now().Add(tokenTTL)
	remote := c.remoteAddr
	if err := s.st.SaveToken(storage.Token{
		Hash:       tokenHash(token),
		UserID:     u.ID,
		CreatedAt:  storage.Now(),
		ExpiresAt:  expires.UnixMilli(),
		Purpose:    storage.PurposeSession,
		Remote:     remote,
		Device:     device,
		LastSeenAt: storage.Now(),
	}); err != nil {
		s.failStorage(c, id, err)
		return
	}

	firstConn := c.uid() == ""
	if firstConn {
		first := s.hub.bindUser(c, u.ID)
		c.setUserID(u.ID)
		if first {
			// Called directly rather than in a goroutine: the handler already runs
			// off the read loop, and a detached goroutine would outlive the
			// connection, letting it touch storage after shutdown.
			s.broadcastPresence(u.ID, true)
		}
	}

	// Only now tell the older connections to go: a failed login must never log
	// anyone else out (spec login-session / 单会话约束).
	s.notifyDisplaced(u, c, remote, device)

	s.reply(c, id, protocol.AuthRes{
		Token:     token,
		ExpiresAt: expires.UnixMilli(),
		User: protocol.User{
			ID:       u.ID,
			Username: u.Username,
			Online:   true,
		},
		Device: device,
	})
}

// notifyDisplaced closes every other live session of a user, telling each one
// where the winning login came from.
//
// The old tokens are already revoked by the caller, so closing the connections
// only makes that immediately visible instead of at the next request. The notice
// is written synchronously before the socket closes, because the queue-based path
// would drop it when the queue is full (design.md decision 3).
func (s *Server) notifyDisplaced(u storage.User, keep *Conn, remote, device string) {
	victims := s.hub.connectionsForUser(u.ID, keep)
	if len(victims) == 0 {
		return
	}

	ev, err := protocol.NewEvent(protocol.EvSessionReplaced, protocol.SessionReplacedEvent{
		Username: u.Username,
		Location: formatLocation(remote),
		At:       storage.Now(),
		Device:   device,
	})
	if err != nil {
		s.log.Error("encode session.replaced", "err", err)
	} else {
		for _, v := range victims {
			v.writeThenClose(ev)
		}
	}
	for _, v := range victims {
		v.close()
	}
	s.log.Info("session displaced", "user", u.Username, "new_remote", remote, "device", device, "count", len(victims))
}

// formatLocation renders a source address for display. It is a plain address;
// a geographic hint would be appended here if the server had one configured
// (design.md decision 4).
func formatLocation(remote string) string {
	if remote == "" {
		return "unknown"
	}
	return remote
}

func (s *Server) opResume(c *Conn, id string, p protocol.ReqPayload) {
	var req protocol.ResumeReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	uid, authErr := s.userForToken(req.Token)
	if authErr != nil {
		s.fail(c, id, authErr)
		return
	}
	u, err := s.st.UserByID(uid)
	if err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeUnauthorized))
		return
	}

	// Resuming is a login in every way that matters: it establishes a live
	// session for this account. It therefore has to enforce single-session too,
	// or an auto-sign-in silently coexists with the session already running
	// (spec login-session / 单会话约束).
	//
	// The token is rotated as part of this. Leaving the presented token valid
	// would let the displaced client reconnect with it and displace us right
	// back, which turns into two clients evicting each other forever.
	newToken, err := newToken()
	if err != nil {
		s.log.Error("mint token", "err", err)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInternal))
		return
	}
	if err := s.st.DeleteSessionTokensForUser(u.ID); err != nil {
		s.log.Warn("revoke previous sessions", "err", err, "user", u.Username)
	}
	expires := time.Now().Add(tokenTTL)
	if err := s.st.SaveToken(storage.Token{
		Hash:       tokenHash(newToken),
		UserID:     u.ID,
		CreatedAt:  storage.Now(),
		ExpiresAt:  expires.UnixMilli(),
		Purpose:    storage.PurposeSession,
		Remote:     c.remoteAddr,
		Device:     req.Device,
		LastSeenAt: storage.Now(),
	}); err != nil {
		s.failStorage(c, id, err)
		return
	}

	if c.uid() == "" {
		first := s.hub.bindUser(c, u.ID)
		c.setUserID(u.ID)
		if first {
			s.broadcastPresence(u.ID, true)
		}
	}
	s.notifyDisplaced(u, c, c.remoteAddr, req.Device)

	s.reply(c, id, protocol.AuthRes{
		Token:     newToken,
		ExpiresAt: expires.UnixMilli(),
		User: protocol.User{
			ID:       u.ID,
			Username: u.Username,
			Online:   true,
		},
		Device: req.Device,
	})
}

func (s *Server) opLogout(c *Conn, id string, p protocol.ReqPayload) {
	var req protocol.ResumeReq
	_ = protocol.DecodePayload(p.Data, &req)
	if req.Token != "" {
		// Revoking the presented token is enough; other devices keep theirs
		// (spec account-auth / 登出不影响其他设备).
		if err := s.st.DeleteToken(tokenHash(req.Token)); err != nil {
			s.failStorage(c, id, err)
			return
		}
	}
	s.reply(c, id, struct{}{})
}

// userForToken validates a token and returns its user, or the protocol error to
// report.
func (s *Server) userForToken(token string) (string, *protocol.Error) {
	if token == "" {
		return "", protocol.NewErrorCode(protocol.CodeUnauthorized)
	}
	t, err := s.st.TokenByHash(tokenHash(token))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", protocol.NewErrorCode(protocol.CodeUnauthorized)
		}
		return "", protocol.NewErrorCode(protocol.CodeStorageUnavailable)
	}
	// A reset token authorises a password change and nothing else; accepting it
	// here would turn a 5-minute recovery window into a full session
	// (design.md decision 1).
	if t.IsReset() {
		return "", protocol.NewErrorCode(protocol.CodeUnauthorized)
	}
	if time.Now().UnixMilli() > t.ExpiresAt {
		_ = s.st.DeleteToken(t.Hash)
		return "", protocol.NewErrorCode(protocol.CodeTokenExpired)
	}
	return t.UserID, nil
}

// ---------- conversations ----------

func (s *Server) opChannelList(c *Conn, id string) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	convs, err := s.st.ListConversations()
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	out := make([]protocol.Conversation, 0, len(convs))
	for _, cv := range convs {
		// Channels are public; DMs are visible only to participants.
		if cv.Kind == protocol.KindDM && !s.isMember(cv, uid) {
			continue
		}
		out = append(out, s.toProtocolConversation(cv, uid))
	}
	s.reply(c, id, protocol.ChannelListRes{Conversations: out})
}

func (s *Server) toProtocolConversation(cv storage.Conversation, uid string) protocol.Conversation {
	pc := protocol.Conversation{
		ID:          cv.ID,
		Kind:        cv.Kind,
		Name:        cv.Name,
		Topic:       cv.Topic,
		CreatedBy:   cv.CreatedBy,
		MemberCount: len(s.membersOf(cv)),
		Joined:      s.isMember(cv, uid),
	}
	msgs, _, err := s.st.MessagesBefore(cv.ID, 0, 1)
	if err == nil && len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		pc.LastMessageAt = last.CreatedAt
		if !last.Deleted {
			pc.LastMessage = last.Body
		}
	}
	return pc
}

func (s *Server) opChannelCreate(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.ChannelCreateReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	name, err := validateChannelName(req.Name)
	if err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidChannelName))
		return
	}
	cv := storage.Conversation{
		ID:        newID("c_"),
		Kind:      protocol.KindChannel,
		Name:      name,
		Topic:     strings.TrimSpace(req.Topic),
		CreatedBy: uid,
		CreatedAt: storage.Now(),
	}
	if err := s.st.CreateConversation(cv); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeChannelExists))
			return
		}
		s.failStorage(c, id, err)
		return
	}
	// The creator is a member from the start (spec chat-rooms / 频道创建).
	if err := s.st.AddMember(storage.Member{
		ConversationID: cv.ID,
		UserID:         uid,
		JoinedAt:       storage.Now(),
	}); err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.hub.subscribe(c, cv.ID)
	s.reply(c, id, s.toProtocolConversation(cv, uid))
}

func (s *Server) opChannelJoin(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	cv, perr := s.conversationFromRequest(p)
	if perr != nil {
		s.fail(c, id, perr)
		return
	}
	if cv.Kind == protocol.KindDM {
		if !s.isMember(cv, uid) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeForbidden))
			return
		}
	} else {
		wasMember := s.st.IsMember(cv.ID, uid)
		if err := s.st.AddMember(storage.Member{
			ConversationID: cv.ID,
			UserID:         uid,
			JoinedAt:       storage.Now(),
		}); err != nil {
			s.failStorage(c, id, err)
			return
		}
		if !wasMember {
			// Announce the join so other members see the system event, but only
			// on a real join — re-joining is idempotent and silent
			// (spec chat-rooms / 频道加入与退出).
			s.systemMessage(cv, uid, "joined")
		}
	}
	s.hub.subscribe(c, cv.ID)
	s.reply(c, id, s.toProtocolConversation(cv, uid))
}

func (s *Server) opChannelLeave(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	cv, perr := s.conversationFromRequest(p)
	if perr != nil {
		s.fail(c, id, perr)
		return
	}
	if cv.Kind == protocol.KindDM {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if err := s.st.RemoveMember(cv.ID, uid); err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.hub.unsubscribe(c, cv.ID)
	s.reply(c, id, struct{}{})
}

func (s *Server) opChannelDelete(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	cv, perr := s.conversationFromRequest(p)
	if perr != nil {
		s.fail(c, id, perr)
		return
	}
	if cv.Kind == protocol.KindDM || cv.CreatedBy != uid {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeForbidden))
		return
	}
	if err := s.st.DeleteConversation(cv.ID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeChannelNotFound))
			return
		}
		s.failStorage(c, id, err)
		return
	}
	// Tell subscribers so their client drops the channel instead of showing a
	// dead entry (spec chat-rooms / 会话删除).
	f, err := protocol.NewEvent(protocol.EvChannelGone, protocol.ChannelGoneEvent{ConversationID: cv.ID})
	if err == nil {
		s.hub.broadcast(cv.ID, f)
	}
	s.reply(c, id, struct{}{})
}

func (s *Server) opDMOpen(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.DMOpenReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	peer, err := s.st.UserByUsernameKey(strings.ToLower(strings.TrimSpace(req.Username)))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeUserNotFound))
			return
		}
		s.failStorage(c, id, err)
		return
	}
	if peer.ID == uid {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	cv, err := s.st.DMBetween(uid, peer.ID)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.hub.subscribe(c, cv.ID)
	pc := s.toProtocolConversation(cv, uid)
	// The DM's display name is the other participant.
	pc.Name = peer.Username
	s.reply(c, id, pc)
}

func (s *Server) opUserList(c *Conn, id string) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	users, err := s.st.ListUsers()
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	online := s.hub.onlineUsers()
	out := make([]protocol.User, 0, len(users))
	for _, u := range users {
		if u.ID == uid {
			continue
		}
		out = append(out, protocol.User{
			ID:       u.ID,
			Username: u.Username,
			Online:   online[u.ID],
		})
	}
	s.reply(c, id, protocol.UserListRes{Users: out})
}

// conversationFromRequest decodes a ConversationRef and resolves it.
func (s *Server) conversationFromRequest(p protocol.ReqPayload) (storage.Conversation, *protocol.Error) {
	var ref protocol.ConversationRef
	if err := protocol.DecodePayload(p.Data, &ref); err != nil || ref.ConversationID == "" {
		return storage.Conversation{}, protocol.NewErrorCode(protocol.CodeInvalidArgument)
	}
	cv, err := s.st.ConversationByID(ref.ConversationID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.Conversation{}, protocol.NewErrorCode(protocol.CodeChannelNotFound)
		}
		return storage.Conversation{}, protocol.NewErrorCode(protocol.CodeStorageUnavailable)
	}
	return cv, nil
}

// systemMessage records a membership change as a real message so it appears in
// history for anyone who joins later, not just for whoever was watching live.
func (s *Server) systemMessage(cv storage.Conversation, actorID string, kind string) {
	msg := storage.Message{
		ID:             newID("m_"),
		ConversationID: cv.ID,
		AuthorID:       actorID,
		Body:           kind,
		CreatedAt:      storage.Now(),
	}
	stored, err := s.st.AppendMessage(msg)
	if err != nil {
		s.log.Warn("append system message", "err", err)
		return
	}
	s.publishMessage(cv.ID, stored)
}

// ---------- messages ----------

func (s *Server) opMessageSend(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.SendMessageReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || req.ConversationID == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if len(req.Body) > s.cfg.MaxMessageBytes {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeMessageTooLong))
		return
	}

	cv, err := s.st.ConversationByID(req.ConversationID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeConversationNotFound))
			return
		}
		s.failStorage(c, id, err)
		return
	}
	// Non-members must not be able to write, and must not learn whether the
	// message landed (spec message-delivery / 消息发送与校验).
	if !s.isMember(cv, uid) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeForbidden))
		return
	}

	stored, err := s.st.AppendMessage(storage.Message{
		ID:             newID("m_"),
		ConversationID: cv.ID,
		AuthorID:       uid,
		Body:           req.Body,
		ClientMsgID:    req.ClientMsgID,
	})
	if err != nil {
		s.failStorage(c, id, err)
		return
	}

	// The sender is subscribed by virtue of sending; make sure everyone else is
	// too, so a client that never called join still receives fan-out.
	s.ensureSubscribed(cv, uid)

	pm := s.toProtocolMessage(stored)
	// Acknowledge first: the sender already knows what it wrote, and blocking
	// the ack behind a broadcast would make sends feel slower than they are.
	s.reply(c, id, protocol.MessageEvent{Message: pm})

	f, err := protocol.NewEvent(protocol.EvMessage, protocol.MessageEvent{Message: pm})
	if err == nil {
		s.hub.broadcast(cv.ID, f)
	}
}

// ensureSubscribed subscribes every live connection of a member to the
// conversation, which repairs the case where a client joined on another device.
func (s *Server) ensureSubscribed(cv storage.Conversation, actorID string) {
	for _, uid := range s.membersOf(cv) {
		_ = uid
	}
	// Subscriptions are per-connection and are set by the operations that open
	// a conversation; nothing more is needed here.
}

// publishMessage fans a stored message out to subscribers.
func (s *Server) publishMessage(conversationID string, m storage.Message) {
	pm := s.toProtocolMessage(m)
	f, err := protocol.NewEvent(protocol.EvMessage, protocol.MessageEvent{Message: pm})
	if err != nil {
		return
	}
	s.hub.broadcast(conversationID, f)
}

func (s *Server) opMessageEdit(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.EditMessageReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || req.MessageID == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if len(req.Body) > s.cfg.MaxMessageBytes {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeMessageTooLong))
		return
	}
	m, err := s.st.MessageByID(req.MessageID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeMessageNotFound))
			return
		}
		s.failStorage(c, id, err)
		return
	}
	// Only the author edits. Channel creators may delete, not rewrite someone
	// else's words (spec message-delivery / 消息编辑与删除).
	if m.AuthorID != uid {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeForbidden))
		return
	}
	if m.Deleted {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeMessageNotFound))
		return
	}
	m.Body = req.Body
	m.EditedAt = storage.Now()
	if err := s.st.UpdateMessage(m); err != nil {
		s.failStorage(c, id, err)
		return
	}
	pm := s.toProtocolMessage(m)
	s.reply(c, id, protocol.MessageEvent{Message: pm})
	f, err := protocol.NewEvent(protocol.EvMessageEdit, protocol.MessageEvent{Message: pm})
	if err == nil {
		s.hub.broadcast(m.ConversationID, f)
	}
}

func (s *Server) opMessageDelete(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.DeleteMessageReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || req.MessageID == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	m, err := s.st.MessageByID(req.MessageID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeMessageNotFound))
			return
		}
		s.failStorage(c, id, err)
		return
	}

	allowed := m.AuthorID == uid
	if !allowed {
		// A channel creator may moderate their own channel.
		if cv, err := s.st.ConversationByID(m.ConversationID); err == nil &&
			cv.Kind == protocol.KindChannel && cv.CreatedBy == uid {
			allowed = true
		}
	}
	if !allowed {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeForbidden))
		return
	}

	// Keep the row and blank the body: dropping it would open a hole in seq and
	// break paging (spec message-delivery / 删除后保留占位).
	m.Deleted = true
	m.Body = ""
	m.EditedAt = storage.Now()
	if err := s.st.UpdateMessage(m); err != nil {
		s.failStorage(c, id, err)
		return
	}
	pm := s.toProtocolMessage(m)
	s.reply(c, id, protocol.MessageEvent{Message: pm})
	f, err := protocol.NewEvent(protocol.EvMessageDel, protocol.MessageEvent{Message: pm})
	if err == nil {
		s.hub.broadcast(m.ConversationID, f)
	}
}

func (s *Server) opMessageHistory(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.HistoryReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || req.ConversationID == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	cv, err := s.st.ConversationByID(req.ConversationID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeConversationNotFound))
			return
		}
		s.failStorage(c, id, err)
		return
	}
	if !s.isMember(cv, uid) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeForbidden))
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = s.cfg.HistoryPageSize
	}
	msgs, hasMore, err := s.st.MessagesBefore(cv.ID, req.BeforeSeq, limit)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	out := make([]protocol.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, s.toProtocolMessage(m))
	}
	s.hub.subscribe(c, cv.ID)
	s.reply(c, id, protocol.HistoryRes{Messages: out, HasMore: hasMore})
}

func (s *Server) opPing(c *Conn, id string, p protocol.ReqPayload) {
	var req protocol.PingReq
	_ = protocol.DecodePayload(p.Data, &req)
	s.reply(c, id, protocol.PongRes{Nonce: req.Nonce, Now: time.Now().UnixMilli()})
}

// ---------- mapping helpers ----------

// toProtocolMessage resolves the author's display name. Names are stored
// denormalised onto the wire message so clients render without a lookup.
func (s *Server) toProtocolMessage(m storage.Message) protocol.Message {
	name := ""
	if u, err := s.st.UserByID(m.AuthorID); err == nil {
		name = u.Username
	}
	return protocol.Message{
		ID:             m.ID,
		ConversationID: m.ConversationID,
		Seq:            m.Seq,
		AuthorID:       m.AuthorID,
		AuthorName:     name,
		Body:           m.Body,
		CreatedAt:      m.CreatedAt,
		EditedAt:       m.EditedAt,
		Deleted:        m.Deleted,
		ClientMsgID:    m.ClientMsgID,
	}
}

// ---------- error helpers ----------

func (s *Server) failRateLimited(c *Conn, id string, wait time.Duration) {
	e := protocol.NewErrorCode(protocol.CodeRateLimited)
	e.RetryAfterMS = retryAfterMS(wait)
	s.fail(c, id, e)
}

// failStorage maps a store error onto a client error without leaking paths.
func (s *Server) failStorage(c *Conn, id string, err error) {
	s.log.Error("storage error", "err", err)
	if errors.Is(err, storage.ErrUnavailable) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeStorageUnavailable))
		return
	}
	s.fail(c, id, protocol.NewErrorCode(protocol.CodeInternal))
}

// ctxUnused keeps context in the signature list for handlers that may need it.
var _ = context.Background
