// Package client holds the terminal client's non-visual half: the connection
// state machine, the request plumbing, and the single AppState that every view
// reads from.
//
// Views never talk to the network. They render AppState and return intents; the
// app layer applies intents, which is what keeps reconnection, unread counts and
// draft handling in one testable place (design.md decision 8).
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"

	"link-chat/pkg/protocol"
)

// ConnState is the connection's lifecycle position.
type ConnState int

const (
	// StateUnconfigured means no server address has been supplied yet.
	StateUnconfigured ConnState = iota
	// StateConnecting is the initial dial plus handshake.
	StateConnecting
	// StateAuthenticating covers the login/register/resume in flight.
	StateAuthenticating
	// StateReady means the connection is live and authenticated.
	StateReady
	// StateDisconnected means the socket dropped and a retry is pending.
	StateDisconnected
	// StateAuthFailed means the credential is gone and the user must log in
	// again; reconnecting would only loop.
	StateAuthFailed
)

func (s ConnState) String() string {
	switch s {
	case StateUnconfigured:
		return "unconfigured"
	case StateConnecting:
		return "connecting"
	case StateAuthenticating:
		return "authenticating"
	case StateReady:
		return "ready"
	case StateDisconnected:
		return "disconnected"
	case StateAuthFailed:
		return "auth failed"
	}
	return "unknown"
}

// ErrClosed is returned when a request cannot be sent because the socket is down.
var ErrClosed = errors.New("connection closed")

// Transport owns the socket: dialing, the handshake, request/response correlation
// and the reconnection backoff. It delivers events to a callback rather than
// holding any UI state.
type Transport struct {
	// ServerURL is the ws:// or wss:// endpoint, e.g. wss://chat.example.com/ws.
	ServerURL string

	// OnEvent is called for every server push. It is invoked from the read
	// goroutine, so it must not block.
	OnEvent func(name string, data json.RawMessage)
	// OnStateChange is called whenever the connection state moves.
	OnStateChange func(ConnState, error)

	mu      sync.Mutex
	ws      *websocket.Conn
	state   ConnState
	nextID  int
	pending map[string]chan protocol.Frame

	closed   chan struct{}
	closeOne sync.Once

	backoff time.Duration
}

// NewTransport builds a transport; call Connect to start it.
func NewTransport(serverURL string) *Transport {
	return &Transport{
		ServerURL: serverURL,
		state:     StateUnconfigured,
		pending:   map[string]chan protocol.Frame{},
		closed:    make(chan struct{}),
		backoff:   time.Second,
	}
}

// State reports the current state.
func (t *Transport) State() ConnState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *Transport) setState(s ConnState, err error) {
	t.mu.Lock()
	t.state = s
	t.mu.Unlock()
	if t.OnStateChange != nil {
		t.OnStateChange(s, err)
	}
}

// Connect dials and performs the protocol handshake.
func (t *Transport) Connect(ctx context.Context) error {
	t.setState(StateConnecting, nil)

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(dialCtx, t.ServerURL, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if err := t.handshake(ctx, ws); err != nil {
		ws.CloseNow()
		return err
	}

	t.mu.Lock()
	t.ws = ws
	t.mu.Unlock()

	go t.readLoop(ctx, ws)
	go t.heartbeatLoop(ctx, ws)
	t.setState(StateAuthenticating, nil)
	return nil
}

// HeartbeatInterval is how often the client proves it is still alive. It is half
// the server's read timeout, so one lost beat does not end the connection.
const HeartbeatInterval = 15 * time.Second

// heartbeatLoop keeps an idle connection from being reaped.
//
// The server times out a read after two heartbeat periods and only application
// frames reset that timer; WebSocket control pings are handled inside the
// library and never reach it. Without this loop a client that is merely sitting
// there gets disconnected after a minute, which is exactly what users see as
// "it drops me for no reason" (design.md decision 2).
func (t *Transport) heartbeatLoop(ctx context.Context, ws *websocket.Conn) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.closed:
			return
		case <-ticker.C:
			// A failed ping means the socket is gone; readLoop will observe the
			// same failure and drive the reconnect, so there is nothing to do
			// here but stop.
			if _, err := t.Call(ctx, protocol.OpPresencePing, protocol.PingReq{
				Nonce: time.Now().UnixMilli(),
			}, 10*time.Second); err != nil {
				return
			}
		}
	}
}

