package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// testServer spins up a real HTTP server so tests exercise the actual upgrade
// path rather than a hand-rolled pipe.
func testServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	return testServerWith(t, nil, io.Discard)
}

// testServerWith is testServer with a config tweak applied before validation
// and logs written to logOut.
func testServerWith(t *testing.T, tweak func(*Config), logOut io.Writer) (*httptest.Server, *Server) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.HeartbeatSeconds = 5
	if tweak != nil {
		tweak(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	st, err := storage.Open(cfg.DataDir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(logOut, nil))
	srv := New(cfg, st, logger)
	hs := httptest.NewServer(srv.Handler())

	t.Cleanup(func() {
		// Stop the server first: Shutdown waits for live connections to drain, so
		// this is what guarantees no handler is mid-write when storage closes.
		hs.CloseClientConnections()
		_ = srv.Shutdown()
		hs.Close()

		// Closing storage must be the last thing that touches the directory, and
		// the actor goroutine must be truly finished before the test framework
		// removes it.
		_ = st.Close()

		// Verify quiescence cheaply: a write that lands after Close would surface
		// as a confusing "directory is not empty" error in the temp-dir cleanup,
		// with no clue which file it was. A couple of short polls catch an
		// in-flight write and name the file.
		if left := waitQuiescent(cfg.DataDir, 3); len(left) > 0 {
			t.Errorf("storage still being written after close: %v", left)
		}
	})
	return hs, srv
}

// waitQuiescent polls a few times for new files to appear after storage closed,
// and returns them. Short by design: this is a diagnostic, not a synchroniser,
// and every test pays for it.
func waitQuiescent(dir string, polls int) []string {
	before := len(snapshotDir(dir))
	for i := 0; i < polls; i++ {
		time.Sleep(10 * time.Millisecond)
		if after := snapshotDir(dir); len(after) > before {
			return after
		}
	}
	return nil
}

// snapshotDir lists every database file under dir with its size and mtime.
// Only names the store itself writes count: on Windows a scanner can briefly
// drop its own copy of a just-deleted -shm file next to it.
func snapshotDir(dir string) []string {
	var out []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || !strings.HasPrefix(info.Name(), storage.DBFile) {
			return nil
		}
		out = append(out, fmt.Sprintf("%s (%d bytes, %s)", p, info.Size(), info.ModTime().Format("15:04:05.000")))
		return nil
	})
	return out
}

// client is a minimal protocol client for tests.
//
// Frames are read by a background goroutine into a channel rather than on
// demand: coder/websocket cancels the connection when a read's context expires,
// so a "poll with a short timeout" helper would silently break the connection it
// is trying to inspect.
type client struct {
	t    *testing.T
	ws   *websocket.Conn
	ctx  context.Context
	next int

	frames chan protocol.Frame
	done   chan struct{}
}

func dial(t *testing.T, hs *httptest.Server) *client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := &client{
		t:      t,
		ws:     ws,
		ctx:    ctx,
		frames: make(chan protocol.Frame, 256),
		done:   make(chan struct{}),
	}
	go c.readLoop()
	t.Cleanup(func() {
		ws.CloseNow()
	})
	return c
}

// readLoop pumps every frame into the channel until the connection ends.
func (c *client) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.ws.Read(c.ctx)
		if err != nil {
			return
		}
		f, err := protocol.Decode(data)
		if err != nil {
			continue
		}
		select {
		case c.frames <- f:
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *client) send(f protocol.Frame) {
	c.t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		c.t.Fatalf("encode: %v", err)
	}
	if err := c.ws.Write(c.ctx, websocket.MessageText, b); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *client) recv() protocol.Frame {
	c.t.Helper()
	select {
	case f, ok := <-c.frames:
		if !ok {
			c.t.Fatal("connection closed while waiting for a frame")
		}
		return f
	case <-time.After(10 * time.Second):
		c.t.Fatal("timed out waiting for a frame")
		return protocol.Frame{}
	}
}

// recvMatching skips events and returns the next response with the given ID.
func (c *client) recvMatching(id string) protocol.Frame {
	c.t.Helper()
	for i := 0; i < 50; i++ {
		f := c.recv()
		if f.ID == id && (f.Type == protocol.TypeRes || f.Type == protocol.TypeError) {
			return f
		}
	}
	c.t.Fatalf("no response with id %q", id)
	return protocol.Frame{}
}

