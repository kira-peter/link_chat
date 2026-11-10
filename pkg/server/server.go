package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Server wires the hub, the store and the HTTP surface together.
type Server struct {
	cfg Config
	st  storage.Storage
	hub *Hub
	log *slog.Logger

	// authLimiters are per-purpose so a burst of logins cannot exhaust the
	// registration budget or vice versa.
	loginLimit    *rateLimiter
	registerLimit *rateLimiter
	// reqLimit throttles ordinary requests per connection.
	reqLimit func() *rateLimiter

	httpSrv *http.Server

	shutdownOnce sync.Once
	shuttingDown chan struct{}

	// conns tracks live connection handlers so Shutdown can wait for them.
	// Without this the process (or a test) can close storage while a request is
	// still writing to it.
	conns sync.WaitGroup
}

// New builds a server. The caller owns the storage handle and must close it.
func New(cfg Config, st storage.Storage, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		cfg:          cfg,
		st:           st,
		hub:          NewHub(),
		log:          log,
		shuttingDown: make(chan struct{}),
	}
	s.loginLimit = newRateLimiter(cfg.LoginAttemptsPerMinute, time.Minute, time.Second, 5*time.Minute)
	s.registerLimit = newRateLimiter(cfg.RegisterPerMinute, time.Minute, time.Second, 5*time.Minute)
	return s
}

// Handler returns the HTTP handler, so tests can drive it without a real listen.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/healthz", s.handleHealth)
	return mux
}

// ListenAndServe runs until ctx is cancelled, then shuts down gracefully.
func (s *Server) ListenAndServe(ctx context.Context) error {
	s.httpSrv = &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		var err error
		if s.cfg.TLSCert != "" {
			err = s.httpSrv.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
		} else {
			err = s.httpSrv.ListenAndServe()
		}
		errCh <- err
	}()

	s.log.Info("listening", "addr", s.cfg.Listen, "tls", s.cfg.TLSCert != "")

	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		return s.Shutdown()
	}
}

// Shutdown stops accepting connections, tells clients to go away, waits for the
// grace period, then returns (spec server-runtime / 优雅关停).
func (s *Server) Shutdown() error {
	s.shutdownOnce.Do(func() {
		s.log.Info("shutting down")
		close(s.shuttingDown)

		// Tell every client first, so they reconnect to another instance
		// instead of hanging on a dead socket.
		f, err := protocol.NewEvent(protocol.EvShutdown, protocol.ShutdownEvent{Reason: "server restarting"})
		if err == nil {
			s.hub.broadcastAll(f)
		}

		// Close idle HTTP connections and stop accepting new ones. Handler
		// goroutines for hijacked (WebSocket) connections are not waited on by
		// net/http, so they are tracked separately below.
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownGrace)
		defer cancel()
		if s.httpSrv != nil {
			if err := s.httpSrv.Shutdown(ctx); err != nil {
				s.log.Warn("http shutdown incomplete", "err", err)
			}
		}

		// Give live connections the grace period to finish their in-flight work,
		// then stop waiting. Waiting is what guarantees an acknowledged message
		// has actually been persisted before the caller closes storage
		// (spec server-runtime / 关停不丢已确认消息).
		done := make(chan struct{})
		go func() {
			s.conns.Wait()
			close(done)
		}()
		select {
		case <-done:
			s.log.Info("all connections drained")
		case <-ctx.Done():
			s.log.Warn("shutdown grace expired with connections still open")
		}
		s.hub.Close()
	})
	return nil
}

