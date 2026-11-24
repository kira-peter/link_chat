package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// pagesApp is signed in on the list, just landed, with a direct message, a
// joined channel and one not joined, on a frozen clock.
func pagesApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a, _ := newKeyTestApp(t)
	now := freezeClock(t, time.Unix(60000, 0))
	a.st.UserID, a.st.Username = "me", "alice"
	land(a, []protocol.Conversation{
		{ID: "d1", Name: "carol", Kind: protocol.KindDM, Joined: true, LastMessageAt: 300},
		{ID: "c1", Name: "general", Kind: protocol.KindChannel, Joined: true, MemberCount: 3, LastMessageAt: 200},
		{ID: "c2", Name: "lounge", Kind: protocol.KindChannel, Joined: false, MemberCount: 7, Topic: "闲聊", LastMessageAt: 100},
	})
	a.st.Conversations["d1"].Unread = 2
	a.st.Conversations["c1"].Unread = 5
	return a, now
}

func down(a *App) { a.handleKey(nil, specialKey(tcell.KeyDown)) }

// ---------- 2.1 / 3.1: the cursor decides the page; moving does not fold ----------

func TestMovingTheCursorChangesThePageWithoutFolding(t *testing.T) {
	a, now := pagesApp(t)
	if a.page() != client.PageWelcome {
		t.Fatalf("just landed the page is %v, want the welcome", a.page())
	}
	steps := []struct {
		page   client.Page
		active string
	}{
		{client.PageNewChannel, ""},       // carol → the channels' new button: its page
		{client.PageConversation, "c1"},   // general
		{client.PageChannelPreview, "c1"}, // lounge: previewed, general stays open behind
		{client.PageFriends, "c1"},        // into the foot
		{client.PageMoments, "c1"},
		{client.PageMe, "c1"},
	}
	for i, s := range steps {
		down(a)
		if a.page() != s.page || a.st.ActiveID != s.active {
			t.Fatalf("step %d: page %v active %q, want %v %q", i, a.page(), a.st.ActiveID, s.page, s.active)
		}
		if a.st.Focus != client.FocusList {
			t.Fatalf("step %d: moving the cursor moved the focus to %v", i, a.st.Focus)
		}
		if a.anim.fold.Target() != 0 || a.foldNow(*now) != 0 {
			t.Fatalf("step %d: moving the cursor folded the list", i)
		}
	}
}

// ---------- 3.2: settling ----------

// Skimming past conversations touches none of them; resting on one marks it read.
func TestUnreadClearsOnlyWhereTheCursorRests(t *testing.T) {
	a, now := pagesApp(t)
	// Hold ↓: carol is skipped over quickly... the cursor starts on carol (landed),
	// so go to general and straight on.
	down(a) // + new channel
	down(a) // general
	advance(now, 50*time.Millisecond)
	a.settlePending(*now)
	down(a) // lounge (preview)
	advance(now, time.Second)
	a.settlePending(*now)
	if a.st.Conversations["c1"].Unread != 5 {
		t.Fatalf("general was only passed over, yet its unread became %d", a.st.Conversations["c1"].Unread)
	}

	// Back up and rest on general.
	a.handleKey(nil, specialKey(tcell.KeyUp))
	advance(now, settleDelay+time.Millisecond)
	a.settlePending(*now)
	if a.st.Conversations["c1"].Unread != 0 {
		t.Fatalf("resting on general left %d unread", a.st.Conversations["c1"].Unread)
	}
	if a.st.Conversations["d1"].Unread != 2 {
		t.Fatal("carol was never shown, yet its unread changed")
	}
}

// The network side is asked for once, for where the cursor stopped.
func TestHistoryIsFetchedOnlyWhereTheCursorStops(t *testing.T) {
	a, now := pagesApp(t)
	var loads []string
	a.onLoad = func(id string) { loads = append(loads, id) }
	down(a)                                   // + new channel
	down(a)                                   // general
	a.handleKey(nil, specialKey(tcell.KeyUp)) // + new channel
	a.handleKey(nil, specialKey(tcell.KeyUp)) // carol
	down(a)                                   // + new channel
	down(a)                                   // general
	advance(now, settleDelay+time.Millisecond)
	a.settlePending(*now)
	if len(loads) != 1 || loads[0] != "c1" {
		t.Fatalf("loads %v, want exactly one, for general", loads)
	}
}

