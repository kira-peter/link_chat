package server

import (
	"testing"
	"time"

	"link-chat/pkg/protocol"
)

// waitEvent reads frames until one of the given name arrives, returning its
// payload. It fails the test on timeout.
func (c *client) waitEvent(name string, d time.Duration) protocol.EventPayload {
	c.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				c.t.Fatalf("connection closed while waiting for event %q", name)
			}
			if f.Type != protocol.TypeEvent {
				continue
			}
			var ep protocol.EventPayload
			if protocol.DecodePayload(f.Payload, &ep) != nil {
				continue
			}
			if ep.Name == name {
				return ep
			}
		case <-deadline:
			c.t.Fatalf("timed out waiting for event %q", name)
			return protocol.EventPayload{}
		}
	}
}

// The second login takes the account over and the first is told where from
// (spec login-session / 被顶掉的提示).
func TestSecondLoginDisplacesFirst(t *testing.T) {
	hs, _ := testServer(t)

	first := dial(t, hs)
	first.registerWithQuestion("alice", "correct horse battery", "Q?", "a")

	second := dial(t, hs)
	second.hello()
	var res protocol.AuthRes
	second.callOK(protocol.OpAuthLogin, protocol.LoginReq{
		Username: "alice", Password: "correct horse battery", Device: "laptop",
	}, &res)
	if res.Token == "" {
		t.Fatal("second login got no token")
	}

	// The displaced connection must learn why it is going away.
	ep := first.waitEvent(protocol.EvSessionReplaced, 5*time.Second)
	var se protocol.SessionReplacedEvent
	if err := protocol.DecodePayload(ep.Data, &se); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if se.Username != "alice" {
		t.Fatalf("wrong username in notice: %q", se.Username)
	}
	if se.Location == "" {
		t.Fatal("notice carries no location")
	}
	if se.At == 0 {
		t.Fatal("notice carries no timestamp")
	}
	if se.Device != "laptop" {
		t.Fatalf("notice should name the new client, got %q", se.Device)
	}

	// And the connection must actually close.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			select {
			case _, ok := <-first.frames:
				if !ok {
					return
				}
			case <-time.After(5 * time.Second):
				return
			}
		}
	}()
	select {
	case <-closed:
	case <-time.After(6 * time.Second):
		t.Fatal("displaced connection was not closed")
	}
}

// The old token must be worthless immediately, without waiting for the socket.
func TestDisplacedTokenIsRevoked(t *testing.T) {
	hs, _ := testServer(t)

	first := dial(t, hs)
	oldSession := first.registerWithQuestion("alice", "correct horse battery", "Q?", "a")

	second := dial(t, hs)
	second.hello()
	var res protocol.AuthRes
	second.callOK(protocol.OpAuthLogin, protocol.LoginReq{
		Username: "alice", Password: "correct horse battery",
	}, &res)

	// A fresh connection presenting the old token must be refused.
	third := dial(t, hs)
	third.hello()
	third.callErr(protocol.OpAuthResume, protocol.ResumeReq{Token: oldSession.Token}, protocol.CodeUnauthorized)

	// The new one still works.
	ok := dial(t, hs)
	ok.hello()
	var got protocol.AuthRes
	ok.callOK(protocol.OpAuthResume, protocol.ResumeReq{Token: res.Token}, &got)
}

// Displacement is per-account: bob keeps chatting while alice is taken over.
func TestDisplacementDoesNotAffectOtherAccounts(t *testing.T) {
	hs, _ := testServer(t)

	bob := dial(t, hs)
	bobSession := bob.registerWithQuestion("bob", "correct horse battery", "Q?", "a")

	a1 := dial(t, hs)
	a1.registerWithQuestion("alice", "correct horse battery", "Q?", "a")
	a2 := dial(t, hs)
	a2.hello()
	a2.callOK(protocol.OpAuthLogin, protocol.LoginReq{
		Username: "alice", Password: "correct horse battery",
	}, nil)

	// Bob's session still resumes.
	check := dial(t, hs)
	check.hello()
	var res protocol.AuthRes
	check.callOK(protocol.OpAuthResume, protocol.ResumeReq{Token: bobSession.Token}, &res)
	if res.User.Username != "bob" {
		t.Fatalf("wrong user: %q", res.User.Username)
	}
}

// A connection re-authenticating as itself is not "another session" and must not
// kick itself (spec login-session / 同一连接重复认证).
func TestReauthOnSameConnectionDoesNotSelfDisplace(t *testing.T) {
	hs, _ := testServer(t)

	c := dial(t, hs)
	c.registerWithQuestion("alice", "correct horse battery", "Q?", "a")

	// Same connection logs in again.
	var res protocol.AuthRes
	c.callOK(protocol.OpAuthLogin, protocol.LoginReq{
		Username: "alice", Password: "correct horse battery",
	}, &res)
	if res.Token == "" {
		t.Fatal("re-auth failed")
	}

	// The connection must still work afterwards.
	c.callOK(protocol.OpPresencePing, protocol.PingReq{}, nil)
}