// handleHealth reports whether the process can serve traffic and whether the
// store answers, which is what a load balancer needs to decide
// (spec server-runtime / 健康检查).
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	select {
	case <-s.shuttingDown:
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("shutting down\n"))
		return
	default:
	}
	// A trivial read proves the store actor is alive.
	if _, err := s.st.ListUsers(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("storage unavailable\n"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// maxInFlightPerConn bounds concurrent request handlers for one connection, so a
// pipelining client cannot make the server hold unbounded work in memory.
const maxInFlightPerConn = 8

// handleWS upgrades a request into a chat connection.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	select {
	case <-s.shuttingDown:
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	default:
	}

	// Register for the shutdown wait *before* the upgrade.
	//
	// Accept hijacks the TCP connection, after which net/http stops tracking this
	// handler and its own shutdown no longer waits for it. If the counter were
	// incremented after Accept, a Shutdown landing in that window would see zero
	// live connections, return immediately, and let the caller close storage while
	// this handler was about to write to it.
	s.conns.Add(1)
	defer s.conns.Done()

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// A terminal client is not a browser; the origin check only serves to
		// stop drive-by browser connections, and a terminal client never sends
		// Origin. Keep the default same-origin rule for that reason.
		InsecureSkipVerify: false,
	})
	if err != nil {
		s.log.Warn("websocket accept failed", "err", err, "addr", r.RemoteAddr)
		return
	}
	ws.SetReadLimit(s.cfg.MaxFrameBytes)

	c := &Conn{
		ws:         ws,
		hub:        s.hub,
		srv:        s,
		out:        make(chan protocol.Frame, s.cfg.SendQueueDepth),
		closed:     make(chan struct{}),
		remoteAddr: s.clientAddr(r),
		sem:        make(chan struct{}, maxInFlightPerConn),
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Register before spawning anything so Shutdown cannot miss this connection.
	s.conns.Add(1)
	defer s.conns.Done()

	// The write loop is waited for on the way out: it holds the socket, and a
	// lingering writer would race a closed connection during shutdown.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.writeLoop(ctx)
	}()

	first := s.hub.attach(c)
	defer func() {
		// Close the socket first so no new request handlers can start, then wait
		// for the ones already running. Without this wait, handleWS returns while
		// a handler is still writing to the store: Shutdown's connection counter
		// would drop to zero early and the caller could close storage underneath
		// a live request.
		c.close()
		c.handlers.Wait()
		c.wg.Wait()

		wentOffline, userID := s.hub.detach(c)
		if wentOffline && userID != "" {
			s.broadcastPresence(userID, false)
		}
		s.log.Info("connection closed", "addr", c.remoteAddr, "user", userID)
	}()

	if first {
		// Presence is broadcast only once the user is actually authenticated;
		// attach already counted a pre-auth connection, so nothing to do here.
		_ = first
	}

	c.touch()
	c.readLoop(ctx)
}

// clientAddr resolves the originating address, honouring a proxy header only
// when the operator declared the proxy trusted
// (spec server-runtime / 监听与传输加密).
func (s *Server) clientAddr(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Leftmost entry is the original client.
			if i := strings.IndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
	}
	return r.RemoteAddr
}

// handleFrame dispatches one decoded frame. It owns the request/response
// envelope so individual handlers only deal with their own payload.
func (s *Server) handleFrame(ctx context.Context, c *Conn, f protocol.Frame) {
	switch f.Type {
	case protocol.TypeHello:
		if c.helloDone {
			c.enqueueMust(protocol.NewErrorCodeFrame(f.ID, protocol.CodeProtocolViolation))
			return
		}
		s.handleHello(ctx, c, f)
	case protocol.TypeReq:
		// Nothing may be requested before the handshake completes
		// (spec chat-protocol / 首个帧不是 hello).
		if !c.helloDone {
			c.writeThenClose(mustErrFrame(protocol.NewErrorCodeFrame(f.ID, protocol.CodeProtocolViolation)))
			return
		}
		s.handleRequest(ctx, c, f)
	default:
		c.enqueueMust(protocol.NewErrorCodeFrame(f.ID, protocol.CodeProtocolViolation))
	}
}

