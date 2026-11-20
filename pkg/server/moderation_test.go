package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

const pw = "correct horse battery"

var admin = Actor{ID: "u_admin", Name: "boss"}

// errData returns the code and SanctionInfo of a failed response.
func errData(t *testing.T, f protocol.Frame) (string, protocol.SanctionInfo) {
	t.Helper()
	if f.Type != protocol.TypeError {
		t.Fatalf("want an error, got %s", f.Payload)
	}
	var p protocol.ResPayload
	_ = json.Unmarshal(f.Payload, &p)
	var d protocol.SanctionInfo
	if len(p.Data) > 0 {
		_ = json.Unmarshal(p.Data, &d)
	}
	return p.Code, d
}

// waitClosed waits for the server to close the client's connection.
func (c *client) waitClosed(d time.Duration) {
	c.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case _, ok := <-c.frames:
			if !ok {
				return
			}
		case <-c.done:
			return
		case <-deadline:
			c.t.Fatal("connection was not closed")
		}
	}
}

func login(c *client, user string) protocol.Frame {
	c.hello()
	return c.call(protocol.OpAuthLogin, protocol.LoginReq{Username: user, Password: pw})
}

// 2.2: a token is only accepted for its own purpose.
func TestTokenPurposeIsEnforced(t *testing.T) {
	hs, srv := testServer(t)
	c := dial(t, hs)
	res := c.register("alice", pw)

	raw, _ := newToken()
	srv.st.SaveToken(storage.Token{
		Hash: tokenHash(raw), UserID: res.User.ID, Purpose: storage.PurposeAdmin,
		CreatedAt: storage.Now(), ExpiresAt: storage.Now() + 3600_000,
	})
	c2 := dial(t, hs)
	c2.hello()
	c2.callErr(protocol.OpAuthResume, protocol.ResumeReq{Token: raw}, protocol.CodeUnauthorized)

	if _, e := srv.userForToken(res.Token, storage.PurposeAdmin); e == nil || e.Code != protocol.CodeUnauthorized {
		t.Fatalf("a chat token was accepted as an admin token: %v", e)
	}
	if uid, e := srv.userForToken(raw, storage.PurposeAdmin); e != nil || uid != res.User.ID {
		t.Fatalf("admin token rejected for its own purpose: %v", e)
	}
}

