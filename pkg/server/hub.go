package server

import (
	"sync"

	"link-chat/pkg/protocol"
)

// Hub owns every piece of mutable live state: which connections exist, who is
// online, and which connections are subscribed to which conversation.
//
// All of it is mutated only from the hub goroutine. Nothing here takes a lock
// because nothing here runs concurrently with itself — callers submit commands
// through a channel and wait for the result. That removes lock ordering as a
// class of bug entirely (design.md decision 3).
type Hub struct {
	cmds      chan func(*hubState)
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	state *hubState
}

type hubState struct {
	conns   map[*Conn]struct{}
	clients map[*Conn]struct{} // Connections that actually consume pushed frames.

	// byUser tracks live connections per user, not "is the user online". A user
	// with two clients stays online until the last one goes away
	// (spec chat-rooms / 在线状态).
	byUser map[string]map[*Conn]struct{}

	// subs maps a conversation ID to the connections subscribed to it.
	subs       map[string]map[*Conn]struct{}
	subsByConn map[*Conn]map[string]struct{}

	// admins are the open admin console event streams.
	admins map[*adminSub]struct{}
}

// NewHub starts the actor goroutine.
func NewHub() *Hub {
	h := &Hub{
		cmds: make(chan func(*hubState)),
		done: make(chan struct{}),
		state: &hubState{
			conns: map[*Conn]struct{}{}, clients: map[*Conn]struct{}{},
			byUser: map[string]map[*Conn]struct{}{}, subs: map[string]map[*Conn]struct{}{},
			subsByConn: map[*Conn]map[string]struct{}{},
		},
	}
	h.wg.Add(1)
	go h.loop()
	return h
}

func (h *Hub) loop() {
	defer h.wg.Done()
	for {
		select {
		case fn := <-h.cmds:
			fn(h.state)
		case <-h.done:
			return
		}
	}
}

// do runs fn on the hub goroutine and waits for it to finish.
func (h *Hub) do(fn func(*hubState)) bool {
	reply := make(chan struct{})
	select {
	case h.cmds <- func(s *hubState) {
		fn(s)
		close(reply)
	}:
	case <-h.done:
		return false
	}
	<-reply
	return true
}

// Close stops the hub. Connections are closed by the server, not here.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		close(h.done)
		h.wg.Wait()
	})
}

// attach registers a connection and returns whether this was the user's first
// live connection (that is what decides if a presence broadcast is due).
func (h *Hub) attach(c *Conn) (firstConnection bool) {
	h.do(func(s *hubState) {
		s.conns[c] = struct{}{}
		if !c.simulated {
			s.clients[c] = struct{}{}
		}
		if c.uid() != "" {
			firstConnection = s.bind(c, c.uid())
		}
	})
	return firstConnection
}

// bindUser is called after authentication succeeds, which may be well after
// attach (the connection exists in a pre-auth state first).
func (h *Hub) bindUser(c *Conn, userID, device string) (firstConnection bool) {
	h.do(func(s *hubState) {
		if c.uid() != "" && c.uid() != userID {
			// Re-authenticating as a different user is not allowed on one
			// connection; the server rejects it before reaching here.
			return
		}
		c.setUserID(userID)
		c.device = device
		firstConnection = s.bind(c, userID)
	})
	return firstConnection
}

func (s *hubState) bind(c *Conn, userID string) bool {
	set := s.byUser[userID]
	if set == nil {
		set = map[*Conn]struct{}{}
		s.byUser[userID] = set
	}
	first := len(set) == 0
	set[c] = struct{}{}
	return first
}

// detach removes a connection. The bool reports whether the user went fully
// offline as a result.
func (h *Hub) detach(c *Conn) (wentOffline bool, userID string) {
	h.do(func(s *hubState) {
		if _, ok := s.conns[c]; !ok {
			return
		}
		delete(s.conns, c)
		delete(s.clients, c)
		for convID := range s.subsByConn[c] {
			set := s.subs[convID]
			delete(set, c)
			if len(set) == 0 {
				delete(s.subs, convID)
			}
		}
		delete(s.subsByConn, c)
		if c.uid() != "" {
			userID = c.uid()
			delete(s.byUser[userID], c)
			if len(s.byUser[userID]) == 0 {
				delete(s.byUser, userID)
				wentOffline = true
			}
		}
	})
	return wentOffline, userID
}

// subscribe makes c receive events for a conversation. It is idempotent.
func (h *Hub) subscribe(c *Conn, conversationID string) {
	h.do(func(s *hubState) {
		set := s.subs[conversationID]
		if set == nil {
			set = map[*Conn]struct{}{}
			s.subs[conversationID] = set
		}
		set[c] = struct{}{}
		if s.subsByConn[c] == nil {
			s.subsByConn[c] = map[string]struct{}{}
		}
		s.subsByConn[c][conversationID] = struct{}{}
	})
}

