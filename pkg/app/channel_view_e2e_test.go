package app

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/server"
	"link-chat/pkg/tui/views"
)

func TestChannelViewFollowsClientPage(t *testing.T) {
	hs, st := startServerWithStorage(t, nil)
	screen := newTestScreen(t, 100, 30)
	a := New(screen, Config{
		Server: "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws",
		Theme:  "ink", Language: "zh",
	}, filepath.Join(appTempDir(t), "alice.json"))
	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.Run(ctx)
	}()
	t.Cleanup(func() { <-done })
	registerAndWait(t, a, ctx, "alice")
	renderSettled(t, a)
	a.mu.Lock()
	id := a.st.UserID
	a.mu.Unlock()
	if err := st.SetUserSuper(id, true); err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	backend := &http.Client{Jar: jar, Timeout: 2 * time.Second}
	login, err := http.NewRequestWithContext(ctx, http.MethodPost, hs.URL+"/admin/api/login",
		strings.NewReader(`{"username":"alice","password":"correct horse battery"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("X-Requested-With", "link-chat-admin")
	res, err := backend.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin login: %s", res.Status)
	}
	expectView := func(channelID string) {
		t.Helper()
		waitFor(t, 5*time.Second, "client viewing "+channelID, func() bool {
			res, err := backend.Get(hs.URL + "/admin/api/live/snapshot")
			if err != nil {
				return false
			}
			defer res.Body.Close()
			var snap server.LiveSnapshot
			if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&snap) != nil {
				return false
			}
			for _, user := range snap.Users {
				if user.ID == id {
					return user.ViewingChannel == channelID
				}
			}
			return false
		})
	}

	var first string
	for _, name := range []string{"alpha", "beta"} {
		a.mu.Lock()
		a.ui.NewPage = views.NewPageModel{Value: name}
		a.mu.Unlock()
		a.submitPrompt(ctx, true)
		var activeID string
		waitFor(t, 10*time.Second, name+" opened", func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			cv := a.st.Active()
			if cv != nil && cv.Name == name {
				activeID = cv.ID
				return true
			}
			return false
		})
		if first == "" {
			first = activeID
		}
		expectView(activeID)
	}
	openViaList(t, a, ctx, first)
	expectView(first)
	a.mu.Lock()
	screen.SetSize(40, 30)
	a.moveFocus(client.FocusList)
	a.mu.Unlock()
	expectView("")
	a.mu.Lock()
	screen.SetSize(100, 30)
	a.mu.Unlock()
	openViaList(t, a, ctx, first)
	expectView(first)
	a.mu.Lock()
	a.handleKey(ctx, tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModAlt))
	a.mu.Unlock()
	expectView("")
}
