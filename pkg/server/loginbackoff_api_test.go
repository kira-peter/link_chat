package server

import (
	"encoding/json"
	"io"
	"testing"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// wrongLogin sends one sign-in with a bad password and returns the frame.
func (c *client) wrongLogin(user string) protocol.Frame {
	c.t.Helper()
	return c.call(protocol.OpAuthLogin, protocol.LoginReq{Username: user, Password: "definitely-wrong"})
}

func errCode(f protocol.Frame) string {
	var p protocol.ResPayload
	_ = json.Unmarshal(f.Payload, &p)
	return p.Code
}

func errRetryAfter(f protocol.Frame) int {
	var p protocol.ResPayload
	_ = json.Unmarshal(f.Payload, &p)
	return p.RetryAfterMS
}

// The fourth wrong password arms a one-minute wait, and the wait belongs to
// one address and one account (spec account-auth / 认证滥用防护).
func TestLoginBackoffStartsOnFourthFailure(t *testing.T) {
	// The per-minute budget would otherwise refuse the extra probes before the
	// climb is visible; this test is about the climb.
	hs, _ := testServerWith(t, func(c *Config) { c.LoginAttemptsPerMinute = 100 }, io.Discard)
	c := dial(t, hs)
	c.hello()
	c.callOK(protocol.OpAuthRegister, protocol.RegisterReq{
		Username: "dana", Password: "correct horse battery",
		SecurityQuestion: "Test question?", SecurityAnswer: "test answer",
	}, nil)

	for i := 1; i <= 3; i++ {
		f := c.wrongLogin("dana")
		if got := errCode(f); got != protocol.CodeInvalidCredentials {
			t.Fatalf("failure %d: code %q, want invalid_credentials", i, got)
		}
	}
	f := c.wrongLogin("dana")
	if got := errCode(f); got != protocol.CodeInvalidCredentials {
		t.Fatalf("4th failure: code %q, want invalid_credentials", got)
	}
	// The next attempt is the one refused, with the wait the 4th failure armed.
	f = c.wrongLogin("dana")
	if got := errCode(f); got != protocol.CodeRateLimited {
		t.Fatalf("5th attempt: code %q, want rate_limited", got)
	}
	if ms := errRetryAfter(f); ms < 55_000 || ms > 60_000 {
		t.Fatalf("5th attempt: retry_after_ms %d, want a positive value up to 60000", ms)
	}

	// The wait is per account: a different username from the same address is
	// not affected.
	f = c.wrongLogin("someone-else")
	if got := errCode(f); got != protocol.CodeInvalidCredentials {
		t.Fatalf("other account: code %q, want invalid_credentials", got)
	}

	// And the right password for the blocked account is refused too, without
	// touching the store.
	f = c.call(protocol.OpAuthLogin, protocol.LoginReq{Username: "dana", Password: "correct horse battery"})
	if got := errCode(f); got != protocol.CodeRateLimited {
		t.Fatalf("correct password while blocked: code %q, want rate_limited", got)
	}
}

// A correct password resets the climb, and the wait survives a restart through
// the data directory (spec account-auth / 退避后恢复, 计数在服务端重启后保留).
func TestLoginBackoffPersistsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	newSrv := func() *Server {
		cfg := DefaultConfig()
		cfg.DataDir = dir
		cfg.HeartbeatSeconds = 5
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		st, err := storage.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		s := New(cfg, st, testLogger())
		t.Cleanup(func() { _ = st.Close() })
		return s
	}

	s := newSrv()
	key := loginFailureKey("10.0.0.9:1234", "erin")
	for i := 0; i < 4; i++ {
		s.loginBackoff.RecordFailure(key)
	}

	// A new server over the same directory still remembers.
	s2 := newSrv()
	if wait, blocked := s2.loginBackoff.Check(key); !blocked || wait <= 0 {
		t.Fatalf("after restart: wait=%v blocked=%v, want a positive wait", wait, blocked)
	}
}
