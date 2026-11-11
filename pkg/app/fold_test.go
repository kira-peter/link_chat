package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// foldApp is a ready app on the main interface with the clock frozen, two
// conversations, one of them open, and the keyboard on the list with the fold
// settled open — the state a user is in right after pressing ← from a chat.
func foldApp(t *testing.T) (*App, context.Context, *time.Time) {
	t.Helper()
	a, ctx := newKeyTestApp(t)
	now := freezeClock(t, time.Unix(20000, 0))
	add := func(id, name, kind string, at int64) {
		c := a.st.EnsureConversation(id)
		c.Name, c.Kind, c.LastMessageAt = name, kind, at
	}
	add("c1", "general", protocol.KindChannel, 200)
	add("c2", "design", protocol.KindChannel, 100)
	a.st.ActiveID = "c1"
	a.st.Focus = client.FocusList
	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c1")
	// The fixture sets state directly, so it also puts the animation state where
	// the app would have left it: the fold open and the focus marker beside the
	// list's title.
	a.settleFold()
	a.anim.slide.MoveTo(a.regionX(client.FocusList), *now, 0, false)
	return a, ctx, now
}

// advance moves the frozen clock forward.
func advance(now *time.Time, d time.Duration) { *now = now.Add(d) }

// ---------- 5.2 the fold follows the focus ----------

// Every way of putting the keyboard in the composer aims the fold at "folded"; every
// way of putting it back on the list aims it at "open".
func TestFoldTargetFollowsFocusWhicheverRouteMovesIt(t *testing.T) {
	toComposer := map[string]func(a *App, ctx context.Context){
		"Enter on a conversation": func(a *App, ctx context.Context) {
			a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c2")
			a.handleKey(ctx, specialKey(tcell.KeyEnter))
		},
		"right arrow": func(a *App, ctx context.Context) { a.handleKey(ctx, specialKey(tcell.KeyRight)) },
		"Tab":         func(a *App, ctx context.Context) { a.handleKey(ctx, specialKey(tcell.KeyTab)) },
		"Esc in the list": func(a *App, ctx context.Context) {
			a.handleKey(ctx, specialKey(tcell.KeyEscape))
		},
	}
	for name, do := range toComposer {
		a, ctx, _ := foldApp(t)
		if a.anim.fold.Target() != 0 {
			t.Fatalf("%s: precondition: fold should start open", name)
		}
		do(a, ctx)
		if a.st.Focus != client.FocusInput {
			t.Fatalf("%s: focus = %v, want the composer", name, a.st.Focus)
		}
		if got := a.anim.fold.Target(); got != 1 {
			t.Errorf("%s: fold target = %v, want 1 (folded)", name, got)
		}
	}

	toList := map[string]func(a *App, ctx context.Context){
		"left arrow at the start": func(a *App, ctx context.Context) { a.handleKey(ctx, specialKey(tcell.KeyLeft)) },
		"Tab":                     func(a *App, ctx context.Context) { a.handleKey(ctx, specialKey(tcell.KeyTab)) },
		"Esc in the composer":     func(a *App, ctx context.Context) { a.handleKey(ctx, specialKey(tcell.KeyEscape)) },
	}
	for name, do := range toList {
		a, ctx, _ := foldApp(t)
		a.moveFocus(client.FocusInput)
		a.settleFold()
		if a.anim.fold.Target() != 1 {
			t.Fatalf("%s: precondition: fold should be folded", name)
		}
		do(a, ctx)
		if a.st.Focus != client.FocusList {
			t.Fatalf("%s: focus = %v, want the list", name, a.st.Focus)
		}
		if got := a.anim.fold.Target(); got != 0 {
			t.Errorf("%s: fold target = %v, want 0 (open)", name, got)
		}
	}
}

