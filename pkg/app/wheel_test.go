package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

func wheelLabels(a *App) []string {
	var out []string
	for _, it := range a.ui.Wheel.Items {
		out = append(out, it.Label.Get(i18n.ZH))
	}
	return out
}

func hasLabel(ls []string, l i18n.T) bool {
	for _, s := range ls {
		if s == l.Get(i18n.ZH) {
			return true
		}
	}
	return false
}

func TestWheelOpensAndCloses(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	if !a.ui.Wheel.Open || a.ui.Wheel.Sel != 0 {
		t.Fatal("F1 did not open the wheel on its first item")
	}
	turn(a, ctx, 1)
	turn(a, ctx, 1)
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	if a.ui.Wheel.Open {
		t.Fatal("the wheel key again did not just close it")
	}
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	focus := a.st.Focus
	a.handleKey(ctx, specialKey(tcell.KeyEscape))
	if a.ui.Wheel.Open || a.st.Focus != focus {
		t.Fatal("Esc did not just close it")
	}
	// Set to Ctrl+Space, a space typed with Ctrl is the same key.
	a.uploadSettings(a.setAccountSetting("wheel_key", "ctrl_space"))
	a.handleKey(ctx, tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModCtrl))
	if !a.ui.Wheel.Open {
		t.Fatal("Ctrl with a space did not open the wheel")
	}
	a.handleEvent(ctx, tcell.NewEventFocus(false))
	if a.ui.Wheel.Open {
		t.Fatal("losing focus left the wheel open")
	}
}