// 2.4: a banned account cannot sign in, resume, or recover; a wrong password
// does not reveal the ban; after expiry it can sign in again.
func TestBanBlocksAuthentication(t *testing.T) {
	hs, srv := testServer(t)
	c := dial(t, hs)
	res := c.registerWithQuestion("carol", pw, "Pet?", "rex")

	sc, err := srv.Sanction(admin, res.User.ID, storage.SanctionBan, "spam", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	c2 := dial(t, hs)
	code, info := errData(t, login(c2, "carol"))
	if code != protocol.CodeAccountBanned || info.Reason != "spam" || info.ExpiresAt != sc.ExpiresAt {
		t.Fatalf("login: %s %+v", code, info)
	}

	c3 := dial(t, hs)
	c3.hello()
	c3.callErr(protocol.OpAuthLogin, protocol.LoginReq{Username: "carol", Password: "wrong password!"}, protocol.CodeInvalidCredentials)

	// The ban revoked the session token, so resuming is refused outright.
	c4 := dial(t, hs)
	c4.hello()
	c4.callErr(protocol.OpAuthResume, protocol.ResumeReq{Token: res.Token}, protocol.CodeUnauthorized)

	c5 := dial(t, hs)
	c5.hello()
	c5.callErr(protocol.OpRecoverQuestion, protocol.RecoverQuestionReq{Username: "carol"}, protocol.CodeCannotRecover)

	// Expiry: move the clock past the end of the ban.
	restore := storage.Now
	storage.Now = func() int64 { return restore() + 2*3600_000 }
	defer func() { storage.Now = restore }()
	c6 := dial(t, hs)
	if f := login(c6, "carol"); f.Type != protocol.TypeRes {
		t.Fatalf("login after expiry: %s", f.Payload)
	}
}

// A token minted before the ban but not swept by it (here: saved after) is
// still refused on resume.
func TestResumeChecksBan(t *testing.T) {
	hs, srv := testServer(t)
	c := dial(t, hs)
	res := c.register("dave", pw)
	srv.Sanction(admin, res.User.ID, storage.SanctionBan, "x", 0)
	raw, _ := newToken()
	srv.st.SaveToken(storage.Token{
		Hash: tokenHash(raw), UserID: res.User.ID, Purpose: storage.PurposeSession,
		CreatedAt: storage.Now(), ExpiresAt: storage.Now() + 3600_000,
	})
	c2 := dial(t, hs)
	c2.hello()
	code, info := errData(t, c2.call(protocol.OpAuthResume, protocol.ResumeReq{Token: raw}))
	if code != protocol.CodeAccountBanned || info.ExpiresAt != 0 {
		t.Fatalf("resume of a permanently banned account: %s %+v", code, info)
	}
}

// 2.5: a muted user cannot send, edit, or create channels; deleting their own
// message and reading history still work; nothing reaches other members.
func TestMuteBlocksWriting(t *testing.T) {
	hs, srv := testServer(t)
	dave := dial(t, hs)
	dres := dave.register("dave", pw)
	erin := dial(t, hs)
	erin.register("erin", pw)

	var cv protocol.Conversation
	dave.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &cv)
	erin.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: cv.ID}, nil)
	var sent protocol.MessageEvent
	dave.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: cv.ID, Body: "before"}, &sent)
	erin.drainEvents()

	sc, _ := srv.Sanction(admin, dres.User.ID, storage.SanctionMute, "noise", 24*time.Hour)
	ep := dave.waitEvent(protocol.EvAccountMuted, 5*time.Second)
	var mi protocol.SanctionInfo
	_ = json.Unmarshal(ep.Data, &mi)
	if mi.ExpiresAt != sc.ExpiresAt || mi.Reason != "noise" {
		t.Fatalf("muted event: %+v", mi)
	}

	code, info := errData(t, dave.call(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: cv.ID, Body: "hello"}))
	if code != protocol.CodeMuted || info.ExpiresAt != sc.ExpiresAt {
		t.Fatalf("send while muted: %s %+v", code, info)
	}
	dave.callErr(protocol.OpMessageEdit, protocol.EditMessageReq{MessageID: sent.Message.ID, Body: "changed"}, protocol.CodeMuted)
	dave.callErr(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "another"}, protocol.CodeMuted)
	for _, f := range erin.drainEvents() {
		var ep protocol.EventPayload
		_ = json.Unmarshal(f.Payload, &ep)
		if ep.Name == protocol.EvMessage || ep.Name == protocol.EvMessageEdit {
			t.Fatalf("another member saw a muted write: %s", ep.Name)
		}
	}

	dave.callOK(protocol.OpMessageHistory, protocol.HistoryReq{ConversationID: cv.ID}, nil)
	dave.callOK(protocol.OpMessageDelete, protocol.DeleteMessageReq{MessageID: sent.Message.ID}, nil)

	restore := storage.Now
	storage.Now = func() int64 { return restore() + 25*3600_000 }
	defer func() { storage.Now = restore }()
	dave.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: cv.ID, Body: "after"}, nil)
}

