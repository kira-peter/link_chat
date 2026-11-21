package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/server"
	"link-chat/pkg/storage"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/version"
)

const unreachableMin = "999.0.0"

func outdatedText(lang i18n.Lang) string {
	return fmt.Sprintf(i18n.ClientOutdated.Get(lang), version.String(), unreachableMin)
}

// Signing in to a server that requires a newer client shows why on the panel,
// keeps the username, and does not retry.
func TestOutdatedClientOnSignIn(t *testing.T) {
	hs := startServerWith(t, func(c *server.Config) { c.MinClientVersion = unreachableMin })
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(appTempDir(t), "o.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	waitFor(t, 3*time.Second, "login dialog", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin
	})
	a.mu.Lock()
	a.ui.Login.Username = "alice"
	a.ui.Login.Password = "correct horse battery"
	a.mu.Unlock()
	a.submitLogin(ctx)

	want := outdatedText(i18n.ZH)
	waitFor(t, 15*time.Second, "outdated message", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Login.Err == want && !a.ui.Login.Busy
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ui.Login.Username != "alice" {
		t.Fatalf("username lost: %q", a.ui.Login.Username)
	}
	if a.st.State != client.StateAuthFailed || a.st.RetryIn != 0 {
		t.Fatalf("still reconnecting: state=%v retry=%v", a.st.State, a.st.RetryIn)
	}
	a.render()
	if text := screenText(scr); !strings.Contains(text, version.String()) || !strings.Contains(text, unreachableMin) {
		t.Fatalf("panel does not show the versions:\n%s", text)
	}
}

// swapHandler routes to whichever server is current, so a test can "restart"
// the server with a new config behind the same address.
type swapHandler struct{ h atomic.Pointer[http.Handler] }

func (s *swapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.h.Load()).ServeHTTP(w, r)
}
func (s *swapHandler) set(h http.Handler) { s.h.Store(&h) }

// connTracker remembers every accepted TCP connection, so a test can drop
// hijacked WebSocket connections, which httptest's CloseClientConnections
// leaves alone.
type connTracker struct {
	mu    sync.Mutex
	conns []net.Conn
}

func (c *connTracker) track(conn net.Conn, s http.ConnState) {
	if s == http.StateNew {
		c.mu.Lock()
		c.conns = append(c.conns, conn)
		c.mu.Unlock()
	}
}

func (c *connTracker) dropAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, conn := range c.conns {
		_ = conn.Close()
	}
	c.conns = nil
}

// A signed-in client whose server raises its minimum while it is disconnected
// stops reconnecting and goes back to the login panel with the reason.
func TestOutdatedClientOnReconnect(t *testing.T) {
	dir := appTempDir(t)
	st, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mk := func(min string) http.Handler {
		cfg := server.DefaultConfig()
		cfg.DataDir = dir
		cfg.MinClientVersion = min
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		return server.New(cfg, st, logger).Handler()
	}
	sw := &swapHandler{}
	sw.set(mk(""))
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
	a := New(scr, Config{Server: url, Theme: "ink", Language: "en"}, filepath.Join(appTempDir(t), "r.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	waitFor(t, 3*time.Second, "login dialog", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin
	})
	registerAndWait(t, a, ctx, "bob")

	// "Restart" with a higher minimum: new connections reach the new config,
	// and the client's socket is dropped so it has to reconnect.
	sw.set(mk(unreachableMin))
	conns.dropAll()

	want := outdatedText(i18n.EN)
	waitFor(t, 15*time.Second, "back on the login panel with the reason", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin && a.ui.Login.Err == want
	})
	a.mu.Lock()
	state, retry, token := a.st.State, a.st.RetryIn, a.st.Token
	a.mu.Unlock()
	if state != client.StateAuthFailed || retry != 0 {
		t.Fatalf("still reconnecting: state=%v retry=%v", state, retry)
	}
	if token == "" {
		t.Fatal("the saved session was dropped; upgrading should be able to resume it")
	}

	// And it stays put: no further attempt replaces the message.
	time.Sleep(1500 * time.Millisecond)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st.State != client.StateAuthFailed || a.ui.Login.Err != want {
		t.Fatalf("retried after refusal: state=%v err=%q", a.st.State, a.ui.Login.Err)
	}
}

// The message fits the login panel's error row in both languages, even with
// two-digit version parts.
func TestOutdatedMessageFitsPanel(t *testing.T) {
	r := views.LoginPanelRect(100, 30)
	rowW := r.W - 8 // the panel's inner padding on both sides
	for _, lang := range []i18n.Lang{i18n.ZH, i18n.EN} {
		msg := fmt.Sprintf(i18n.ClientOutdated.Get(lang), "10.10.10", "10.10.11")
		if w := layout.StringWidth(msg); w > rowW {
			t.Fatalf("%v: %q is %d wide, error row is %d", lang, msg, w, rowW)
		}
	}
}
