package server

import (
	"errors"
	"strings"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Friends (spec friends).

// friendTarget decodes a FriendUserReq and resolves it as visible to uid.
func (s *Server) friendTarget(c *Conn, id, uid string, p protocol.ReqPayload) (storage.User, bool) {
	var req protocol.FriendUserReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || strings.TrimSpace(req.Username) == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return storage.User{}, false
	}
	u, perr := s.lookupVisibleUser(uid, req.Username)
	if perr != nil {
		s.fail(c, id, perr)
		return storage.User{}, false
	}
	if u.ID == uid {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return storage.User{}, false
	}
	return u, true
}

func (s *Server) opFriendRequest(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.FriendRequestReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || strings.TrimSpace(req.Username) == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	note := strings.TrimSpace(req.Note)
	if !protocol.ValidProfileText(note, protocol.MaxFriendNoteLen) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	target, perr := s.lookupVisibleUser(uid, req.Username)
	if perr != nil {
		s.fail(c, id, perr)
		return
	}
	if target.ID == uid {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if already, err := s.st.AreFriends(uid, target.ID); err != nil {
		s.failStorage(c, id, err)
		return
	} else if already {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeAlreadyFriends))
		return
	}
	if ok, wait := s.friendLimit.Allow(uid); !ok {
		s.failRateLimited(c, id, wait)
		return
	}
	now := storage.Now()

	// They already asked us: asking back is accepting
	// (spec friends / 申请的合并与互相申请).
	if _, err := s.st.FriendRequestBetween(target.ID, uid); err == nil {
		if err := s.st.AddFriendship(uid, target.ID, now); err != nil {
			s.failStorage(c, id, err)
			return
		}
		friend := s.userByID(target.ID)
		s.reply(c, id, protocol.FriendRequestRes{Result: protocol.FriendRequestAdded, Friend: &friend})
		s.announceFriendship(uid, target.ID)
		return
	} else if !errors.Is(err, storage.ErrNotFound) {
		s.failStorage(c, id, err)
		return
	}

	fr, err := s.st.SaveFriendRequest(storage.FriendRequest{FromID: uid, ToID: target.ID, Note: note, CreatedAt: now})
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, protocol.FriendRequestRes{Result: protocol.FriendRequestSent})
	s.log.Info("friend requested", "from", s.userName(uid), "to", target.Username)
	s.pushTo([]string{target.ID}, protocol.EvFriendRequested, protocol.FriendRequest{
		ID: fr.ID, From: s.userByID(uid), Note: fr.Note, CreatedAt: fr.CreatedAt,
	})
}

// announceFriendship tells both sides, each about the other.
func (s *Server) announceFriendship(a, b string) {
	s.pushTo([]string{a}, protocol.EvFriendAdded, s.userByID(b))
	s.pushTo([]string{b}, protocol.EvFriendAdded, s.userByID(a))
	s.log.Info("friends", "a", s.userName(a), "b", s.userName(b))
}

func (s *Server) opFriendRespond(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.FriendRespondReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || req.RequestID == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	fr, err := s.st.FriendRequestByID(req.RequestID)
	// Someone else's request reads as missing: its existence is not ours to see.
	if errors.Is(err, storage.ErrNotFound) || (err == nil && fr.ToID != uid) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeNotFound))
		return
	}
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	if !req.Accept {
		if err := s.st.DeleteFriendRequests(fr.FromID, uid); err != nil {
			s.failStorage(c, id, err)
			return
		}
		s.reply(c, id, struct{}{})
		return
	}
	if err := s.st.AddFriendship(fr.FromID, uid, storage.Now()); err != nil {
		s.failStorage(c, id, err)
		return
	}
	friend := s.userByID(fr.FromID)
	s.reply(c, id, protocol.FriendRequestRes{Result: protocol.FriendRequestAdded, Friend: &friend})
	s.announceFriendship(uid, fr.FromID)
}

func (s *Server) opFriendList(c *Conn, id string) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	res, err := s.friendList(uid)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, res)
}

