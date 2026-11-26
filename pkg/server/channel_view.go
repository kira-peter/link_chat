package server

import (
	"net/http"

	"link-chat/pkg/protocol"
)

type channelView struct {
	conn *Conn
	id   string
}

type channelFocus struct {
	Sequence  uint64 `json:"sequence"`
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id"`
}

func (s *Server) reportChannelView(c *Conn, id string) {
	if id == "" {
		s.clearChannelView(c, "")
		return
	}
	uid := c.uid()
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	select {
	case <-c.closed:
		return
	default:
	}
	if s.live.viewed[uid] == (channelView{c, id}) {
		return
	}
	cv, err := s.st.ConversationByID(id)
	if err != nil || cv.Kind != protocol.KindChannel || !s.isMember(cv, uid) {
		if s.live.viewed[uid].conn == c {
			delete(s.live.viewed, uid)
			s.emitChannelFocus(uid, "")
		}
		return
	}
	if s.live.viewed == nil {
		s.live.viewed = make(map[string]channelView)
	}
	s.live.viewed[uid] = channelView{c, id}
	s.emitChannelFocus(uid, id)
}

func (s *Server) clearChannelView(c *Conn, channelID string) {
	uid := c.uid()
	if uid == "" {
		return
	}
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	view := s.live.viewed[uid]
	if view.conn == c && (channelID == "" || view.id == channelID) {
		delete(s.live.viewed, uid)
		s.emitChannelFocus(uid, "")
	}
}

func (s *Server) clearDeletedChannelViews(channelID string) {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	for uid, view := range s.live.viewed {
		if view.id == channelID {
			delete(s.live.viewed, uid)
			s.emitChannelFocus(uid, "")
		}
	}
}

// emitChannelFocus requires the activity-state lock so snapshots and events
// agree on the sequence watermark.
func (s *Server) emitChannelFocus(uid, channelID string) {
	s.live.focusSequence++
	s.adminEmit(adminEvent{
		Type: "channel.focus", Required: PermStatsView, Perm: PermChatView,
		Data: channelFocus{s.live.focusSequence, uid, channelID},
	})
}

func (s *Server) apiLiveFriends(w http.ResponseWriter, r *http.Request, _ adminSession) {
	friends, err := s.st.FriendIDs(r.PathValue("id"))
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	if friends == nil {
		friends = []string{}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"friends": friends})
}