// With text in the composer the left arrow moves the caret first, so it must NOT
// unfold the list; only a caret at the start hands the keyboard (and the fold) back.
func TestLeftArrowUnfoldsOnlyOnceTheCaretIsAtTheStart(t *testing.T) {
	a, ctx, _ := foldApp(t)
	a.moveFocus(client.FocusInput)
	a.settleFold()
	a.st.RestoreInput("abc")
	a.st.CursorEnd()

	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	if a.anim.fold.Target() != 1 || a.st.Focus != client.FocusInput {
		t.Fatalf("← with the caret mid-text unfolded the list (target %v, focus %v)", a.anim.fold.Target(), a.st.Focus)
	}
	a.st.CursorHome()
	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	if a.anim.fold.Target() != 0 || a.st.Focus != client.FocusList {
		t.Fatalf("← at the start of the text did not unfold (target %v, focus %v)", a.anim.fold.Target(), a.st.Focus)
	}
}

// ---------- 5.3 no conversation, no fold ----------

// With nothing open the composer cannot take the keyboard: →, Tab and Esc all leave
// the list open and focused.
func TestNoOpenConversationBlocksTheComposerAndTheFold(t *testing.T) {
	for name, k := range map[string]tcell.Key{
		"right arrow": tcell.KeyRight, "Tab": tcell.KeyTab, "Esc": tcell.KeyEscape,
	} {
		a, ctx, _ := foldApp(t)
		a.st.ActiveID = "" // the state right after login
		a.st.Focus = client.FocusList
		a.settleFold()

		a.handleKey(ctx, specialKey(k))

		if a.st.Focus != client.FocusList {
			t.Errorf("%s: the keyboard reached the composer with no conversation open", name)
		}
		if a.anim.fold.Target() != 0 || a.foldNow(nowTime()) != 0 {
			t.Errorf("%s: the list folded with nothing open (target %v)", name, a.anim.fold.Target())
		}
	}
}

// Opening a conversation is what makes folding possible.
func TestOpeningAConversationFoldsTheList(t *testing.T) {
	a, ctx, _ := foldApp(t)
	a.st.ActiveID = ""
	a.settleFold()

	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c2")
	a.handleKey(ctx, specialKey(tcell.KeyEnter))

	if a.st.ActiveID != "c2" || a.st.Focus != client.FocusInput {
		t.Fatalf("Enter did not open and focus (active %q, focus %v)", a.st.ActiveID, a.st.Focus)
	}
	if a.anim.fold.Target() != 1 {
		t.Fatalf("opening a conversation did not fold the list (target %v)", a.anim.fold.Target())
	}
}

// The other route to opening — creating a channel — folds too.
func TestForcedOpenAlsoFolds(t *testing.T) {
	a, _, _ := foldApp(t)
	a.st.ActiveID = ""
	a.st.Focus = client.FocusInput // the create dialogs hand the keyboard to the composer
	a.settleFold()
	if a.anim.fold.Target() != 0 {
		t.Fatalf("precondition: nothing open, so the target must be open")
	}
	a.openConversationForced("c2")
	if a.anim.fold.Target() != 1 {
		t.Fatalf("openConversationForced did not re-aim the fold (target %v)", a.anim.fold.Target())
	}
}

