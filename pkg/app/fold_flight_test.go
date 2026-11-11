package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
)

// ---------- 5.6 the cursor returns to the open conversation ----------

// Wander the cursor off in the list, go into the composer, come back: the cursor is
// on the conversation that is open, not where it last wandered.
func TestUnfoldPutsTheCursorBackOnTheOpenConversation(t *testing.T) {
	a, _, _ := foldApp(t)
	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c2") // design, not open
	a.moveFocus(client.FocusInput)
	a.moveFocus(client.FocusList)

	rows := a.st.SidebarRows()
	want := client.IndexOfConversation(rows, "c1")
	if a.ui.Selected != want {
		t.Fatalf("the cursor is on row %d (%s), want the open conversation at row %d",
			a.ui.Selected, rows[a.ui.Selected].Conversation.Name, want)
	}
}

// It also works when the open conversation sits far down a long list: the row is
// reachable and on screen after unfolding, so the name has somewhere to land.
func TestUnfoldKeepsTheOpenConversationOnScreenInALongList(t *testing.T) {
	a, _, now := foldApp(t)
	for i := 0; i < 40; i++ {
		id := "x" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		c := a.st.EnsureConversation(id)
		c.Name, c.Kind, c.LastMessageAt = "extra-"+id, "channel", int64(10_000+i)
	}
	a.st.ActiveID = "c2" // an old, quiet conversation, many rows down
	a.ui.Selected = client.FirstSelectable(a.st.SidebarRows())
	a.moveFocus(client.FocusInput)
	advance(now, time.Second)
	a.moveFocus(client.FocusList)
	advance(now, time.Second)
	a.render()

	rows := a.st.SidebarRows()
	if got := rows[a.ui.Selected]; got.Conversation == nil || got.Conversation.ID != "c2" {
		t.Fatalf("the cursor is not on the open conversation")
	}
	if !strings.Contains(sidebarText(a), "design") {
		t.Fatalf("the open conversation is not visible after unfolding:\n%s", sidebarText(a))
	}
}

// The cursor is only moved when the keyboard actually returns to the list. Browsing
// the list normally never drags it back to the open conversation.
func TestBrowsingTheListDoesNotSnapTheCursorBack(t *testing.T) {
	a, ctx, _ := foldApp(t)
	// Already in the list with the cursor on a different row.
	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c2")
	before := a.ui.Selected

	a.handleKey(ctx, specialKey(tcell.KeyLeft)) // no-op at the left edge: already here
	a.moveFocus(client.FocusList)               // asking for where it already is
	if a.ui.Selected != before {
		t.Fatalf("a move to the region that already had focus changed the cursor: %d -> %d", before, a.ui.Selected)
	}
}

// With a filter hiding the open conversation the cursor is left alone rather than
// pointed at a row that is not on screen.
func TestUnfoldWithTheOpenConversationFilteredOutLeavesTheCursor(t *testing.T) {
	a, _, _ := foldApp(t)
	a.moveFocus(client.FocusInput)
	a.st.Filter = []rune("des") // matches design only; the open one, general, is hidden
	rows := a.st.SidebarRows()
	a.ui.Selected = client.IndexOfConversation(rows, "c2")
	want := a.ui.Selected

	a.moveFocus(client.FocusList)
	if a.ui.Selected != want {
		t.Fatalf("the cursor moved to %d though the open conversation is filtered out", a.ui.Selected)
	}
	if r := a.st.SidebarRows()[a.ui.Selected]; !r.Selectable() {
		t.Fatalf("the cursor is on an unselectable row")
	}
}

// ---------- the flight end to end ----------

