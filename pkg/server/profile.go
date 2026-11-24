package server

import (
	"errors"
	"strings"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Profiles (spec user-profile).

// publicUser is u as others see it: profile filled in, defaults for fields
// never set.
func publicUser(u storage.User, p storage.Profile, online bool) protocol.User {
	avatar, color := protocol.DefaultAvatar(u.Username)
	if p.Avatar != "" {
		avatar = p.Avatar
	}
	if p.Color != "" {
		color = p.Color
	}
	return protocol.User{
		ID: u.ID, Username: u.Username, Online: online,
		Nickname: p.Nickname, Avatar: avatar, Color: color,
		Bio: p.Bio, Gender: p.Gender, Age: p.Age,
	}
}

// usersByID resolves ids into public users, skipping any that no longer exist.
// The order of ids is kept.
func (s *Server) usersByID(ids []string) ([]protocol.User, error) {
	profiles, err := s.st.ProfilesOf(ids)
	if err != nil {
		return nil, err
	}
	online := s.hub.onlineUsers()
	out := make([]protocol.User, 0, len(ids))
	for _, id := range ids {
		u, err := s.st.UserByID(id)
		if err != nil {
			continue
		}
		out = append(out, publicUser(u, profiles[id], online[id]))
	}
	return out, nil
}

// userByID is usersByID for one user.
func (s *Server) userByID(id string) protocol.User {
	if us, err := s.usersByID([]string{id}); err == nil && len(us) == 1 {
		return us[0]
	}
	return protocol.User{ID: id}
}

// userCache resolves users once per request.
type userCache struct {
	s    *Server
	have map[string]protocol.User
}

func (s *Server) newUserCache() *userCache {
	return &userCache{s: s, have: map[string]protocol.User{}}
}

// load fetches every id not yet known in one go.
func (c *userCache) load(ids []string) {
	var need []string
	for _, id := range ids {
		if _, ok := c.have[id]; !ok && id != "" {
			need = append(need, id)
			c.have[id] = protocol.User{ID: id}
		}
	}
	if len(need) == 0 {
		return
	}
	us, err := c.s.usersByID(need)
	if err != nil {
		return
	}
	for _, u := range us {
		c.have[u.ID] = u
	}
}

func (c *userCache) get(id string) protocol.User {
	c.load([]string{id})
	return c.have[id]
}

// pushTo sends an event to every live connection of each user.
func (s *Server) pushTo(uids []string, name string, data any) {
	if len(uids) == 0 {
		return
	}
	f, err := protocol.NewEvent(name, data)
	if err != nil {
		s.log.Error("encode event", "event", name, "err", err)
		return
	}
	s.hub.broadcastToUsers(uids, f)
}

// lookupVisibleUser resolves a username for viewer. A user who blocked the
// viewer, or whom the viewer blocked, reads as not found, so a block is never
// revealed (spec friends / 拉黑).
func (s *Server) lookupVisibleUser(viewer, username string) (storage.User, *protocol.Error) {
	u, err := s.st.UserByUsernameKey(strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.User{}, protocol.NewErrorCode(protocol.CodeUserNotFound)
		}
		return storage.User{}, protocol.NewErrorCode(protocol.CodeStorageUnavailable)
	}
	if u.ID != viewer {
		if blocked, err := s.st.IsBlocked(viewer, u.ID); err != nil {
			return storage.User{}, protocol.NewErrorCode(protocol.CodeStorageUnavailable)
		} else if blocked {
			return storage.User{}, protocol.NewErrorCode(protocol.CodeUserNotFound)
		}
	}
	return u, nil
}