func TestWheelTurnsAndKeepsTheKeyboard(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.render()
	sel := a.ui.Selected
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	n := len(a.ui.Wheel.Items)
	turn(a, ctx, -1)
	if a.ui.Wheel.Sel != n-1 {
		t.Fatalf("back from the first item went to %d, want the last, %d", a.ui.Wheel.Sel, n-1)
	}
	turn(a, ctx, 1)
	if a.ui.Wheel.Sel != 0 {
		t.Fatal("on from the last item did not start over at the first")
	}
	a.handleKey(ctx, tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if a.ui.Selected != sel || a.st.Filter != nil || !a.ui.Wheel.Open {
		t.Fatal("a key leaked to the page under the wheel")
	}
}

func TestWheelEnterRuns(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	// The app-wide items are on the home list's foot entries.
	a.st.Focus = client.FocusList
	a.ui.Selected = client.IndexOfEntry(a.st.SidebarRows(), client.EntryMe)
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	for a.ui.Wheel.Items[a.ui.Wheel.Sel].Label != i18n.WheelSettings {
		turn(a, ctx, 1)
	}
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	if a.ui.Wheel.Open || a.page() != client.PageSettings {
		t.Fatalf("Enter on 设置: open=%v page=%v", a.ui.Wheel.Open, a.page())
	}
}

func TestWheelNotOverDialogs(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.openLogoutConfirm()
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	if a.ui.Wheel.Open {
		t.Fatal("the wheel opened over a dialog")
	}
}

func TestWheelKeySetting(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.uploadSettings(a.setAccountSetting("wheel_key", "ctrl_g"))
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	if a.ui.Wheel.Open {
		t.Fatal("F1 still opens the wheel after choosing Ctrl+G")
	}
	a.handleKey(ctx, specialKey(tcell.KeyCtrlG))
	if !a.ui.Wheel.Open {
		t.Fatal("Ctrl+G did not open the wheel")
	}
	a.handleKey(ctx, specialKey(tcell.KeyCtrlG))
	if a.ui.Wheel.Open {
		t.Fatal("Ctrl+G again did not close it")
	}
	if a.cfg.accountSettings()["wheel_key"] != "ctrl_g" {
		t.Fatal("the wheel key is not an account setting")
	}
	a.uploadSettings(a.setAccountSetting("wheel_key", "ctrl_space"))
	if got := a.settingText(views.SetWheelKey); got != "Ctrl+Space" {
		t.Fatalf("the settings page shows %q", got)
	}
}

func TestWheelItemsFollowTheSelection(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.st.UserID = "me"

	// Someone else's moment on the timeline.
	a.st.Focus = client.FocusInput
	a.ui.Page = client.PageMoments
	m := &a.ui.Moments.Model
	m.Moments = []protocol.Moment{{ID: "mo1", Author: protocol.User{ID: "bob", Username: "bob"}}}
	m.CommentSel = -1
	m.Loaded = true
	m.Selected[views.TabTimeline] = 1 // row 0 is the compose prompt
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	ls := wheelLabels(a)
	for _, want := range []i18n.T{i18n.WheelLike, i18n.WheelComment, i18n.WheelAuthor, i18n.WheelPost} {
		if !hasLabel(ls, want) {
			t.Errorf("moment wheel lacks %s: %v", want.Get(i18n.ZH), ls)
		}
	}
	if hasLabel(ls, i18n.WheelDelete) || hasLabel(ls, i18n.WheelHelp) || ls[0] != i18n.WheelLike.Get(i18n.ZH) {
		t.Fatalf("moment wheel: %v", ls)
	}
	a.closeWheel()

	// A direct message with a friend in the list.
	cv := a.st.EnsureConversation("d1")
	cv.Name, cv.Kind = "carol", protocol.KindDM
	a.ui.Moments.Model.Friends = []protocol.User{{ID: "c", Username: "carol"}}
	a.st.Focus = client.FocusList
	for i, r := range a.st.SidebarRows() {
		if r.Kind == client.RowConversation && r.Conversation.ID == "d1" {
			a.ui.Selected = i
		}
	}
	a.openWheel()
	ls = wheelLabels(a)
	if ls[0] != i18n.WheelMessage.Get(i18n.ZH) || !hasLabel(ls, i18n.WheelViewProfile) || !hasLabel(ls, i18n.WheelRemoveFriend) || hasLabel(ls, i18n.WheelQuit) {
		t.Fatalf("dm wheel: %v", ls)
	}
	a.closeWheel()

	a.st.Focus = client.FocusInput
	a.ui.Page = client.PageProfile
	a.ui.Moments.Profile = views.ProfileModel{Profile: &protocol.Profile{IsSelf: true}}
	a.openWheel()
	ls = wheelLabels(a)
	if hasLabel(ls, i18n.WheelBlock) || hasLabel(ls, i18n.WheelAddFriend) || !hasLabel(ls, i18n.WheelEditProfile) {
		t.Fatalf("own profile wheel: %v", ls)
	}
	a.closeWheel()

	a.st.Focus = client.FocusList
	a.st.State = client.StateDisconnected
	a.openWheel()
	ls = wheelLabels(a)
	if hasLabel(ls, i18n.WheelNewDM) {
		t.Fatalf("offline list wheel: %v", ls)
	}
	a.closeWheel()

	// The app-wide items are only on the home list's foot entries.
	a.st.State = client.StateReady
	a.ui.Selected = client.IndexOfEntry(a.st.SidebarRows(), client.EntryMe)
	a.openWheel()
	ls = wheelLabels(a)
	for _, want := range []i18n.T{i18n.WheelQuit, i18n.WheelHelp} {
		if !hasLabel(ls, want) {
			t.Errorf("the 我 entry lacks %s: %v", want.Get(i18n.ZH), ls)
		}
	}
	a.closeWheel()

	// The conversation offers its look as settings.
	a.st.Focus = client.FocusInput
	a.st.ActiveID = "d1"
	a.ui.Page = client.PageConversation
	a.openWheel()
	if ls = wheelLabels(a); !strings.HasPrefix(strings.Join(ls, "|"), "") || !containsPrefix(ls, i18n.SetBubbleWidth.Get(i18n.ZH)) || hasLabel(ls, i18n.WheelQuit) {
		t.Errorf("conversation wheel: %v", ls)
	}
	a.closeWheel()
}

// The fan grows from the selected row, which stays as it was, and everything
// else is dimmed.
func TestWheelFansFromTheSelection(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	cv := a.st.EnsureConversation("d1")
	cv.Name, cv.Kind = "carol", protocol.KindDM
	a.st.Focus = client.FocusList
	for i, r := range a.st.SidebarRows() {
		if r.Kind == client.RowConversation {
			a.ui.Selected = i
		}
	}
	a.st.MotionOff = true
	a.render()
	scr := a.scr.(tcell.SimulationScreen)
	scr.Show()
	row, col := -1, -1
	_, h := scr.Size()
	for y := 0; y < h && row < 0; y++ {
		if x := strings.Index(screenRow(scr, y), "carol"); x >= 0 && x < 40 {
			row, col = y, x
		}
	}
	if row < 0 {
		t.Fatal("carol is not on screen")
	}
	_, _, before, _ := scr.GetContent(col, row)
	_, _, title, _ := scr.GetContent(2, 0)
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	a.render()
	scr.Show()
	if _, _, after, _ := scr.GetContent(col, row); after != before {
		t.Fatal("the selected row did not stay as it was")
	}
	// Only the fan's surroundings are shaded; the title bar, far from it, is
	// left as it was.
	if _, _, top, _ := scr.GetContent(2, 0); top != title {
		t.Fatal("the shade reached the far top of the screen")
	}
	// The fan opens to the right of carol: the selection is framed beyond the
	// row's end, and a line leaves carol's row towards it. Near the top of the
	// list it sits a few rows lower, so all the items fit.
	msg := i18n.WheelMessage.Get(i18n.ZH)
	selRow := -1
	for y := 0; y < h; y++ {
		if x := strings.Index(screenRow(scr, y), msg); x > col+len("carol") {
			selRow = y
		}
	}
	if selRow < 0 || !strings.ContainsAny(screenRow(scr, row)[col:], "─┐┘") {
		t.Fatalf("the fan did not open to the right of carol:\n%s", screenText(scr))
	}
}

// screenRow reads one row; wide runes count as one character.
func screenRow(s tcell.SimulationScreen, y int) string {
	w, _ := s.Size()
	var b strings.Builder
	for x := 0; x < w; x++ {
		r, _, _, width := s.GetContent(x, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
		if width == 2 {
			x++
		}
	}
	return b.String()
}

func TestWheelAnimation(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	t0 := nowTime()
	defer func() { nowTime = time.Now }()
	nowTime = func() time.Time { return t0 }
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	a.render()
	sel := a.ui.Wheel.Sel
	turn(a, ctx, 1)
	if a.ui.Wheel.Sel != (sel+1)%len(a.ui.Wheel.Items) {
		t.Fatal("the turn did not take effect at once")
	}
	// Closing hands the keyboard back at once but keeps drawing for a while.
	a.handleKey(ctx, specialKey(tcell.KeyEscape))
	if a.ui.Wheel.Open || !a.wheelMoving(nowTime()) {
		t.Fatal("closing: the wheel should be shut but still animating")
	}
	nowTime = func() time.Time { return t0.Add(time.Second) }
	if a.wheelMoving(nowTime()) {
		t.Fatal("the closing did not finish")
	}
	// With animation off nothing lingers.
	a.st.MotionOff = true
	a.openWheel()
	a.render()
	a.closeWheel()
	if a.wheelMoving(nowTime()) {
		t.Fatal("with animation off the wheel lingered after closing")
	}
}

func TestLegendShowsTheWheelKey(t *testing.T) {
	a, _ := newKeyTestApp(t)
	a.render()
	txt := screenText(a.scr.(tcell.SimulationScreen))
	if !strings.Contains(txt, "F1 打开快捷转盘") || strings.Contains(txt, "Alt+N") {
		t.Fatalf("legend: %q", txt)
	}
	a.uploadSettings(a.setAccountSetting("wheel_key", "ctrl_g"))
	a.render()
	if txt := screenText(a.scr.(tcell.SimulationScreen)); !strings.Contains(txt, "Ctrl+G 打开快捷转盘") {
		t.Fatal("the legend did not follow the wheel key")
	}
}

// settingsOn opens the settings page with the cursor on item of category name.
func settingsOn(t *testing.T, a *App, name string, item int) {
	t.Helper()
	a.openSettings()
	for i, c := range views.Categories() {
		if c.Title.Get(a.lang) == name {
			a.ui.SettingsNav = views.SettingsNav{Cat: i, Level: views.LevelItem, Item: item}
			return
		}
	}
	t.Fatalf("no category %s", name)
}

// The choice mode leaves the page where it was and opens the candidates as a
// fan from the item's value: the current one in the middle, in yellow.
func TestChoiceModeOpensAFan(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.st.MotionOff = true
	settingsOn(t, a, "聊天", 1) // density: 舒适 / 紧凑
	a.render()
	scr := a.scr.(tcell.SimulationScreen)
	scr.Show()
	_, h := scr.Size()
	row := -1
	for y := 0; y < h; y++ {
		if strings.Contains(screenRow(scr, y), "消息密度") {
			row = y
		}
	}
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	a.render()
	scr.Show()
	line := screenRow(scr, row)
	if !strings.Contains(line, "消息密度") || strings.Count(line, "舒适") != 2 || !strings.Contains(line, "─") {
		t.Fatalf("no fan from the item's value:\n%s", screenText(scr))
	}
	if !strings.Contains(screenRow(scr, row+1), "紧凑") {
		t.Fatalf("the other candidate is not beside the current one:\n%s", screenText(scr))
	}
	// ↓ moves to the next candidate; Esc closes without saving.
	a.handleKey(ctx, specialKey(tcell.KeyDown))
	a.handleKey(ctx, specialKey(tcell.KeyEscape))
	a.render()
	scr.Show()
	if strings.Contains(screenText(scr), "紧凑") || a.cfg.Density == "compact" {
		t.Fatal("Esc left the fan or saved the candidate")
	}
}

// On the settings page the wheel key does what Enter does, and closes the
// candidates again unchanged.
func TestWheelKeyOnTheSettingsPageIsEnter(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	settingsOn(t, a, "聊天", 1)
	a.ui.SettingsNav.Level = views.LevelCategory
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	if a.ui.Wheel.Open || a.ui.SettingsNav.Level != views.LevelItem {
		t.Fatal("on a category the wheel key did not enter it")
	}
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	if a.ui.SettingsNav.Level != views.LevelChoose {
		t.Fatal("on an item the wheel key did not open its candidates")
	}
	a.handleKey(ctx, specialKey(tcell.KeyDown))
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	if a.ui.SettingsNav.Level != views.LevelItem || a.cfg.Density == "compact" {
		t.Fatal("the wheel key again did not close the candidates unchanged")
	}
}

func containsPrefix(ls []string, p string) bool {
	for _, l := range ls {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// A setting item opens its candidates as a second fan; turning it shows each
// candidate at once without saving, Enter keeps it and folds back, Esc puts the
// original back.
func TestWheelSubmenuPreviewsAndKeeps(t *testing.T) {
	a, ctx := newOpenKeyTestApp(t)
	a.st.Focus = client.FocusInput
	a.ui.Page = client.PageConversation
	a.handleKey(ctx, specialKey(tcell.KeyF1))
	for a.ui.Wheel.Items[a.ui.Wheel.Sel].Label != i18n.SetDensity {
		turn(a, ctx, 1)
	}
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	if !a.ui.Wheel.Sub.Open {
		t.Fatal("Enter on a setting did not open its candidates")
	}
	turn(a, ctx, 1)
	if !a.cfg.Layout().Compact {
		t.Fatal("the candidate is not shown on the page")
	}
	a.handleKey(ctx, specialKey(tcell.KeyEscape))
	if a.cfg.Layout().Compact || a.ui.Wheel.Sub.Open || !a.ui.Wheel.Open {
		t.Fatal("Esc did not put the original back and return to the first level")
	}
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	turn(a, ctx, 1)
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	if a.cfg.Density != "compact" || a.ui.Wheel.Sub.Open || !a.ui.Wheel.Open {
		t.Fatal("Enter did not keep the candidate and fold back to the first level")
	}
}

// turn turns the open wheel (or its submenu) by one, with whichever keys its
// direction gives.
func turn(a *App, ctx context.Context, d int) {
	a.render()
	dir := a.ui.Wheel.dir
	if a.ui.Wheel.Sub.Open {
		dir = a.ui.Wheel.Sub.dir
	}
	next, prev, _, _ := views.FanKeys(dir)
	k := next
	if d < 0 {
		k = prev
	}
	a.handleKey(ctx, specialKey(k))
}
