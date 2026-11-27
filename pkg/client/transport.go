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
	"link-chat/pkg/version"
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
// and its lifetime. It delivers events to a callback rather than
// holding any UI state.
type Transport struct {
	// ServerURL is the ws:// or wss:// endpoint, e.g. wss://chat.example.com/ws.
	ServerURL string

	// OnEvent is called for every server push. It is invoked from the read
	// goroutine, so it must not block.
	OnEvent func(name string, data json.RawMessage)
	// OnStateChange is called whenever the connection state moves.
	OnStateChange func(ConnState, error)

	mu            sync.Mutex
	ws            *websocket.Conn
	state         ConnState
	nextID        int
	pending       map[string]chan protocol.Frame
	viewedChannel string
	viewChanged   chan struct{}
	channelUpdate bool

	closed   chan struct{}
	closeOne sync.Once

	cancel context.CancelFunc // Set once by Connect; protected by mu.
}

// NewTransport builds a transport; call Connect to start it.
func NewTransport(serverURL string) *Transport {
	return &Transport{
		ServerURL: serverURL,
		state:     StateUnconfigured,
		pending:   map[string]chan protocol.Frame{},
		closed:    make(chan struct{}),
	}
}

// State reports the current state.
func (t *Transport) State() ConnState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// SupportsChannelUpdate reports the capability advertised during the handshake.
func (t *Transport) SupportsChannelUpdate() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.channelUpdate
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
	t.mu.Lock()
	select {
	case <-t.closed:
		t.mu.Unlock()
		return ErrClosed
	default:
	}
	if t.cancel != nil {
		t.mu.Unlock()
		return errors.New("transport already started")
	}
	ctx, stop := context.WithCancel(ctx)
	t.cancel = stop
	t.mu.Unlock()
	t.setState(StateConnecting, nil)

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(dialCtx, t.ServerURL, nil)
	if err != nil {
		stop()
		return fmt.Errorf("connect: %w", err)
	}

	// History and timeline pages can legally exceed the library default.
	ws.SetReadLimit(4 << 20)
	if err := t.handshake(ctx, ws); err != nil {
		stop()
		ws.CloseNow()
		return err
	}

	t.mu.Lock()
	if ctx.Err() != nil {
		t.mu.Unlock()
		ws.CloseNow()
		return ctx.Err()
	}
	t.ws = ws
	viewChanged := make(chan struct{}, 1)
	t.viewChanged = viewChanged
	t.mu.Unlock()

	t.setState(StateAuthenticating, nil)
	go t.readLoop(ctx, ws)
	go t.heartbeatLoop(ctx, ws, viewChanged)
	return nil
}

// HeartbeatInterval is how often the client proves it is still alive. It is half
// the server's read timeout, so one lost beat does not end the connection.
const HeartbeatInterval = 15 * time.Second

// SetViewedChannel updates the focus reported by the heartbeat loop.
func (t *Transport) SetViewedChannel(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if id != t.viewedChannel {
		t.viewedChannel = id
		t.wakeChannelReport()
	}
}

// ChannelJoined retries the report after joining a channel that was just opened.
func (t *Transport) ChannelJoined(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.viewedChannel == id {
		t.wakeChannelReport()
	}
}

func (t *Transport) wakeChannelReport() {
	select {
	case t.viewChanged <- struct{}{}:
	default:
	}
}

// heartbeatLoop keeps the socket alive and reports page changes promptly.
// Only application frames reset the server's read timer.
func (t *Transport) heartbeatLoop(ctx context.Context, ws *websocket.Conn, viewChanged <-chan struct{}) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	var retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.closed:
			return
		case <-ticker.C:
			if retry != nil {
				continue
			}
		case <-viewChanged:
			if retry != nil {
				continue
			}
		case <-retry:
			retry = nil
		}
		t.mu.Lock()
		channel := t.viewedChannel
		t.mu.Unlock()
		frame, err := t.call(ctx, ws, protocol.OpPresencePing, protocol.PingReq{
			Nonce: time.Now().UnixMilli(), ViewedChannel: &channel,
		}, 10*time.Second)
		if err != nil {
			return
		}
		if frame.Type == protocol.TypeError {
			var failure protocol.ResPayload
			if protocol.DecodePayload(frame.Payload, &failure) == nil && failure.Code == protocol.CodeRateLimited {
				// A burst of page actions can use the request budget. Keep only
				// the latest view and retry it once the server allows requests.
				retry = time.After(max(50*time.Millisecond, time.Duration(failure.RetryAfterMS)*time.Millisecond))
			}
		}
	}
}