// handleHello performs version negotiation. It must be the first frame
// (spec chat-protocol / 协议版本协商).
func (s *Server) handleHello(ctx context.Context, c *Conn, f protocol.Frame) {
	var p protocol.HelloPayload
	if err := protocol.DecodePayload(f.Payload, &p); err != nil {
		c.enqueueMust(protocol.NewErrorCodeFrame(f.ID, protocol.CodeMalformed))
		c.close()
		return
	}
	if p.Version > protocol.Version {
		e := protocol.NewErrorCode(protocol.CodeVersionUnsupported)
		// Include the range so a client can tell the user what to upgrade.
		e.Message = e.Message + "; server supports up to v" + itoa(protocol.Version)
		c.writeThenClose(mustErrFrame(protocol.NewError(f.ID, e)))
		return
	}
	c.helloDone = true
	ack, err := protocol.NewRes(f.ID, protocol.HelloPayload{
		Version: protocol.Version,
		Server:  "link-chat",
	})
	c.enqueueMust(ack, err)
}

// handleRequest routes one request, enforcing per-connection rate limits and the
// duplicate-ID rule.
func (s *Server) handleRequest(ctx context.Context, c *Conn, f protocol.Frame) {
	var p protocol.ReqPayload
	if err := protocol.DecodePayload(f.Payload, &p); err != nil {
		c.enqueueMust(protocol.NewErrorCodeFrame(f.ID, protocol.CodeMalformed))
		return
	}
	if p.Op == "" {
		c.enqueueMust(protocol.NewErrorCodeFrame(f.ID, protocol.CodeInvalidArgument))
		return
	}
	if !c.claimRequestID(f.ID) {
		c.enqueueMust(protocol.NewErrorCodeFrame(f.ID, protocol.CodeDuplicateRequestID))
		return
	}

	// Per-connection request budget. Auth has its own, stricter limiter, so it
	// is exempt here to avoid double-counting.
	if !isAuthOp(p.Op) {
		lim := s.limiterFor(c)
		if ok, wait := lim.Allow(c.remoteAddr); !ok {
			e := protocol.NewErrorCode(protocol.CodeRateLimited)
			e.RetryAfterMS = retryAfterMS(wait)
			c.enqueueMust(protocol.NewError(f.ID, e))
			return
		}
	}

	// Handling runs off the read loop so one slow request (a password hash, a
	// big history page) cannot delay heartbeats or other requests. The ID stays
	// claimed until the handler finishes, which is what makes an in-flight
	// duplicate detectable.
	c.spawn(func() {
		defer c.releaseRequestID(f.ID)
		s.dispatch(ctx, c, f.ID, p)
	})
}

// limiterFor returns a limiter scoped to the connection, created lazily. A
// per-connection budget is what the spec describes ("同一连接"), and it keeps
// one abusive client from spending everyone else's allowance.
func (s *Server) limiterFor(c *Conn) *rateLimiter {
	c.limiterOnce.Do(func() {
		c.limiter = newRateLimiter(s.cfg.RequestsPerSecond, time.Second, time.Second, time.Minute)
	})
	return c.limiter
}

func isAuthOp(op string) bool {
	switch op {
	case protocol.OpAuthRegister, protocol.OpAuthLogin, protocol.OpAuthResume, protocol.OpAuthLogout,
		protocol.OpRecoverQuestion, protocol.OpRecoverVerify, protocol.OpRecoverReset:
		// Recovery has its own stricter limiter, so it must not also be counted
		// against the general per-connection budget.
		return true
	}
	return false
}

