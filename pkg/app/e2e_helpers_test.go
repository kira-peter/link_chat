package app

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
)

// registerAndWait registers a user on a running app and waits until it is signed
// in. Signing in opens nothing, so afterwards the list is showing and the keyboard
// is on it.
func registerAndWait(t *testing.T, a *App, ctx context.Context, user string) {
	t.Helper()
	a.mu.Lock()
	a.ui.Register = views.RegisterModel{
		Username: user, Password: "correct horse battery",
		Question: "Test question?", Answer: "test answer",
	}
	a.mu.Unlock()
	a.submitRegister(ctx)
	waitFor(t, 15*time.Second, user+" ready", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady
	})
}

// renderSettled draws a frame once the sign-in entrance (if one is playing) has
// finished, so what a test reads is the main interface itself. These tests run on
// the real clock, so this waits the entrance out rather than skipping it: the
// entrance's own tests cover what it draws.
func renderSettled(t *testing.T, a *App) {
	t.Helper()
	waitFor(t, 2*time.Second, "the sign-in entrance to finish", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return !a.entranceRunning(nowTime())
	})
	a.mu.Lock()
	a.render()
	a.mu.Unlock()
}

// openViaList opens a conversation the way a user does: put the cursor on its row
// in the list and press Enter. It waits for the conversation to be known first,
// because the list arrives a moment after sign-in.
func openViaList(t *testing.T, a *App, ctx context.Context, id string) {
	t.Helper()
	waitFor(t, 15*time.Second, "conversation "+id+" in the list", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return client.IndexOfConversation(a.st.SidebarRows(), id) >= 0
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	a.moveFocus(client.FocusList)
	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), id)
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	if a.st.ActiveID != id || a.st.Focus != client.FocusInput {
		t.Fatalf("Enter on %q did not open it (active %q, focus %v)", id, a.st.ActiveID, a.st.Focus)
	}
}
