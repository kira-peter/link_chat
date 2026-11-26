package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Live events for the admin console (design.md decision 7). Every open console
// tab holds one Server-Sent Events stream. Events are produced at the points
// where chat state changes and delivered outside the hub's turn, trimmed to
// what each subscriber's permissions allow; a subscriber that falls behind is
// dropped rather than slowing chat down, and its page reloads what it shows
// when it reconnects.

// adminEvent is one item on the stream. Data is already safe for any
// administrator who passes the event's permission check.
type adminEvent struct {
	Type string `json:"type"`
	// Required gates both versions of the event.
	Required string `json:"-"`
	// Perm is the permission needed to receive the full event. Without it a
	// subscriber gets Redacted instead, or nothing when Redacted is nil.
	Perm     string `json:"-"`
	Data     any    `json:"data,omitempty"`
	Redacted any    `json:"-"`
}

// adminSub is one open stream.
type adminSub struct {
	uid string
	ch  chan []byte

	mu     sync.Mutex
	perms  map[string]bool
	closed bool
	done   chan struct{}
}

func (a *adminSub) permsNow() map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.perms
}

func (a *adminSub) setPerms(p map[string]bool) {
	a.mu.Lock()
	for perm := range a.perms {
		if !p[perm] && !a.closed {
			// Reconnect so queued events cannot outlive their permission grant.
			a.closed = true
			close(a.done)
			break
		}
	}
	a.perms = p
	a.mu.Unlock()
}

// end closes the stream once.
func (a *adminSub) end() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.closed = true
		close(a.done)
	}
}

// adminSubQueue bounds how far a stream may fall behind before it is dropped.
const adminSubQueue = 256

// adminStreamHeartbeat is how often an idle stream is kept open and its
// permissions re-checked.
var adminStreamHeartbeat = 15 * time.Second

func (h *Hub) addAdminSub(a *adminSub) {
	h.do(func(s *hubState) {
		if s.admins == nil {
			s.admins = map[*adminSub]struct{}{}
		}
		s.admins[a] = struct{}{}
	})
}

func (h *Hub) removeAdminSub(a *adminSub) {
	h.do(func(s *hubState) { delete(s.admins, a) })
}

// adminSubs snapshots the open streams, optionally only those of one user.
func (h *Hub) adminSubs(uid string) []*adminSub {
	var out []*adminSub
	h.do(func(s *hubState) {
		for a := range s.admins {
			if uid == "" || a.uid == uid {
				out = append(out, a)
			}
		}
	})
	return out
}

// adminEmit delivers ev to every open stream allowed to see it.
func (s *Server) adminEmit(ev adminEvent) {
	subs := s.hub.adminSubs("")
	if len(subs) == 0 {
		return
	}
	full, err := encodeAdminEvent(ev.Type, ev.Data)
	if err != nil {
		s.log.Error("encode admin event", "err", err)
		return
	}
	var redacted []byte
	if ev.Redacted != nil {
		redacted, _ = encodeAdminEvent(ev.Type, ev.Redacted)
	}
	for _, a := range subs {
		perms := a.permsNow()
		if ev.Required != "" && !perms[ev.Required] {
			continue
		}
		msg := full
		if ev.Perm != "" && !perms[ev.Perm] {
			if redacted == nil {
				continue
			}
			msg = redacted
		}
		select {
		case a.ch <- msg:
		default:
			// Too slow: drop it, never block chat for a console tab.
			s.log.Warn("closing slow admin stream", "user", s.userName(a.uid))
			a.end()
		}
	}
}

func encodeAdminEvent(typ string, data any) ([]byte, error) {
	b, err := json.Marshal(adminEvent{Type: typ, Data: data})
	if err != nil {
		return nil, err
	}
	return []byte("data: " + string(b) + "\n\n"), nil
}

// closeAdminStreams ends a user's open console streams, after a ban or after
// losing every permission.
func (s *Server) closeAdminStreams(uid string) {
	for _, a := range s.hub.adminSubs(uid) {
		a.end()
	}
}

// refreshAdminStreams re-reads a user's permissions into their open streams, so
// a narrowed grant stops full events at once.
func (s *Server) refreshAdminStreams(uid string) {
	u, err := s.st.UserByID(uid)
	if err != nil {
		return
	}
	p := effectivePerms(u)
	for _, a := range s.hub.adminSubs(uid) {
		a.setPerms(p)
	}
}

// ---------- producers ----------