func (s *Server) opProfileGet(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.ProfileGetReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil || strings.TrimSpace(req.Username) == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	target, perr := s.lookupVisibleUser(uid, req.Username)
	if perr != nil {
		s.fail(c, id, perr)
		return
	}
	prof, err := s.st.ProfileOf(target.ID)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	res := protocol.Profile{
		User:      publicUser(target, prof, s.hub.onlineUsers()[target.ID]),
		Bio:       prof.Bio,
		CreatedAt: target.CreatedAt,
		IsSelf:    target.ID == uid,
		Moments:   []protocol.Moment{},
	}
	if !res.IsSelf {
		if res.IsFriend, err = s.st.AreFriends(uid, target.ID); err != nil {
			s.failStorage(c, id, err)
			return
		}
		if _, err := s.st.FriendRequestBetween(uid, target.ID); err == nil {
			res.RequestSent = true
		}
	}
	// Moments only for the owner and friends (spec user-profile / 查看主页).
	if res.IsSelf || res.IsFriend {
		var from storage.MomentCursor
		if req.Cursor != nil {
			from = storage.MomentCursor{At: req.Cursor.At, ID: req.Cursor.ID}
		}
		page, err := s.st.MomentsBy([]string{target.ID}, uid, from, protocol.ProfileMomentsPage)
		if err != nil {
			s.failStorage(c, id, err)
			return
		}
		v, err := s.newViewer(uid)
		if err != nil {
			s.failStorage(c, id, err)
			return
		}
		if res.Moments, err = s.renderMoments(v, page.Moments); err != nil {
			s.failStorage(c, id, err)
			return
		}
		res.Next = toProtoCursor(page.Next)
	}
	s.reply(c, id, res)
}

func (s *Server) opProfileUpdate(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.ProfileUpdateReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if s.failIfMuted(c, id, uid) {
		return
	}
	prof, err := s.st.ProfileOf(uid)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	bad := false
	if req.Nickname != nil {
		n := strings.TrimSpace(*req.Nickname)
		// Empty clears the nickname back to the username.
		bad = bad || !protocol.ValidProfileText(n, protocol.MaxNicknameLen)
		prof.Nickname = n
	}
	if req.Bio != nil {
		b := strings.TrimSpace(*req.Bio)
		bad = bad || !protocol.ValidProfileText(b, protocol.MaxBioLen)
		prof.Bio = b
	}
	if req.Avatar != nil {
		a := strings.TrimSpace(*req.Avatar)
		bad = bad || (a != "" && !protocol.ValidAvatar(a))
		prof.Avatar = a
	}
	if req.Color != nil {
		bad = bad || (*req.Color != "" && !protocol.ValidAvatarColor(*req.Color))
		prof.Color = *req.Color
	}
	if req.Gender != nil {
		bad = bad || !protocol.ValidGender(*req.Gender)
		prof.Gender = *req.Gender
	}
	if req.Age != nil {
		bad = bad || *req.Age < 0 || *req.Age > protocol.MaxAge
		prof.Age = *req.Age
	}
	if bad {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidProfile))
		return
	}
	prof.UserID, prof.UpdatedAt = uid, storage.Now()
	if err := s.st.SaveProfile(prof); err != nil {
		s.failStorage(c, id, err)
		return
	}
	u, err := s.st.UserByID(uid)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	ev := protocol.ProfileUpdatedEvent{User: publicUser(u, prof, true), Bio: prof.Bio}
	s.reply(c, id, ev)
	s.log.Info("profile updated", "user", u.Username, "addr", c.remoteAddr)

	// The owner's other devices and online friends refresh their view
	// (spec user-profile / 资料变更的推送).
	friends, _ := s.st.FriendIDs(uid)
	for _, o := range s.hub.connectionsForUser(uid, c) {
		s.event(o, protocol.EvProfileUpdated, ev)
	}
	s.pushTo(friends, protocol.EvProfileUpdated, ev)
	// Someone in the plaza shows their profile to everyone there.
	if set, ok, _ := s.st.UserSettings(uid); ok && set["plaza"] == "on" {
		if f, err := protocol.NewEvent(protocol.EvPlazaChanged, struct{}{}); err == nil {
			s.hub.broadcastEvery(f)
		}
	}
}