func (c *client) hello() {
	c.t.Helper()
	c.send(mustFrame(protocol.NewResRequest("h1", protocol.TypeHello, protocol.HelloPayload{Version: protocol.Version})))
	f := c.recvMatching("h1")
	if f.Type != protocol.TypeRes {
		c.t.Fatalf("hello failed: %+v", f)
	}
}

func (c *client) call(op string, data any) protocol.Frame {
	c.t.Helper()
	c.next++
	id := "r" + itoa(c.next)
	c.send(mustFrame(protocol.NewReq(id, op, data)))
	return c.recvMatching(id)
}

// callOK fails the test when the call did not succeed.
func (c *client) callOK(op string, data any, out any) {
	c.t.Helper()
	f := c.call(op, data)
	if f.Type != protocol.TypeRes {
		c.t.Fatalf("%s failed: %s", op, string(f.Payload))
	}
	if out != nil {
		var p protocol.ResPayload
		if err := json.Unmarshal(f.Payload, &p); err != nil {
			c.t.Fatalf("%s: bad payload: %v", op, err)
		}
		if err := json.Unmarshal(p.Data, out); err != nil {
			c.t.Fatalf("%s: decode data: %v", op, err)
		}
	}
}

// callErr asserts the call failed with the given code.
func (c *client) callErr(op string, data any, code string) {
	c.t.Helper()
	f := c.call(op, data)
	if f.Type != protocol.TypeError {
		c.t.Fatalf("%s unexpectedly succeeded: %s", op, string(f.Payload))
	}
	var p protocol.ResPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		c.t.Fatalf("decode: %v", err)
	}
	if p.Code != code {
		c.t.Fatalf("%s: want code %q, got %q", op, code, p.Code)
	}
}

func (c *client) login(username, password string) protocol.AuthRes {
	c.t.Helper()
	c.hello()
	var res protocol.AuthRes
	c.callOK(protocol.OpAuthRegister, protocol.RegisterReq{Username: username, Password: password}, &res)
	if res.Token == "" {
		c.t.Fatal("no token issued")
	}
	return res
}

func (c *client) register(username, password string) protocol.AuthRes {
	c.t.Helper()
	c.hello()
	var res protocol.AuthRes
	// Recovery material is required by the server; tests that do not care about
	// it still have to supply some.
	c.callOK(protocol.OpAuthRegister, protocol.RegisterReq{
		Username: username, Password: password,
		SecurityQuestion: "Test question?", SecurityAnswer: "test answer",
	}, &res)
	return res
}

// drainEvents collects whatever has already arrived, without waiting. A small
// settle delay lets in-flight broadcasts land first.
func (c *client) drainEvents() []protocol.Frame {
	c.t.Helper()
	time.Sleep(80 * time.Millisecond)
	var out []protocol.Frame
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				return out
			}
			out = append(out, f)
		default:
			return out
		}
	}
}

func mustFrame(f protocol.Frame, err error) protocol.Frame {
	if err != nil {
		panic(err)
	}
	return f
}

// ---------- protocol handshake ----------

func TestHelloNegotiatesVersion(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)
	c.hello()
}

func TestHelloRejectsNewerVersion(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)

	c.send(mustFrame(protocol.NewResRequest("h1", protocol.TypeHello, protocol.HelloPayload{Version: protocol.Version + 1})))
	f := c.recvMatching("h1")
	if f.Type != protocol.TypeError {
		t.Fatalf("want error, got %+v", f)
	}
	var p protocol.ResPayload
	_ = json.Unmarshal(f.Payload, &p)
	if p.Code != protocol.CodeVersionUnsupported {
		t.Fatalf("want %s, got %s", protocol.CodeVersionUnsupported, p.Code)
	}
}

// The first frame must be hello; anything else is a protocol violation.
func TestRequestBeforeHelloIsRejected(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)

	c.send(mustFrame(protocol.NewReq("r1", protocol.OpChannelList, nil)))
	f := c.recvMatching("r1")
	if f.Type != protocol.TypeError {
		t.Fatalf("want error, got %+v", f)
	}
	var p protocol.ResPayload
	_ = json.Unmarshal(f.Payload, &p)
	if p.Code != protocol.CodeProtocolViolation {
		t.Fatalf("want %s, got %s", protocol.CodeProtocolViolation, p.Code)
	}
}