// A reset token survives a login from elsewhere: recovery is not a session.
func TestDisplacementKeepsResetToken(t *testing.T) {
	hs, _ := testServer(t)

	victim := dial(t, hs)
	victim.registerWithQuestion("alice", "correct horse battery", "Q?", "a")

	// Start recovery and hold a reset token.
	rec := dial(t, hs)
	rec.hello()
	var v protocol.RecoverVerifyRes
	rec.callOK(protocol.OpRecoverVerify, protocol.RecoverVerifyReq{Username: "alice", Answer: "a"}, &v)

	// Someone logs in elsewhere.
	other := dial(t, hs)
	other.hello()
	other.callOK(protocol.OpAuthLogin, protocol.LoginReq{
		Username: "alice", Password: "correct horse battery",
	}, nil)

	// The reset token still works.
	rec.callOK(protocol.OpRecoverReset, protocol.RecoverResetReq{
		ResetToken: v.ResetToken, NewPassword: "a brand new password",
	}, nil)
}

// An idle connection stays alive because the client pings: this asserts the
// server's read timeout is refreshed by a ping request
// (spec chat-protocol / 客户端主动心跳保持空闲连接).
func TestPingKeepsConnectionAlive(t *testing.T) {
	hs, srv := testServer(t)
	// A short heartbeat makes the timeout observable without a long test.
	c := dial(t, hs)
	c.registerWithQuestion("alice", "correct horse battery", "Q?", "a")

	interval := srv.cfg.HeartbeatInterval
	if interval <= 0 {
		t.Fatal("heartbeat interval not configured")
	}
	// Ping at half the heartbeat, for longer than the full timeout. Without the
	// pings the server would have dropped this connection long before the end.
	deadline := time.Now().Add(interval*2 + time.Second)
	pinged := 0
	for time.Now().Before(deadline) {
		c.callOK(protocol.OpPresencePing, protocol.PingReq{}, nil)
		pinged++
		time.Sleep(interval / 2)
	}
	if pinged < 2 {
		t.Fatalf("only sent %d pings", pinged)
	}
	// Still usable.
	c.callOK(protocol.OpUserList, nil, nil)
}

// Auto sign-in (resume) must displace a newer session exactly like a fresh
// login does. This is the path a client takes on startup, and it was the one
// that silently allowed two live sessions.
func TestResumeDisplacesExistingSession(t *testing.T) {
	hs, _ := testServer(t)

	// Establish a session and keep its token.
	first := dial(t, hs)
	session := first.registerWithQuestion("alice", "correct horse battery", "Q?", "a")
	// first stays connected: that is the session that must be displaced.

	// A second client resumes the account on a new connection.
	second := dial(t, hs)
	second.hello()
	var res protocol.AuthRes
	second.callOK(protocol.OpAuthResume, protocol.ResumeReq{
		Token: session.Token, Device: "laptop",
	}, &res)

	if res.Token == "" {
		t.Fatal("resume returned no token")
	}
	if res.Token == session.Token {
		t.Fatal("resume returned the same token; it must rotate so the loser cannot reconnect with it")
	}

	// The first connection must be told, and told where from.
	ep := first.waitEvent(protocol.EvSessionReplaced, 5*time.Second)
	var se protocol.SessionReplacedEvent
	if err := protocol.DecodePayload(ep.Data, &se); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if se.Username != "alice" {
		t.Fatalf("wrong username: %q", se.Username)
	}
	if se.Location == "" {
		t.Fatal("no location in the notice")
	}
	if se.Device != "laptop" {
		t.Fatalf("wrong device: %q", se.Device)
	}
}

// The displaced client's old token must be dead, so reconnecting with it cannot
// start a tug of war.
func TestResumeRotatesAwayOldToken(t *testing.T) {
	hs, _ := testServer(t)

	first := dial(t, hs)
	session := first.registerWithQuestion("alice", "correct horse battery", "Q?", "a")

	second := dial(t, hs)
	second.hello()
	var res protocol.AuthRes
	second.callOK(protocol.OpAuthResume, protocol.ResumeReq{Token: session.Token}, &res)

	// The old token no longer resumes.
	stale := dial(t, hs)
	stale.hello()
	stale.callErr(protocol.OpAuthResume, protocol.ResumeReq{Token: session.Token}, protocol.CodeUnauthorized)

	// The rotated one does.
	fresh := dial(t, hs)
	fresh.hello()
	var ok protocol.AuthRes
	fresh.callOK(protocol.OpAuthResume, protocol.ResumeReq{Token: res.Token}, &ok)
	if ok.User.Username != "alice" {
		t.Fatalf("wrong user: %q", ok.User.Username)
	}
}

// Two clients sharing one credentials file (the same token) must still end up
// with a single live session: the second assumes the account and the first is
// told.
func TestResumeWithSharedTokenLeavesOneSession(t *testing.T) {
	hs, srv := testServer(t)

	first := dial(t, hs)
	session := first.registerWithQuestion("alice", "correct horse battery", "Q?", "a")

	second := dial(t, hs)
	second.hello()
	var res protocol.AuthRes
	second.callOK(protocol.OpAuthResume, protocol.ResumeReq{Token: session.Token}, &res)

	// Exactly one connection for this account should remain live.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if n := len(srv.hub.connectionsForUser(res.User.ID, nil)); n == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected 1 live session, found %d", len(srv.hub.connectionsForUser(res.User.ID, nil)))
}