// dispatch is split out so each handler is testable on its own.
func (s *Server) dispatch(ctx context.Context, c *Conn, id string, p protocol.ReqPayload) {
	switch p.Op {
	case protocol.OpAuthRegister:
		s.opRegister(c, id, p)
	case protocol.OpAuthLogin:
		s.opLogin(c, id, p)
	case protocol.OpAuthResume:
		s.opResume(c, id, p)
	case protocol.OpAuthLogout:
		s.opLogout(c, id, p)

	case protocol.OpRecoverQuestion:
		s.opRecoverQuestion(c, id, p)
	case protocol.OpRecoverVerify:
		s.opRecoverVerify(c, id, p)
	case protocol.OpRecoverReset:
		s.opRecoverReset(c, id, p)

	case protocol.OpChannelList:
		s.opChannelList(c, id)
	case protocol.OpChannelCreate:
		s.opChannelCreate(c, id, p)
	case protocol.OpChannelJoin:
		s.opChannelJoin(c, id, p)
	case protocol.OpChannelLeave:
		s.opChannelLeave(c, id, p)
	case protocol.OpChannelDelete:
		s.opChannelDelete(c, id, p)
	case protocol.OpDMOpen:
		s.opDMOpen(c, id, p)
	case protocol.OpUserList:
		s.opUserList(c, id)

	case protocol.OpMessageSend:
		s.opMessageSend(c, id, p)
	case protocol.OpMessageEdit:
		s.opMessageEdit(c, id, p)
	case protocol.OpMessageDelete:
		s.opMessageDelete(c, id, p)
	case protocol.OpMessageHistory:
		s.opMessageHistory(c, id, p)

	case protocol.OpPresencePing:
		s.opPing(c, id, p)

	default:
		c.enqueueMust(protocol.NewErrorCodeFrame(id, protocol.CodeInvalidArgument))
	}
}

// reply sends a successful response.
func (s *Server) reply(c *Conn, id string, data any) {
	f, err := protocol.NewRes(id, data)
	if err != nil {
		s.log.Error("encode response", "err", err)
		return
	}
	c.enqueue(f)
}

// fail sends a structured error response.
func (s *Server) fail(c *Conn, id string, e *protocol.Error) {
	f, err := protocol.NewError(id, e)
	if err != nil {
		s.log.Error("encode error", "err", err)
		return
	}
	c.enqueue(f)
}

// event pushes a server event to one connection.
func (s *Server) event(c *Conn, name string, data any) {
	f, err := protocol.NewEvent(name, data)
	if err != nil {
		s.log.Error("encode event", "err", err)
		return
	}
	c.enqueue(f)
}

// broadcastPresence tells everyone who shares a conversation with userID that
// their online state changed.
func (s *Server) broadcastPresence(userID string, online bool) {
	u, err := s.st.UserByID(userID)
	if err != nil {
		return
	}
	// Who cares? Everyone who shares at least one conversation.
	targets := s.sharedConversationPeers(userID)
	f, err := protocol.NewEvent(protocol.EvPresence, protocol.PresenceEvent{
		UserID:   userID,
		Username: u.Username,
		Online:   online,
	})
	if err != nil {
		return
	}
	s.hub.broadcastToUsers(targets, f)
}

// sharedConversationPeers lists users who share any conversation with userID,
// including userID itself so their own other devices stay in sync.
func (s *Server) sharedConversationPeers(userID string) []string {
	seen := map[string]struct{}{userID: {}}
	convs, err := s.st.ListConversations()
	if err != nil {
		return nil
	}
	for _, cv := range convs {
		if !s.isMember(cv, userID) {
			continue
		}
		for _, m := range s.membersOf(cv) {
			seen[m] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

// isMember checks membership for either conversation kind.
func (s *Server) isMember(cv storage.Conversation, userID string) bool {
	if cv.Kind == protocol.KindDM {
		for _, id := range cv.MemberIDs {
			if id == userID {
				return true
			}
		}
		return false
	}
	return s.st.IsMember(cv.ID, userID)
}

// membersOf returns every participant, whichever way membership is stored.
func (s *Server) membersOf(cv storage.Conversation) []string {
	if cv.Kind == protocol.KindDM {
		return cv.MemberIDs
	}
	list, err := s.st.ListMembers(cv.ID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, m.UserID)
	}
	return out
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// mustErrFrame unwraps a frame constructor whose only failure mode is a JSON
// encoding error on a payload this package built, which cannot happen.
func mustErrFrame(f protocol.Frame, err error) protocol.Frame {
	if err != nil {
		return protocol.Frame{Type: protocol.TypeError}
	}
	return f
}