// ---------- auth ----------

func TestRegisterThenLogin(t *testing.T) {
	hs, _ := testServer(t)

	c1 := dial(t, hs)
	c1.register("alice", "correct horse battery")

	c2 := dial(t, hs)
	c2.hello()
	var res protocol.AuthRes
	c2.callOK(protocol.OpAuthLogin, protocol.LoginReq{Username: "alice", Password: "correct horse battery"}, &res)
	if res.User.Username != "alice" {
		t.Fatalf("wrong user: %+v", res.User)
	}
}

func TestDuplicateUsernameRejected(t *testing.T) {
	hs, _ := testServer(t)
	dial(t, hs).register("alice", "correct horse battery")

	c := dial(t, hs)
	c.hello()
	c.callErr(protocol.OpAuthRegister, protocol.RegisterReq{
		Username: "Alice", Password: "correct horse battery",
		SecurityQuestion: "Test question?", SecurityAnswer: "test answer",
	}, protocol.CodeUsernameTaken)
}

func TestWeakPasswordRejected(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)
	c.hello()
	c.callErr(protocol.OpAuthRegister, protocol.RegisterReq{Username: "alice", Password: "short"}, protocol.CodeWeakPassword)
}

func TestInvalidUsernameRejected(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)
	c.hello()
	c.callErr(protocol.OpAuthRegister, protocol.RegisterReq{Username: "has space", Password: "correct horse battery"}, protocol.CodeInvalidUsername)
}

// Wrong password must not reveal whether the username exists
// (spec account-auth / 凭据错误).
func TestWrongPasswordIsIndistinguishableFromUnknownUser(t *testing.T) {
	hs, _ := testServer(t)
	dial(t, hs).register("alice", "correct horse battery")

	c := dial(t, hs)
	c.hello()
	known := c.call(protocol.OpAuthLogin, protocol.LoginReq{Username: "alice", Password: "wrong password"})
	unknown := c.call(protocol.OpAuthLogin, protocol.LoginReq{Username: "nobody", Password: "wrong password"})

	var pk, pu protocol.ResPayload
	_ = json.Unmarshal(known.Payload, &pk)
	_ = json.Unmarshal(unknown.Payload, &pu)
	if pk.Code != pu.Code {
		t.Fatalf("codes differ: %q vs %q", pk.Code, pu.Code)
	}
	if pk.Code != protocol.CodeInvalidCredentials {
		t.Fatalf("want %s, got %s", protocol.CodeInvalidCredentials, pk.Code)
	}
}

func TestUnauthenticatedRequestRejected(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)
	c.hello()
	c.callErr(protocol.OpChannelList, nil, protocol.CodeUnauthorized)
}

// ---------- channels ----------

func TestChannelCreateJoinAndMembership(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")

	var created protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &created)
	if created.Name != "general" || !created.Joined {
		t.Fatalf("bad channel: %+v", created)
	}
	if created.MemberCount != 1 {
		t.Fatalf("creator should be a member, count=%d", created.MemberCount)
	}

	// A second user sees it but is not a member yet.
	b := dial(t, hs)
	b.register("bob", "correct horse battery")
	var list protocol.ChannelListRes
	b.callOK(protocol.OpChannelList, nil, &list)
	var found *protocol.Conversation
	for i := range list.Conversations {
		if list.Conversations[i].Name == "general" {
			found = &list.Conversations[i]
		}
	}
	if found == nil {
		t.Fatal("public channel not listed for another user")
	}
	if found.Joined {
		t.Fatal("bob should not be a member yet")
	}

	var joined protocol.Conversation
	b.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: created.ID}, &joined)
	if joined.MemberCount != 2 {
		t.Fatalf("want 2 members, got %d", joined.MemberCount)
	}

	// Joining twice is idempotent.
	b.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: created.ID}, &joined)
	if joined.MemberCount != 2 {
		t.Fatalf("re-join changed membership: %d", joined.MemberCount)
	}
}

