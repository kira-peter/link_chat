package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// signedIn waits until a sign-in has finished.
func signedIn(t *testing.T, a *App, who string) {
	t.Helper()
	waitFor(t, 20*time.Second, who+" signed in", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady && a.ui.Dialog == views.DialogNone && a.st.Username == who
	})
}

// Against a real server: sign out, then one Enter signs straight back in with
// the saved sign-in — which only works because signing out no longer revokes it.
func TestSignOutThenOneEnterSignsBackIn(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	cfg := Config{Server: url, Theme: "ink", Language: "zh"}
	cfg.SetRemember(true)
	a := New(newTestScreen(t, 100, 30), cfg, filepath.Join(appTempDir(t), "so.json"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Run(ctx); close(done) }()
	// Stop the app and let its last config write finish before the test's
	// temporary directory is removed; on Windows a file still being written
	// makes that removal fail.
	defer func() { cancel(); <-done; time.Sleep(50 * time.Millisecond) }()
	registerAndWait(t, a, ctx, "olive")

	// Like the key handler, without the lock: logout takes it where it needs it.
	a.logout()
	a.mu.Lock()
	m := a.ui.Login
	a.mu.Unlock()
	if !m.Remembered || m.Focus != views.LoginFocusSubmit || m.Username != "olive" {
		t.Fatalf("after signing out the panel is %+v, want remembered with the button focused", m)
	}

	a.skipEntrance()
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	signedIn(t, a, "olive")
}

// Two accounts, both remembered: from alice, switch to the list, pick bob and
// he is signed in with no password and the entrance plays; switch back the same
// way. Neither switch costs the other account its saved sign-in.
func TestSwitchingBetweenTwoRememberedAccounts(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	cfgPath := filepath.Join(appTempDir(t), "sw.json")
	cfg := Config{Server: url, Theme: "ink", Language: "zh"}
	cfg.SetRemember(true)
	a := New(newTestScreen(t, 100, 30), cfg, cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	// Two accounts on this device: alice first, then bob from "use another".
	registerAndWait(t, a, ctx, "alice")
	a.switchAccount()
	a.ui.Accounts.Cursor = len(a.cfg.Accounts) // use another account
	a.chooseAccount(ctx)
	a.ui.Dialog = views.DialogRegister
	registerAndWait(t, a, ctx, "bob")

	pick := func(name string) {
		t.Helper()
		// Keys go in the way the main loop sends them: without the lock.
		a.skipEntrance()
		a.showEntryPage(client.EntryMe)
		a.ui.Me.Selected = 0                         // the page remembers its row; start at the top
		a.handleKey(ctx, specialKey(tcell.KeyUp))    // wraps to sign out, at the bottom
		a.handleKey(ctx, specialKey(tcell.KeyUp))    // switch account
		a.handleKey(ctx, specialKey(tcell.KeyEnter)) // straight to the list
		if a.ui.Dialog != views.DialogAccounts {
			t.Fatalf("switching did not open the account list (%v)", a.ui.Dialog)
		}
		for i, n := range a.cfg.Accounts {
			if n == name {
				a.ui.Accounts.Cursor = i
			}
		}
		a.handleKey(ctx, specialKey(tcell.KeyEnter))
		signedIn(t, a, name)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.anim.entrance == nil && a.animated() {
			t.Fatalf("signing in as %s from the list played no entrance", name)
		}
	}
	pick("alice")
	pick("bob")
	pick("alice")

	a.mu.Lock()
	defer a.mu.Unlock()
	for _, n := range []string{"alice", "bob"} {
		if !a.creds.forUser(n).has() {
			t.Errorf("%s lost the saved sign-in after switching", n)
		}
	}
	if got := strings.Join(a.cfg.Accounts, ","); got != "alice,bob" {
		t.Errorf("recent accounts %q, want alice,bob", got)
	}
}

// A direct message shows the other person's name on each side, also after the
// list is fetched again.
func TestDMIsNamedAfterThePeerInTheSidebar(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	mk := func(name string) (*App, context.Context) {
		a := New(newTestScreen(t, 100, 30), Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(appTempDir(t), name+".json"))
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go a.Run(ctx)
		registerAndWait(t, a, ctx, name)
		return a, ctx
	}
	a, ctxA := mk("alice")
	b, _ := mk("bob")
	// A direct message needs a friendship.
	a.socialCall(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "bob"})
	b.socialCall(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: "alice"})

	a.ui.NewPage = views.NewPageModel{Value: "bob"}
	a.submitPrompt(ctxA, false)
	waitFor(t, 15*time.Second, "alice's DM", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.Active() != nil && a.st.Active().Kind == "dm"
	})

	dmName := func(x *App) string {
		x.refreshConversations()
		x.mu.Lock()
		defer x.mu.Unlock()
		for _, cv := range x.st.Conversations {
			if cv.Kind == "dm" {
				return cv.Name
			}
		}
		return "<none>"
	}
	if got := dmName(a); got != "bob" {
		t.Errorf("alice's sidebar names the DM %q, want bob", got)
	}
	if got := dmName(b); got != "alice" {
		t.Errorf("bob's sidebar names the DM %q, want alice", got)
	}
	renderSettled(t, b)
	b.mu.Lock()
	defer b.mu.Unlock()
	// The row reads "· A  alice": marker, alice's avatar, then her name.
	if !strings.Contains(screenText(b.scr.(tcell.SimulationScreen)), "· A  alice") {
		t.Errorf("bob's sidebar does not show the DM as alice:\n%s", screenText(b.scr.(tcell.SimulationScreen)))
	}
}
