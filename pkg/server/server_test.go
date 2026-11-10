package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.HeartbeatSeconds = 5
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	st, err := storage.OpenJSON(cfg.DataDir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
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

// snapshotDir lists every file under dir with its size and mtime.
func snapshotDir(dir string) []string {
	var out []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
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

func TestDMIsVisibleOnlyToParticipants(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct horse battery")
	b := dial(t, hs)
	b.register("bob", "correct horse battery")

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
