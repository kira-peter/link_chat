package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	st, err := storage.Open(dir)
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

	a.mu.Lock()
	a.ui.NewPage = views.NewPageModel{Value: "restart"}
	a.mu.Unlock()
	a.submitPrompt(ctx, true)
	waitFor(t, 5*time.Second, "open channel", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.Active() != nil && a.st.Active().Name == "restart"
	})
	a.mu.Lock()
	a.st.RestoreInput("before the restart")
	a.mu.Unlock()
	a.sendCurrent(ctx)
	waitFor(t, 5*time.Second, "initial message", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.Active() != nil && a.st.Active().LastKnownSeq > 0
	})
	a.mu.Lock()
	channelID, actorID := a.st.ActiveID, a.st.UserID
	lastSeq := a.st.Active().LastKnownSeq
	a.mu.Unlock()

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
	conns.dropAll()
	for i := 0; i < 57; i++ {
		if _, err := st.AppendMessage(storage.Message{
			ID: fmt.Sprintf("offline-%d", i), ConversationID: channelID, AuthorID: actorID, Body: "offline message",
		}); err != nil {
			t.Fatal(err)
		}
	}
	sw.set(second.Handler())

	waitFor(t, 15*time.Second, "reconnected with the notice cleared", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady && a.st.StatusMsg == ""
	})
	waitFor(t, 10*time.Second, "all offline pages loaded", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		cv := a.st.Conversations[channelID]
		if cv == nil {
			return false
		}
		var count int
		for _, message := range cv.Messages {
			if message.Seq > lastSeq {
				count++
			}
		}
		return count == 57 && cv.LastKnownSeq == lastSeq+57 && cv.Unread == 0
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

func TestReplacedTransportCannotUpdateCurrentAccount(t *testing.T) {
	a, _ := stageApp(t)
	old := client.NewTransport("ws://old")
	current := client.NewTransport("ws://current")
	a.tr.Store(current)
	a.st.State = client.StateReady
	a.st.Token = "current-token"
	a.st.Username = "current-user"

	a.onConnectFailed(old, errors.New("old connection failed"), true)
	a.onStateChange(old, client.StateDisconnected, errors.New("old connection closed"))
	payload, err := json.Marshal(protocol.SessionReplacedEvent{Username: "previous-user"})
	if err != nil {
		t.Fatal(err)
	}
	a.onTransportEvent(old, protocol.EvSessionReplaced, payload)
	if a.tr.Load() != current || a.st.State != client.StateReady || a.st.Token != "current-token" {
		t.Fatal("an obsolete transport changed the current session")
	}
}

func TestReconnectDelayGrowsAcrossTransports(t *testing.T) {
	a, _ := stageApp(t)
	a.st.Token = "saved-session"
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second} {
		tr := client.NewTransport("ws://server")
		a.tr.Store(tr)
		a.st.RetryIn = 0
		a.onConnectFailed(tr, errors.New("dial failed"), true)
		if got := a.st.RetryIn; got != want {
			t.Fatalf("retry delay = %s, want %s", got, want)
		}
	}
	a.onAuthenticated(protocol.AuthRes{})
	if a.st.RetryIn != 0 || a.retryDelay != 0 {
		t.Fatalf("authentication did not reset retry delay: %s / %s", a.st.RetryIn, a.retryDelay)
	}
}
