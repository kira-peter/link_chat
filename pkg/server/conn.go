package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"link-chat/pkg/protocol"
)

// Conn is one live client connection.
//
// Two goroutines touch it: readLoop owns the socket for reading, writeLoop owns
// it for writing. Everything they share goes through channels, so the WebSocket
// is never written from two places at once — which would interleave frames.
type Conn struct {
	ws  *websocket.Conn
	hub *Hub
	srv *Server

	// userID is written once by the hub goroutine during bindUser and read by
	// the read loop afterwards. atomic because the race detector would
	// otherwise (correctly) complain about the handoff.
	userIDVal atomic.Value // string

	out chan protocol.Frame

	// closeOnce guards shutdown so detach/close happen exactly once, whichever
	// goroutine gets there first.
	closeOnce sync.Once
	closed    chan struct{}

	// remoteAddr is captured at accept time for rate limiting and logging.
	remoteAddr string

	// inFlight tracks request IDs currently being handled, to reject duplicates.
	inflightMu sync.Mutex
	inflight   map[string]struct{}

	// limiter is this connection's own request budget, created on first use.
	limiterOnce sync.Once
	limiter     *rateLimiter

	// writeMu serialises socket writes. writeLoop is normally the only writer,
	// but a fatal protocol error must reach the client before close() and is
	// written directly rather than queued.
	writeMu sync.Mutex

	// helloDone is owned by readLoop alone, so it needs no synchronisation.
	helloDone bool

	// handlers tracks in-flight request goroutines so Close can wait for them.
	handlers sync.WaitGroup
	// wg tracks the write loop, so the connection is fully quiescent before the
	// handler returns and shutdown can proceed.
	wg sync.WaitGroup
	// sem bounds how many requests one connection can have in flight at once.
	sem chan struct{}
}

// spawn runs a request handler, bounded by the connection's in-flight limit.
// Falling back to a synchronous run when the limit is reached keeps memory
// bounded without dropping the request.
func (c *Conn) spawn(fn func()) {
	select {
	case c.sem <- struct{}{}:
		c.handlers.Add(1)
		go func() {
			defer func() {
				<-c.sem
				c.handlers.Done()
			}()
			fn()
		}()
	default:
		fn()
	}
}

func (c *Conn) uid() string {
	v, _ := c.userIDVal.Load().(string)
	return v
}

func (c *Conn) setUserID(id string) {
	c.userIDVal.Store(id)
}

// enqueue queues a frame for the write loop.
//
// When the queue is full the connection is closed rather than dropping the
// frame. Dropping would open a hole in the conversation's sequence numbers and
// silently break ordering; closing makes the client reconnect and backfill,
// which converges to the right state (design.md decision 3).
func (c *Conn) enqueue(f protocol.Frame) {
	select {
	case c.out <- f:
	case <-c.closed:
	default:
		c.srv.log.Warn("closing slow connection", "addr", c.remoteAddr, "user", c.uid())
		c.close()
	}
}

// close shuts the connection down exactly once.
func (c *Conn) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		// Closing the socket unblocks readLoop.
		_ = c.ws.CloseNow()
	})
}

// readLoop reads frames, hands requests to the dispatcher, and enforces the
// liveness timeout.
func (c *Conn) readLoop(ctx context.Context) {
	defer c.close()

	for {
		c.ws.SetReadLimit(c.srv.cfg.MaxFrameBytes)
		// Any frame counts as liveness, so the timeout starts fresh per Read
		// (spec chat-protocol / 心跳与超时).
		rctx, cancel := context.WithTimeout(ctx, c.srv.cfg.HeartbeatInterval*2)
		_, data, err := c.ws.Read(rctx)
		cancel()
		if err != nil {
			return
		}

		f, err := protocol.Decode(data)
		if err != nil {
			c.enqueueMust(protocol.NewErrorCodeFrame("", protocol.CodeMalformed))
			continue
		}
		c.srv.handleFrame(ctx, c, f)
	}
}

// touch is a no-op marker kept for readability: coder/websocket has no
// per-connection read deadline, so liveness is enforced by giving each Read its
// own timeout in readLoop instead.
func (c *Conn) touch() {}

// writeLoop is the only writer on the socket.
func (c *Conn) writeLoop(ctx context.Context) {
	ticker := time.NewTicker(c.srv.cfg.HeartbeatInterval)
	defer ticker.Stop()
	defer c.close()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case f := <-c.out:
			if err := c.write(ctx, f); err != nil {
				return
			}
		case <-ticker.C:
			// Server-side ping keeps intermediaries from idling the connection
			// out and gives the client something to measure us by.
			if err := c.ws.Ping(ctx); err != nil {
				return
			}
		}
	}
}

func (c *Conn) write(ctx context.Context, f protocol.Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// A bounded write deadline stops one stuck peer from pinning the goroutine.
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, b)
}

// writeThenClose delivers one final frame and then shuts the connection down.
//
// A queued frame would be dropped by close(), so a fatal protocol error (wrong
// version, bad ordering) writes straight to the socket to make sure the client
// learns why it is being disconnected.
func (c *Conn) writeThenClose(f protocol.Frame) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.write(ctx, f)
	c.close()
}

// enqueueMust queues a frame that was built by a constructor known not to fail.
func (c *Conn) enqueueMust(f protocol.Frame, err error) {
	if err != nil {
		return
	}
	c.enqueue(f)
}

// claimRequestID records an in-flight request ID, reporting false when the same
// ID is already being handled (spec chat-protocol / 请求与响应关联).
func (c *Conn) claimRequestID(id string) bool {
	if id == "" {
		return true // responses to id-less requests are not correlated anyway
	}
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	if c.inflight == nil {
		c.inflight = map[string]struct{}{}
	}
	if _, exists := c.inflight[id]; exists {
		return false
	}
	c.inflight[id] = struct{}{}
	return true
}

func (c *Conn) releaseRequestID(id string) {
	if id == "" {
		return
	}
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	delete(c.inflight, id)
}

var errConnClosed = errors.New("connection closed")