// ClientName is how this client identifies itself in the handshake, followed
// by its release version (spec chat-protocol / 握手交换发布版本).
const ClientName = "link-chat-tui"

// OutdatedError is a handshake the server refused because this client is older
// than its min_client_version. Retrying cannot help, so callers must stop
// reconnecting and tell the user to upgrade (spec tui-chat-client /
// 客户端版本过旧时的界面).
type OutdatedError struct {
	Min     string // the server's minimum client version
	Current string // this client's version
}

func (e *OutdatedError) Error() string {
	return "client " + e.Current + " is older than the server's minimum " + e.Min
}

// handshake performs version negotiation. A mismatch is fatal for the session:
// there is no point retrying against a server that speaks another version.
func (t *Transport) handshake(ctx context.Context, ws *websocket.Conn) error {
	f, err := protocol.NewResRequest("hello", protocol.TypeHello, protocol.HelloPayload{
		Version: protocol.Version,
		Client:  version.ClientTag(ClientName),
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
		if p.Code == protocol.CodeClientOutdated {
			var d protocol.ClientOutdatedData
			_ = json.Unmarshal(p.Data, &d)
			return &OutdatedError{Min: d.MinVersion, Current: version.String()}
		}
		return fmt.Errorf("%s: %s", p.Code, p.Message)
	}
	// The ack is a response envelope; the hello fields are its data.
	var rp protocol.ResPayload
	var hp protocol.HelloPayload
	if err := json.Unmarshal(reply.Payload, &rp); err != nil {
		return fmt.Errorf("hello-ack: %w", err)
	}
	if len(rp.Data) > 0 {
		if err := json.Unmarshal(rp.Data, &hp); err != nil {
			return fmt.Errorf("hello-ack: %w", err)
		}
	}
	if hp.Version > protocol.Version {
		return fmt.Errorf("server speaks protocol v%d, client v%d", hp.Version, protocol.Version)
	}
	t.mu.Lock()
	t.channelUpdate = hp.ChannelUpdate
	t.mu.Unlock()
	return nil
}

// readLoop pumps frames off the socket.
func (t *Transport) readLoop(ctx context.Context, ws *websocket.Conn) {
	var disconnect error
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
		t.cancel()
		t.mu.Unlock()
		_ = ws.CloseNow()
		select {
		case <-t.closed:
		default:
			t.setState(StateDisconnected, disconnect)
		}
	}()

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			disconnect = err
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
	return t.call(ctx, nil, op, data, timeout)
}

func (t *Transport) call(ctx context.Context, expected *websocket.Conn, op string, data any, timeout time.Duration) (protocol.Frame, error) {
	t.mu.Lock()
	ws := t.ws
	if ws == nil || (expected != nil && ws != expected) {
		t.mu.Unlock()
		return protocol.Frame{}, ErrClosed
	}
	t.nextID++
	id := fmt.Sprintf("q%d", t.nextID)
	ch := make(chan protocol.Frame, 1)
	t.pending[id] = ch
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
	}()
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
		return protocol.Frame{}, fmt.Errorf("send %s: %w", op, err)
	}

	select {
	case reply, ok := <-ch:
		if !ok {
			return protocol.Frame{}, ErrClosed
		}
		return reply, nil
	case <-wctx.Done():
		return protocol.Frame{}, fmt.Errorf("%s: %w", op, wctx.Err())
	}
}

// Close tears the transport down for good.
func (t *Transport) Close() {
	t.closeOne.Do(func() {
		close(t.closed)
		t.mu.Lock()
		if t.cancel != nil {
			t.cancel()
		}
		ws := t.ws
		t.ws = nil
		t.mu.Unlock()
		if ws != nil {
			ws.CloseNow()
		}
	})
}