func TestDashboardReportsDeviceAndChannelMembership(t *testing.T) {
	hs, srv := testServer(t)
	const password = "correct horse battery"
	alice := dial(t, hs)
	account := alice.register("alice", password)
	desktop := dial(t, hs)
	desktop.hello()
	desktop.callOK(protocol.OpAuthLogin, protocol.LoginReq{Username: "alice", Password: password, Device: "desktop"}, nil)
	matches := func(connection OnlineConn) bool {
		return connection.UserID == account.User.ID && connection.Device == "desktop" && connection.Since > 0
	}
	if !slices.ContainsFunc(srv.DashboardConnections(), matches) {
		t.Fatalf("desktop connection missing from %+v", srv.DashboardConnections())
	}

	var channel protocol.Conversation
	desktop.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "lobby"}, &channel)
	bob := dial(t, hs)
	bob.register("bob", password)
	bob.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: channel.ID}, nil)
	counts, err := srv.DashboardChannelCounts([]string{channel.ID})
	if c := counts[channel.ID]; err != nil || c.OnlineCount != 2 || c.MemberCount != 2 {
		t.Fatalf("dashboard channel counts = %+v, %v", counts, err)
	}
}

func TestChannelVisibilityAndDiscovery(t *testing.T) {
	hs, _ := testServer(t)
	alice, bob, guest := dial(t, hs), dial(t, hs), dial(t, hs)
	alice.register("alice", "correct horse battery")
	bob.register("bob", "correct horse battery")
	guest.hello()

	var private, public protocol.Conversation
	alice.callOK(protocol.OpChannelCreatePrivate, protocol.ChannelCreateReq{Name: "staff", Topic: "  内部讨论  "}, &private)
	alice.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "lobby", Topic: "欢迎来聊天"}, &public)
	if private.Visibility != protocol.ChannelPrivate || private.Topic != "内部讨论" || !private.Joined ||
		public.Visibility != protocol.ChannelPublic || public.Topic != "欢迎来聊天" {
		t.Fatalf("unexpected channel metadata: private=%+v public=%+v", private, public)
	}
	var announcements []string
	for _, frame := range bob.drainEvents() {
		var event protocol.EventPayload
		if frame.Type == protocol.TypeEvent && json.Unmarshal(frame.Payload, &event) == nil && event.Name == protocol.EvChannelCreated {
			var ref protocol.ConversationRef
			if err := json.Unmarshal(event.Data, &ref); err != nil {
				t.Fatal(err)
			}
			announcements = append(announcements, ref.ConversationID)
		}
	}
	if len(announcements) != 1 || announcements[0] != public.ID {
		t.Fatalf("bob's channel announcements: %v", announcements)
	}
	for _, frame := range guest.drainEvents() {
		var event protocol.EventPayload
		if frame.Type == protocol.TypeEvent && json.Unmarshal(frame.Payload, &event) == nil && event.Name == protocol.EvChannelCreated {
			t.Fatal("unauthenticated connection received a channel announcement")
		}
	}

	var list protocol.ChannelListRes
	bob.callOK(protocol.OpChannelList, nil, &list)
	if len(list.Conversations) != 1 || list.Conversations[0].ID != public.ID || list.Conversations[0].Joined ||
		list.Conversations[0].Topic != public.Topic {
		t.Fatalf("bob's channel list: %+v", list.Conversations)
	}
	privateRef := protocol.ConversationRef{ConversationID: private.ID}
	bob.callErr(protocol.OpChannelJoin, privateRef, protocol.CodeForbidden)
	bob.callErr(protocol.OpMessageHistory, protocol.HistoryReq{ConversationID: private.ID}, protocol.CodeForbidden)
	alice.callOK(protocol.OpChannelJoin, privateRef, nil)
	bob.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: public.ID}, nil)
	alice.callErr(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "bad", Visibility: "unknown"}, protocol.CodeInvalidArgument)
	alice.callErr(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "long", Topic: strings.Repeat("字", protocol.ChannelTopicMaxRunes+1)}, protocol.CodeInvalidArgument)
}