// run a full open: pick a conversation in the list, watch the fold and the name,
// then unfold and watch them come back.
func TestOpenAndCloseEndToEnd(t *testing.T) {
	a, ctx, now := foldApp(t)
	a.st.ActiveID = ""
	a.settleFold()
	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c2")
	a.render()

	// 1. Open: Enter.
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	if a.st.ActiveID != "c2" || a.st.Focus != client.FocusInput {
		t.Fatalf("Enter did not open and focus (active %q, focus %v)", a.st.ActiveID, a.st.Focus)
	}

	// 2. Mid-fold: the list is part-way gone and the name is in the air.
	advance(now, 120*time.Millisecond)
	a.render()
	mid := a.foldNow(*now)
	if mid <= 0 || mid >= 1 {
		t.Fatalf("expected a mid-fold value after 120ms, got %v", mid)
	}
	if n := strings.Count(screenText(a.scr.(tcell.SimulationScreen)), "design"); n != 1 {
		t.Fatalf("mid-fold the name is on screen %d times, want exactly 1 (in the air)", n)
	}

	// 3. Settled folded: the sidebar's own contents are gone and the title carries
	// the name. The strip where the sidebar used to be now belongs to the
	// conversation, so it legitimately holds the title; what must be absent are the
	// words only the sidebar has.
	advance(now, time.Second)
	a.render()
	for _, only := range []string{"私聊", "群聊", "设置", "帮助", "关于"} {
		if strings.Contains(sidebarText(a), only) {
			t.Fatalf("the folded sidebar still shows %q:\n%s", only, sidebarText(a))
		}
	}
	if n := strings.Count(screenText(a.scr.(tcell.SimulationScreen)), "design"); n != 1 {
		t.Fatalf("folded, the name is on screen %d times, want 1 (the title)", n)
	}

	// 4. Unfold: ←. The cursor is back on the open conversation.
	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	if a.st.Focus != client.FocusList {
		t.Fatalf("← did not return to the list (focus %v)", a.st.Focus)
	}
	advance(now, 120*time.Millisecond)
	a.render()
	if n := strings.Count(screenText(a.scr.(tcell.SimulationScreen)), "design"); n != 1 {
		t.Fatalf("unfolding, the name is on screen %d times, want exactly 1 (flying back)", n)
	}

	// 5. Settled open: the list is back, the name in its row and in the title.
	advance(now, time.Second)
	a.render()
	if !strings.Contains(sidebarText(a), "design") || !strings.Contains(sidebarText(a), "设置") {
		t.Fatalf("the list did not return:\n%s", sidebarText(a))
	}
	rows := a.st.SidebarRows()
	if got := rows[a.ui.Selected]; got.Conversation == nil || got.Conversation.ID != "c2" {
		t.Fatalf("the cursor is not on the open conversation after unfolding")
	}
}

// With animation off every step is a single frame: no airborne name ever appears.
func TestNoAirborneNameWhenAnimationIsOff(t *testing.T) {
	a, ctx, now := foldApp(t)
	a.st.MotionOff = true
	a.st.ActiveID = ""
	a.settleFold()
	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c2")

	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	a.render()
	if got := a.foldNow(*now); got != 1 {
		t.Fatalf("fold = %v straight after opening with animation off, want 1", got)
	}
	if _, flying := a.nameFlight(views.ContentRect(a.scr.Size()), a.foldNow(*now)); flying {
		t.Fatal("a name was in flight with animation off")
	}

	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	a.render()
	if got := a.foldNow(*now); got != 0 {
		t.Fatalf("fold = %v straight after unfolding with animation off, want 0", got)
	}
}

// A conversation whose row is scrolled out of the sidebar has nothing to fly from:
// the name is simply absent until the title shows it.
func TestNoFlightWhenTheRowIsScrolledOut(t *testing.T) {
	a, _, now := foldApp(t)
	for i := 0; i < 40; i++ {
		id := "y" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		c := a.st.EnsureConversation(id)
		c.Name, c.Kind, c.LastMessageAt = "pad-"+id, "channel", int64(20_000+i)
	}
	a.st.ActiveID = "c2"
	// Park the cursor at the bottom of the list so c2, near the top, is off screen.
	rows := a.st.SidebarRows()
	a.ui.Selected = len(rows) - 1
	a.settleFold()
	a.moveFocus(client.FocusInput)
	advance(now, 100*time.Millisecond)

	w, h := a.scr.Size()
	if _, flying := a.nameFlight(views.ContentRect(w, h), a.foldNow(*now)); flying {
		t.Fatal("a name flew out of a row that is not on screen")
	}
}

// ---------- the frame budget with the whole feature ----------

// The fold and the flight stop asking for frames once they land, and the list
// sweep stays quiet while the fold is moving.
func TestFlightCostsFramesOnlyWhileMoving(t *testing.T) {
	a, ctx, now := foldApp(t)
	a.st.ActiveID = ""
	a.settleFold()
	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c2")
	a.handleKey(ctx, specialKey(tcell.KeyEnter))

	if !a.needsFrames(*now) {
		t.Fatal("a fold in flight must ask for frames")
	}
	if a.listSweeping() {
		t.Fatal("the list sweep ran while the sidebar was folding")
	}

	advance(now, time.Second)
	a.st.RestoreInput("a draft, so the empty-composer hint is not sweeping")
	if a.needsFrames(*now) {
		t.Fatal("frames are still requested after the fold landed")
	}
	_ = context.Background
}