// adminMessageData is a chat change as the console sees it.
type adminMessageData struct {
	Message           protocol.Message `json:"message"`
	ConversationLabel string           `json:"conversation_label"`
	ConversationKind  string           `json:"conversation_kind"`
}

// adminNotifyMessage reports a new, edited, or deleted message. Without
// chat.view a subscriber only learns that a message was sent, so the overview
// count can move; edits and deletions are not counted.
func (s *Server) adminNotifyMessage(typ, actorID string, cv storage.Conversation, m protocol.Message) {
	targetID := ""
	if cv.Kind == protocol.KindDM {
		for _, uid := range cv.MemberIDs {
			if uid != actorID {
				targetID = uid
				break
			}
		}
	}
	content := ""
	if typ == "message" {
		content = m.Body
	}
	s.activity(typ, ActivityMessage, actorID, targetID, cv, content)
	if len(s.hub.adminSubs("")) == 0 {
		return
	}
	ev := adminEvent{
		Type: typ,
		Perm: PermChatView,
		Data: adminMessageData{
			Message:           m,
			ConversationLabel: s.conversationLabel(cv, s.userNames()),
			ConversationKind:  cv.Kind,
		},
	}
	if typ == "message" {
		ev.Redacted = struct{}{}
	}
	s.adminEmit(ev)
}

// adminNotifyPresence reports a user going online or offline.
func (s *Server) adminNotifyPresence(uid string, online bool) {
	typ := "offline"
	if online {
		typ = "online"
	}
	s.activity(typ, ActivityAccount, uid, "", storage.Conversation{}, "")
	s.adminEmit(adminEvent{Type: typ, Data: map[string]string{"user_id": uid, "username": s.userName(uid)}})
}

// adminNotifyUser reports a registration or a sign-in.
func (s *Server) adminNotifyUser(typ, uid string) {
	s.activity(typ, ActivityAccount, uid, "", storage.Conversation{}, "")
	s.adminEmit(adminEvent{Type: typ, Data: map[string]string{"user_id": uid, "username": s.userName(uid)}})
}

// adminNotifySanction reports a ban, mute, or revocation to those who can see
// users.
func (s *Server) adminNotifySanction(actor Actor, sc storage.Sanction) {
	kind := "sanction." + sc.Kind
	if sc.RevokedAt != 0 {
		kind = "sanction.revoked"
	}
	s.activity(kind, ActivitySystem, actor.ID, sc.UserID, storage.Conversation{}, "")
	s.adminEmit(adminEvent{Type: "sanction", Perm: PermUsersModerate, Data: map[string]any{
		"user_id":  sc.UserID,
		"username": s.userName(sc.UserID),
		"kind":     sc.Kind,
		"active":   sc.ActiveAt(storage.Now()),
	}})
}

// ---------- the stream ----------

// apiEvents serves one Server-Sent Events stream. It is tracked in s.conns so
// shutdown waits for it, and ends on shutdown, on the client going away, when
// the account loses its access, or when it falls too far behind.
func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request, a adminSession) {
	select {
	case <-s.shuttingDown:
		writeErr(w, http.StatusServiceUnavailable, protocol.CodeShuttingDown)
		return
	default:
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, protocol.CodeInternal)
		return
	}
	s.conns.Add(1)
	defer s.conns.Done()

	sub := &adminSub{uid: a.user.ID, ch: make(chan []byte, adminSubQueue), perms: a.perms, done: make(chan struct{})}
	s.hub.addAdminSub(sub)
	defer s.hub.removeAdminSub(sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // let a proxy pass events through at once
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	tick := time.NewTicker(adminStreamHeartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shuttingDown:
			return
		case <-sub.done:
			return
		case msg := <-sub.ch:
			select {
			case <-sub.done:
				return
			default:
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			fl.Flush()
		case <-tick.C:
			// Re-authenticate: an expired token, a ban, or no permissions left
			// ends the stream even if nothing announced it.
			if !s.adminStillValid(r, sub) {
				return
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// adminStillValid re-checks a stream's session and refreshes its permissions.
func (s *Server) adminStillValid(r *http.Request, sub *adminSub) bool {
	ck, err := r.Cookie(adminCookie)
	if err != nil {
		return false
	}
	uid, perr := s.userForToken(ck.Value, storage.PurposeAdmin)
	if perr != nil || uid != sub.uid {
		return false
	}
	u, err := s.st.UserByID(uid)
	if err != nil || s.isBanned(uid) {
		return false
	}
	p := effectivePerms(u)
	if len(p) == 0 {
		return false
	}
	sub.setPerms(p)
	return true
}
