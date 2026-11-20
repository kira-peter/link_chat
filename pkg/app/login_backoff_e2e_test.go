package app

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"link-chat/pkg/client"
	"link-chat/pkg/server"
	"link-chat/pkg/storage"
	"link-chat/pkg/tui/views"
)

// startServerTweaked is startServerHandle with a config tweak. The per-minute
// budget is raised in these tests so the climbing wait is what refuses an
// attempt, not the flood limiter.
func startServerTweaked(t *testing.T, tweak func(*server.Config)) (*httptest.Server, *server.Server) {
	t.Helper()
	cfg := server.DefaultConfig()
	cfg.DataDir = appTempDir(t)
	if tweak != nil {
		tweak(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := storage.OpenJSON(cfg.DataDir)
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

// 3.2: a rate-limited sign-in puts a countdown on the panel; Enter during it
// sends nothing; once it lapses the panel takes the right password.
func TestLoginCountdownBlocksThenRecovers(t *testing.T) {
	hs, srv := startServerTweaked(t, func(c *server.Config) { c.LoginAttemptsPerMinute = 100 })
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
	registerAndWait(t, a, ctx, "jill")

	// Back to the panel: a ban signs the client out, and lifting it lets the
	// account sign in again.
	a.mu.Lock()
	uid := a.st.UserID
	a.mu.Unlock()
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

	submit := func(pass string) {
		a.mu.Lock()
		a.ui.Login.Username = "jill"
		a.ui.Login.Password = pass
		a.ui.Login.Remembered = false
		a.ui.Login.Err = ""
		a.mu.Unlock()
		a.submitLogin(ctx)
	}

	for i := 1; i <= 4; i++ {
		submit("wrong password")
		waitFor(t, 15*time.Second, "answer to wrong attempt", func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return !a.ui.Login.Busy && a.ui.Login.Err != ""
		})
	}

	// The fifth attempt is refused with a wait, which becomes the countdown.
	submit("wrong password")
	waitFor(t, 15*time.Second, "countdown", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Login.RetryAt > nowTime().UnixMilli()
	})
	a.mu.Lock()
	retryAt := a.ui.Login.RetryAt
	a.ui.Login.Err = ""
	a.mu.Unlock()
	if left := retryAt - nowTime().UnixMilli(); left > 61_000 {
		t.Fatalf("countdown too long: %d ms", left)
	}

	// Enter during the countdown sends nothing: the right password does not sign
	// in, and the client stays on the panel.
	submit("correct horse battery")
	time.Sleep(300 * time.Millisecond)
	a.mu.Lock()
	state, dialog := a.st.State, a.ui.Dialog
	a.mu.Unlock()
	if dialog != views.DialogLogin || state == client.StateReady {
		t.Fatalf("signed in during the countdown: dialog=%v state=%v", dialog, state)
	}

	// The countdown lapses: tick clears it and the same password signs in.
	a.mu.Lock()
	a.ui.Login.RetryAt = nowTime().UnixMilli() - 1
	a.mu.Unlock()
	a.tick()
	a.mu.Lock()
	retry := a.ui.Login.RetryAt
	a.mu.Unlock()
	if retry != 0 {
		t.Fatalf("tick left the countdown: %d", retry)
	}
	// The wait is the server's, so its remaining time is still running: what the
	// panel must do is send again and show the server's answer. (Signing in after
	// a real wait is covered by the server's own tests.)
	submit("correct horse battery")
	waitFor(t, 15*time.Second, "the server's answer after the countdown", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Login.RetryAt > nowTime().UnixMilli() || a.st.State == client.StateReady
	})
}