func (s *Server) friendList(uid string) (protocol.FriendListRes, error) {
	res := protocol.FriendListRes{Friends: []protocol.User{}, Incoming: []protocol.FriendRequest{}, Blocked: []protocol.User{}}
	ids, err := s.st.FriendIDs(uid)
	if err != nil {
		return res, err
	}
	if res.Friends, err = s.usersByID(ids); err != nil {
		return res, err
	}
	sortUsers(res.Friends)

	reqs, err := s.st.IncomingFriendRequests(uid)
	if err != nil {
		return res, err
	}
	users := s.newUserCache()
	for _, r := range reqs {
		users.load([]string{r.FromID})
	}
	for _, r := range reqs {
		res.Incoming = append(res.Incoming, protocol.FriendRequest{ID: r.ID, From: users.get(r.FromID), Note: r.Note, CreatedAt: r.CreatedAt})
	}

	blocked, err := s.st.BlockedIDs(uid)
	if err != nil {
		return res, err
	}
	if res.Blocked, err = s.usersByID(blocked); err != nil {
		return res, err
	}
	removed, err := s.st.RemovedByIDs(uid)
	if err != nil {
		return res, err
	}
	if res.RemovedBy, err = s.usersByID(removed); err != nil {
		return res, err
	}

	// The plaza: everyone listed, minus yourself, your friends, and anyone
	// blocked either way.
	plaza, err := s.st.PlazaUserIDs()
	if err != nil {
		return res, err
	}
	skip := map[string]bool{uid: true}
	for _, id := range ids {
		skip[id] = true
	}
	var listed []string
	for _, id := range plaza {
		if skip[id] {
			continue
		}
		if b, _ := s.st.IsBlocked(uid, id); b {
			continue
		}
		listed = append(listed, id)
	}
	if res.Plaza, err = s.usersByID(listed); err != nil {
		return res, err
	}
	sortUsers(res.Plaza)
	return res, nil
}

// sortUsers orders users by display name, case-insensitively.
func sortUsers(us []protocol.User) {
	for i := 1; i < len(us); i++ {
		for j := i; j > 0 && strings.ToLower(us[j].DisplayName()) < strings.ToLower(us[j-1].DisplayName()); j-- {
			us[j], us[j-1] = us[j-1], us[j]
		}
	}
}

// endFriendship tells both sides a friendship is over. remover's devices drop
// the friend; removed lists them as "removed you" when shown is true.
func (s *Server) endFriendship(remover, removed string, shown bool) {
	s.pushTo([]string{remover}, protocol.EvFriendRemoved, protocol.FriendRemovedEvent{UserID: removed})
	s.pushTo([]string{removed}, protocol.EvFriendRemoved, protocol.FriendRemovedEvent{UserID: remover, ByThem: shown})
}

func (s *Server) opFriendRemove(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	target, ok := s.friendTarget(c, id, uid, p)
	if !ok {
		return
	}
	wereFriends, _ := s.st.AreFriends(uid, target.ID)
	if err := s.st.RemoveFriendship(uid, target.ID); err != nil {
		s.failStorage(c, id, err)
		return
	}
	// Removing someone who removed you only clears them from your list.
	_ = s.st.DeleteRemoval(uid, target.ID)
	if wereFriends {
		if err := s.st.AddRemoval(target.ID, uid, storage.Now()); err != nil {
			s.failStorage(c, id, err)
			return
		}
	}
	s.reply(c, id, struct{}{})
	if wereFriends {
		s.endFriendship(uid, target.ID, true)
	}
}

func (s *Server) opFriendBlock(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.FriendUserReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || strings.TrimSpace(req.Username) == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	// Blocking someone who blocked you still works, so resolve without the
	// block check.
	target, err := s.st.UserByUsernameKey(strings.ToLower(strings.TrimSpace(req.Username)))
	if errors.Is(err, storage.ErrNotFound) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeUserNotFound))
		return
	}
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	if target.ID == uid {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	wereFriends, _ := s.st.AreFriends(uid, target.ID)
	if err := s.st.Block(uid, target.ID, storage.Now()); err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, struct{}{})
	s.log.Info("blocked", "user", s.userName(uid), "target", target.Username)
	if wereFriends {
		s.endFriendship(uid, target.ID, false)
	}
}

func (s *Server) opFriendUnblock(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.FriendUserReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	target, err := s.st.UserByUsernameKey(strings.ToLower(strings.TrimSpace(req.Username)))
	if errors.Is(err, storage.ErrNotFound) {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeUserNotFound))
		return
	}
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	if err := s.st.Unblock(uid, target.ID); err != nil {
		s.failStorage(c, id, err)
		return
	}
	s.reply(c, id, struct{}{})
}