func TestChannelCreatorUpdatesPropertiesAndDiscovery(t *testing.T) {
	hs, _ := testServer(t)
	alice, bob, carol := dial(t, hs), dial(t, hs), dial(t, hs)
	alice.register("alice", "correct horse battery")
	bob.register("bob", "correct horse battery")
	carol.register("carol", "correct horse battery")

	var channel protocol.Conversation
	alice.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "lobby"}, &channel)
	ref := protocol.ConversationRef{ConversationID: channel.ID}
	bob.callOK(protocol.OpChannelJoin, ref, nil)
	alice.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: channel.ID, Body: "existing message"}, nil)
	carol.callOK(protocol.OpChannelList, nil, nil)
	update := protocol.ChannelUpdateReq{ConversationID: channel.ID, ChannelCreateReq: protocol.ChannelCreateReq{
		Name: "staff", Topic: "  Internal notes  ", Visibility: protocol.ChannelPrivate,
	}}
	bob.callErr(protocol.OpChannelUpdate, update, protocol.CodeForbidden)
	var edited protocol.Conversation
	alice.callOK(protocol.OpChannelUpdate, update, &edited)
	if edited.ID != channel.ID || edited.CreatedBy != channel.CreatedBy || edited.MemberCount != 2 ||
		edited.Name != "staff" || edited.Topic != "Internal notes" || edited.Visibility != protocol.ChannelPrivate {
		t.Fatalf("updated channel: %+v", edited)
	}
	assertCatalogEvent := func(c *client, want string) {
		t.Helper()
		for _, f := range c.drainEvents() {
			var ev protocol.EventPayload
			if f.Type != protocol.TypeEvent || json.Unmarshal(f.Payload, &ev) != nil || ev.Name != want {
				continue
			}
			var got protocol.ConversationRef
			if json.Unmarshal(ev.Data, &got) == nil && got == ref {
				return
			}
		}
		t.Fatalf("missing event %s for %s", want, ref.ConversationID)
	}
	assertCatalogEvent(bob, protocol.EvChannelUpdated)
	assertCatalogEvent(carol, protocol.EvChannelGone)

	var list protocol.ChannelListRes
	carol.callOK(protocol.OpChannelList, nil, &list)
	if len(list.Conversations) != 0 {
		t.Fatalf("outsider sees private channel: %+v", list.Conversations)
	}
	carol.callErr(protocol.OpChannelJoin, ref, protocol.CodeForbidden)
	var history protocol.HistoryRes
	bob.callOK(protocol.OpMessageHistory, protocol.HistoryReq{ConversationID: channel.ID}, &history)
	if len(history.Messages) == 0 || history.Messages[len(history.Messages)-1].Body != "existing message" {
		t.Fatalf("existing member lost message history: %+v", history.Messages)
	}

	update.Visibility, update.Topic = protocol.ChannelPublic, ""
	alice.callOK(protocol.OpChannelUpdate, update, &edited)
	assertCatalogEvent(carol, protocol.EvChannelCreated)
	carol.callOK(protocol.OpChannelList, nil, &list)
	if len(list.Conversations) != 1 || list.Conversations[0].ID != channel.ID || list.Conversations[0].Topic != "" {
		t.Fatalf("reopened channel catalog: %+v", list.Conversations)
	}
	carol.callOK(protocol.OpChannelJoin, ref, nil)
}

func TestDuplicateChannelNameRejected(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, nil)
	a.callErr(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "General"}, protocol.CodeChannelExists)
}

func TestNonCreatorCannotDeleteChannel(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	b := dial(t, hs)
	b.register("bob", "correct horse battery")
	b.callErr(protocol.OpChannelDelete, protocol.ConversationRef{ConversationID: ch.ID}, protocol.CodeForbidden)
}

func TestCreatorCanDeleteChannel(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	a.callOK(protocol.OpChannelDelete, protocol.ConversationRef{ConversationID: ch.ID}, nil)

	var list protocol.ChannelListRes
	a.callOK(protocol.OpChannelList, nil, &list)
	for _, cv := range list.Conversations {
		if cv.ID == ch.ID {
			t.Fatal("deleted channel still listed")
		}
	}
}

// ---------- direct messages ----------

// makeFriends befriends two signed-in clients; a direct message needs it.
func makeFriends(a, b *client, aName, bName string) {
	a.callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: bName}, nil)
	b.callOK(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: aName}, nil)
}

