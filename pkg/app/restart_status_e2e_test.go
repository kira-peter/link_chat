package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
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

// When the server restarts, the client shows "server restarting"; once it has
// reconnected and signed back in, that notice is gone.
func TestRestartNoticeClearsAfterReconnect(t *testing.T) {
	dir := appTempDir(t)
	st, err := storage.OpenJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mk := func() *server.Server {
		cfg := server.DefaultConfig()
		cfg.DataDir = dir
		cfg.ShutdownGraceSeconds = 1
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		return server.New(cfg, st, logger)
	}
	first := mk()
	sw := &swapHandler{}
	sw.set(first.Handler())
	conns := &connTracker{}
	hs := httptest.NewUnstartedServer(sw)
	hs.Config.ConnState = conns.track
	hs.Start()
	t.Cleanup(func() {
		conns.dropAll()
		hs.Close()
		_ = st.Close()
	})

	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(appTempDir(t), "s.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	waitFor(t, 3*time.Second, "login dialog", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin
	})
	registerAndWait(t, a, ctx, "dora")

	// Restart: the old server announces it is going away; a new one takes over
	// the same address; the old connection is dropped.
	second := mk()
	t.Cleanup(func() { _ = second.Shutdown() })
	notice := i18n.ErrText("server_shutting_down", "").Get(i18n.ZH)
	go func() { _ = first.Shutdown() }()
	waitFor(t, 5*time.Second, "restart notice", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.StatusMsg == notice
	})
	sw.set(second.Handler())
	conns.dropAll()

	waitFor(t, 15*time.Second, "reconnected with the notice cleared", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady && a.st.StatusMsg == ""
	})
	a.mu.Lock()
	a.render()
	text := screenText(scr)
	a.mu.Unlock()
	if strings.Contains(text, notice) {
		t.Fatalf("the restart notice is still on screen:\n%s", text)
	}
}

// Another message set after the restart notice is not cleared by reconnecting:
// only the connection notice is stale.
func TestReconnectKeepsUnrelatedStatus(t *testing.T) {
	a, _ := stageApp(t)
	a.setStatus("服务端正在重启", true)
	a.connStatus = true
	a.setStatus("设置已保存", false)
	a.onAuthenticated(protocol.AuthRes{User: protocol.User{ID: "u1", Username: "alice"}})
	if a.st.StatusMsg != "设置已保存" {
		t.Fatalf("an unrelated status was cleared: %q", a.st.StatusMsg)
	}
}

var _ http.Handler = (*swapHandler)(nil)
