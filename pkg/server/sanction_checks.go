package server

import (
	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Sanction checks used by the chat handlers (spec account-moderation). They
// read the store on every call: a sanction takes effect on the next request
// with no cache to invalidate, and the lookup is an in-memory index.

// activeSanction returns the user's sanction of kind in force now. A storage
// failure reads as "none": refusing every login because the sanction index
// could not be read would be worse than letting one through.
func (s *Server) activeSanction(uid, kind string) (storage.Sanction, bool) {
	sc, ok, err := s.st.ActiveSanction(uid, kind, storage.Now())
	if err != nil {
		s.log.Warn("read sanction", "err", err, "user", s.userName(uid))
		return storage.Sanction{}, false
	}
	return sc, ok
}

func (s *Server) isBanned(uid string) bool {
	_, ok := s.activeSanction(uid, storage.SanctionBan)
	return ok
}

// failIfBanned replies account_banned with the reason and expiry when the user
// is banned, and reports whether it did.
func (s *Server) failIfBanned(c *Conn, id, uid string) bool {
	sc, ok := s.activeSanction(uid, storage.SanctionBan)
	if !ok {
		return false
	}
	s.log.Info("banned login refused", "user", s.userName(uid), "addr", c.remoteAddr)
	s.failWithData(c, id, protocol.NewErrorCode(protocol.CodeAccountBanned),
		protocol.SanctionInfo{Reason: sc.Reason, ExpiresAt: sc.ExpiresAt})
	return true
}

// failIfMuted replies muted with the expiry when the user is muted, and
// reports whether it did.
func (s *Server) failIfMuted(c *Conn, id, uid string) bool {
	sc, ok := s.activeSanction(uid, storage.SanctionMute)
	if !ok {
		return false
	}
	s.failWithData(c, id, protocol.NewErrorCode(protocol.CodeMuted),
		protocol.SanctionInfo{ExpiresAt: sc.ExpiresAt})
	return true
}

// failWithData is fail for errors that carry structured data.
func (s *Server) failWithData(c *Conn, id string, e *protocol.Error, data any) {
	f, err := protocol.NewErrorData(id, e, data)
	if err != nil {
		s.log.Error("encode error", "err", err)
		return
	}
	c.enqueue(f)
}
