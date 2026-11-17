package server

import (
	"link-chat/pkg/protocol"
)

// Account settings (spec user-settings-sync). The store keeps a plain map; the
// protocol's table decides what is valid.

// userName is the username for logs, falling back to the ID.
func (s *Server) userName(uid string) string {
	if u, err := s.st.UserByID(uid); err == nil {
		return u.Username
	}
	return uid
}

func (s *Server) opSettingsGet(c *Conn, id string) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	got, exists, err := s.st.UserSettings(uid)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	res := protocol.SettingsGetRes{Settings: protocol.Settings{}, Exists: exists}
	if exists {
		// Drop anything a newer version stored that this one does not know.
		res.Settings = protocol.Settings(got).Normalize()
	}
	s.log.Info("settings sent", "user", s.userName(uid), "addr", c.remoteAddr, "saved", exists)
	s.reply(c, id, res)
}

func (s *Server) opSettingsPut(c *Conn, id string, p protocol.ReqPayload) {
	uid, ok := s.requireAuth(c, id)
	if !ok {
		return
	}
	var req protocol.SettingsPutReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if err := req.Settings.Validate(); err != nil {
		s.log.Warn("settings rejected", "user", s.userName(uid), "addr", c.remoteAddr, "err", err)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	merged, err := s.st.MergeUserSettings(uid, req.Settings)
	if err != nil {
		s.failStorage(c, id, err)
		return
	}
	full := protocol.SettingsRes{Settings: protocol.Settings(merged).Normalize()}
	s.reply(c, id, full)
	s.log.Info("settings saved", "user", s.userName(uid), "addr", c.remoteAddr, "keys", len(req.Settings))

	// The other live connections of the same account take the change at once;
	// the one that saved already has it.
	others := s.hub.connectionsForUser(uid, c)
	if len(others) == 0 {
		return
	}
	ev, err := protocol.NewEvent(protocol.EvSettingsUpdated, full)
	if err != nil {
		s.log.Error("encode settings.updated", "err", err)
		return
	}
	for _, o := range others {
		o.enqueue(ev)
	}
}
