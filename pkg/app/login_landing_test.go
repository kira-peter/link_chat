package app

import (
	"testing"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
)

func landingConversations() []protocol.Conversation {
	return []protocol.Conversation{
		{ID: "d1", Name: "carol", Kind: protocol.KindDM, LastMessageAt: 300},
		{ID: "c1", Name: "general", Kind: protocol.KindChannel, LastMessageAt: 200},
	}
}

// land runs the same steps refreshConversations does once the list arrives.
func land(a *App, convs []protocol.Conversation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.SetConversations(convs)
	a.reconcileFocus()
	a.landOnList()
}

// Signing in opens nothing: the list is open, the keyboard is on it, the first
// selectable row is under the cursor and the welcome page shows.
func TestSignInLandsOnTheListWithNothingOpen(t *testing.T) {
	a, _, now := foldApp(t)
	a.st.ActiveID = ""
	a.st.Focus = client.FocusInput
	a.ui.Selected = 0
	land(a, landingConversations())

	if a.st.ActiveID != "" {
		t.Fatalf("a conversation was opened by itself: %q", a.st.ActiveID)
	}
	if a.st.Focus != client.FocusList {
		t.Fatalf("focus = %v, want the list", a.st.Focus)
	}
	if f := a.foldNow(*now); f != 0 {
		t.Fatalf("fold = %v, want the list open", f)
	}
	rows := a.st.SidebarRows()
	if a.ui.Selected != client.FirstSelectable(rows) {
		t.Fatalf("cursor on row %d, want the first selectable row %d", a.ui.Selected, client.FirstSelectable(rows))
	}
}

// A reconnect refresh must not throw away what the user had open.
func TestRefreshKeepsAnAlreadyOpenConversation(t *testing.T) {
	a, _, _ := foldApp(t)
	a.st.ActiveID = "c1"
	a.st.Focus = client.FocusInput
	a.settleFold()
	land(a, landingConversations())

	if a.st.ActiveID != "c1" || a.st.Focus != client.FocusInput {
		t.Fatalf("refresh disturbed the open chat: active %q focus %v", a.st.ActiveID, a.st.Focus)
	}
}