func TestDMIsVisibleOnlyToParticipants(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	b := dial(t, hs)
	b.register("bob", "correct horse battery")
	makeFriends(a, b, "alice", "bob")

	var dmFromA protocol.Conversation
	a.callOK(protocol.OpDMOpen, protocol.DMOpenReq{Username: "bob"}, &dmFromA)

	// Both peers must land on the same conversation ID.
	var dmFromB protocol.Conversation
	b.callOK(protocol.OpDMOpen, protocol.DMOpenReq{Username: "alice"}, &dmFromB)
	if dmFromA.ID != dmFromB.ID {
		t.Fatalf("dm ids differ: %s vs %s", dmFromA.ID, dmFromB.ID)
	}

	// A third user cannot see it at all.
	c := dial(t, hs)
	c.register("carol", "correct horse battery")
	var list protocol.ChannelListRes
	c.callOK(protocol.OpChannelList, nil, &list)
	for _, cv := range list.Conversations {
		if cv.ID == dmFromA.ID {
			t.Fatal("third party can see someone else's dm")
		}
	}
	c.callErr(protocol.OpMessageSend, protocol.SendMessageReq{
		ConversationID: dmFromA.ID, Body: "hi",
	}, protocol.CodeForbidden)
}

func TestSelfDMRejected(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	a.callErr(protocol.OpDMOpen, protocol.DMOpenReq{Username: "alice"}, protocol.CodeInvalidArgument)
}

// ---------- messages ----------

func TestMessageBroadcastAndAck(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	b := dial(t, hs)
	b.register("bob", "correct horse battery")
	b.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: ch.ID}, nil)
	b.drainEvents()

	var acked protocol.MessageEvent
	a.callOK(protocol.OpMessageSend, protocol.SendMessageReq{
		ConversationID: ch.ID, Body: "hello", ClientMsgID: "local-1",
	}, &acked)

	if acked.Message.Body != "hello" {
		t.Fatalf("bad ack: %+v", acked.Message)
	}
	if acked.Message.ClientMsgID != "local-1" {
		t.Fatalf("client id not echoed: %+v", acked.Message)
	}

	// bob must receive the same message as an event.
	var got *protocol.MessageEvent
	for _, f := range b.drainEvents() {
		if f.Type != protocol.TypeEvent {
			continue
		}
		var ep protocol.EventPayload
		if err := json.Unmarshal(f.Payload, &ep); err != nil {
			continue
		}
		if ep.Name != protocol.EvMessage {
			continue
		}
		var me protocol.MessageEvent
		if err := json.Unmarshal(ep.Data, &me); err == nil && me.Message.Body == "hello" {
			got = &me
		}
	}
	if got == nil {
		t.Fatal("bob did not receive the broadcast")
	}
	if got.Message.ID != acked.Message.ID {
		t.Fatalf("ids differ: %s vs %s", got.Message.ID, acked.Message.ID)
	}
	if f := got.Message.AuthorName; f != "alice" {
		t.Fatalf("want author alice, got %q", f)
	}
}

func TestEmptyMessageRejected(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	a.callErr(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: ch.ID, Body: "   "}, protocol.CodeInvalidArgument)
}

func TestOverlongMessageRejected(t *testing.T) {
	hs, srv := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	body := strings.Repeat("x", srv.cfg.MaxMessageBytes+1)
	a.callErr(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: ch.ID, Body: body}, protocol.CodeMessageTooLong)
}

// A resend with the same client id must not create a second message
// (spec message-delivery / 重试不产生重复).
func TestResendDoesNotDuplicate(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	var first, second protocol.MessageEvent
	a.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: ch.ID, Body: "hi", ClientMsgID: "k1"}, &first)
	a.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: ch.ID, Body: "hi", ClientMsgID: "k1"}, &second)

	if first.Message.ID != second.Message.ID {
		t.Fatalf("resend created a new message: %s vs %s", first.Message.ID, second.Message.ID)
	}

	var hist protocol.HistoryRes
	a.callOK(protocol.OpMessageHistory, protocol.HistoryReq{ConversationID: ch.ID}, &hist)
	count := 0
	for _, m := range hist.Messages {
		if m.Body == "hi" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want exactly one stored message, got %d", count)
	}
}

