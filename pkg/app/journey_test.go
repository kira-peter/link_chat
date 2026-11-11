package app

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
)

// The whole journey from the moment the list arrives after sign-in: list open and
// focused, nothing opened, Enter on a chat folds the list and flies the name to the
// title, ← unfolds and the name flies back.
func TestJourneyFromSignInThroughOpenAndBack(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	now := freezeClock(t, time.Unix(30000, 0))
	a.st.Focus = client.FocusInput
	land(a, []protocol.Conversation{
		{ID: "d1", Name: "carol", Kind: protocol.KindDM, LastMessageAt: 300},
		{ID: "c1", Name: "planet", Kind: protocol.KindChannel, LastMessageAt: 200},
	})
	scr := a.scr.(tcell.SimulationScreen)
	count := func(s string) int { a.render(); return strings.Count(screenText(scr), s) }

	// 1. Just signed in: nothing open, list open and focused, welcome page.
	if a.st.ActiveID != "" || a.st.Focus != client.FocusList || a.foldNow(*now) != 0 {
		t.Fatalf("after sign-in: active %q focus %v fold %v", a.st.ActiveID, a.st.Focus, a.foldNow(*now))
	}
	a.render()
	if side := sidebarText(a); !strings.Contains(side, "planet") || !strings.Contains(side, "私聊") || !strings.Contains(side, "设置") {
		t.Fatalf("the open list is not showing:\n%s", screenText(scr))
	}

	// → and Tab have nowhere to go with nothing open: the list stays open.
	a.handleKey(ctx, specialKey(tcell.KeyRight))
	a.handleKey(ctx, specialKey(tcell.KeyTab))
	if a.st.Focus != client.FocusList || a.foldNow(*now) != 0 {
		t.Fatalf("keys moved focus to an empty composer: focus %v fold %v", a.st.Focus, a.foldNow(*now))
	}

	// 2. Choose the channel and press Enter.
	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c1")
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	if a.st.ActiveID != "c1" || a.st.Focus != client.FocusInput {
		t.Fatalf("Enter: active %q focus %v", a.st.ActiveID, a.st.Focus)
	}

	// 3. Part-way: folding, the name is in the air exactly once.
	*now = now.Add(120 * time.Millisecond)
	if f := a.foldNow(*now); f <= 0 || f >= 1 {
		t.Fatalf("not mid-fold after 120ms: %v", f)
	}
	if n := count("planet"); n != 1 {
		t.Fatalf("mid-fold the name appears %d times, want 1", n)
	}

	// 4. Settled: the list is gone, the title holds the name.
	*now = now.Add(time.Second)
	if a.foldNow(*now) != 1 || count("planet") != 1 || count("私聊") != 0 {
		t.Fatalf("folded state wrong (fold %v):\n%s", a.foldNow(*now), screenText(scr))
	}

	// 5. ← unfolds; the name flies back.
	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	*now = now.Add(120 * time.Millisecond)
	if f := a.foldNow(*now); f <= 0 || f >= 1 {
		t.Fatalf("not mid-unfold: %v", f)
	}
	if n := count("planet"); n != 1 {
		t.Fatalf("mid-unfold the name appears %d times, want 1", n)
	}
	*now = now.Add(time.Second)
	if f := a.foldNow(*now); f != 0 {
		t.Fatalf("still folded: %v", f)
	}
	if a.st.Focus != client.FocusList {
		t.Fatalf("focus after ← is %v, want the list", a.st.Focus)
	}
	a.render()
	if side := sidebarText(a); !strings.Contains(side, "planet") ||
		!strings.Contains(side, "私聊") || !strings.Contains(side, "设置") {
		t.Fatalf("the list did not come back:\n%s", side)
	}
}
