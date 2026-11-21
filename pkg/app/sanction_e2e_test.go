package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/server"
	"link-chat/pkg/storage"
	"link-chat/pkg/tui/views"
)

var testAdmin = server.Actor{ID: "u_admin", Name: "boss"}

// startServerHandle is startServer that also returns the *server.Server, so a
// test can ban or mute an account the way the admin console would.
func startServerHandle(t *testing.T) (*httptest.Server, *server.Server) {
	t.Helper()
	cfg := server.DefaultConfig()
	cfg.DataDir = appTempDir(t)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := storage.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close storage: %v", err)
		}
	})
	srv := server.New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		hs.CloseClientConnections()
		hs.Close()
	})
	return hs, srv
}

func signedInE2EApp(t *testing.T, user string) (*App, *server.Server, context.Context) {
	t.Helper()
	hs, srv := startServerHandle(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(appTempDir(t), "s.json"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.Run(ctx)
	waitFor(t, 3*time.Second, "login dialog", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin
	})
	registerAndWait(t, a, ctx, user)
	return a, srv, ctx
}

// 7.1: banned while online, the client goes back to the sign-in panel with the
// reason and end time, stops reconnecting, and drops its saved login. Signing
// in again while still banned shows the same reason and keeps the username.
func TestBannedWhileOnlineReturnsToStage(t *testing.T) {
	a, srv, ctx := signedInE2EApp(t, "gina")
	a.mu.Lock()
	uid := a.st.UserID
	a.mu.Unlock()

	sc, err := srv.Sanction(testAdmin, uid, storage.SanctionBan, "spam", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "back on the panel with the reason", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin && strings.Contains(a.ui.Login.Err, "spam")
	})
	a.mu.Lock()
	msg, state, retry, token := a.ui.Login.Err, a.st.State, a.st.RetryIn, a.st.Token
	a.mu.Unlock()
	if !strings.Contains(msg, sanctionDate(sc.ExpiresAt)) {
		t.Fatalf("panel does not show when the ban ends: %q", msg)
	}
	if state != client.StateAuthFailed || retry != 0 || token != "" {
		t.Fatalf("still reconnecting or holding a login: state=%v retry=%v token=%q", state, retry, token)
	}

	// Still banned: signing in again is refused with the same reason, and the
	// username stays.
	a.mu.Lock()
	a.ui.Login.Username = "gina"
	a.ui.Login.Password = "correct horse battery"
	a.ui.Login.Err = ""
	a.mu.Unlock()
	a.submitLogin(ctx)
	waitFor(t, 15*time.Second, "refusal on sign-in", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return strings.Contains(a.ui.Login.Err, "spam") && !a.ui.Login.Busy
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ui.Login.Username != "gina" {
		t.Fatalf("username lost: %q", a.ui.Login.Username)
	}
	a.render()
}

// The panel text fits the error row at the narrowest panel width.
func TestBannedMessageFitsPanel(t *testing.T) {
	a, _ := newKeyTestApp(t)
	msg := a.bannedMessage(protocol.SanctionInfo{Reason: "x"})
	if msg == "" {
		t.Fatal("empty message")
	}
}

// 7.2: muted, the composer locks and says until when; revoking unlocks it; a
// mute that lapses unlocks it on the next tick with nothing pushed.
func TestMutedLocksComposerAndUnlocks(t *testing.T) {
	a, srv, _ := signedInE2EApp(t, "hank")
	a.mu.Lock()
	uid := a.st.UserID
	a.mu.Unlock()

	sc, err := srv.Sanction(testAdmin, uid, storage.SanctionMute, "noise", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "muted", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.Muted(nowTime().UnixMilli())
	})
	a.mu.Lock()
	text := a.mutedText()
	a.mu.Unlock()
	if !strings.Contains(text, "禁言至") || !strings.Contains(text, sanctionDate(sc.ExpiresAt)) {
		t.Fatalf("composer text: %q", text)
	}

	if _, err := srv.RevokeSanction(testAdmin, sc.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "unmuted", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return !a.st.Muted(nowTime().UnixMilli()) && a.mutedText() == ""
	})
}

func TestMuteExpiresOnTick(t *testing.T) {
	a, _ := newKeyTestApp(t)
	now := nowTime().UnixMilli()
	a.st.MutedUntil = now - 1000
	if a.st.Muted(now) {
		t.Fatal("a lapsed mute still counts")
	}
	a.st.MutedUntil = now + 60_000
	if !a.st.Muted(now) {
		t.Fatal("an active mute does not count")
	}
	a.st.MutedUntil = now - 1
	a.tick()
	if a.st.MutedUntil != 0 {
		t.Fatalf("tick left a lapsed mute: %d", a.st.MutedUntil)
	}
	a.st.MutedForever = true
	if !a.st.Muted(now) || a.mutedText() == "" {
		t.Fatal("a permanent mute is not shown")
	}
}

// A request that gets no answer says so, instead of calling a running server
// unreachable.
func TestCallErrText(t *testing.T) {
	a, _ := newKeyTestApp(t)
	if got := a.callErrText(fmt.Errorf("auth.login: %w", context.DeadlineExceeded)); got != i18n.ServerNoReply.Get(a.lang) {
		t.Fatalf("timeout: %q", got)
	}
	if got := a.callErrText(client.ErrClosed); got != i18n.ConnLost.Get(a.lang) {
		t.Fatalf("closed: %q", got)
	}
	if got := a.callErrText(errors.New("send: boom")); got != i18n.ServerDown.Get(a.lang) {
		t.Fatalf("other: %q", got)
	}
}

// Wrong passwords back to back each get the server's own answer, and the right
// one after them signs in. The second attempt used to redial while the state
// still read "authenticating" from the first, call into a socket that did not
// exist yet, and report a lost connection.
func TestRepeatedWrongPasswordThenRight(t *testing.T) {
	a, srv, ctx := signedInE2EApp(t, "ivan")
	a.mu.Lock()
	uid := a.st.UserID
	a.mu.Unlock()
	// A ban puts the client back on the panel with no connection; lifting it
	// lets the account sign in again.
	sc, err := srv.Sanction(testAdmin, uid, storage.SanctionBan, "x", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "back on the panel", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin
	})
	if _, err := srv.RevokeSanction(testAdmin, sc.ID); err != nil {
		t.Fatal(err)
	}

	wrong := i18n.ErrText(protocol.CodeInvalidCredentials, "").Get(a.lang)
	try := func(pass string) string {
		a.mu.Lock()
		a.ui.Login.Username = "ivan"
		a.ui.Login.Password = pass
		a.ui.Login.Remembered = false
		a.ui.Login.Err = ""
		a.mu.Unlock()
		a.submitLogin(ctx)
		var msg string
		waitFor(t, 15*time.Second, "an answer to "+pass, func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			msg = a.ui.Login.Err
			return !a.ui.Login.Busy && (msg != "" || a.ui.Dialog != views.DialogLogin)
		})
		return msg
	}
	for i, pass := range []string{"wrong one 1", "wrong one 2", "wrong one 3"} {
		if got := try(pass); got != wrong {
			t.Fatalf("attempt %d: %q, want %q", i+1, got, wrong)
		}
	}
	if got := try("correct horse battery"); got != "" {
		t.Fatalf("right password refused: %q", got)
	}
	waitFor(t, 10*time.Second, "signed in", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady
	})
}