func TestHistoryPagingAndOrdering(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	for i := 0; i < 12; i++ {
		a.callOK(protocol.OpMessageSend, protocol.SendMessageReq{
			ConversationID: ch.ID, Body: "msg " + itoa(i),
		}, nil)
	}

	var page1 protocol.HistoryRes
	a.callOK(protocol.OpMessageHistory, protocol.HistoryReq{ConversationID: ch.ID, Limit: 5}, &page1)
	if len(page1.Messages) != 5 {
		t.Fatalf("want 5, got %d", len(page1.Messages))
	}
	if !page1.HasMore {
		t.Fatal("expected more history")
	}
	// Ascending order so the client can append directly.
	for i := 1; i < len(page1.Messages); i++ {
		if page1.Messages[i].Seq <= page1.Messages[i-1].Seq {
			t.Fatalf("not ascending at %d", i)
		}
	}

	var page2 protocol.HistoryRes
	a.callOK(protocol.OpMessageHistory, protocol.HistoryReq{
		ConversationID: ch.ID, BeforeSeq: page1.Messages[0].Seq, Limit: 5,
	}, &page2)
	if len(page2.Messages) == 0 {
		t.Fatal("second page empty")
	}
	for _, old := range page1.Messages {
		for _, newer := range page2.Messages {
			if old.ID == newer.ID {
				t.Fatal("page overlap")
			}
		}
	}
}

func TestEmptyHistoryIsNotAnError(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	var hist protocol.HistoryRes
	a.callOK(protocol.OpMessageHistory, protocol.HistoryReq{ConversationID: ch.ID}, &hist)
	if len(hist.Messages) != 0 {
		t.Fatalf("want empty, got %d", len(hist.Messages))
	}
}

func TestEditAndDeletePermissions(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	var sent protocol.MessageEvent
	a.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: ch.ID, Body: "original"}, &sent)

	// The author may edit.
	var edited protocol.MessageEvent
	a.callOK(protocol.OpMessageEdit, protocol.EditMessageReq{MessageID: sent.Message.ID, Body: "revised"}, &edited)
	if edited.Message.Body != "revised" {
		t.Fatalf("edit did not apply: %+v", edited.Message)
	}
	if edited.Message.EditedAt == 0 {
		t.Fatal("edit timestamp missing")
	}

	// A non-author, non-creator may not edit.
	b := dial(t, hs)
	b.register("bob", "correct horse battery")
	b.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: ch.ID}, nil)
	b.callErr(protocol.OpMessageEdit, protocol.EditMessageReq{MessageID: sent.Message.ID, Body: "hacked"}, protocol.CodeForbidden)

	// Deleting keeps a placeholder so seq stays gapless.
	var deleted protocol.MessageEvent
	a.callOK(protocol.OpMessageDelete, protocol.DeleteMessageReq{MessageID: sent.Message.ID}, &deleted)
	if !deleted.Message.Deleted || deleted.Message.Body != "" {
		t.Fatalf("delete did not blank the body: %+v", deleted.Message)
	}
	if deleted.Message.Seq != sent.Message.Seq {
		t.Fatal("delete changed the seq")
	}

	var hist protocol.HistoryRes
	a.callOK(protocol.OpMessageHistory, protocol.HistoryReq{ConversationID: ch.ID}, &hist)
	for _, m := range hist.Messages {
		if m.ID == sent.Message.ID && !m.Deleted {
			t.Fatal("deleted message still returned as live")
		}
	}
}

// The channel creator may moderate other people's messages.
func TestChannelCreatorCanDeleteOthersMessages(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	var ch protocol.Conversation
	a.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &ch)

	b := dial(t, hs)
	b.register("bob", "correct horse battery")
	b.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: ch.ID}, nil)

	var bobMsg protocol.MessageEvent
	b.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: ch.ID, Body: "spam"}, &bobMsg)

	a.callOK(protocol.OpMessageDelete, protocol.DeleteMessageReq{MessageID: bobMsg.Message.ID}, nil)
}

// ---------- duplicate request id ----------

func TestDuplicateRequestIDRejected(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)
	c.hello()

	// The collision window is "while the first request is still being handled".
	// A ping finishes too fast to occupy it reliably, so this uses registration:
	// hashing the password with Argon2id is deliberately slow, which holds the
	// id for long enough that the second frame is guaranteed to land inside it.
	id := "dup-1"
	body := protocol.RegisterReq{
		Username: "alice", Password: "correct horse battery",
		SecurityQuestion: "Test question?", SecurityAnswer: "test answer",
	}
	c.send(mustFrame(protocol.NewReq(id, protocol.OpAuthRegister, body)))
	c.send(mustFrame(protocol.NewReq(id, protocol.OpAuthRegister, body)))

	sawDuplicate := false
	for i := 0; i < 6; i++ {
		f := c.recv()
		if f.Type != protocol.TypeError {
			continue
		}
		var p protocol.ResPayload
		_ = json.Unmarshal(f.Payload, &p)
		if p.Code == protocol.CodeDuplicateRequestID {
			sawDuplicate = true
			break
		}
	}
	if !sawDuplicate {
		t.Fatal("duplicate request id was not rejected")
	}
}