// When the open conversation disappears under the user (a deleted channel), the
// list unfolds on its own instead of leaving a full-width blank screen.
func TestDeletingTheOpenConversationUnfoldsTheList(t *testing.T) {
	a, _, now := foldApp(t)
	a.moveFocus(client.FocusInput)
	a.settleFold()
	if a.foldNow(*now) != 1 {
		t.Fatalf("precondition: the list should be folded")
	}

	// The server tells the client the open channel is gone — through the real event
	// path, so the wiring is what is under test, not just the helper behind it.
	payload, err := json.Marshal(protocol.ChannelGoneEvent{ConversationID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	a.onEvent(protocol.EvChannelGone, payload)

	if a.st.ActiveID != "" {
		t.Fatalf("precondition: the open conversation should be gone, got %q", a.st.ActiveID)
	}
	// The keyboard leaves the composer, which has nothing to type into.
	if a.st.Focus != client.FocusList {
		t.Fatalf("focus stayed on %v with no conversation open, want the list", a.st.Focus)
	}
	// And the fold heads open and gets there.
	if a.anim.fold.Target() != 0 {
		t.Fatalf("the fold is not heading open (target %v)", a.anim.fold.Target())
	}
	advance(now, time.Second)
	if got := a.foldNow(*now); got != 0 {
		t.Fatalf("the list stayed folded after its conversation vanished (fold %v)", got)
	}
}

// Deleting a conversation that is NOT the open one must not disturb the user: the
// composer keeps the keyboard and the list stays folded.
func TestDeletingAnotherConversationLeavesTheFoldAlone(t *testing.T) {
	a, _, now := foldApp(t)
	a.moveFocus(client.FocusInput)
	a.settleFold()

	payload, _ := json.Marshal(protocol.ChannelGoneEvent{ConversationID: "c2"})
	a.onEvent(protocol.EvChannelGone, payload)

	if a.st.Focus != client.FocusInput || a.st.ActiveID != "c1" {
		t.Fatalf("deleting a different channel disturbed the open one (focus %v, active %q)", a.st.Focus, a.st.ActiveID)
	}
	if got := a.foldNow(*now); got != 1 {
		t.Fatalf("the list unfolded for an unrelated deletion (fold %v)", got)
	}
}

// ---------- 5.5 timing ----------

// A fold finishes inside the 400 ms ceiling and then stops asking for frames.
func TestFoldFinishesWithinTheCeilingAndStopsAskingForFrames(t *testing.T) {
	a, _, now := foldApp(t)
	a.moveFocus(client.FocusInput)

	if !a.needsFrames(*now) {
		t.Fatal("a fold in flight must ask for frames")
	}
	advance(now, 400*time.Millisecond)
	if !a.anim.fold.Done(*now) {
		t.Fatal("the fold was still running after 400 ms")
	}
	if got := a.foldNow(*now); got != 1 {
		t.Fatalf("the fold ended at %v, want 1", got)
	}
	// Give the composer a draft so its own hint sweep is not what is measured.
	a.st.RestoreInput("draft")
	advance(now, 500*time.Millisecond)
	if a.needsFrames(*now) {
		t.Fatal("frames are still being requested after the fold finished")
	}
}

// Keys land the instant they are pressed, whatever the fold is doing.
func TestKeysAreNotBlockedByAFoldInFlight(t *testing.T) {
	a, ctx, now := foldApp(t)
	a.moveFocus(client.FocusInput)
	advance(now, 50*time.Millisecond) // well inside the fold

	a.handleKey(ctx, key('h'))
	a.handleKey(ctx, key('i'))
	if got := a.st.InputText(); got != "hi" {
		t.Fatalf("typing during the fold gave %q, want %q", got, "hi")
	}
}

// Reversing mid-way turns from the current value instead of jumping, and never
// takes longer in total than a single fold.
func TestReversingMidFoldContinuesFromTheCurrentValue(t *testing.T) {
	a, ctx, now := foldApp(t)
	a.moveFocus(client.FocusInput)
	advance(now, 100*time.Millisecond)
	mid := a.foldNow(*now)
	if mid <= 0 || mid >= 1 {
		t.Fatalf("precondition: expected a mid-fold value, got %v", mid)
	}

	a.handleKey(ctx, specialKey(tcell.KeyLeft)) // empty draft, caret at 0: unfold
	if got := a.foldNow(*now); got != mid {
		t.Fatalf("reversing jumped from %v to %v", mid, got)
	}
	if a.anim.fold.Target() != 0 {
		t.Fatalf("the fold is not heading back open (target %v)", a.anim.fold.Target())
	}

	advance(now, 20*time.Millisecond)
	if got := a.foldNow(*now); got >= mid {
		t.Fatalf("after reversing the fold went %v -> %v, want it to fall", mid, got)
	}
	// And it is done within one fold's time of the reversal, not two.
	advance(now, foldDuration)
	if !a.anim.fold.Done(*now) {
		t.Fatal("a reversed fold took longer than a single fold")
	}
	if got := a.foldNow(*now); got != 0 {
		t.Fatalf("the reversed fold ended at %v, want 0", got)
	}
}

// With animation off the fold lands on its target immediately, with no middle frame.
func TestFoldIsInstantWhenAnimationIsOff(t *testing.T) {
	a, _, now := foldApp(t)
	a.st.MotionOff = true

	a.moveFocus(client.FocusInput)
	if got := a.foldNow(*now); got != 1 {
		t.Fatalf("fold = %v at the instant of the move, want 1", got)
	}
	if a.needsFrames(*now) {
		t.Fatal("frames requested with animation off")
	}

	a.handleKey(context.Background(), specialKey(tcell.KeyLeft))
	if got := a.foldNow(*now); got != 0 {
		t.Fatalf("unfold = %v at the instant of the move, want 0", got)
	}
}

// Switching animation off part-way through a fold lands it where it was heading.
func TestTurningAnimationOffSettlesAFoldInFlight(t *testing.T) {
	a, _, now := foldApp(t)
	a.moveFocus(client.FocusInput)
	advance(now, 60*time.Millisecond)
	if v := a.foldNow(*now); v <= 0 || v >= 1 {
		t.Fatalf("precondition: expected a mid-fold value, got %v", v)
	}
	a.toggleMotion() // turns animation off
	if !a.st.MotionOff {
		t.Fatal("precondition: animation should now be off")
	}
	if got := a.foldNow(*now); got != 1 {
		t.Fatalf("a fold was stranded at %v when animation went off, want it settled at 1", got)
	}
}

// ---------- 5.9 narrow windows ----------

// In the single-pane layout there is no sidebar beside a conversation to fold, so
// the fold stays 0 and the keys keep their meaning.
func TestNarrowWindowNeverFolds(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.scr.(tcell.SimulationScreen).SetSize(40, 20) // below the two-column threshold
	freezeClock(t, time.Unix(21000, 0))
	c := a.st.EnsureConversation("c1")
	c.Name, c.Kind = "general", protocol.KindChannel
	a.st.ActiveID = "c1"
	a.st.Focus = client.FocusList

	if a.canFold() {
		t.Fatal("a 40-column window should not support folding")
	}
	a.handleKey(ctx, specialKey(tcell.KeyRight))
	if a.st.Focus != client.FocusInput {
		t.Fatalf("→ in a narrow window gave %v, want the composer", a.st.Focus)
	}
	if a.foldNow(nowTime()) != 0 || a.anim.fold.Target() != 0 {
		t.Fatal("a narrow window folded")
	}
	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	if a.st.Focus != client.FocusList {
		t.Fatalf("← in a narrow window gave %v, want the list", a.st.Focus)
	}
}

// ---------- the rendered result ----------

// screenHas reports whether any row of the screen contains s.
func screenHas(a *App, s string) bool {
	return strings.Contains(screenText(a.scr.(tcell.SimulationScreen)), s)
}

// sidebarText reads only the strip of the screen the open sidebar occupies, from
// the top of the content area to the bottom. Asserting on the whole screen is
// wrong here: the status bar's "Alt+S 设置" and a conversation's own title contain
// the same words as the sidebar's entries, so a whole-screen search cannot tell a
// leaked list from an ordinary label elsewhere.
func sidebarText(a *App) string {
	s := a.scr.(tcell.SimulationScreen)
	s.Show()
	w, h := s.Size()
	content := views.ContentRect(w, h)
	side := views.SidebarRectAt(content, 0)
	var b strings.Builder
	for y := content.Y; y < content.Y+content.H; y++ {
		skip := 0
		for x := side.X; x < side.X+side.W; x++ {
			if skip > 0 {
				skip--
				continue
			}
			r, _, _, width := s.GetContent(x, y)
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
			if width == 2 {
				skip = 1
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Folded: the list's contents are gone from where it was and the conversation
// reaches the left edge. Open: the list is back.
func TestFoldedScreenShowsNoListAndOpenScreenDoes(t *testing.T) {
	a, _, now := foldApp(t)
	a.render()
	open := sidebarText(a)
	for _, want := range []string{"私聊", "群聊", "design", "设置", "帮助", "关于"} {
		if !strings.Contains(open, want) {
			t.Fatalf("an open list should show %q:\n%s", want, open)
		}
	}

	a.moveFocus(client.FocusInput)
	advance(now, time.Second)
	a.render()
	folded := sidebarText(a)
	for _, gone := range []string{"私聊", "群聊", "design", "设置", "帮助", "关于"} {
		if strings.Contains(folded, gone) {
			t.Fatalf("a folded list still shows %q where the sidebar was:\n%s", gone, folded)
		}
	}

	a.handleKey(context.Background(), specialKey(tcell.KeyLeft))
	advance(now, time.Second)
	a.render()
	if back := sidebarText(a); !strings.Contains(back, "设置") || !strings.Contains(back, "私聊") {
		t.Fatalf("the list did not come back after unfolding:\n%s", back)
	}
}

// The threshold exists for the middle of the fold, not its end: at fold 1 the
// sidebar is zero cells wide and nothing could be drawn anyway. Part-way, past the
// threshold, the strip is still several cells wide and a list squeezed into it
// would show clipped words — so the contents must already be gone there while the
// strip is still non-empty. This is the case a "draw it regardless" bug would
// break, and the settled-state test above cannot see it.
func TestListContentsVanishBeforeTheStripDoes(t *testing.T) {
	a, _, now := foldApp(t)
	a.moveFocus(client.FocusInput)

	// Walk the fold and find a moment that is past the threshold but not finished.
	var mid float64
	found := false
	for ms := 0; ms <= 320; ms += 5 {
		cur := time.Unix(20000, 0).Add(time.Duration(ms) * time.Millisecond)
		*now = cur
		f := a.anim.fold.At(cur)
		w, h := a.scr.Size()
		stripW := views.SidebarRectAt(views.ContentRect(w, h), f).W
		if !views.SidebarContentVisible(f) && stripW >= 3 {
			mid, found = f, true
			break
		}
	}
	if !found {
		t.Fatal("never saw a frame past the threshold with the strip still 3+ cells wide")
	}

	a.render()
	// Only the strip that is left of the divider: read the sidebar's current width,
	// not its open width, so a leak into the shrinking strip is what is detected.
	s := a.scr.(tcell.SimulationScreen)
	w, h := s.Size()
	content := views.ContentRect(w, h)
	side := views.SidebarRectAt(content, mid)
	var strip strings.Builder
	for y := content.Y; y < content.Y+content.H; y++ {
		for x := side.X; x < side.X+side.W; x++ {
			if r, _, _, _ := s.GetContent(x, y); r != 0 && r != ' ' {
				strip.WriteRune(r)
			}
		}
	}
	got := strip.String()

	// Nothing of the sidebar's own contents may remain: no section titles, no other
	// conversation, no foot entry, no count or time.
	for _, leaked := range []string{"私聊", "群聊", "design", "设置", "帮助", "关于", "会话"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("at fold %.2f the %d-cell strip still shows the sidebar's %q (strip holds %q)",
				mid, side.W, leaked, got)
		}
	}
	// The things that may cross the strip are the ones that travel with the fold:
	// the open conversation's name in flight and the focus marker beside its title.
	// Whatever else is there beyond those is a leak.
	for _, r := range got {
		if !strings.ContainsRune("general…▌", r) {
			t.Fatalf("at fold %.2f the strip holds %q: %q is neither the flying name nor the focus marker",
				mid, got, r)
		}
	}
}

// The composer widens with the fold: its frame reaches further left when folded.
func TestComposerWidensWhenTheListFolds(t *testing.T) {
	a, _, now := foldApp(t)
	left := func() int {
		a.render()
		s := a.scr.(tcell.SimulationScreen)
		w, h := s.Size()
		min := w
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if r, _, _, _ := s.GetContent(x, y); r == '┌' && x < min && y > h/2 {
					min = x
				}
			}
		}
		return min
	}
	open := left()
	a.moveFocus(client.FocusInput)
	advance(now, time.Second)
	folded := left()
	if folded >= open {
		t.Fatalf("the composer did not widen when the list folded: left edge %d -> %d", open, folded)
	}
}

var _ = views.DialogNone