// handshake performs version negotiation. A mismatch is fatal for the session:
// there is no point retrying against a server that speaks another version.
func (t *Transport) handshake(ctx context.Context, ws *websocket.Conn) error {
	f, err := protocol.NewResRequest("hello", protocol.TypeHello, protocol.HelloPayload{
		Version: protocol.Version,
		Client:  "link-chat-tui",
	})
	if err != nil {
		return err
	}
	b, _ := json.Marshal(f)
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ws.Write(wctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("hello: %w", err)
	}

	rctx, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	_, data, err := ws.Read(rctx)
	if err != nil {
		return fmt.Errorf("hello-ack: %w", err)
	}
	reply, err := protocol.Decode(data)
	if err != nil {
		return fmt.Errorf("hello-ack: %w", err)
	}
	if reply.Type == protocol.TypeError {
		var p protocol.ResPayload
		_ = json.Unmarshal(reply.Payload, &p)
		return fmt.Errorf("%s: %s", p.Code, p.Message)
	}
	var hp protocol.HelloPayload
	if err := json.Unmarshal(reply.Payload, &hp); err != nil {
		return fmt.Errorf("hello-ack: %w", err)
	}
	if hp.Version > protocol.Version {
		return fmt.Errorf("server speaks protocol v%d, client v%d", hp.Version, protocol.Version)
	}
	return nil
}

// readLoop pumps frames off the socket.
func (t *Transport) readLoop(ctx context.Context, ws *websocket.Conn) {
	defer func() {
		t.mu.Lock()
		if t.ws == ws {
			t.ws = nil
		}
		// Fail every in-flight request so callers do not hang forever.
		for id, ch := range t.pending {
			close(ch)
			delete(t.pending, id)
		}
		t.mu.Unlock()
	}()

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			select {
			case <-t.closed:
			default:
				t.setState(StateDisconnected, err)
			}
			return
		}
		f, err := protocol.Decode(data)
		if err != nil {
			continue
		}
		switch f.Type {
		case protocol.TypeEvent:
			var ep protocol.EventPayload
			if err := json.Unmarshal(f.Payload, &ep); err != nil {
				continue
			}
			if t.OnEvent != nil {
				t.OnEvent(ep.Name, ep.Data)
			}
		case protocol.TypeRes, protocol.TypeError:
			t.mu.Lock()
			ch, ok := t.pending[f.ID]
			if ok {
				delete(t.pending, f.ID)
			}
			t.mu.Unlock()
			if ok {
				ch <- f
			}
		}
	}
}

// Call sends a request and waits for its response.
//
// The response is returned even when it is an error frame; the caller decides
// what a failure means. A nil frame with ErrClosed means the socket went away
// first.
func (t *Transport) Call(ctx context.Context, op string, data any, timeout time.Duration) (protocol.Frame, error) {
	t.mu.Lock()
	ws := t.ws
	if ws == nil {
		t.mu.Unlock()
		return protocol.Frame{}, ErrClosed
	}
	t.nextID++
	id := fmt.Sprintf("q%d", t.nextID)
	ch := make(chan protocol.Frame, 1)
	t.pending[id] = ch
	t.mu.Unlock()

	f, err := protocol.NewReq(id, op, data)
	if err != nil {
		return protocol.Frame{}, err
	}
	b, err := json.Marshal(f)
	if err != nil {
		return protocol.Frame{}, err
	}

	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ws.Write(wctx, websocket.MessageText, b); err != nil {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return protocol.Frame{}, fmt.Errorf("send %s: %w", op, err)
	}

	select {
	case reply, ok := <-ch:
		if !ok {
			return protocol.Frame{}, ErrClosed
		}
		return reply, nil
	case <-wctx.Done():
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return protocol.Frame{}, fmt.Errorf("%s: %w", op, wctx.Err())
	}
}

// Close tears the transport down for good.
func (t *Transport) Close() {
	t.closeOne.Do(func() {
		close(t.closed)
		t.mu.Lock()
		ws := t.ws
		t.ws = nil
		t.mu.Unlock()
		if ws != nil {
			ws.CloseNow()
		}
	})
}

// NextBackoff returns the delay before the next reconnect attempt and doubles it,
// capped at 30s. A successful connect resets it.
func (t *Transport) NextBackoff() time.Duration {
	d := t.backoff
	if d < time.Second {
		d = time.Second
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	t.backoff = d * 2
	return d
}

// ResetBackoff is called after a successful connect.
func (t *Transport) ResetBackoff() {
	t.backoff = time.Second
}