// The main loop wakes for a settle that falls due.
func TestTheLoopWakesForAPendingSettle(t *testing.T) {
	a, now := pagesApp(t)
	down(a)
	down(a) // general
	if d, ok := a.pendingIn(*now); !ok || d <= 0 || d > settleDelay {
		t.Fatalf("pending in %v (%v), want within %v", d, ok, settleDelay)
	}
}

// Entering a conversation settles it at once: no waiting once the user chose.
func TestEnteringAConversationSettlesItAtOnce(t *testing.T) {
	a, _ := pagesApp(t)
	down(a) // + new channel
	down(a) // general
	a.handleKey(nil, specialKey(tcell.KeyRight))
	if a.st.Focus != client.FocusInput || a.st.Conversations["c1"].Unread != 0 {
		t.Fatalf("→ on general: focus %v unread %d", a.st.Focus, a.st.Conversations["c1"].Unread)
	}
}

// ---------- 3.3: previews ----------

func TestPreviewDoesNotJoinAndEnteringDoes(t *testing.T) {
	a, now := pagesApp(t)
	var loads []string
	a.onLoad = func(id string) { loads = append(loads, id) }
	down(a)
	down(a)
	down(a) // lounge
	advance(now, time.Second)
	a.settlePending(*now)
	if a.st.Conversations["c2"].Joined {
		t.Fatal("looking at a channel joined it")
	}
	for _, id := range loads {
		if id == "c2" {
			t.Fatal("looking at a channel fetched (and so joined) it")
		}
	}
	a.render()
	if !strings.Contains(screenText(a.scr.(tcell.SimulationScreen)), "闲聊") {
		t.Fatal("the preview does not show the channel's description")
	}

	a.handleKey(nil, specialKey(tcell.KeyRight))
	if !a.st.Conversations["c2"].Joined || a.st.ActiveID != "c2" || a.st.Focus != client.FocusInput {
		t.Fatalf("→ on the preview: joined %v active %q focus %v", a.st.Conversations["c2"].Joined, a.st.ActiveID, a.st.Focus)
	}
	if len(loads) == 0 || loads[len(loads)-1] != "c2" {
		t.Fatalf("entering the preview did not join and load it: %v", loads)
	}
}

// ---------- 3.4 / 2.2 / 2.3: entering a page, leaving it ----------

func TestEnteringAnEntryPageFoldsAndLeavingUnfolds(t *testing.T) {
	for _, c := range []struct {
		downs int
		page  client.Page
		title string
	}{{4, client.PageFriends, "好友"}, {5, client.PageMoments, "发现"}, {6, client.PageMe, "我"}} {
		a, now := pagesApp(t)
		for i := 0; i < c.downs; i++ {
			down(a)
		}
		a.handleKey(nil, specialKey(tcell.KeyRight))
		if a.page() != c.page || a.st.Focus != client.FocusInput {
			t.Fatalf("%s: → gave page %v focus %v", c.title, a.page(), a.st.Focus)
		}
		advance(now, time.Second)
		if a.foldNow(*now) != 1 {
			t.Fatalf("%s: entering the page did not fold the list", c.title)
		}
		a.render()
		scr := a.scr.(tcell.SimulationScreen)
		w, h := scr.Size()
		content := views.ContentRect(w, h)
		x, ok := a.pageTitleX(content, 1)
		if !ok {
			t.Fatalf("%s: no title position", c.title)
		}
		if r, _, _, _ := scr.GetContent(x, content.Y); r != []rune(c.title)[0] {
			t.Fatalf("%s: the title is not where it lands (found %q at %d)", c.title, r, x)
		}
		if x < w/3 {
			t.Fatalf("%s: the title is at %d, not centred", c.title, x)
		}

		// Esc leaves (← too, except the settings page walks its levels first).
		a.handleKey(nil, specialKey(tcell.KeyEscape))
		if a.st.Focus != client.FocusList {
			t.Fatalf("%s: Esc did not return to the list", c.title)
		}
		if row := a.st.SidebarRows()[a.ui.Selected]; row.Kind != client.RowEntry {
			t.Fatalf("%s: back on the list the cursor is not on the entry", c.title)
		}
	}
}