// ---------- health ----------

func TestHealthEndpoint(t *testing.T) {
	hs, _ := testServer(t)
	res, err := hs.Client().Get(hs.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("want 200, got %d", res.StatusCode)
	}
}

// A direct message is named after the other person, for each side, in the
// conversation list as well as in the reply that opened it.
func TestDMIsNamedAfterThePeerForEachSide(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	b := dial(t, hs)
	b.register("bob", "correct horse battery")
	makeFriends(a, b, "alice", "bob")

	var opened protocol.Conversation
	a.callOK(protocol.OpDMOpen, protocol.DMOpenReq{Username: "bob"}, &opened)
	if opened.Name != "bob" {
		t.Fatalf("alice opened a DM named %q, want bob", opened.Name)
	}
	nameIn := func(cl *client, id string) string {
		var list protocol.ChannelListRes
		cl.callOK(protocol.OpChannelList, nil, &list)
		for _, cv := range list.Conversations {
			if cv.ID == id {
				return cv.Name
			}
		}
		return "<absent>"
	}
	if got := nameIn(a, opened.ID); got != "bob" {
		t.Errorf("in alice's list the DM is named %q, want bob", got)
	}
	if got := nameIn(b, opened.ID); got != "alice" {
		t.Errorf("in bob's list the DM is named %q, want alice", got)
	}
}

func TestShutdownClosesIdleWebSocketBeforeGrace(t *testing.T) {
	hs, srv := testServer(t)
	c := dial(t, hs)
	c.hello()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Shutdown()
	}()
	c.waitEvent(protocol.EvShutdown, 2*time.Second)
	select {
	case <-done:
		if got := srv.ShutdownPhase(); got != ShutdownDrained {
			t.Fatalf("shutdown phase = %v, want drained", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle WebSocket kept shutdown open")
	}
}

func TestRateLimitedRequestReleasesID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RequestsPerSecond = 1
	srv := &Server{cfg: cfg}
	c := &Conn{srv: srv, out: make(chan protocol.Frame, 1)}
	if ok, _ := srv.limiterFor(c).Allow(c.remoteAddr); !ok {
		t.Fatal("first request should fit the limit")
	}
	request, err := protocol.NewReq("request", protocol.OpPresencePing, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.handleRequest(context.Background(), c, request)
	select {
	case f := <-c.out:
		var response protocol.ResPayload
		if err := protocol.DecodePayload(f.Payload, &response); err != nil || response.Code != protocol.CodeRateLimited {
			t.Fatalf("rate limit response = %+v, %v", response, err)
		}
	default:
		t.Fatal("no rate limit response")
	}
	if len(c.inflight) != 0 {
		t.Fatalf("completed request remains registered: %v", c.inflight)
	}
}

func TestForwardedHeadersRequireTrustedProxy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TrustProxy = true
	cfg.TrustedProxyCIDRs = []string{"192.0.2.0/24", "2001:db8::/48"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	srv := &Server{cfg: cfg}
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Header.Set("X-Forwarded-For", "198.51.100.22, 203.0.113.10, 192.0.2.20")
	r.Header.Set("X-Forwarded-Proto", "https")

	r.RemoteAddr = "203.0.113.11:45000"
	if got := srv.clientAddr(r); got != "203.0.113.11" || srv.isHTTPS(r) {
		t.Fatalf("direct connection: address %q, HTTPS %t", got, srv.isHTTPS(r))
	}
	r.RemoteAddr = "[2001:db8::11]:45000"
	if got := srv.clientAddr(r); got != "203.0.113.10" || !srv.isHTTPS(r) {
		t.Fatalf("trusted proxy chain: address %q, HTTPS %t", got, srv.isHTTPS(r))
	}
}