func (h *Hub) unsubscribe(c *Conn, conversationID string) {
	h.do(func(s *hubState) {
		set := s.subs[conversationID]
		if set == nil {
			return
		}
		delete(set, c)
		delete(s.subsByConn[c], conversationID)
		if len(set) == 0 {
			delete(s.subs, conversationID)
		}
	})
}

// broadcast sends one frame to every connection subscribed to a conversation.
//
// The hub snapshots the subscriber set and delivers outside its own turn: a slow
// socket must never stall the actor, and must never delay delivery to anyone
// else (spec message-delivery / 广播范围).
func (h *Hub) broadcast(conversationID string, f protocol.Frame) {
	var targets []*Conn
	h.do(func(s *hubState) {
		set := s.subs[conversationID]
		targets = make([]*Conn, 0, len(set))
		for c := range set {
			if !c.simulated {
				targets = append(targets, c)
			}
		}
	})
	for _, c := range targets {
		c.enqueue(f)
	}
}

// broadcastToUsers delivers to every live connection of the given users. Used
// for presence events, which are not scoped to one conversation.
func (h *Hub) broadcastToUsers(userIDs []string, f protocol.Frame) {
	want := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		want[id] = struct{}{}
	}
	var targets []*Conn
	h.do(func(s *hubState) {
		for id := range want {
			for c := range s.byUser[id] {
				if !c.simulated {
					targets = append(targets, c)
				}
			}
		}
	})
	for _, c := range targets {
		c.enqueue(f)
	}
}

// broadcastAll delivers to every connection, used for shutdown notices.
func (h *Hub) broadcastAll(f protocol.Frame) {
	h.broadcastEvery(f)
}

func (h *Hub) broadcastEvery(f protocol.Frame) {
	h.broadcastConnections(f, false, nil)
}

func (h *Hub) broadcastAuthenticated(f protocol.Frame) {
	h.broadcastConnections(f, true, nil)
}

func (h *Hub) broadcastAuthenticatedExcept(f protocol.Frame, userIDs []string) {
	excluded := make(map[string]bool, len(userIDs))
	for _, id := range userIDs {
		excluded[id] = true
	}
	h.broadcastConnections(f, true, excluded)
}

func (h *Hub) broadcastConnections(f protocol.Frame, authenticatedOnly bool, excluded map[string]bool) {
	var targets []*Conn
	h.do(func(s *hubState) {
		targets = make([]*Conn, 0, len(s.clients))
		for c := range s.clients {
			if uid := c.uid(); (!authenticatedOnly || uid != "") && !excluded[uid] {
				targets = append(targets, c)
			}
		}
	})
	for _, c := range targets {
		c.enqueue(f)
	}
}

// connectionsForUser returns every live connection bound to a user, excluding
// keep. Used when a newer login displaces the older sessions.
func (h *Hub) connectionsForUser(userID string, keep *Conn) []*Conn {
	var out []*Conn
	h.do(func(s *hubState) {
		for c := range s.byUser[userID] {
			if c != keep {
				out = append(out, c)
			}
		}
	})
	return out
}

// subscribersOf returns the user IDs currently subscribed to a conversation.
func (h *Hub) subscribersOf(conversationID string) []string {
	var out []string
	h.do(func(s *hubState) {
		seen := map[string]struct{}{}
		for c := range s.subs[conversationID] {
			if c.uid() == "" {
				continue
			}
			if _, ok := seen[c.uid()]; ok {
				continue
			}
			seen[c.uid()] = struct{}{}
			out = append(out, c.uid())
		}
	})
	return out
}

// onlineUsers reports which of the given user IDs currently have a connection.
func (h *Hub) onlineUsers() map[string]bool {
	out := map[string]bool{}
	h.do(func(s *hubState) {
		for id := range s.byUser {
			out[id] = true
		}
	})
	return out
}

func (h *Hub) hasClients() bool {
	var found bool
	h.do(func(s *hubState) { found = len(s.clients) > 0 })
	return found
}

// OnlineConn describes one signed-in connection for the admin overview.
type OnlineConn struct {
	UserID        string
	Remote        string
	ClientVersion string
	Device        string
	Since         int64
}

// hubStats is a snapshot of live state for the admin overview.
type hubStats struct {
	Connections int
	Online      []OnlineConn
}

// stats snapshots every connection. Unauthenticated connections count towards
// Connections but are not listed.
func (h *Hub) stats() hubStats {
	var out hubStats
	h.do(func(s *hubState) {
		out.Connections = len(s.conns)
		for c := range s.conns {
			if uid := c.uid(); uid != "" {
				out.Online = append(out.Online, OnlineConn{
					UserID:        uid,
					Remote:        c.remoteAddr,
					ClientVersion: c.clientVersion,
					Device:        c.device,
					Since:         c.connectedAt,
				})
			}
		}
	})
	return out
}