// The title flies from the entry's row in the foot.
func TestAnEntryPageTitleFlies(t *testing.T) {
	a, now := pagesApp(t)
	for i := 0; i < 5; i++ {
		down(a)
	}
	a.handleKey(nil, specialKey(tcell.KeyRight))
	advance(now, foldDuration/2)
	w, h := a.scr.Size()
	f, flying := a.nameFlight(views.ContentRect(w, h), a.foldNow(*now))
	if !flying || f.Name != "发现" {
		t.Fatalf("mid-fold: flying %v name %q", flying, f.Name)
	}
	if n := strings.Count(func() string { a.render(); return screenText(a.scr.(tcell.SimulationScreen)) }(), "发现"); n > 1 {
		t.Fatalf("mid-fold \"发现\" is on screen %d times", n)
	}
}

// Alt+S and Alt+H work from anywhere: the page comes up with the keyboard on it.
func TestAltShortcutsOpenTheirPages(t *testing.T) {
	for _, focus := range []client.Focus{client.FocusList, client.FocusInput} {
		a, _ := pagesApp(t)
		if focus == client.FocusInput {
			down(a)
			down(a)
			a.handleKey(nil, specialKey(tcell.KeyRight)) // in a conversation
		}
		a.handleKey(nil, altKey('s'))
		if a.page() != client.PageSettings || a.st.Focus != client.FocusInput {
			t.Errorf("from %v: Alt+S gave page %v focus %v", focus, a.page(), a.st.Focus)
		}
		a.handleKey(nil, altKey('h'))
		if a.page() != client.PageHelp || a.st.Focus != client.FocusInput {
			t.Errorf("from %v: Alt+H gave page %v focus %v", focus, a.page(), a.st.Focus)
		}
		if a.st.InputText() != "" {
			t.Errorf("from %v: an Alt shortcut typed into the draft", focus)
		}
	}
}

// ---------- 4.5 / 4.6: choosing, saving, cancelling ----------

// enterSettingsAt opens the settings page on the category titled cat.
func enterSettingsAt(t *testing.T, a *App, cat string) {
	t.Helper()
	a.handleKey(nil, altKey('s'))
	for i, c := range views.Categories() {
		if c.Title.Get(a.lang) == cat {
			a.ui.SettingsNav.Cat = i
			return
		}
	}
	t.Fatalf("no category %q", cat)
}

func TestChoosingAValueSavesItAndCancellingDoesNot(t *testing.T) {
	a, _ := pagesApp(t)
	enterSettingsAt(t, a, "聊天")
	press := func(k tcell.Key) { a.handleKey(nil, specialKey(k)) }
	press(tcell.KeyRight) // into the items: bubble width
	press(tcell.KeyRight) // the wheel, on 70%
	if a.ui.SettingsNav.Level != views.LevelChoose || a.ui.SettingsNav.Choice != 1 {
		t.Fatalf("the wheel did not open on the current value: %+v", a.ui.SettingsNav)
	}
	press(tcell.KeyDown) // 80%
	if a.cfg.BubbleWidth != 0 {
		t.Fatal("moving through the wheel saved the candidate")
	}
	if lay, _, _ := a.previewLayout(); lay.BubblePercent != 80 {
		t.Fatalf("the preview is not showing the candidate: %+v", lay)
	}
	press(tcell.KeyEscape) // cancel
	if a.cfg.BubbleWidth != 0 || a.cfg.Layout().BubblePercent != 70 {
		t.Fatalf("cancelling changed the setting: %+v", a.cfg)
	}
	if lay, _, _ := a.previewLayout(); lay.BubblePercent != 70 {
		t.Fatal("after cancelling the preview still shows the candidate")
	}

	press(tcell.KeyRight)
	press(tcell.KeyDown)
	press(tcell.KeyEnter) // commit 80%
	if a.cfg.Layout().BubblePercent != 80 {
		t.Fatalf("Enter did not save 80%%: %+v", a.cfg)
	}
	saved, err := LoadConfig(a.cfgPath)
	if err != nil || saved.BubbleWidth != 80 {
		t.Fatalf("not persisted: %+v, %v", saved, err)
	}
}