// 2.6: banning an online user delivers account.banned before closing, revokes
// tokens, and audits without secrets; revoking a mute pushes account.unmuted.
func TestModerationActions(t *testing.T) {
	hs, srv := testServer(t)
	carol := dial(t, hs)
	res := carol.register("carol", pw)

	if _, err := srv.Sanction(admin, res.User.ID, storage.SanctionBan, "  ", time.Hour); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("empty reason: %v", err)
	}

	sc, err := srv.Sanction(admin, res.User.ID, storage.SanctionBan, "abuse", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ep := carol.waitEvent(protocol.EvAccountBanned, 5*time.Second)
	var bi protocol.SanctionInfo
	_ = json.Unmarshal(ep.Data, &bi)
	if bi.Reason != "abuse" || bi.ExpiresAt != sc.ExpiresAt {
		t.Fatalf("banned event: %+v", bi)
	}
	carol.waitClosed(5 * time.Second)
	if toks, _ := srv.st.ListTokensForUser(res.User.ID); len(toks) != 0 {
		t.Fatalf("tokens survive a ban: %+v", toks)
	}

	// Revoking the ban lets them back in; a mute and its revocation are pushed.
	if _, err := srv.RevokeSanction(admin, sc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.RevokeSanction(admin, sc.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("revoking twice: %v", err)
	}
	c2 := dial(t, hs)
	if f := login(c2, "carol"); f.Type != protocol.TypeRes {
		t.Fatalf("login after revoke: %s", f.Payload)
	}
	mute, _ := srv.Sanction(admin, res.User.ID, storage.SanctionMute, "calm down", 0)
	c2.waitEvent(protocol.EvAccountMuted, 5*time.Second)
	srv.RevokeSanction(admin, mute.ID)
	c2.waitEvent(protocol.EvAccountUnmuted, 5*time.Second)

	// Reset password: sessions end, the new password works, nothing secret in the audit.
	if err := srv.ResetPassword(admin, res.User.ID, "short", false); err == nil {
		t.Fatal("weak password accepted")
	}
	if err := srv.ResetPassword(admin, res.User.ID, "a brand new secret", false); err != nil {
		t.Fatal(err)
	}
	c2.waitEvent(protocol.EvSessionReplaced, 5*time.Second)
	c2.waitClosed(5 * time.Second)
	c3 := dial(t, hs)
	c3.hello()
	c3.callOK(protocol.OpAuthLogin, protocol.LoginReq{Username: "carol", Password: "a brand new secret"}, nil)

	// Kick: signed out, but free to sign straight back in.
	if n, err := srv.Kick(admin, res.User.ID); err != nil || n != 1 {
		t.Fatalf("kick: %d %v", n, err)
	}
	ep = c3.waitEvent(protocol.EvSessionReplaced, 5*time.Second)
	var se protocol.SessionReplacedEvent
	_ = json.Unmarshal(ep.Data, &se)
	if !strings.Contains(se.Location, "管理员") {
		t.Fatalf("kick notice location: %q", se.Location)
	}
	c4 := dial(t, hs)
	c4.hello()
	c4.callOK(protocol.OpAuthLogin, protocol.LoginReq{Username: "carol", Password: "a brand new secret"}, nil)

	entries, _ := srv.st.ListAudit(storage.AuditFilter{Limit: 100})
	actions := map[string]bool{}
	for _, e := range entries {
		actions[e.Action] = true
		if e.ActorID != admin.ID || e.ActorName != admin.Name || e.Target != res.User.ID || e.At == 0 {
			t.Fatalf("incomplete audit entry: %+v", e)
		}
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "brand new secret") {
			t.Fatalf("audit contains the password: %s", b)
		}
	}
	for _, want := range []string{"ban", "mute", "revoke", "reset_password", "kick"} {
		if !actions[want] {
			t.Errorf("no audit entry for %s", want)
		}
	}
}

// The super administrator cannot be moderated.
func TestSuperIsProtected(t *testing.T) {
	hs, srv := testServer(t)
	res := dial(t, hs).register("root", pw)
	srv.st.SetUserSuper(res.User.ID, true)
	if _, err := srv.Sanction(admin, res.User.ID, storage.SanctionBan, "x", 0); !errors.Is(err, ErrProtectedUser) {
		t.Fatalf("ban: %v", err)
	}
	if _, err := srv.Kick(admin, res.User.ID); !errors.Is(err, ErrProtectedUser) {
		t.Fatalf("kick: %v", err)
	}
	if err := srv.ResetPassword(admin, res.User.ID, "another secret!", false); !errors.Is(err, ErrProtectedUser) {
		t.Fatalf("reset: %v", err)
	}
}

// 2.7: the chat user list exposes no admin attributes.
func TestUserListHidesAdminFields(t *testing.T) {
	hs, srv := testServer(t)
	root := dial(t, hs).register("root", pw)
	srv.st.SetUserSuper(root.User.ID, true)
	srv.st.SetUserPerms(root.User.ID, []string{"chat.view"})
	srv.Sanction(admin, root.User.ID, storage.SanctionMute, "x", 0) // refused: super
	other := dial(t, hs)
	other.register("bob", pw)

	f := other.call(protocol.OpUserList, nil)
	for _, word := range []string{"perms", "super", "sanction", "chat.view"} {
		if strings.Contains(string(f.Payload), word) {
			t.Fatalf("user list leaks %q: %s", word, f.Payload)
		}
	}
}