// The language and theme candidates only reach the preview: the rest of the
// screen stays as it was until the value is confirmed.
func TestLanguageCandidateOnlyReachesThePreview(t *testing.T) {
	a, _ := pagesApp(t)
	enterSettingsAt(t, a, "通用")
	a.handleKey(nil, specialKey(tcell.KeyRight))
	a.handleKey(nil, specialKey(tcell.KeyRight)) // wheel on 中文
	a.handleKey(nil, specialKey(tcell.KeyDown))  // English
	if a.lang != "zh" {
		t.Fatal("moving onto English changed the whole interface's language")
	}
	if _, _, lang := a.previewLayout(); lang != "en" {
		t.Fatalf("the preview does not show the English candidate (%v)", lang)
	}
	a.handleKey(nil, specialKey(tcell.KeyEnter))
	if a.lang != "en" {
		t.Fatal("confirming English did not switch the language")
	}
}

// Sign out lives at the bottom of "me" now; cancelling leaves you there.
func TestSignOutFromMeAndCancelReturnsThere(t *testing.T) {
	a, _ := pagesApp(t)
	a.st.Token = "tok"
	a.showEntryPage(client.EntryMe)
	a.handleKey(nil, specialKey(tcell.KeyUp)) // wraps to the last row
	if views.MeItems[a.ui.Me.Selected] != views.MeSignOut {
		t.Fatalf("the last row of me is not sign out (%d)", a.ui.Me.Selected)
	}
	a.handleKey(nil, specialKey(tcell.KeyEnter))
	if a.ui.Dialog != views.DialogLogout {
		t.Fatalf("Enter on sign out opened %v", a.ui.Dialog)
	}
	a.handleKey(nil, specialKey(tcell.KeyEnter)) // cancel is focused
	if a.ui.Dialog != views.DialogNone || a.page() != client.PageMe || a.st.Token == "" {
		t.Fatalf("cancel: dialog %v page %v token %q", a.ui.Dialog, a.page(), a.st.Token)
	}
	if views.MeItems[a.ui.Me.Selected] != views.MeSignOut {
		t.Fatal("after cancelling the cursor left sign out")
	}
}

// ---------- 6.1 help scrolls ----------

func TestHelpScrollsToItsLastRow(t *testing.T) {
	a, _ := pagesApp(t)
	a.scr.(tcell.SimulationScreen).SetSize(100, 14) // too short for the whole help
	a.handleKey(nil, altKey('h'))
	for i := 0; i < 100; i++ {
		a.handleKey(nil, specialKey(tcell.KeyPgDn))
	}
	a.render()
	if !strings.Contains(screenText(a.scr.(tcell.SimulationScreen)), "Esc") {
		// The composer group ends with "Esc", the last row of the help.
		t.Fatalf("scrolled to the end, the last row is not shown:\n%s", screenText(a.scr.(tcell.SimulationScreen)))
	}
	a.handleKey(nil, specialKey(tcell.KeyPgDn))
	if a.ui.HelpScroll > views.HelpRowCount(a.lang) {
		t.Fatal("the help scrolled past its end")
	}
}

var _ = os.Remove

// A conversation list that arrives while the user is already browsing — a late
// refresh, a reconnect — leaves their page and their keyboard where they are.
func TestALateListRefreshDoesNotResetBrowsing(t *testing.T) {
	a, _ := pagesApp(t)
	down(a)
	down(a) // lounge: a preview, so nothing is open
	a.handleKey(nil, altKey('s'))
	if a.page() != client.PageSettings || a.st.Focus != client.FocusInput {
		t.Fatalf("precondition: page %v focus %v", a.page(), a.st.Focus)
	}
	land(a, []protocol.Conversation{
		{ID: "d1", Name: "carol", Kind: protocol.KindDM, Joined: true, LastMessageAt: 300},
		{ID: "c1", Name: "general", Kind: protocol.KindChannel, Joined: true, LastMessageAt: 200},
		{ID: "c2", Name: "lounge", Kind: protocol.KindChannel, Joined: false, LastMessageAt: 100},
	})
	if a.page() != client.PageSettings || a.st.Focus != client.FocusInput {
		t.Fatalf("a late refresh moved the user: page %v focus %v", a.page(), a.st.Focus)
	}
}
